package workflow

import (
	"context"
	"sync"
	"testing"

	"github.com/Timwood0x10/ares/internal/ares_runtime"
)

type lifecycleEvolutionPlugin struct {
	mu      sync.Mutex
	outcome *ares_runtime.ExecutionOutcome
}

func (p *lifecycleEvolutionPlugin) Name() string { return "lifecycle-evolution" }

func (p *lifecycleEvolutionPlugin) Capabilities() []ares_runtime.Capability {
	return []ares_runtime.Capability{ares_runtime.CapEvolution}
}

func (p *lifecycleEvolutionPlugin) Start(context.Context, ares_runtime.EventBus) error { return nil }

func (p *lifecycleEvolutionPlugin) Stop(context.Context) error { return nil }

func (p *lifecycleEvolutionPlugin) Recommend(context.Context, ares_runtime.ExecutionState) (*ares_runtime.RuntimeRecommendation, error) {
	return nil, nil
}

func (p *lifecycleEvolutionPlugin) RecordOutcome(_ context.Context, outcome ares_runtime.ExecutionOutcome) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	copyValue := outcome
	p.outcome = &copyValue
	return nil
}

func (p *lifecycleEvolutionPlugin) recordedOutcome() *ares_runtime.ExecutionOutcome {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.outcome == nil {
		return nil
	}
	copyValue := *p.outcome
	return &copyValue
}

func TestRunner_Contract_RecordsUnifiedLifecycleOutcome(t *testing.T) {
	bus := ares_runtime.NewPluginBus()
	evolution := &lifecycleEvolutionPlugin{}
	if err := bus.Register(evolution); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	spec := NewWorkflow("lifecycle-contract").
		AddNode(NodeSpec{ID: "source"}).
		AddNode(NodeSpec{ID: "selected"}).
		AddNode(NodeSpec{ID: "skipped"}).
		AddEdge(EdgeSpec{From: "source", To: "selected", Kind: EdgeControlFlow, Branch: BranchOne, Group: "route", Cond: &ConditionExpr{Type: conditionTypeState, Value: "selected"}}).
		AddEdge(EdgeSpec{From: "source", To: "skipped", Kind: EdgeControlFlow, Branch: BranchOne, Group: "route", Cond: &ConditionExpr{Type: conditionTypeState, Value: "skipped"}}).
		WithEntry("source")

	executor := NewFuncNodeExecutor()
	executor.Register("source", func(context.Context, StateView) (map[string]any, error) {
		return map[string]any{"output": "selected"}, nil
	})
	executor.Register("selected", func(context.Context, StateView) (map[string]any, error) {
		return map[string]any{"output": "done"}, nil
	})
	executor.Register("skipped", func(context.Context, StateView) (map[string]any, error) {
		t.Fatal("unselected branch executed")
		return nil, nil
	})

	collector := ares_runtime.NewExecutionCollector("lifecycle-exec")
	runner := NewRunner(
		executor,
		WithExecutionID("lifecycle-exec"),
		WithExecutionCollector(collector),
		WithPluginBus(bus),
	)
	bound := &BoundWorkflow{
		Spec: spec,
		Routers: map[NodeID]Router{
			"source": func(context.Context, string, map[string]any, string) string { return "selected" },
		},
	}
	result, err := runner.ExecuteBound(context.Background(), bound)
	if err != nil {
		t.Fatalf("ExecuteBound() error = %v", err)
	}
	if result.ExecutionID != collector.ExecutionID() {
		t.Fatalf("execution ID = %q, collector ID = %q", result.ExecutionID, collector.ExecutionID())
	}
	if got := len(collector.RouteHistory()); got != 1 {
		t.Fatalf("route count = %d, want 1", got)
	}
	outcome := evolution.recordedOutcome()
	if outcome == nil {
		t.Fatal("evolution outcome was not recorded")
	}
	if outcome.ExecutionID != result.ExecutionID || outcome.WorkflowID != spec.ID {
		t.Fatalf("outcome identity = %#v, want execution %q workflow %q", outcome, result.ExecutionID, spec.ID)
	}
	if outcome.Status != string(ares_runtime.StepStatusCompleted) || outcome.TotalSteps != 3 || outcome.SkippedSteps != 1 || outcome.RouteCount != 1 {
		t.Fatalf("outcome = %#v, want completed totals 3/1/1", outcome)
	}
}
