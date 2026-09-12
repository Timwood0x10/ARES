package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

const (
	eventChanBufferSize = 64
)

// subscriber holds a channel and filter for event distribution.
type subscriber struct {
	ch     chan *ares_events.Event
	filter ares_events.EventFilter
}

// namedHook pairs a WorkflowHook with its plugin name for diagnostics.
type namedHook struct {
	pluginName string
	hook       WorkflowHook
}

// PluginBus manages plugin registration, lifecycle, and hook invocation.
// It provides the EventBus interface to plugins and coordinates BeforeStep/
// AfterStep hook calls with timeout and panic recovery.
type PluginBus struct {
	plugins     []RuntimePlugin
	hooks       []namedHook
	caps        map[Capability][]RuntimePlugin
	subscribers []*subscriber
	mu          sync.RWMutex
	started     bool
	// startCtx is the lifetime ctx handed to Start. Hot-plug registration
	// (Register after Start) reuses it so a late plugin's Start runs under
	// the SAME lifecycle as the batch plugins instead of an orphan ctx.
	startCtx      context.Context
	pluginTimeout time.Duration
	logger        *slog.Logger
	droppedEvents atomic.Int64
	// stopDone is closed exactly once by Stop so every Subscribe cleanup
	// goroutine can exit even when its caller's ctx is never cancelled
	// (e.g. context.Background) — previously those goroutines leaked for
	// the process lifetime after Stop.
	stopDone chan struct{}
	stopOnce sync.Once
}

