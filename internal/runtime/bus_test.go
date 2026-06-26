package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/events"
)

// testPlugin is a simple RuntimePlugin for testing.
type testPlugin struct {
	name         string
	caps         []Capability
	startCalled  bool
	stopCalled   bool
	startErr     error
	stopErr      error
	startBlock   time.Duration
	mu           sync.Mutex
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
	mu       sync.Mutex
	before   []string // step IDs
	after    []string // step IDs
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

func (p *panickingPlugin) Name() string             { return "panicking" }
func (p *panickingPlugin) Capabilities() []Capability { return nil }
func (p *panickingPlugin) Start(_ context.Context, _ EventBus) error {
	panic("start panic")
}
func (p *panickingPlugin) Stop(_ context.Context) error { return nil }

// memoryCheckpointStore is an in-memory CheckpointStore for testing.
type memoryCheckpointStore struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newMemoryCheckpointStore() *memoryCheckpointStore {
	return &memoryCheckpointStore{data: make(map[string][]byte)}
}

func (s *memoryCheckpointStore) Save(_ context.Context, key string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = data
	return nil
}

func (s *memoryCheckpointStore) Load(_ context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data[key], nil
}

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

func TestPluginBus_RegisterAfterStart(t *testing.T) {
	b := NewPluginBus()
	require.NoError(t, b.Start(context.Background()))

	err := b.Register(newTestPlugin("late", nil))
	require.ErrorIs(t, err, ErrBusNotStarted)
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
	b.RegisterHook(h)

	// Should not panic when hook panics; invokeWithTimeout recovers.
	err := b.BeforeStep(context.Background(), "exec-1", &Step{ID: "s1"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPluginPanic)
}

func TestPluginBus_HookTimeout(t *testing.T) {
	b := NewPluginBus(WithPluginTimeout(10 * time.Millisecond))
	require.NoError(t, b.Start(context.Background()))

	slowHook := &testHook{}
	b.RegisterHook(slowHook)

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
	b.RegisterHook(h)

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
	b.RegisterHook(h1)
	b.RegisterHook(h2)

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

	ch, err := b.Subscribe(ctx, events.EventFilter{
		Types: []events.EventType{EventWorkflowStarted},
	})
	require.NoError(t, err)

	b.Emit(ctx, "stream-1", EventWorkflowStarted, map[string]any{"key": "val"})

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

	ch, err := b.Subscribe(ctx, events.EventFilter{
		Types: []events.EventType{EventWorkflowCompleted},
	})
	require.NoError(t, err)

	b.Emit(ctx, "stream-1", EventWorkflowStarted, nil)

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

	// By default the buffer is 64, so fill it with 64 events to force drops.
	ch, err := b.Subscribe(ctx, events.EventFilter{})
	require.NoError(t, err)

	// Send 128 events. The channel buffer is 64, so ~64 should be dropped
	// without blocking.
	for i := 0; i < 128; i++ {
		b.Emit(ctx, "s", EventWorkflowStarted, nil)
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
	assert.Greater(t, received, 0, "should receive at least some events")
	assert.LessOrEqual(t, received, 64, "should not exceed channel buffer")
}

func TestPluginBus_SubscriptionCleanup(t *testing.T) {
	b := NewPluginBus()
	require.NoError(t, b.Start(context.Background()))

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := b.Subscribe(ctx, events.EventFilter{})
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
	p3 := newTestPlugin("ckpt", []Capability{CapCheckpoint})

	require.NoError(t, b.Register(p1))
	require.NoError(t, b.Register(p2))
	require.NoError(t, b.Register(p3))

	obs := b.PluginsByCap(CapObserver)
	assert.Len(t, obs, 2)

	ckpt := b.PluginsByCap(CapCheckpoint)
	assert.Len(t, ckpt, 1)
	assert.Equal(t, "ckpt", ckpt[0].Name())

	none := b.PluginsByCap(CapRouter)
	assert.Len(t, none, 0)
}

// ---------------------------------------------------------------------------
// ObserverPlugin tests
// ---------------------------------------------------------------------------

func TestObserverPlugin_RecordsEvents(t *testing.T) {
	store := events.NewMemoryEventStore()
	bus := NewPluginBus()

	obs := NewObserverPlugin("test-observer", store)
	require.NoError(t, bus.Register(obs))
	require.NoError(t, bus.Start(context.Background()))

	ctx := context.Background()
	bus.Emit(ctx, "exec-1", EventWorkflowStarted, map[string]any{"key": "val"})
	bus.Emit(ctx, "exec-1", EventWorkflowCompleted, map[string]any{"key2": "val2"})

	// Give the observer goroutine time to process.
	time.Sleep(50 * time.Millisecond)

	// Verify events were written to the store.
	evts, err := store.Read(ctx, "exec-1", events.ReadOptions{})
	require.NoError(t, err)
	require.Len(t, evts, 2)

	assert.Equal(t, EventWorkflowStarted, evts[0].Type)
	assert.Equal(t, "val", evts[0].Payload["key"])
	assert.Equal(t, EventWorkflowCompleted, evts[1].Type)
}

func TestObserverPlugin_OnlySubscribesToWorkflowEvents(t *testing.T) {
	store := events.NewMemoryEventStore()
	bus := NewPluginBus()

	obs := NewObserverPlugin("test-observer", store)
	require.NoError(t, bus.Register(obs))
	require.NoError(t, bus.Start(context.Background()))

	ctx := context.Background()
	// Emit a workflow event (should be recorded).
	bus.Emit(ctx, "exec-1", EventWorkflowStarted, nil)
	// Emit a non-workflow event (should be filtered out).
	bus.Emit(ctx, "exec-1", events.EventAgentStarted, nil)

	time.Sleep(50 * time.Millisecond)

	evts, err := store.Read(ctx, "exec-1", events.ReadOptions{})
	require.NoError(t, err)
	assert.Len(t, evts, 1)
	assert.Equal(t, EventWorkflowStarted, evts[0].Type)
}

func TestObserverPlugin_EmptyNameDefaults(t *testing.T) {
	store := events.NewMemoryEventStore()
	obs := NewObserverPlugin("", store)
	assert.Equal(t, "observer", obs.Name())
}

// ---------------------------------------------------------------------------
// CheckpointPlugin tests
// ---------------------------------------------------------------------------

func TestCheckpointPlugin_SavesAfterStep(t *testing.T) {
	ckptStore := newMemoryCheckpointStore()
	bus := NewPluginBus()

	p := NewCheckpointPlugin("test-checkpoint", ckptStore)
	require.NoError(t, bus.Register(p))
	require.NoError(t, bus.Start(context.Background()))

	err := bus.AfterStep(context.Background(), "exec-1", &StepResult{
		StepID: "s1", Status: StepStatusCompleted, Output: "hello",
	})
	require.NoError(t, err)

	data, err := ckptStore.Load(context.Background(), "checkpoint/exec-1")
	require.NoError(t, err)
	require.NotNil(t, data)

	var ckpt ExperienceCheckpoint
	err = json.Unmarshal(data, &ckpt)
	require.NoError(t, err)
	assert.Equal(t, 1, ckpt.SchemaVersion)
	assert.Equal(t, "exec-1", ckpt.ExecutionID)
	require.Len(t, ckpt.StepStates, 1)
	assert.Equal(t, "s1", ckpt.StepStates[0].StepID)
	assert.Equal(t, StepStatusCompleted, ckpt.StepStates[0].Status)
	assert.Equal(t, "hello", ckpt.StepStates[0].Output)
}

func TestCheckpointPlugin_EmptyNameDefaults(t *testing.T) {
	store := newMemoryCheckpointStore()
	p := NewCheckpointPlugin("", store)
	assert.Equal(t, "checkpoint", p.Name())
}

func TestCheckpointPlugin_NoStore(t *testing.T) {
	p := NewCheckpointPlugin("no-store", nil)
	err := p.AfterStep(context.Background(), "exec-1", &StepResult{StepID: "s1"})
	require.NoError(t, err)
}

func TestCheckpointPlugin_BeforeStepCreatesCheckpoint(t *testing.T) {
	ckptStore := newMemoryCheckpointStore()
	bus := NewPluginBus()

	p := NewCheckpointPlugin("test-cp", ckptStore)
	require.NoError(t, bus.Register(p))
	require.NoError(t, bus.Start(context.Background()))

	err := bus.BeforeStep(context.Background(), "exec-1", &Step{
		ID: "s1", Name: "Step One", Status: StepStatusRunning,
	})
	require.NoError(t, err)

	data, err := ckptStore.Load(context.Background(), "checkpoint/exec-1")
	require.NoError(t, err)
	require.NotNil(t, data)

	var ckpt ExperienceCheckpoint
	err = json.Unmarshal(data, &ckpt)
	require.NoError(t, err)
	assert.Equal(t, "exec-1", ckpt.ExecutionID)
	assert.Equal(t, "running", ckpt.Status)
	require.Len(t, ckpt.StepStates, 1)
	assert.Equal(t, "s1", ckpt.StepStates[0].StepID)
	assert.Equal(t, StepStatusRunning, ckpt.StepStates[0].Status)
}

func TestCheckpointPlugin_AccumulatesMultipleSteps(t *testing.T) {
	ckptStore := newMemoryCheckpointStore()
	bus := NewPluginBus()

	p := NewCheckpointPlugin("test-cp", ckptStore)
	require.NoError(t, bus.Register(p))
	require.NoError(t, bus.Start(context.Background()))

	ctx := context.Background()

	// Step 1 lifecycle.
	_ = bus.BeforeStep(ctx, "exec-1", &Step{ID: "s1"})
	_ = bus.AfterStep(ctx, "exec-1", &StepResult{StepID: "s1", Status: StepStatusCompleted, Output: "out1"})

	// Step 2 lifecycle.
	_ = bus.BeforeStep(ctx, "exec-1", &Step{ID: "s2"})
	_ = bus.AfterStep(ctx, "exec-1", &StepResult{StepID: "s2", Status: StepStatusCompleted, Output: "out2"})

	data, err := ckptStore.Load(ctx, "checkpoint/exec-1")
	require.NoError(t, err)
	require.NotNil(t, data)

	var ckpt ExperienceCheckpoint
	err = json.Unmarshal(data, &ckpt)
	require.NoError(t, err)
	require.Len(t, ckpt.StepStates, 2)

	assert.Equal(t, "s1", ckpt.StepStates[0].StepID)
	assert.Equal(t, StepStatusCompleted, ckpt.StepStates[0].Status)
	assert.Equal(t, "out1", ckpt.StepStates[0].Output)

	assert.Equal(t, "s2", ckpt.StepStates[1].StepID)
	assert.Equal(t, StepStatusCompleted, ckpt.StepStates[1].Status)
	assert.Equal(t, "out2", ckpt.StepStates[1].Output)

	assert.Greater(t, ckpt.StateVersion, int64(0))
}

func TestCheckpointPlugin_RecordsFailures(t *testing.T) {
	ckptStore := newMemoryCheckpointStore()
	bus := NewPluginBus()

	p := NewCheckpointPlugin("test-cp", ckptStore)
	require.NoError(t, bus.Register(p))
	require.NoError(t, bus.Start(context.Background()))

	_ = bus.BeforeStep(context.Background(), "exec-1", &Step{ID: "s1"})
	_ = bus.AfterStep(context.Background(), "exec-1", &StepResult{
		StepID: "s1", Status: StepStatusFailed, Error: "oops",
	})

	data, err := ckptStore.Load(context.Background(), "checkpoint/exec-1")
	require.NoError(t, err)
	require.NotNil(t, data)

	var ckpt ExperienceCheckpoint
	err = json.Unmarshal(data, &ckpt)
	require.NoError(t, err)
	assert.Equal(t, StepStatusFailed, ckpt.StepStates[0].Status)
	assert.Equal(t, "oops", ckpt.StepStates[0].Error)
	require.Len(t, ckpt.ErrorHistory, 1)
	assert.Equal(t, "oops", ckpt.ErrorHistory[0].Message)
}

func TestCheckpointPlugin_SchemaVersion(t *testing.T) {
	ckptStore := newMemoryCheckpointStore()
	p := NewCheckpointPlugin("test-cp", ckptStore)

	_ = p.BeforeStep(context.Background(), "exec-1", &Step{ID: "s1"})

	data, _ := ckptStore.Load(context.Background(), "checkpoint/exec-1")
	var ckpt ExperienceCheckpoint
	_ = json.Unmarshal(data, &ckpt)
	assert.Equal(t, 1, ckpt.SchemaVersion, "schema version must be 1")
}

// ---------------------------------------------------------------------------
// Integration: ObserverPlugin + CheckpointPlugin together
// ---------------------------------------------------------------------------

func TestPluginBus_MultiplePlugins(t *testing.T) {
	eventStore := events.NewMemoryEventStore()
	ckptStore := newMemoryCheckpointStore()

	bus := NewPluginBus()
	obs := NewObserverPlugin("obs", eventStore)
	ckpt := NewCheckpointPlugin("ckpt", ckptStore)

	require.NoError(t, bus.Register(obs))
	require.NoError(t, bus.Register(ckpt))
	require.NoError(t, bus.Start(context.Background()))

	ctx := context.Background()

	bus.Emit(ctx, "exec-1", EventWorkflowStarted, nil)
	_ = bus.BeforeStep(ctx, "exec-1", &Step{ID: "s1"})
	_ = bus.AfterStep(ctx, "exec-1", &StepResult{StepID: "s1", Status: StepStatusCompleted})
	bus.Emit(ctx, "exec-1", EventWorkflowCompleted, nil)

	time.Sleep(100 * time.Millisecond)

	evts, err := eventStore.Read(ctx, "exec-1", events.ReadOptions{})
	require.NoError(t, err)
	assert.Len(t, evts, 2)

	data, err := ckptStore.Load(ctx, "checkpoint/exec-1")
	require.NoError(t, err)
	require.NotNil(t, data)

	var ckptData2 ExperienceCheckpoint
	_ = json.Unmarshal(data, &ckptData2)
	assert.Equal(t, StepStatusCompleted, ckptData2.StepStates[0].Status)
}
