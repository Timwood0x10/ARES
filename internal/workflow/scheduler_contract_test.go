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

// TestScheduler_Contract_OnNodeFailedPropagatesToDataDependents locks the C4
// failure-propagation semantics (code-review-2025-01-16): when a node fails,
// its data-dependency downstream must NOT stay invisibly stuck — it is recorded
// in Pending() and never becomes schedulable. The failure cascades through
// markInactive (A→B→C), so no node in the failed chain enters the ready queue.
func TestScheduler_Contract_OnNodeFailedPropagatesToDataDependents(t *testing.T) {
	t.Parallel()

	spec := NewWorkflow("fail-propagation").
		AddNode(NodeSpec{ID: "a"}).
		AddNode(NodeSpec{ID: "b"}).
		AddNode(NodeSpec{ID: "c"}).
		AddEdge(EdgeSpec{From: "a", To: "b", Kind: EdgeDataDependency}).
		AddEdge(EdgeSpec{From: "b", To: "c", Kind: EdgeDataDependency}).
		WithEntry("a")

	scheduler, err := NewScheduler(spec, ScheduleFIFO)
	if err != nil {
		t.Fatalf("NewScheduler() error = %v", err)
	}
	if got := scheduler.Next(); got != "a" {
		t.Fatalf("first node = %q, want a", got)
	}

	// a fails → b must be recorded as pending (observable, not stuck) and the
	// whole downstream chain must stay out of the ready queue.
	scheduler.OnNodeFailed("a")

	pending := scheduler.Pending()
	found := false
	for _, id := range pending {
		if id == "b" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("b must be in Pending() after a's failure, got %v", pending)
	}
	if got := scheduler.Next(); got != "" {
		t.Fatalf("no node must be ready after failure propagation, got %q", got)
	}
}

// TestScheduler_Contract_FailureDoesNotDeadlockJoinAll locks the C4 follow-up:
// a JoinAll node waits only for ACTIVATED predecessors (failed edges become
// inactive). When one predecessor completes and another fails, the join is
// still scheduled — failure prunes the dead edge instead of deadlocking the
// workflow (complements TestScheduler_Contract_JoinAllWaitsOnlyForActivatedPredecessors).
func TestScheduler_Contract_FailureDoesNotDeadlockJoinAll(t *testing.T) {
	t.Parallel()

	spec := NewWorkflow("fail-join-all").
		AddNode(NodeSpec{ID: "left"}).
		AddNode(NodeSpec{ID: "right"}).
		AddNode(NodeSpec{ID: "join", Join: JoinAll}).
		AddEdge(EdgeSpec{From: "left", To: "join", Kind: EdgeDataDependency}).
		AddEdge(EdgeSpec{From: "right", To: "join", Kind: EdgeDataDependency}).
		WithEntry("left", "right")

	scheduler, err := NewScheduler(spec, ScheduleFIFO)
	if err != nil {
		t.Fatalf("NewScheduler() error = %v", err)
	}
	_ = scheduler.Next()
	_ = scheduler.Next()

	// left completes; right fails. JoinAll waits only for activated
	// predecessors — the failed right edge is inactive, so join still runs.
	scheduler.OnNodeCompleted("left")
	scheduler.OnNodeFailed("right")

	if got := scheduler.Next(); got != "join" {
		t.Fatalf("join must still be schedulable after one predecessor fails, got %q", got)
	}
}
