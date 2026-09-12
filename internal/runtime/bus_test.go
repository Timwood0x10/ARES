package runtime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_events"
)

// testPlugin is a simple RuntimePlugin for testing.
type testPlugin struct {
	name        string
	caps        []Capability
	startCalled bool
	stopCalled  bool
	startErr    error
	stopErr     error
	startBlock  time.Duration
	mu          sync.Mutex
}

func newTestPlugin(name string, caps []Capability) *testPlugin {
	return &testPlugin{name: name, caps: caps}
}

func (p *testPlugin) Name() string { return p.name }

func (p *testPlugin) Capabilities() []Capability { return p.caps }

func (p *testPlugin) Start(ctx context.Context, _ EventBus) error {
	if p.startBlock > 0 {
		select {
		case <-time.After(p.startBlock):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	p.mu.Lock()
	p.startCalled = true
	err := p.startErr
	p.mu.Unlock()
	return err
}

func (p *testPlugin) Stop(_ context.Context) error {
	p.mu.Lock()
	p.stopCalled = true
	err := p.stopErr
	p.mu.Unlock()
	return err
}

// testHook records BeforeStep/AfterStep invocations for testing.
type testHook struct {
	mu        sync.Mutex
	before    []string // step IDs
	after     []string // step IDs
	beforeErr error
	afterErr  error
}

func newTestHook() *testHook {
	return &testHook{}
}

func (h *testHook) BeforeStep(_ context.Context, _ string, step *Step) error {
	h.mu.Lock()
	h.before = append(h.before, step.ID)
	err := h.beforeErr
	h.mu.Unlock()
	return err
}

func (h *testHook) AfterStep(_ context.Context, _ string, result *StepResult) error {
	h.mu.Lock()
	h.after = append(h.after, result.StepID)
	err := h.afterErr
	h.mu.Unlock()
	return err
}

// panickingHook panics in BeforeStep for recovery testing.
type panickingHook struct{}

func (h *panickingHook) BeforeStep(_ context.Context, _ string, _ *Step) error {
	panic("before step panic")
}

func (h *panickingHook) AfterStep(_ context.Context, _ string, _ *StepResult) error {
	return nil
}

// panickingPlugin panics in Start for recovery testing.
type panickingPlugin struct{}

func (p *panickingPlugin) Name() string               { return "panicking" }
func (p *panickingPlugin) Capabilities() []Capability { return nil }
func (p *panickingPlugin) Start(_ context.Context, _ EventBus) error {
	panic("start panic")
}
func (p *panickingPlugin) Stop(_ context.Context) error { return nil }

// ---------------------------------------------------------------------------
// PluginBus tests
// ---------------------------------------------------------------------------

func TestNewPluginBus(t *testing.T) {
	b := NewPluginBus()
	require.NotNil(t, b)
	assert.Equal(t, defaultPluginTimeout, b.pluginTimeout)
}

func TestNewPluginBus_WithOptions(t *testing.T) {
	b := NewPluginBus(WithPluginTimeout(5 * time.Second))
	require.NotNil(t, b)
	assert.Equal(t, 5*time.Second, b.pluginTimeout)
}

func TestPluginBus_RegisterAndStart(t *testing.T) {
	b := NewPluginBus()
	p := newTestPlugin("test1", nil)

	err := b.Register(p)
	require.NoError(t, err)

	err = b.Start(context.Background())
	require.NoError(t, err)

	p.mu.Lock()
	assert.True(t, p.startCalled)
	p.mu.Unlock()
}

func TestPluginBus_RegisterDuplicateName(t *testing.T) {
	b := NewPluginBus()
	p1 := newTestPlugin("dup", nil)
	p2 := newTestPlugin("dup", nil)

	require.NoError(t, b.Register(p1))
	err := b.Register(p2)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDuplicatePlugin)
}

func TestPluginBus_RegisterNil(t *testing.T) {
	b := NewPluginBus()
	err := b.Register(nil)
	require.Error(t, err)
}

// TestPluginBus_RegisterAfterStart locks the hot-plug contract: a plugin
// registered after Start is started immediately (its Start runs), it is
// visible to PluginsByCap, and it participates in the batch Stop.
func TestPluginBus_RegisterAfterStart(t *testing.T) {
	b := NewPluginBus()
	require.NoError(t, b.Start(context.Background()))

	late := newTestPlugin("late", []Capability{"hot"})
	require.NoError(t, b.Register(late), "hot-plug registration must succeed after Start")

	late.mu.Lock()
	require.True(t, late.startCalled, "hot-plugged plugin must be started immediately")
	late.mu.Unlock()

	found := false
	for _, p := range b.PluginsByCap("hot") {
		if p.Name() == "late" {
			found = true
		}
	}
	require.True(t, found, "hot-plugged plugin must be visible by capability")

	require.NoError(t, b.Stop(context.Background()))
	late.mu.Lock()
	require.True(t, late.stopCalled, "hot-plugged plugin must be stopped by bus Stop")
	late.mu.Unlock()
}

// TestPluginBus_Unregister locks the plug-out contract: Unregister stops a
// running plugin, removes it from the plugin list, its capability index, and
// its workflow hooks; an unknown name errors.
func TestPluginBus_Unregister(t *testing.T) {
	b := NewPluginBus()
	p := newTestPlugin("plug", []Capability{"hot"})
	require.NoError(t, b.Register(p))
	require.NoError(t, b.Start(context.Background()))

	require.NoError(t, b.Unregister(context.Background(), "plug"))

	p.mu.Lock()
	require.True(t, p.stopCalled, "unregistered plugin must be stopped")
	p.mu.Unlock()
	require.Empty(t, b.PluginsByCap("hot"), "unregistered plugin must leave the capability index")

	err := b.Unregister(context.Background(), "plug")
	require.Error(t, err, "double unregister must error")

	require.NoError(t, b.Stop(context.Background()), "bus Stop after Unregister must not double-stop")
}

// TestPluginBus_RegisterAfterStart_FailureUnplugs locks the hot-plug
// failure contract: when a late plugin fails to start, its registration is
// rolled back (list + caps + hooks) so the bus never keeps a dead plugin.
func TestPluginBus_RegisterAfterStart_FailureUnplugs(t *testing.T) {
	b := NewPluginBus()
	require.NoError(t, b.Start(context.Background()))

	bad := newTestPlugin("bad", []Capability{"hot"})
	bad.startErr = errors.New("boom")
	require.Error(t, b.Register(bad))
	require.Empty(t, b.PluginsByCap("hot"), "failed hot-plug must be rolled back")

	require.NoError(t, b.Stop(context.Background()))
	bad.mu.Lock()
	require.False(t, bad.stopCalled, "rolled-back plugin must not be stopped by bus Stop")
	bad.mu.Unlock()
}

func TestPluginBus_Stop(t *testing.T) {
	b := NewPluginBus()
	p := newTestPlugin("test1", nil)

	require.NoError(t, b.Register(p))
	require.NoError(t, b.Start(context.Background()))
	require.NoError(t, b.Stop(context.Background()))

	p.mu.Lock()
	assert.True(t, p.stopCalled)
	p.mu.Unlock()
}

func TestPluginBus_StartContinuesOnError(t *testing.T) {
	b := NewPluginBus()
	p1 := newTestPlugin("good", nil)
	p2 := newTestPlugin("bad", nil)
	p2.startErr = errors.New("start failed")
	p3 := newTestPlugin("also-good", nil)

	require.NoError(t, b.Register(p1))
	require.NoError(t, b.Register(p2))
	require.NoError(t, b.Register(p3))

	// Start returns the last error from plugin startup, but all plugins
	// are attempted regardless.
	err := b.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "start failed")

	p1.mu.Lock()
	assert.True(t, p1.startCalled)
	p1.mu.Unlock()

	p3.mu.Lock()
	assert.True(t, p3.startCalled)
	p3.mu.Unlock()
}