// NewPluginBus creates a PluginBus with the given options.
func NewPluginBus(opts ...PluginBusOption) *PluginBus {
	b := &PluginBus{
		caps:          make(map[Capability][]RuntimePlugin),
		pluginTimeout: defaultPluginTimeout,
		logger:        slog.Default(),
		stopDone:      make(chan struct{}),
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Register adds a plugin to the bus. Returns ErrDuplicatePlugin if a plugin
// with the same name is already registered.
//
// HOT-PLUG: Register is valid at any point in the bus lifecycle — before OR
// after Start. When the bus is already running, the plugin is started
// immediately under the bus's lifetime ctx (same invokeStart contract as the
// batch path: timeout + panic recovery + started/failed events), so a late
// plugin receives its EventBus reference and begins observing steps right
// away. A start failure on the hot path returns the error AND removes the
// half-started plugin again (unplug-on-failure) so the bus never keeps a
// dead registration.
//
// If the plugin also implements WorkflowHook, it is automatically registered
// as a hook.
func (b *PluginBus) Register(plugin RuntimePlugin) error {
	if plugin == nil {
		return errors.New("runtime: cannot register nil plugin")
	}
	b.mu.Lock()
	for _, p := range b.plugins {
		if p.Name() == plugin.Name() {
			b.mu.Unlock()
			return fmt.Errorf("runtime: %w: %s", ErrDuplicatePlugin, plugin.Name())
		}
	}
	b.plugins = append(b.plugins, plugin)
	for _, cap := range plugin.Capabilities() {
		b.caps[cap] = append(b.caps[cap], plugin)
	}
	// Auto-register as WorkflowHook if the plugin implements it.
	if hook, ok := plugin.(WorkflowHook); ok {
		b.hooks = append(b.hooks, namedHook{pluginName: plugin.Name(), hook: hook})
	}
	started := b.started
	lifetime := b.startCtx
	b.mu.Unlock()

	if !started {
		return nil
	}
	// Hot path: the bus is already running — bring the plugin up now under
	// the same lifetime ctx Start used.
	if lifetime == nil {
		lifetime = context.Background()
	}
	// A concurrent Unregister may have removed the plugin we just appended;
	// never start a plugin the bus no longer tracks.
	if !b.tracked(plugin.Name()) {
		return fmt.Errorf("runtime: plugin %s was unregistered during registration", plugin.Name())
	}
	if err := b.invokeStart(lifetime, plugin); err != nil {
		b.Emit(lifetime, plugin.Name(), EventPluginFailed, "runtime", map[string]any{
			PayloadKeyPluginName: plugin.Name(),
			PayloadKeyError:      err.Error(),
		})
		// Unplug-on-failure: remove the half-registered plugin so the bus
		// state matches reality (no dead registration, no dangling hook).
		b.remove(plugin)
		return err
	}
	// Stop-race guard: the bus may have been stopped while this plugin was
	// starting (started was read under the lock, then released). Without
	// this check the plugin would stay running on a stopped bus with no
	// one left to tear it down.
	//
	// Note the residual window: Stop snapshots the plugin list under the
	// lock, so a plugin appended just before Stop's snapshot is torn down
	// by Stop AND re-torn-down here. Both invokeStop calls are idempotent
	// from the bus's perspective (stop errors are log-and-continue, and a
	// second Stop on an already-stopped plugin is the plugin's own
	// contract), so the race costs a redundant Stop rather than a leak —
	// which is the trade we want.
	b.mu.Lock()
	stillRunning := b.started
	b.mu.Unlock()
	if !stillRunning {
		b.mu.Lock()
		b.removeLocked(plugin.Name())
		b.mu.Unlock()
		_ = b.invokeStop(context.Background(), plugin)
		return fmt.Errorf("runtime: bus stopped during hot-plug start of %s", plugin.Name())
	}
	// Post-start re-check: an Unregister that slipped between the pre-check
	// and Start removed and stopped a not-yet-started plugin — undo our
	// start so the bus never keeps a running plugin outside its bookkeeping.
	if !b.tracked(plugin.Name()) {
		b.invokeStop(lifetime, plugin)
		return fmt.Errorf("runtime: plugin %s was unregistered during hot-plug start", plugin.Name())
	}
	b.Emit(lifetime, plugin.Name(), EventPluginStarted, "runtime", map[string]any{
		PayloadKeyPluginName:         plugin.Name(),
		PayloadKeyPluginCapabilities: fmt.Sprintf("%v", plugin.Capabilities()),
	})
	return nil
}

// Unregister removes a plugin from the bus by name. When the bus is running,
// the plugin is stopped first (same timeout/panic contract as Stop's per-
// plugin teardown), then deregistered: capability entries, workflow hooks,
// and the plugin list all drop it, so hooks stop firing and PluginsByCap
// stops returning it immediately. This is the plug-out half of hot-plug —
// the counterpart to Register-after-Start.
//
// Returns an error naming the plugin when no such plugin is registered. A
// plugin that fails to stop is still removed (stop errors are reported, not
// retryable state), matching Stop's log-and-continue contract.
func (b *PluginBus) Unregister(ctx context.Context, name string) error {
	b.mu.Lock()
	plugin := b.removeLocked(name)
	started := b.started
	b.mu.Unlock()

	if plugin == nil {
		return fmt.Errorf("runtime: plugin not registered: %s", name)
	}
	if !started {
		return nil
	}
	// Stop outside the lock (invokeStart/Stop emit events, and Emit takes
	// RLock — never call them under the write lock).
	return b.invokeStop(ctx, plugin)
}

// removeLocked drops every registration entry for name (plugin list, capability
// index, workflow hooks) and returns the removed plugin, or nil when no plugin
// with that name is registered. Caller must hold b.mu.
//
// The hook loop drains ALL matches rather than the first: RegisterHook is
// public and permits duplicate names, so a single break would leave a
// surviving hook still firing in BeforeStep/AfterStep after its plugin is
// gone.
func (b *PluginBus) removeLocked(name string) RuntimePlugin {
	var removed RuntimePlugin
	for i, p := range b.plugins {
		if p.Name() == name {
			removed = p
			b.plugins = append(b.plugins[:i], b.plugins[i+1:]...)
			break
		}
	}
	for cap, ps := range b.caps {
		filtered := ps[:0]
		for _, p := range ps {
			if p.Name() != name {
				filtered = append(filtered, p)
			}
		}
		if len(filtered) == 0 {
			delete(b.caps, cap)
		} else {
			b.caps[cap] = filtered
		}
	}
	kept := b.hooks[:0]
	for _, nh := range b.hooks {
		if nh.pluginName != name {
			kept = append(kept, nh)
		}
	}
	b.hooks = kept
	return removed
}

// tracked reports whether a plugin name is still in the bus's plugin list
// (read side of the hot-plug/unplug race guards in Register).
func (b *PluginBus) tracked(name string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, p := range b.plugins {
		if p.Name() == name {
			return true
		}
	}
	return false
}

// remove deletes a plugin's registration entries (list, caps, hooks). Used
// to roll back a failed hot-plug start.
func (b *PluginBus) remove(plugin RuntimePlugin) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.removeLocked(plugin.Name())
}

// Start initializes all registered plugins. If a plugin fails to start,
// the error is logged but Start continues with remaining plugins.
// Returns a combined error if any plugin failed.
//
// The ctx becomes the bus lifetime: hot-plug registration after Start runs
// late plugins under the same ctx.
func (b *PluginBus) Start(ctx context.Context) error {
	b.mu.Lock()
	b.started = true
	b.startCtx = ctx
	// Snapshot under the lock: hot-plug Register may append to b.plugins
	// concurrently (it no longer rejects post-Start calls), so iterating the
	// live slice here would race with that write.
	plugins := make([]RuntimePlugin, len(b.plugins))
	copy(plugins, b.plugins)
	b.mu.Unlock()

	var errs []error
	for _, p := range plugins {
		if err := b.invokeStart(ctx, p); err != nil {
			b.Emit(ctx, p.Name(), EventPluginFailed, "runtime", map[string]any{
				PayloadKeyPluginName: p.Name(),
				PayloadKeyError:      err.Error(),
			})
			errs = append(errs, err)
		} else {
			b.Emit(ctx, p.Name(), EventPluginStarted, "runtime", map[string]any{
				PayloadKeyPluginName:         p.Name(),
				PayloadKeyPluginCapabilities: fmt.Sprintf("%v", p.Capabilities()),
			})
		}
	}
	return errors.Join(errs...)
}

// Stop shuts down all plugins in reverse registration order.
func (b *PluginBus) Stop(ctx context.Context) error {
	b.mu.Lock()
	b.started = false
	// Signal every Subscribe cleanup goroutine: they park on their caller's
	// ctx OR this channel, so a subscriber whose ctx is never cancelled is
	// still released at Stop instead of leaking for the process lifetime.
	b.stopOnce.Do(func() { close(b.stopDone) })
	// Clean up all subscribers to prevent leaked goroutines.
	// Close subscriber channels and clear the slice so Emit (which still
	// holds RLock) can no longer send to stale channels after Stop.
	for _, s := range b.subscribers {
		close(s.ch)
	}
	b.subscribers = b.subscribers[:0]
	// Snapshot under the lock for the same reason as Start: hot-plug
	// Register/Unregister mutate b.plugins while this teardown runs.
	plugins := make([]RuntimePlugin, len(b.plugins))
	copy(plugins, b.plugins)
	b.mu.Unlock()

	var errs []error
	for i := len(plugins) - 1; i >= 0; i-- {
		if err := b.invokeStop(ctx, plugins[i]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// invokeStop tears one plugin down with the shared timeout/panic contract
// and emits the stopped/failed event. Used by Stop (batch) and Unregister
// (single hot-plug removal).
func (b *PluginBus) invokeStop(ctx context.Context, p RuntimePlugin) error {
	if err := invokeWithTimeout(ctx, b.pluginTimeout, p.Name(), func(sctx context.Context) error {
		return p.Stop(sctx)
	}); err != nil {
		b.logger.Error("runtime: plugin stop failed",
			"plugin", p.Name(),
			"error", err,
		)
		b.Emit(ctx, p.Name(), EventPluginFailed, "runtime", map[string]any{
			PayloadKeyPluginName: p.Name(),
			PayloadKeyError:      err.Error(),
		})
		return err
	}
	b.Emit(ctx, p.Name(), EventPluginStopped, "runtime", map[string]any{
		PayloadKeyPluginName: p.Name(),
	})
	return nil
}

// RegisterHook adds a named WorkflowHook to be called before and after each step.
// The name is used in logs and error messages to identify the hook.
func (b *PluginBus) RegisterHook(name string, hook WorkflowHook) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.hooks = append(b.hooks, namedHook{pluginName: name, hook: hook})
}

// BeforeStep calls all registered hooks before a step executes.
// Each hook is invoked sequentially. If a hook fails (error, panic, or
// timeout), the error is logged and the remaining hooks still execute.
// Hooks are observational and therefore use a log-and-continue contract.
func (b *PluginBus) BeforeStep(ctx context.Context, executionID string, step *Step) error {
	b.mu.RLock()
	hooks := make([]namedHook, len(b.hooks))
	copy(hooks, b.hooks)
	b.mu.RUnlock()

	var errs []error
	for _, nh := range hooks {
		if err := invokeWithTimeout(ctx, b.pluginTimeout, nh.pluginName+":beforeStep", func(sctx context.Context) error {
			return nh.hook.BeforeStep(sctx, executionID, step)
		}); err != nil {
			b.logger.Warn("runtime: before step hook failed (continuing)",
				"plugin", nh.pluginName,
				"error", err,
			)
			errs = append(errs, fmt.Errorf("runtime: before step hook %s: %w", nh.pluginName, err))
		}
	}
	return errors.Join(errs...)
}

// AfterStep calls all registered hooks after a step completes.
// Each hook is invoked sequentially. If a hook fails, the error is logged
// and the remaining hooks still execute.
func (b *PluginBus) AfterStep(ctx context.Context, executionID string, result *StepResult) error {
	b.mu.RLock()
	hooks := make([]namedHook, len(b.hooks))
	copy(hooks, b.hooks)
	b.mu.RUnlock()

	var errs []error
	for _, nh := range hooks {
		if err := invokeWithTimeout(ctx, b.pluginTimeout, nh.pluginName+":afterStep", func(sctx context.Context) error {
			return nh.hook.AfterStep(sctx, executionID, result)
		}); err != nil {
			b.logger.Warn("runtime: after step hook failed (continuing)",
				"plugin", nh.pluginName,
				"error", err,
			)
			errs = append(errs, fmt.Errorf("runtime: after step hook %s: %w", nh.pluginName, err))
		}
	}
	return errors.Join(errs...)
}

// Emit publishes an event with the given stream ID to all matching
// subscribers. Non-blocking; ares_events are dropped for subscribers whose
// channel buffer is full.
//
// PERF: Holds RLock during the entire dispatch to prevent close-channel race
// with Subscribe's cleanup goroutine. The subscriber list copy + sends are
// fast (buffered channel sends); holding RLock is negligible.
func (b *PluginBus) Emit(ctx context.Context, streamID string, eventType ares_events.EventType, moduleName string, payload map[string]any) {
	evt := &ares_events.Event{
		ID:         ares_events.NewEventID(),
		StreamID:   streamID,
		Type:       eventType,
		ModuleName: moduleName,
		Payload:    payload,
		Timestamp:  time.Now(),
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	for _, s := range b.subscribers {
		if !matchFilter(evt, s.filter) {
			continue
		}
		select {
		case s.ch <- evt:
		case <-ctx.Done():
			return
		default:
			b.droppedEvents.Add(1)
			b.logger.Warn("plugin bus: event dropped, subscriber buffer full",
				"event_type", evt.Type, "stream_id", evt.StreamID)
		}
	}
}

// Subscribe returns a channel that receives ares_events matching the given filter.
// The channel must be drained to prevent backpressure; when ctx is cancelled
// the subscription is automatically removed and the channel is closed.
//
// SAFETY: Emit holds RLock during dispatch, and the cleanup goroutine holds
// exclusive Lock when closing the channel, so close() and send() never race.
func (b *PluginBus) Subscribe(ctx context.Context, filter ares_events.EventFilter) (<-chan *ares_events.Event, error) {
	ch := make(chan *ares_events.Event, eventChanBufferSize)

	sub := &subscriber{
		ch:     ch,
		filter: filter,
	}

	b.mu.Lock()
	b.subscribers = append(b.subscribers, sub)
	b.mu.Unlock()

	go func() {
		// Exit when the caller's ctx dies OR the bus stops: Stop already
		// removed every subscriber, so the not-found fall-through below is
		// the normal exit on the stop path (no double close of ch).
		select {
		case <-ctx.Done():
		case <-b.stopDone:
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		for i, s := range b.subscribers {
			if s == sub {
				b.subscribers = append(b.subscribers[:i], b.subscribers[i+1:]...)
				close(ch)
				return
			}
		}
	}()

	return ch, nil
}

// Stats returns runtime metrics for the bus. Currently exposes the count of
// events dropped because a subscriber's channel buffer was full, enabling
// monitoring/debugging of data loss in the event pipeline.
func (b *PluginBus) Stats() map[string]int64 {
	return map[string]int64{
		"dropped_events": b.droppedEvents.Load(),
	}
}

// PluginsByCap returns a copy of the registered plugins with the given capability.
func (b *PluginBus) PluginsByCap(cap Capability) []RuntimePlugin {
	b.mu.RLock()
	defer b.mu.RUnlock()
	plugins := b.caps[cap]
	if len(plugins) == 0 {
		return nil
	}
	result := make([]RuntimePlugin, len(plugins))
	copy(result, plugins)
	return result
}

func (b *PluginBus) invokeStart(ctx context.Context, p RuntimePlugin) error {
	err := invokeWithTimeout(ctx, b.pluginTimeout, p.Name(), func(sctx context.Context) error {
		return p.Start(sctx, b)
	})
	if err != nil {
		b.logger.Error("runtime: plugin start failed",
			"plugin", p.Name(),
			"error", err,
		)
		return err
	}
	b.logger.Info("runtime: plugin started", "plugin", p.Name())
	return nil
}

// invokeWithTimeout runs fn with a timeout derived from the parent context.
// pluginName is attached to any panic error for diagnostics.
func invokeWithTimeout(ctx context.Context, timeout time.Duration, pluginName string, fn func(context.Context) error) (err error) {
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Default().Error("plugin panicked",
					"plugin", pluginName,
					"panic_value", fmt.Sprintf("%v", r),
					"panic_type", fmt.Sprintf("%T", r),
				)
				done <- &PluginError{
					PluginName: pluginName,
					Err:        ErrPluginPanic,
					Recovered:  r,
				}
			}
		}()
		done <- fn(callCtx)
	}()

	select {
	case err = <-done:
		return err
	case <-callCtx.Done():
		return fmt.Errorf("%w: %w", ErrPluginTimeout, callCtx.Err())
	}
}

func matchFilter(evt *ares_events.Event, filter ares_events.EventFilter) bool {
	if len(filter.Types) > 0 {
		matched := false
		for _, t := range filter.Types {
			if evt.Type == t {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if len(filter.StreamIDs) > 0 {
		matched := false
		for _, sid := range filter.StreamIDs {
			if evt.StreamID == sid {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}
