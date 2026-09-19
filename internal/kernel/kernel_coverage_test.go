package kernel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// ── Mode.String ─────────────────────────────────────────────────────────────

func TestModeString(t *testing.T) {
	tests := []struct {
		mode Mode
		want string
	}{
		{ModeRequired, "required"},
		{ModeOptional, "optional"},
		{ModeDegraded, "degraded"},
		{Mode(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.mode.String(); got != tt.want {
			t.Errorf("Mode(%d).String() = %q, want %q", tt.mode, got, tt.want)
		}
	}
}

// ── ComponentStatus.String ──────────────────────────────────────────────────

func TestComponentStatusString(t *testing.T) {
	s := ComponentStatus{
		Name:   "test-comp",
		Mode:   ModeRequired,
		State:  StateReady,
		Reason: "all good",
	}
	got := s.String()
	if !strings.Contains(got, "test-comp") {
		t.Errorf("String() = %q, should contain component name", got)
	}
	if !strings.Contains(got, "required") {
		t.Errorf("String() = %q, should contain mode", got)
	}
	if !strings.Contains(got, "all good") {
		t.Errorf("String() = %q, should contain reason", got)
	}
}

// ── Registry.Get / Names ────────────────────────────────────────────────────

func TestRegistryGetAndNames(t *testing.T) {
	r := NewRegistry()
	comp := &testComponent{name: "comp-a"}
	if err := r.Register(comp, ModeRequired); err != nil {
		t.Fatalf("Register: %v", err)
	}

	got := r.Get("comp-a")
	if got == nil {
		t.Error("Get should return component")
	}

	missing := r.Get("nonexistent")
	if missing != nil {
		t.Errorf("Get(nonexistent) = %v, want nil", missing)
	}

	names := r.Names()
	if len(names) != 1 || names[0] != "comp-a" {
		t.Errorf("Names() = %v, want [comp-a]", names)
	}
}

// ── Snapshot.JSON ───────────────────────────────────────────────────────────

func TestSnapshotJSON(t *testing.T) {
	s := Snapshot{
		Components: []ComponentStatus{
			{Name: "c1", Mode: ModeRequired, State: StateReady},
			{Name: "c2", Mode: ModeOptional, State: StateDegraded},
		},
		Summary: SnapshotSummary{Total: 2, Ready: 1, Degraded: 1},
	}
	data, err := s.JSON()
	if err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var decoded Snapshot
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(decoded.Components) != 2 {
		t.Errorf("Components len = %d", len(decoded.Components))
	}
	if decoded.Summary.Ready != 1 {
		t.Errorf("Summary.Ready = %d", decoded.Summary.Ready)
	}
}

// ── Scheduler.RegisterExecutorIfAbsent ──────────────────────────────────────

func TestSchedulerRegisterExecutorIfAbsent(t *testing.T) {
	fab := taskfabric.NewFabric()
	sched := New(fab, nil, nil)

	existing := &stubExecutor{id: "agent-1", typ: "code"}
	got, stored := sched.RegisterExecutorIfAbsent("agent-1", existing)
	if !stored {
		t.Error("first registration should store")
	}
	if got != CapabilityExecutor(existing) {
		t.Error("should return the stored executor")
	}

	second := &stubExecutor{id: "agent-1", typ: "review"}
	got2, stored2 := sched.RegisterExecutorIfAbsent("agent-1", second)
	if stored2 {
		t.Error("second registration should not overwrite")
	}
	if got2 != CapabilityExecutor(existing) {
		t.Error("should return the existing executor")
	}
}

func TestSchedulerRegisterExecutorIfAbsentInvalid(t *testing.T) {
	fab := taskfabric.NewFabric()
	sched := New(fab, nil, nil)

	got, stored := sched.RegisterExecutorIfAbsent("", &stubExecutor{id: "x", typ: "code"})
	if stored {
		t.Error("empty agentID should not store")
	}
	if got != nil {
		t.Error("should return nil for invalid input")
	}

	got2, stored2 := sched.RegisterExecutorIfAbsent("agent", nil)
	if stored2 {
		t.Error("nil executor should not store")
	}
	if got2 != nil {
		t.Error("should return nil for nil executor")
	}
}

// ── Scheduler.HasStaticExecutorFor ─────────────────────────────────────────

func TestSchedulerHasStaticExecutorFor(t *testing.T) {
	fab := taskfabric.NewFabric()
	execs := map[string]CapabilityExecutor{
		"a1": &stubExecutor{id: "a1", typ: "code"},
		"a2": &stubExecutor{id: "a2", typ: "review"},
	}
	sched := New(fab, execs, nil)

	if !sched.HasStaticExecutorFor("code") {
		t.Error("should find executor for code capability")
	}
	if !sched.HasStaticExecutorFor("review") {
		t.Error("should find executor for review capability")
	}
	if sched.HasStaticExecutorFor("nonexistent") {
		t.Error("should not find executor for unknown capability")
	}
	if sched.HasStaticExecutorFor("") {
		t.Error("empty capability should return false")
	}
}

// ── fabricAgentExecutor.ID / Type ──────────────────────────────────────────

func TestFabricAgentExecutorID(t *testing.T) {
	e := &fabricAgentExecutor{id: "fabric-agent-1"}
	if e.ID() != "fabric-agent-1" {
		t.Errorf("ID = %q", e.ID())
	}
}

// ── Orchestrator.SetEventSink ──────────────────────────────────────────────

func TestOrchestratorSetEventSink(t *testing.T) {
	reg := NewRegistry()
	orch := NewOrchestrator(reg, context.Background())
	if orch == nil {
		t.Fatal("NewOrchestrator returned nil")
	}
	orch.SetEventSink(nil)
}

// ── Scheduler.WithEventStore ───────────────────────────────────────────────

func TestSchedulerWithEventStore(t *testing.T) {
	fab := taskfabric.NewFabric()
	sched := New(fab, nil, nil)
	if sched == nil {
		t.Fatal("New returned nil")
	}
	result := sched.WithEventStore(nil)
	if result != sched {
		t.Error("WithEventStore should return the scheduler for chaining")
	}
}

// ── testComponent helper ───────────────────────────────────────────────────

type testComponent struct {
	name string
}

func (c *testComponent) Name() string           { return c.name }
func (c *testComponent) Dependencies() []string { return nil }

var _ Component = (*testComponent)(nil)
var _ = context.Background