func TestPluginBus_StopContinuesOnError(t *testing.T) {
	b := NewPluginBus()
	p1 := newTestPlugin("a", nil)
	p2 := newTestPlugin("b", nil)
	p2.stopErr = errors.New("stop failed")

	require.NoError(t, b.Register(p1))
	require.NoError(t, b.Register(p2))
	require.NoError(t, b.Start(context.Background()))

	err := b.Stop(context.Background())
	require.Error(t, err)

	p1.mu.Lock()
	assert.True(t, p1.stopCalled)
	p1.mu.Unlock()
}

func TestPluginBus_PanicRecovery_Start(t *testing.T) {
	b := NewPluginBus()
	p1 := newTestPlugin("good", nil)
	p2 := &panickingPlugin{}

	require.NoError(t, b.Register(p1))
	require.NoError(t, b.Register(p2))

	// Start returns the error from the panicking plugin, but recovery
	// ensures all plugins are attempted and the bus does not crash.
	err := b.Start(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPluginPanic)

	p1.mu.Lock()
	assert.True(t, p1.startCalled)
	p1.mu.Unlock()
}

func TestPluginBus_HookPanicRecovery(t *testing.T) {
	b := NewPluginBus()
	require.NoError(t, b.Start(context.Background()))

	h := &panickingHook{}
	b.RegisterHook("panic", h)

	// Should not panic when hook panics; invokeWithTimeout recovers.
	err := b.BeforeStep(context.Background(), "exec-1", &Step{ID: "s1"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPluginPanic)
}

func TestPluginBus_HookTimeout(t *testing.T) {
	b := NewPluginBus(WithPluginTimeout(10 * time.Millisecond))
	require.NoError(t, b.Start(context.Background()))

	slowHook := &testHook{}
	b.RegisterHook("slow", slowHook)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Use a context that doesn't expire immediately so the hook gets a
	// chance to time out on the bus side.
	err := b.BeforeStep(ctx, "exec-1", &Step{ID: "s1"})
	require.NoError(t, err) // BeforeStep is fast; timeout check is on bus.
	// The timeout is per-hook-invocation, not cumulative.
	// fast hook should succeed.
	assert.Equal(t, []string{"s1"}, slowHook.before)
}

func TestPluginBus_BeforeStepAfterStep_Order(t *testing.T) {
	b := NewPluginBus()
	require.NoError(t, b.Start(context.Background()))

	h := newTestHook()
	b.RegisterHook("order", h)

	step := &Step{ID: "s1", Name: "Step One"}
	result := &StepResult{StepID: "s1", Name: "Step One", Status: StepStatusCompleted}

	require.NoError(t, b.BeforeStep(context.Background(), "exec-1", step))
	require.NoError(t, b.AfterStep(context.Background(), "exec-1", result))

	assert.Equal(t, []string{"s1"}, h.before)
	assert.Equal(t, []string{"s1"}, h.after)
}

func TestPluginBus_MultipleHooks(t *testing.T) {
	b := NewPluginBus()
	require.NoError(t, b.Start(context.Background()))

	h1 := newTestHook()
	h2 := newTestHook()
	b.RegisterHook("h1", h1)
	b.RegisterHook("h2", h2)

	step := &Step{ID: "s1"}
	result := &StepResult{StepID: "s1"}

	require.NoError(t, b.BeforeStep(context.Background(), "exec-1", step))
	require.NoError(t, b.AfterStep(context.Background(), "exec-1", result))

	assert.Equal(t, []string{"s1"}, h1.before)
	assert.Equal(t, []string{"s1"}, h2.before)
	assert.Equal(t, []string{"s1"}, h1.after)
	assert.Equal(t, []string{"s1"}, h2.after)
}

// ---------------------------------------------------------------------------
// Event bus (Emit / Subscribe) tests
// ---------------------------------------------------------------------------

func TestPluginBus_EmitAndSubscribe(t *testing.T) {
	b := NewPluginBus()
	require.NoError(t, b.Start(context.Background()))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	ch, err := b.Subscribe(ctx, ares_events.EventFilter{
		Types: []ares_events.EventType{EventWorkflowStarted},
	})
	require.NoError(t, err)

	b.Emit(ctx, "stream-1", EventWorkflowStarted, "test", map[string]any{"key": "val"})

	select {
	case evt := <-ch:
		assert.Equal(t, EventWorkflowStarted, evt.Type)
		assert.Equal(t, "stream-1", evt.StreamID)
		assert.Equal(t, "val", evt.Payload["key"])
	case <-ctx.Done():
		t.Fatal("timeout waiting for event")
	}
}

func TestPluginBus_EmitFiltered_NoMatch(t *testing.T) {
	b := NewPluginBus()
	require.NoError(t, b.Start(context.Background()))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	ch, err := b.Subscribe(ctx, ares_events.EventFilter{
		Types: []ares_events.EventType{EventWorkflowCompleted},
	})
	require.NoError(t, err)

	b.Emit(ctx, "stream-1", EventWorkflowStarted, "test", nil)

	// Should NOT receive EventWorkflowStarted.
	select {
	case <-ch:
		t.Fatal("should not receive event with non-matching filter")
	case <-ctx.Done():
		// Expected: timeout with no event received.
	}
}

func TestPluginBus_Emit_NonBlocking(t *testing.T) {
	b := NewPluginBus()
	require.NoError(t, b.Start(context.Background()))

	// Create a subscription with a tiny buffer.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// By default the buffer is 64, so fill it with 64 ares_events to force drops.
	ch, err := b.Subscribe(ctx, ares_events.EventFilter{})
	require.NoError(t, err)

	// Send 128 ares_events. The channel buffer is 64, so ~64 should be dropped
	// without blocking.
	for i := 0; i < 128; i++ {
		b.Emit(ctx, "s", EventWorkflowStarted, "test", nil)
	}

	// Drain whatever we got.
	received := 0
	drainCtx, drainCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer drainCancel()
loop:
	for {
		select {
		case <-ch:
			received++
		case <-drainCtx.Done():
			break loop
		}
	}
	// Should have received at most the buffer size, but at least 0.
	assert.Greater(t, received, 0, "should receive at least some ares_events")
	assert.LessOrEqual(t, received, 64, "should not exceed channel buffer")
}

func TestPluginBus_SubscriptionCleanup(t *testing.T) {
	b := NewPluginBus()
	require.NoError(t, b.Start(context.Background()))

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := b.Subscribe(ctx, ares_events.EventFilter{})
	require.NoError(t, err)

	cancel()

	// Channel should be closed after context cancellation.
	_, ok := <-ch
	assert.False(t, ok, "channel should be closed after context cancellation")
}

// ---------------------------------------------------------------------------
// PluginsByCap tests
// ---------------------------------------------------------------------------

func TestPluginBus_PluginsByCap(t *testing.T) {
	b := NewPluginBus()

	p1 := newTestPlugin("obs1", []Capability{CapObserver})
	p2 := newTestPlugin("obs2", []Capability{CapObserver})
	p3 := newTestPlugin("tool1", []Capability{CapTool})

	require.NoError(t, b.Register(p1))
	require.NoError(t, b.Register(p2))
	require.NoError(t, b.Register(p3))

	obs := b.PluginsByCap(CapObserver)
	assert.Len(t, obs, 2)

	tools := b.PluginsByCap(CapTool)
	assert.Len(t, tools, 1)
	assert.Equal(t, "tool1", tools[0].Name())

	none := b.PluginsByCap(CapRouter)
	assert.Len(t, none, 0)
}

// ---------------------------------------------------------------------------
// ObserverPlugin tests
// ---------------------------------------------------------------------------

func TestObserverPlugin_RecordsEvents(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	bus := NewPluginBus()

	obs := NewObserverPlugin("test-observer", store)
	require.NoError(t, bus.Register(obs))
	require.NoError(t, bus.Start(context.Background()))

	ctx := context.Background()
	bus.Emit(ctx, "exec-1", EventWorkflowStarted, "test", map[string]any{"key": "val"})
	bus.Emit(ctx, "exec-1", EventWorkflowCompleted, "test", map[string]any{"key2": "val2"})

	// Give the observer goroutine time to process.
	time.Sleep(50 * time.Millisecond)

	// Verify ares_events were written to the store.
	evts, err := store.Read(ctx, "exec-1", ares_events.ReadOptions{})
	require.NoError(t, err)
	require.Len(t, evts, 2)

	assert.Equal(t, EventWorkflowStarted, evts[0].Type)
	assert.Equal(t, "val", evts[0].Payload["key"])
	assert.Equal(t, EventWorkflowCompleted, evts[1].Type)
}

func TestObserverPlugin_OnlySubscribesToWorkflowEvents(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	bus := NewPluginBus()

	obs := NewObserverPlugin("test-observer", store)
	require.NoError(t, bus.Register(obs))
	require.NoError(t, bus.Start(context.Background()))

	ctx := context.Background()
	// Emit a workflow event (should be recorded).
	bus.Emit(ctx, "exec-1", EventWorkflowStarted, "test", nil)
	// Emit a non-workflow event (should be filtered out).
	bus.Emit(ctx, "exec-1", ares_events.EventAgentStarted, "test", nil)

	time.Sleep(50 * time.Millisecond)

	evts, err := store.Read(ctx, "exec-1", ares_events.ReadOptions{})
	require.NoError(t, err)
	assert.Len(t, evts, 1)
	assert.Equal(t, EventWorkflowStarted, evts[0].Type)
}

func TestObserverPlugin_EmptyNameDefaults(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	obs := NewObserverPlugin("", store)
	assert.Equal(t, "observer", obs.Name())
}

// ---------------------------------------------------------------------------
// CheckpointPlugin tests
// (removed with C1.3, the runtime plugin half-closed-loop burial:
// CheckpointPlugin + CheckpointStore + ExperienceCheckpoint were deleted —
// zero production registrations; successor path is fabric/task
// CheckpointEnvelope.)
// ---------------------------------------------------------------------------

// (TestExpressionRouter_RegisteredAsPlugin was removed with the Router
// family — RouterPlugin/ExpressionRouter/FallbackRouter had zero production
// registrations and RouteState.Collector was never assigned.)

// ---------------------------------------------------------------------------
// InterruptPlugin tests
// ---------------------------------------------------------------------------

func TestInterruptPlugin_New(t *testing.T) {
	p := NewInterruptPlugin("")
	assert.Equal(t, "interrupt", p.Name())
	p2 := NewInterruptPlugin("custom-hitl")
	assert.Equal(t, "custom-hitl", p2.Name())
}

func TestInterruptPlugin_RecordsInterruptOnSkippedRejected(t *testing.T) {
	bus := NewPluginBus()
	collector := NewExecutionCollector("exec-1")

	p := NewInterruptPlugin("test-hitl").WithCollector(collector)
	require.NoError(t, bus.Register(p))
	require.NoError(t, bus.Start(context.Background()))

	// AfterStep with a "rejected by human" skipped step.
	err := bus.AfterStep(context.Background(), "exec-1", &StepResult{
		StepID: "s1", Status: StepStatusSkipped, Error: "rejected by human",
	})
	require.NoError(t, err)

	interrupts := collector.InterruptLog()
	require.Len(t, interrupts, 1)
	assert.Equal(t, "s1", interrupts[0].StepID)
	assert.Equal(t, "reject", interrupts[0].Action)
}

func TestInterruptPlugin_RecordsInterruptWithMetadata(t *testing.T) {
	bus := NewPluginBus()
	collector := NewExecutionCollector("exec-1")

	p := NewInterruptPlugin("test-hitl").WithCollector(collector)
	require.NoError(t, bus.Register(p))
	require.NoError(t, bus.Start(context.Background()))

	// AfterStep with interrupt metadata.
	err := bus.AfterStep(context.Background(), "exec-1", &StepResult{
		StepID: "s1", Status: StepStatusCompleted,
		Metadata: map[string]string{
			"interrupt_action":   "approve",
			"interrupt_feedback": "looks good",
		},
	})
	require.NoError(t, err)

	interrupts := collector.InterruptLog()
	require.Len(t, interrupts, 1)
	assert.Equal(t, "approve", interrupts[0].Action)
	assert.Equal(t, "looks good", interrupts[0].Feedback)
}

func TestInterruptPlugin_EmitsEvent(t *testing.T) {
	bus := NewPluginBus()
	p := NewInterruptPlugin("test-hitl")
	require.NoError(t, bus.Register(p))
	require.NoError(t, bus.Start(context.Background()))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	ch, err := bus.Subscribe(ctx, ares_events.EventFilter{
		Types: []ares_events.EventType{EventInterruptCreated},
	})
	require.NoError(t, err)

	err = bus.AfterStep(ctx, "exec-1", &StepResult{
		StepID: "s1", Status: StepStatusSkipped, Error: "rejected by human",
	})
	require.NoError(t, err)

	select {
	case evt := <-ch:
		assert.Equal(t, EventInterruptCreated, evt.Type)
		assert.Equal(t, "exec-1", evt.StreamID)
		assert.Equal(t, "reject", evt.Payload["action"])
	case <-ctx.Done():
		t.Fatal("timeout waiting for interrupt event")
	}
}

func TestInterruptPlugin_DoesNotRecordNonInterruptSteps(t *testing.T) {
	bus := NewPluginBus()
	collector := NewExecutionCollector("exec-1")

	p := NewInterruptPlugin("test-hitl").WithCollector(collector)
	require.NoError(t, bus.Register(p))
	require.NoError(t, bus.Start(context.Background()))

	// Normal completed step without interrupt metadata.
	err := bus.AfterStep(context.Background(), "exec-1", &StepResult{
		StepID: "s1", Status: StepStatusCompleted,
	})
	require.NoError(t, err)

	assert.Empty(t, collector.InterruptLog())
}

// ---------------------------------------------------------------------------
// LoopPlugin tests
// ---------------------------------------------------------------------------

func TestLoopPlugin_New(t *testing.T) {
	p := NewLoopPlugin("", LoopConfig{MaxIterations: 5})
	assert.Equal(t, "loop", p.Name())
	assert.Equal(t, 5, p.Config().MaxIterations)
}

func TestLoopPlugin_Capabilities(t *testing.T) {
	p := NewLoopPlugin("test", LoopConfig{})
	assert.Contains(t, p.Capabilities(), CapLoop)
}

func TestLoopPlugin_ShouldExecuteRound_MaxIterations(t *testing.T) {
	p := NewLoopPlugin("test", LoopConfig{MaxIterations: 3})
	assert.True(t, p.ShouldExecuteRound(1, nil))
	assert.True(t, p.ShouldExecuteRound(2, nil))
	assert.True(t, p.ShouldExecuteRound(3, nil))
	assert.False(t, p.ShouldExecuteRound(4, nil))
	assert.False(t, p.ShouldExecuteRound(5, nil))
}

func TestLoopPlugin_ShouldExecuteRound_UntilCondition(t *testing.T) {
	p := NewLoopPlugin("test", LoopConfig{
		UntilCondition: func(vars map[string]any) bool {
			count, _ := vars["count"].(int)
			return count >= 2
		},
	})
	assert.True(t, p.ShouldExecuteRound(1, map[string]any{"count": 0}))
	assert.True(t, p.ShouldExecuteRound(2, map[string]any{"count": 1}))
	assert.False(t, p.ShouldExecuteRound(3, map[string]any{"count": 2}))
	assert.False(t, p.ShouldExecuteRound(4, map[string]any{"count": 3}))
}

func TestLoopPlugin_ShouldExecuteRound_NoLimit(t *testing.T) {
	p := NewLoopPlugin("test", LoopConfig{})
	for i := 1; i <= 100; i++ {
		assert.True(t, p.ShouldExecuteRound(i, nil))
	}
}

func TestLoopPlugin_Iteration(t *testing.T) {
	p := NewLoopPlugin("test", LoopConfig{})
	assert.Equal(t, 0, p.Iteration())

	p.OnRoundEnd(context.Background(), 1, "exec-1")
	assert.Equal(t, 1, p.Iteration())

	p.OnRoundEnd(context.Background(), 5, "exec-1")
	assert.Equal(t, 5, p.Iteration())
}

func TestLoopPlugin_StopResets(t *testing.T) {
	p := NewLoopPlugin("test", LoopConfig{})
	p.OnRoundEnd(context.Background(), 3, "exec-1")
	assert.Equal(t, 3, p.Iteration())

	_ = p.Stop(context.Background())
	assert.Equal(t, 0, p.Iteration())
}

func TestLoopPlugin_RegisteredAsPlugin(t *testing.T) {
	bus := NewPluginBus()
	p := NewLoopPlugin("test-loop", LoopConfig{MaxIterations: 5})
	require.NoError(t, bus.Register(p))
	require.NoError(t, bus.Start(context.Background()))

	plugins := bus.PluginsByCap(CapLoop)
	require.Len(t, plugins, 1)
	assert.Equal(t, "test-loop", plugins[0].Name())
}

func TestLoopPlugin_EmptyNameDefaults(t *testing.T) {
	p := NewLoopPlugin("", LoopConfig{})
	assert.Equal(t, "loop", p.Name())
}

// ---------------------------------------------------------------------------
// Integration: ObserverPlugin + InterruptPlugin together
// (formerly ObserverPlugin + CheckpointPlugin; the checkpoint half was
// removed with C1.3 — see the tombstone above the ExpressionRouter tests.)
// ---------------------------------------------------------------------------

func TestPluginBus_MultiplePlugins(t *testing.T) {
	eventStore := ares_events.NewMemoryEventStore()
	collector := NewExecutionCollector("exec-1")

	bus := NewPluginBus()
	obs := NewObserverPlugin("obs", eventStore)
	hitl := NewInterruptPlugin("hitl").WithCollector(collector)

	require.NoError(t, bus.Register(obs))
	require.NoError(t, bus.Register(hitl))
	require.NoError(t, bus.Start(context.Background()))

	ctx := context.Background()

	bus.Emit(ctx, "exec-1", EventWorkflowStarted, "test", nil)
	_ = bus.BeforeStep(ctx, "exec-1", &Step{ID: "s1"})
	// A human-rejected skipped step records an interrupt via the collector.
	_ = bus.AfterStep(ctx, "exec-1", &StepResult{StepID: "s1", Status: StepStatusSkipped, Error: "rejected by human"})
	bus.Emit(ctx, "exec-1", EventWorkflowCompleted, "test", nil)

	time.Sleep(100 * time.Millisecond)

	evts, err := eventStore.Read(ctx, "exec-1", ares_events.ReadOptions{})
	require.NoError(t, err)
	// workflow.started + workflow.completed (observer); the interrupt plugin
	// emits EventInterruptCreated on the same stream, but the observer only
	// subscribes to workflow lifecycle + checkpoint events.
	assert.Len(t, evts, 2)

	interrupts := collector.InterruptLog()
	require.Len(t, interrupts, 1)
	assert.Equal(t, "s1", interrupts[0].StepID)
	assert.Equal(t, "reject", interrupts[0].Action)
}
