// Package compiler tests for KMDistiller — the distillation-is-pruning bridge.
package compiler

import (
	"context"
	"strings"
	"testing"
	"time"
)

// newTestKM builds a KM with an entity, a connected fact, and a decision
// sharing the entity topic. Edges link the fact to the entity.
func newTestKM(t *testing.T) *KnowledgeModel {
	t.Helper()
	km := NewKnowledgeModel()
	now := time.Now()
	entity := &Node{
		ID: "entity-ARES", Type: NodeEntity, Confidence: 0.9, CreatedAt: now, UpdatedAt: now,
		Attributes: map[string]any{"name": "ARES", "type": "system"},
	}
	fact := &Node{
		ID: "fact-0", Type: NodeFact, Confidence: 0.8, CreatedAt: now, UpdatedAt: now,
		Attributes: map[string]any{"subject": "ARES", "predicate": "uses", "object": "Patch"},
	}
	decision := &Node{
		ID: "decision-1", Type: NodeDecision, Confidence: 0.85, CreatedAt: now, UpdatedAt: now,
		Attributes: map[string]any{"choice": "Patch to fix errors", "rejection": "hot reload"},
	}
	for _, n := range []*Node{entity, fact, decision} {
		if err := km.AddNode(n); err != nil {
			t.Fatalf("AddNode(%s): %v", n.ID, err)
		}
	}
	for _, e := range []Edge{
		{ID: "e1", Type: EdgeMentions, Source: "fact-0", Target: "entity-ARES", Weight: 0.8, CreatedAt: now},
		{ID: "e2", Type: EdgeSupports, Source: "decision-1", Target: "fact-0", Weight: 0.85, CreatedAt: now},
	} {
		if err := km.AddEdge(e); err != nil {
			t.Fatalf("AddEdge(%s): %v", e.ID, err)
		}
	}
	return km
}

func TestKMDistillerCompressesClusterToMemory(t *testing.T) {
	km := newTestKM(t)
	sub := &SubGraph{
		Nodes: []*Node{km.Nodes["entity-ARES"], km.Nodes["fact-0"], km.Nodes["decision-1"]},
		Edges: km.Edges,
	}
	before := km.NodeCount()

	d := NewKMDistiller(WithMinScore(0.3))
	res, err := d.DistillSubGraph(context.Background(), km, sub)
	if err != nil {
		t.Fatalf("DistillSubGraph: %v", err)
	}
	if res.MemoryNodesCreated != 1 {
		t.Errorf("expected 1 memory created, got %d", res.MemoryNodesCreated)
	}
	if res.NodesPruned != 3 {
		t.Errorf("expected 3 nodes pruned, got %d", res.NodesPruned)
	}
	if res.MemoryMerged != 0 {
		t.Errorf("expected 0 merged, got %d", res.MemoryMerged)
	}
	// KM shrinks from 3 to 1 (the single memory node).
	if got := km.NodeCount(); got != 1 {
		t.Errorf("expected KM node count 1 after distill, got %d (before=%d)", got, before)
	}
	if len(res.MemoryNodes) != 1 || res.MemoryNodes[0].Type != NodeMemory {
		t.Errorf("expected one NodeMemory in result, got %+v", res.MemoryNodes)
	}
}

func TestKMDistillerPrunesLowScoreWithoutMemory(t *testing.T) {
	km := NewKnowledgeModel()
	now := time.Now()
	gibberish := &Node{
		ID: "entity-xyz", Type: NodeEntity, Confidence: 0.2, CreatedAt: now, UpdatedAt: now,
		Attributes: map[string]any{"name": "xyz qwe rty"},
	}
	if err := km.AddNode(gibberish); err != nil {
		t.Fatal(err)
	}
	sub := &SubGraph{Nodes: []*Node{gibberish}}

	d := NewKMDistiller() // default minScore 0.6
	res, err := d.DistillSubGraph(context.Background(), km, sub)
	if err != nil {
		t.Fatalf("DistillSubGraph: %v", err)
	}
	if res.MemoryNodesCreated != 0 {
		t.Errorf("expected 0 memory created for low-score cluster, got %d", res.MemoryNodesCreated)
	}
	if res.NodesPruned != 1 {
		t.Errorf("expected 1 node pruned, got %d", res.NodesPruned)
	}
	if km.NodeCount() != 0 {
		t.Errorf("expected empty KM after pruning, got %d", km.NodeCount())
	}
}

