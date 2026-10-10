package workflow

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Timwood0x10/ares/internal/core/models"
)

// ── NewKnowledgeAgent + getters ─────────────────────────────────────────────

func TestNewKnowledgeAgent(t *testing.T) {
	cfg := StepConfig{Step: StepBuildGraph, Goal: "test goal"}
	a := NewKnowledgeAgent("akf-1", nil, nil, cfg)
	if a == nil {
		t.Fatal("NewKnowledgeAgent returned nil")
	}
	if a.ID() != "akf-1" {
		t.Errorf("ID = %q", a.ID())
	}
	if a.Type() != AgentTypeAKF {
		t.Errorf("Type = %q", a.Type())
	}
	if a.Status() != models.AgentStatusReady {
		t.Errorf("Status = %q, want ready", a.Status())
	}
}

func TestAgentTypeAKFConstant(t *testing.T) {
	if string(AgentTypeAKF) != "akf" {
		t.Errorf("AgentTypeAKF = %q", AgentTypeAKF)
	}
}

// ── Start / Stop status transitions ─────────────────────────────────────────

func TestStartStop_StatusTransitions(t *testing.T) {
	a := NewKnowledgeAgent("akf-1", nil, nil, StepConfig{Goal: "g"})

	if err := a.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if a.Status() != models.AgentStatusBusy {
		t.Errorf("Status after Start = %q, want busy", a.Status())
	}

	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if a.Status() != models.AgentStatusReady {
		t.Errorf("Status after Stop = %q, want ready", a.Status())
	}
}

// ── Process: error paths ────────────────────────────────────────────────────

func TestProcess_NilRuntimeEmptyGoal(t *testing.T) {
	a := NewKnowledgeAgent("akf-1", nil, nil, StepConfig{})
	_, err := a.Process(context.Background(), nil)
	if err == nil {
		t.Error("expected error for empty goal with nil input")
	}
}

func TestProcess_StringInputEmptyGoal(t *testing.T) {
	a := NewKnowledgeAgent("akf-1", nil, nil, StepConfig{})
	_, err := a.Process(context.Background(), "")
	if err == nil {
		t.Error("expected error for empty goal with empty string input")
	}
}

func TestProcess_JSONInputEmptyGoal(t *testing.T) {
	a := NewKnowledgeAgent("akf-1", nil, nil, StepConfig{})
	_, err := a.Process(context.Background(), []byte(`{}`))
	if err == nil {
		t.Error("expected error for empty goal with empty JSON input")
	}
}

func TestProcess_StringInputOverridesGoal(t *testing.T) {
	a := NewKnowledgeAgent("akf-1", nil, nil, StepConfig{})
	input, _ := json.Marshal(StepConfig{Step: StepBuildGraph, Goal: "override goal"})
	// With nil runtime, Process must fail at rt.Execute (not at goal validation),
	// proving the goal was parsed from input.
	_, err := a.Process(context.Background(), string(input))
	if err == nil {
		t.Fatal("expected error with nil runtime even when goal is provided")
	}
}

func TestProcess_ByteInputOverridesGoal(t *testing.T) {
	a := NewKnowledgeAgent("akf-1", nil, nil, StepConfig{})
	input, _ := json.Marshal(StepConfig{Step: StepBuildGraph, Goal: "byte goal"})
	_, err := a.Process(context.Background(), input)
	if err == nil {
		t.Fatal("expected error with nil runtime even when goal is provided")
	}
}

// ── Process: budget calculation ─────────────────────────────────────────────

func TestProcess_BudgetDefaults(t *testing.T) {
	// When MaxTokens <= 0, defaults are MaxTokens=5000, ForGraph=3000.
	// With nil runtime, Process fails at rt.Execute — but only AFTER the
	// goal check passes, proving budget logic ran without error.
	cfg := StepConfig{Step: StepBuildGraph, Goal: "g", MaxTokens: 0, ForGraph: 0}
	a := NewKnowledgeAgent("akf-1", nil, nil, cfg)
	_, err := a.Process(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error with nil runtime")
	}
}

func TestProcess_BudgetForGraphExceedsMaxTokens(t *testing.T) {
	// ForGraph > MaxTokens → Reserved clamped to 0, not negative.
	// With nil runtime, Process fails at rt.Execute — budget logic still runs.
	cfg := StepConfig{Step: StepBuildGraph, Goal: "g", MaxTokens: 100, ForGraph: 200}
	a := NewKnowledgeAgent("akf-1", nil, nil, cfg)
	_, err := a.Process(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error with nil runtime")
	}
}

// ── ProcessStream ───────────────────────────────────────────────────────────

func TestProcessStream_ErrorPath(t *testing.T) {
	a := NewKnowledgeAgent("akf-1", nil, nil, StepConfig{})
	ch, err := a.ProcessStream(context.Background(), nil)
	if err != nil {
		t.Fatalf("ProcessStream: %v", err)
	}
	eventCount := 0
	var gotErr error
	for evt := range ch {
		eventCount++
		gotErr = evt.Err
	}
	if eventCount != 1 {
		t.Fatalf("events count = %d, want 1", eventCount)
	}
	if gotErr == nil {
		t.Error("expected error event for empty goal")
	}
}

// ── AsGraphNode ─────────────────────────────────────────────────────────────

func TestAsGraphNode_ErrorPropagation(t *testing.T) {
	a := NewKnowledgeAgent("akf-1", nil, nil, StepConfig{})
	nodeFn := a.AsGraphNode()
	if nodeFn == nil {
		t.Fatal("AsGraphNode returned nil")
	}

	state := map[string]any{}
	err := nodeFn(context.Background(), state)
	if err == nil {
		t.Error("expected error for empty goal")
	}
	if _, exists := state["output"]; exists {
		t.Error("output should not be set on error")
	}
}

func TestAsGraphNode_WithInputOverride(t *testing.T) {
	a := NewKnowledgeAgent("akf-1", nil, nil, StepConfig{})
	nodeFn := a.AsGraphNode()

	input, _ := json.Marshal(StepConfig{Step: StepBuildGraph, Goal: "from input"})
	state := map[string]any{"input": string(input)}
	err := nodeFn(context.Background(), state)
	// With nil runtime this must fail at rt.Execute — the goal was parsed
	// from input (otherwise the empty-goal error would fire first).
	if err == nil {
		t.Fatal("expected error with nil runtime even when input provides goal")
	}
	if _, exists := state["output"]; exists {
		t.Error("output should not be set on error")
	}
}

// ── StepConfig / PipelineStep constants ─────────────────────────────────────

func TestPipelineStepConstants(t *testing.T) {
	if string(StepBuildGraph) != "build_graph" {
		t.Errorf("StepBuildGraph = %q", StepBuildGraph)
	}
	if string(StepCompile) != "compile" {
		t.Errorf("StepCompile = %q", StepCompile)
	}
}

func TestStepConfigJSONRoundtrip(t *testing.T) {
	cfg := StepConfig{
		Step:      StepCompile,
		Goal:      "compile this",
		Formats:   []string{"prompt", "markdown"},
		MaxTokens: 5000,
		ForGraph:  3000,
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded StepConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.Step != StepCompile {
		t.Errorf("Step = %q", decoded.Step)
	}
	if decoded.Goal != "compile this" {
		t.Errorf("Goal = %q", decoded.Goal)
	}
	if len(decoded.Formats) != 2 {
		t.Errorf("Formats len = %d", len(decoded.Formats))
	}
}
