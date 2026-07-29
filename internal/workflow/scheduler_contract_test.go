package workflow

import "testing"

func TestScheduler_Contract_JoinAllWaitsOnlyForActivatedPredecessors(t *testing.T) {
	t.Parallel()

	spec := NewWorkflow("join-activated").
		AddNode(NodeSpec{ID: "start"}).
		AddNode(NodeSpec{ID: "left"}).
		AddNode(NodeSpec{ID: "right"}).
		AddNode(NodeSpec{ID: "join", Join: JoinAll}).
		AddEdge(EdgeSpec{
			From:   "start",
			To:     "left",
			Kind:   EdgeControlFlow,
			Branch: BranchOne,
			Group:  "route",
			Cond:   &ConditionExpr{Type: "test", Value: "left"},
		}).
		AddEdge(EdgeSpec{
			From:   "start",
			To:     "right",
			Kind:   EdgeControlFlow,
			Branch: BranchOne,
			Group:  "route",
			Cond:   &ConditionExpr{Type: "test", Value: "right"},
		}).
		AddEdge(EdgeSpec{From: "left", To: "join", Kind: EdgeControlFlow}).
		AddEdge(EdgeSpec{From: "right", To: "join", Kind: EdgeControlFlow}).
		WithEntry("start")

	scheduler, err := NewScheduler(spec, ScheduleFIFO)
	if err != nil {
		t.Fatalf("NewScheduler() error = %v", err)
	}
	scheduler.SetCondEval(func(expr *ConditionExpr) bool {
		return expr.Value == "left"
	})

	if got := scheduler.Next(); got != "start" {
		t.Fatalf("first node = %q, want start", got)
	}
	scheduler.OnNodeCompleted("start")
	if got := scheduler.Next(); got != "left" {
		t.Fatalf("selected branch = %q, want left", got)
	}
	scheduler.OnNodeCompleted("left")
	if got := scheduler.Next(); got != "join" {
		t.Fatalf("join node = %q, want join", got)
	}
}

func TestScheduler_Contract_MergePreservesEveryArrival(t *testing.T) {
	t.Parallel()

	spec := NewWorkflow("merge-arrivals").
		AddNode(NodeSpec{ID: "left"}).
		AddNode(NodeSpec{ID: "right"}).
		AddNode(NodeSpec{ID: "merge", Join: Merge}).
		AddEdge(EdgeSpec{From: "left", To: "merge", Kind: EdgeDataDependency}).
		AddEdge(EdgeSpec{From: "right", To: "merge", Kind: EdgeDataDependency}).
		WithEntry("left", "right")

	scheduler, err := NewScheduler(spec, ScheduleFIFO)
	if err != nil {
		t.Fatalf("NewScheduler() error = %v", err)
	}
	if got := scheduler.Next(); got != "left" {
		t.Fatalf("first node = %q, want left", got)
	}
	if got := scheduler.Next(); got != "right" {
		t.Fatalf("second node = %q, want right", got)
	}

	scheduler.OnNodeCompleted("left")
	scheduler.OnNodeCompleted("right")

	if got := scheduler.Next(); got != "merge" {
		t.Fatalf("first merge arrival = %q, want merge", got)
	}
	if got := scheduler.Next(); got != "merge" {
		t.Fatalf("second merge arrival = %q, want merge", got)
	}
	if got := scheduler.Next(); got != "" {
		t.Fatalf("unexpected extra node %q", got)
	}
}

func TestScheduler_Contract_MergeForwardsEveryExecutionArrival(t *testing.T) {
	t.Parallel()

	spec := NewWorkflow("merge-forwarding").
		AddNode(NodeSpec{ID: "left"}).
		AddNode(NodeSpec{ID: "right"}).
		AddNode(NodeSpec{ID: "merge", Join: Merge}).
		AddNode(NodeSpec{ID: "sink", Join: Merge}).
		AddEdge(EdgeSpec{From: "left", To: "merge", Kind: EdgeDataDependency}).
		AddEdge(EdgeSpec{From: "right", To: "merge", Kind: EdgeDataDependency}).
		AddEdge(EdgeSpec{From: "merge", To: "sink", Kind: EdgeDataDependency}).
		WithEntry("left", "right")

	scheduler, err := NewScheduler(spec, ScheduleFIFO)
	if err != nil {
		t.Fatalf("NewScheduler() error = %v", err)
	}
	_ = scheduler.Next()
	_ = scheduler.Next()
	scheduler.OnNodeCompleted("left")
	scheduler.OnNodeCompleted("right")

	if got := scheduler.Next(); got != "merge" {
		t.Fatalf("first merge token = %q, want merge", got)
	}
	scheduler.OnNodeCompleted("merge")
	if got := scheduler.Next(); got != "merge" {
		t.Fatalf("second merge token = %q, want merge", got)
	}
	scheduler.OnNodeCompleted("merge")

	if got := scheduler.Next(); got != "sink" {
		t.Fatalf("first sink token = %q, want sink", got)
	}
	if got := scheduler.Next(); got != "sink" {
		t.Fatalf("second sink token = %q, want sink", got)
	}
}