func TestKMDistillerMergesIntoSimilarExistingMemory(t *testing.T) {
	km := NewKnowledgeModel()
	now := time.Now()
	// Pre-existing memory node whose summary is identical to what the cluster
	// below will produce (nodes sorted by ID: entity then fact).
	existing := &Node{
		ID: "memory-existing", Type: NodeMemory, Confidence: 0.7, CreatedAt: now, UpdatedAt: now,
		Attributes: map[string]any{
			"summary":         "entity: ARES; fact: ARES uses Patch",
			"topic":           "knowledge cluster about ARES",
			"source_node_ids": []string{"old-1"},
		},
	}
	if err := km.AddNode(existing); err != nil {
		t.Fatal(err)
	}

	// Cluster producing an identical summary.
	entity := &Node{
		ID: "entity-ARES", Type: NodeEntity, Confidence: 0.9, CreatedAt: now, UpdatedAt: now,
		Attributes: map[string]any{"name": "ARES"},
	}
	fact := &Node{
		ID: "fact-0", Type: NodeFact, Confidence: 0.8, CreatedAt: now, UpdatedAt: now,
		Attributes: map[string]any{"subject": "ARES", "predicate": "uses", "object": "Patch"},
	}
	for _, n := range []*Node{entity, fact} {
		if err := km.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	// Edge connects them inside the subgraph so they form ONE cluster.
	sub := &SubGraph{
		Nodes: []*Node{entity, fact},
		Edges: []Edge{{ID: "se1", Type: EdgeMentions, Source: "fact-0", Target: "entity-ARES", Weight: 0.8, CreatedAt: now}},
	}

	d := NewKMDistiller(WithMinScore(0.3))
	res, err := d.DistillSubGraph(context.Background(), km, sub)
	if err != nil {
		t.Fatalf("DistillSubGraph: %v", err)
	}
	if res.MemoryMerged == 0 {
		t.Error("expected at least one merge into existing memory")
	}
	if res.MemoryNodesCreated != 0 {
		t.Errorf("expected 0 new memories when merging, got %d", res.MemoryNodesCreated)
	}
	// Existing memory node survives; cluster nodes pruned.
	if _, ok := km.Nodes["memory-existing"]; !ok {
		t.Error("expected existing memory node to survive")
	}
}

func TestKMDistillerPassesThroughMemoryNodes(t *testing.T) {
	km := NewKnowledgeModel()
	now := time.Now()
	mem := &Node{
		ID: "memory-1", Type: NodeMemory, Confidence: 0.9, CreatedAt: now, UpdatedAt: now,
		Attributes: map[string]any{"summary": "prior memory"},
	}
	if err := km.AddNode(mem); err != nil {
		t.Fatal(err)
	}
	sub := &SubGraph{Nodes: []*Node{mem}}

	d := NewKMDistiller()
	res, err := d.DistillSubGraph(context.Background(), km, sub)
	if err != nil {
		t.Fatalf("DistillSubGraph: %v", err)
	}
	if res.NodesPruned != 0 {
		t.Errorf("memory node must not be pruned, got %d pruned", res.NodesPruned)
	}
	if _, ok := km.Nodes["memory-1"]; !ok {
		t.Error("memory node was removed")
	}
}

func TestKMDistillerRelinksEdgesToMemoryNode(t *testing.T) {
	km := NewKnowledgeModel()
	now := time.Now()
	// entity-ARES is in the subgraph (will be pruned); entity-B is NOT in the
	// subgraph (survives). An edge connects them; it must re-link to the memory.
	pruned := &Node{
		ID: "entity-ARES", Type: NodeEntity, Confidence: 0.9, CreatedAt: now, UpdatedAt: now,
		Attributes: map[string]any{"name": "ARES uses Patch to fix errors"},
	}
	survivor := &Node{
		ID: "entity-B", Type: NodeEntity, Confidence: 0.8, CreatedAt: now, UpdatedAt: now,
		Attributes: map[string]any{"name": "Runtime"},
	}
	for _, n := range []*Node{pruned, survivor} {
		if err := km.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	if err := km.AddEdge(Edge{
		ID: "e1", Type: EdgeDependsOn, Source: "entity-ARES", Target: "entity-B",
		Weight: 0.9, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	sub := &SubGraph{Nodes: []*Node{pruned}}
	d := NewKMDistiller(WithMinScore(0.3))
	res, err := d.DistillSubGraph(context.Background(), km, sub)
	if err != nil {
		t.Fatalf("DistillSubGraph: %v", err)
	}
	if res.MemoryNodesCreated != 1 {
		t.Fatalf("expected 1 memory created, got %d", res.MemoryNodesCreated)
	}
	memID := res.MemoryNodes[0].ID

	// The edge should now connect the memory node to the survivor.
	var relinked bool
	for _, e := range km.Edges {
		if e.Source == memID && e.Target == "entity-B" {
			relinked = true
			break
		}
	}
	if !relinked {
		t.Errorf("expected edge re-linked to %s -> entity-B; edges=%+v", memID, km.Edges)
	}
	if _, ok := km.Nodes["entity-ARES"]; ok {
		t.Error("pruned node should be removed")
	}
	if _, ok := km.Nodes["entity-B"]; !ok {
		t.Error("survivor node should remain")
	}
}

func TestKMDistillerNilKnowledgeModelError(t *testing.T) {
	d := NewKMDistiller()
	if _, err := d.DistillSubGraph(context.Background(), nil, &SubGraph{}); err == nil {
		t.Error("expected error for nil knowledge model")
	}
}

func TestKMDistillerNilSubGraphNoOp(t *testing.T) {
	km := NewKnowledgeModel()
	d := NewKMDistiller()
	res, err := d.DistillSubGraph(context.Background(), km, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil || res.NodesPruned != 0 || res.MemoryNodesCreated != 0 {
		t.Errorf("expected zero result for nil subgraph, got %+v", res)
	}
}

func TestKMDistillerCancelledContext(t *testing.T) {
	km := newTestKM(t)
	sub := &SubGraph{Nodes: []*Node{km.Nodes["entity-ARES"]}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	d := NewKMDistiller()
	if _, err := d.DistillSubGraph(ctx, km, sub); err == nil {
		t.Error("expected error for cancelled context")
	}
}

func TestKMDistillerDeterministicMemoryID(t *testing.T) {
	// The same cluster summary must always yield the same memory node ID.
	summary := "entity: ARES; fact: ARES uses Patch"
	id1 := memoryID(summary)
	id2 := memoryID(summary)
	if id1 != id2 {
		t.Errorf("memoryID not deterministic: %q vs %q", id1, id2)
	}
	if !strings.HasPrefix(id1, "memory-") {
		t.Errorf("memory ID should have memory- prefix, got %q", id1)
	}
}

func TestTokenJaccard(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want float64
	}{
		{"identical", "a b c", "a b c", 1.0},
		{"disjoint", "a b", "c d", 0.0},
		{"partial", "a b c", "b c d", 0.5}, // intersection {b,c}=2, union {a,b,c,d}=4
		{"empty", "", "a b", 0.0},
		{"case-insensitive", "ARES Patch", "ares patch", 1.0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tokenJaccard(tc.a, tc.b)
			if abs64(got-tc.want) > 1e-9 {
				t.Errorf("tokenJaccard(%q,%q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// abs64 returns the absolute value of a float64.
func abs64(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
