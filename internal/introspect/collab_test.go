package introspect

import (
	"testing"
	"time"
)

// TestCollabReporterSnapshots verifies the reporter accumulates edges, the
// snapshot collapses repeated pairs, and the ring cap evicts oldest first.
func TestCollabReporterSnapshots(t *testing.T) {
	r := NewCollabReporter()

	// Empty reporter → empty snapshot.
	snap := r.Snapshot()
	if len(snap.Graph) != 0 || len(snap.Recent) != 0 {
		t.Fatalf("empty reporter should yield empty snapshot, got %+v", snap)
	}

	// Record three edges (two collisions).
	now := time.Now()
	r.Record(CollabEdge{From: "coder", To: "reviewer", Topic: "handoff", TaskID: "t1", TS: now})
	r.Record(CollabEdge{From: "reviewer", To: "tester", Topic: "verify", TaskID: "t1", TS: now.Add(1 * time.Second)})
	r.Record(CollabEdge{From: "coder", To: "reviewer", Topic: "handoff", TaskID: "t2", TS: now.Add(2 * time.Second)})

	snap = r.Snapshot()
	if len(snap.Graph) != 2 {
		t.Fatalf("expected 2 collapsed edges, got %d", len(snap.Graph))
	}
	if len(snap.Recent) != 3 {
		t.Fatalf("expected 3 raw edges, got %d", len(snap.Recent))
	}
	// First graph edge should be coder→reviewer (collapsed, latest TS=t2).
	if snap.Graph[0].From != "coder" || snap.Graph[0].To != "reviewer" {
		t.Fatalf("first graph edge wrong: %+v", snap.Graph[0])
	}
	if snap.Graph[0].TaskID != "t2" {
		t.Fatalf("collapsed edge should keep the latest task id, got %s", snap.Graph[0].TaskID)
	}

	// Ring cap: record more than max, verify oldest dropped.
	r2 := NewCollabReporter()
	for i := 0; i < maxCollabEdges+10; i++ {
		r2.Record(CollabEdge{From: "a", To: "b", Topic: "work", TaskID: "t", TS: time.Now()})
	}
	snap2 := r2.Snapshot()
	if len(snap2.Recent) != maxCollabEdges {
		t.Fatalf("expected %d recent edges (ring cap), got %d", maxCollabEdges, len(snap2.Recent))
	}
}
