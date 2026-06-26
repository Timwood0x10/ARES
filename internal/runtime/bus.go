package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/events"
)

const (
	eventChanBufferSize = 64
)

// subscriber holds a channel and filter for event distribution.
type subscriber struct {
	ch     chan *events.Event
	filter events.EventFilter
}

// PluginBus manages plugin registration, lifecycle, and hook invocation.
// It provides the EventBus interface to plugins and coordinates BeforeStep/
// AfterStep hook calls with timeout and panic recovery.
type PluginBus struct {
	plugins       []RuntimePlugin
	hooks         []WorkflowHook
	caps          map[Capability][]RuntimePlugin
	subscribers   []*subscriber
	mu            sync.RWMutex
	started       bool
	pluginTimeout time.Duration
	logger        *slog.Logger
}

// NewPluginBus creates a PluginBus with the given options.
func NewPluginBus(opts ...PluginBusOption) *PluginBus {
	b := &PluginBus{
		caps:          make(map[Capability][]RuntimePlugin),
		pluginTimeout: defaultPluginTimeout,
		logger:        slog.Default(),
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Register adds a plugin to the bus. Returns ErrDuplicatePlugin if a plugin
// with the same name is already registered. Returns ErrBusNotStarted if
// called after Start.
// If the plugin also implements WorkflowHook, it is automatically registered
// as a hook.
func (b *PluginBus) Register(plugin RuntimePlugin) error {
	if plugin == nil {
		return fmt.Errorf("runtime: cannot register nil plugin")
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.started {
		return ErrBusNotStarted
	}
	for _, p := range b.plugins {
		if p.Name() == plugin.Name() {
			return fmt.Errorf("runtime: %w: %s", ErrDuplicatePlugin, plugin.Name())
		}
	}
	b.plugins = append(b.plugins, plugin)
	for _, cap := range plugin.Capabilities() {
		b.caps[cap] = append(b.caps[cap], plugin)
	}
	// Auto-register as WorkflowHook if the plugin implements it.
	if hook, ok := plugin.(WorkflowHook); ok {
		b.hooks = append(b.hooks, hook)
	}
	return nil
}

// Start initializes all registered plugins. If a plugin fails to start,
// the error is logged but Start continues with remaining plugins.
// Returns a combined error if any plugin failed.
func (b *PluginBus) Start(ctx context.Context) error {
	b.mu.Lock()
	b.started = true
	b.mu.Unlock()

	var lastErr error
	for _, p := range b.plugins {
		if err := b.invokeStart(ctx, p); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// Stop shuts down all plugins in reverse registration order.
func (b *PluginBus) Stop(ctx context.Context) error {
	b.mu.Lock()
	b.started = false
	b.mu.Unlock()

	var lastErr error
	for i := len(b.plugins) - 1; i >= 0; i-- {
		if err := invokeWithTimeout(ctx, b.pluginTimeout, func(sctx context.Context) error {
			return b.plugins[i].Stop(sctx)
		}); err != nil {
			b.logger.Error("runtime: plugin stop failed",
				"plugin", b.plugins[i].Name(),
				"error", err,
			)
			lastErr = err
		}
	}
	return lastErr
}

// RegisterHook adds a WorkflowHook to be called before and after each step.
func (b *PluginBus) RegisterHook(hook WorkflowHook) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.hooks = append(b.hooks, hook)
}

// BeforeStep calls all registered hooks before a step executes.
func (b *PluginBus) BeforeStep(ctx context.Context, executionID string, step *Step) error {
	b.mu.RLock()
	hooks := make([]WorkflowHook, len(b.hooks))
	copy(hooks, b.hooks)
	b.mu.RUnlock()

	for _, h := range hooks {
		if err := invokeWithTimeout(ctx, b.pluginTimeout, func(sctx context.Context) error {
			return h.BeforeStep(sctx, executionID, step)
		}); err != nil {
			return fmt.Errorf("runtime: before step hook: %w", err)
		}
	}
	return nil
}

// AfterStep calls all registered hooks after a step completes.
func (b *PluginBus) AfterStep(ctx context.Context, executionID string, result *StepResult) error {
	b.mu.RLock()
	hooks := make([]WorkflowHook, len(b.hooks))
	copy(hooks, b.hooks)
	b.mu.RUnlock()

	for _, h := range hooks {
		if err := invokeWithTimeout(ctx, b.pluginTimeout, func(sctx context.Context) error {
			return h.AfterStep(sctx, executionID, result)
		}); err != nil {
			return fmt.Errorf("runtime: after step hook: %w", err)
		}
	}
	return nil
}

// Emit publishes an event with the given stream ID to all matching
// subscribers. Non-blocking; events are dropped for subscribers whose
// channel buffer is full.
func (b *PluginBus) Emit(ctx context.Context, streamID string, eventType events.EventType, payload map[string]any) {
	evt := &events.Event{
		ID:        events.NewEventID(),
		StreamID:  streamID,
		Type:      eventType,
		Payload:   payload,
		Timestamp: time.Now(),
	}

	b.mu.RLock()
	subs := make([]*subscriber, len(b.subscribers))
	copy(subs, b.subscribers)
	b.mu.RUnlock()

	for _, s := range subs {
		if !matchFilter(evt, s.filter) {
			continue
		}
		select {
		case s.ch <- evt:
		case <-ctx.Done():
			return
		default:
			// Drop event if buffer full.
		}
	}
}

// Subscribe returns a channel that receives events matching the given filter.
// The channel must be drained to prevent backpressure; when ctx is cancelled
// the subscription is automatically removed and the channel is closed.
func (b *PluginBus) Subscribe(ctx context.Context, filter events.EventFilter) (<-chan *events.Event, error) {
	ch := make(chan *events.Event, eventChanBufferSize)

	sub := &subscriber{
		ch:     ch,
		filter: filter,
	}

	b.mu.Lock()
	b.subscribers = append(b.subscribers, sub)
	b.mu.Unlock()

	go func() {
		<-ctx.Done()
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

// PluginsByCap returns all registered plugins with the given capability.
func (b *PluginBus) PluginsByCap(cap Capability) []RuntimePlugin {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.caps[cap]
}

func (b *PluginBus) invokeStart(ctx context.Context, p RuntimePlugin) error {
	err := invokeWithTimeout(ctx, b.pluginTimeout, func(sctx context.Context) error {
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

func invokeWithTimeout(ctx context.Context, timeout time.Duration, fn func(context.Context) error) (err error) {
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- &PluginError{
					PluginName: "",
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

func matchFilter(evt *events.Event, filter events.EventFilter) bool {
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
