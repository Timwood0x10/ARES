package aresrecovery

import (
	"testing"
)

// TestEvolutionTracerRecordsSnapshots verifies snapshots are recorded oldest
// first with correct fields.
func TestEvolutionTracerRecordsSnapshots(t *testing.T) {
	tr := NewEvolutionTracer()
	tr.Record(1, 0.5, []string{"s1"}, []GenerationChange{{StrategyID: "s1", Description: "init"}})
	tr.Record(2, 0.7, []string{"s2"}, []GenerationChange{{StrategyID: "s2", Description: "mutate"}})

	snaps := tr.Snapshot()
	if len(snaps) != 2 {
		t.Fatalf("want 2 snapshots, got %d", len(snaps))
	}
	if snaps[0].Generation != 1 || snaps[0].BestScore != 0.5 {
		t.Fatalf("snapshot 0 wrong: %+v", snaps[0])
	}
	if snaps[1].Generation != 2 || snaps[1].BestScore != 0.7 {
		t.Fatalf("snapshot 1 wrong: %+v", snaps[1])
	}
	if !snaps[1].Breakthrough {
		t.Fatal("gen 2 (+40%) must be a breakthrough")
	}
	if tr.GenerationCount() != 2 {
		t.Fatalf("generation count = %d, want 2", tr.GenerationCount())
	}
}

// TestEvolutionTracerFlagsRegression verifies a score drop is flagged as a
// regression and a modest gain is neither breakthrough nor regression.
func TestEvolutionTracerFlagsRegression(t *testing.T) {
	tr := NewEvolutionTracer()
	tr.Record(1, 0.8, nil, nil)
	tr.Record(2, 0.5, nil, nil)
	tr.Record(3, 0.55, nil, nil)

	snaps := tr.Snapshot()
	if !snaps[1].Regression {
		t.Fatal("gen 2 (-37%) must be a regression")
	}
	if snaps[1].Breakthrough {
		t.Fatal("gen 2 must not be a breakthrough")
	}
	if snaps[2].Regression || snaps[2].Breakthrough {
		t.Fatal("gen 3 (+10%) must be neither breakthrough nor regression")
	}
}

// TestEvolutionTracerLatest verifies Latest returns the most recent snapshot
// (nil when empty).
func TestEvolutionTracerLatest(t *testing.T) {
	tr := NewEvolutionTracer()
	if tr.Latest() != nil {
		t.Fatal("Latest must be nil when nothing recorded")
	}
	tr.Record(1, 0.5, nil, nil)
	tr.Record(2, 0.9, []string{"top"}, nil)
	latest := tr.Latest()
	if latest == nil || latest.Generation != 2 || latest.BestScore != 0.9 {
		t.Fatalf("Latest wrong: %+v", latest)
	}
	if len(latest.TopStrategies) != 1 || latest.TopStrategies[0] != "top" {
		t.Fatalf("Latest top strategies wrong: %+v", latest.TopStrategies)
	}
}

// TestEvolutionTracerMaxGenerations verifies history is capped to the most
// recent n snapshots.
func TestEvolutionTracerMaxGenerations(t *testing.T) {
	tr := NewEvolutionTracer().WithMaxGenerations(2)
	for i := 1; i <= 4; i++ {
		tr.Record(i, float64(i)/10, nil, nil)
	}
	snaps := tr.Snapshot()
	if len(snaps) != 2 {
		t.Fatalf("want 2 retained snapshots, got %d", len(snaps))
	}
	if snaps[0].Generation != 3 || snaps[1].Generation != 4 {
		t.Fatalf("must keep the most recent generations, got %d..%d", snaps[0].Generation, snaps[1].Generation)
	}
}

// TestEvolutionTracerSnapshotIsACopy verifies Snapshot and Record do not
// alias internal slices.
func TestEvolutionTracerSnapshotIsACopy(t *testing.T) {
	tr := NewEvolutionTracer()
	tr.Record(1, 0.5, []string{"s1"}, []GenerationChange{{StrategyID: "s1"}})
	snaps := tr.Snapshot()
	snaps[0].TopStrategies[0] = "mutated"
	snaps[0].Changes[0].StrategyID = "mutated"
	latest := tr.Latest()
	if latest.TopStrategies[0] != "s1" || latest.Changes[0].StrategyID != "s1" {
		t.Fatal("snapshot must be a deep copy")
	}
}
