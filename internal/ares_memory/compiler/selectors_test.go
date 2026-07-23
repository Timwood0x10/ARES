// Package compiler tests for the Selector implementations (PromptSelector,
// AKGSelector, AnalyticsSelector) and the shared SelectByType helper.
package compiler

import (
	"fmt"
	"sort"
	"testing"
	"time"
)

// Compile-time assertion that MemorySelector satisfies Selector now that
// selector.go adds the Name() method.
var _ Selector = (*MemorySelector)(nil)

// ── PromptSelector tests ─────────────────────────────────────────────────

func TestPromptSelectorName(t *testing.T) {
	s := NewPromptSelector(0, 0)
	if got := s.Name(); got != "prompt" {
		t.Errorf("Name() = %q, want %q", got, "prompt")
	}
}

func TestPromptSelectorNil(t *testing.T) {
	s := NewPromptSelector(0, 0)
	sg := s.Select(nil)
	if sg == nil {
		t.Fatal("Select(nil) returned nil SubGraph")
	}
	if len(sg.Nodes) != 0 {
		t.Errorf("expected 0 nodes for nil km, got %d", len(sg.Nodes))
	}
}

func TestPromptSelectorPriority(t *testing.T) {
	// Build a KM with one decision, one constraint, one fact, and one entity.
	// Entities are excluded by default (no priority); decisions and
	// constraints share the top priority (3.0) and should appear before the
	// fact (priority 1.5).
	km := NewKnowledgeModel()
	_ = km.AddNode(&Node{
		ID:         "dec1",
		Type:       NodeDecision,
		Confidence: 0.9,
		Attributes: map[string]any{"choice": "use RuntimePatch"},
	})
	_ = km.AddNode(&Node{
		ID:         "con1",
		Type:       NodeConstraint,
		Confidence: 0.9,
		Attributes: map[string]any{"name": "cost must be bounded"},
	})
	_ = km.AddNode(&Node{
		ID:         "fact1",
		Type:       NodeFact,
		Confidence: 0.9,
		Attributes: map[string]any{"description": "ARES uses RuntimePatch"},
	})
	_ = km.AddNode(&Node{
		ID:         "ent1",
		Type:       NodeEntity,
		Confidence: 0.99,
		Attributes: map[string]any{"name": "ARES"},
	})

	s := NewPromptSelector(0, 0) // no caps
	sg := s.Select(km)

	if len(sg.Nodes) != 3 {
		t.Fatalf("expected 3 nodes (decision, constraint, fact), got %d", len(sg.Nodes))
	}

	// Entity must be excluded.
	for _, n := range sg.Nodes {
		if n.Type == NodeEntity {
			t.Errorf("entity node %q should not be selected by PromptSelector", n.ID)
		}
	}

	// The first two nodes must be the decision and the constraint (priority 3.0).
	for i := 0; i < 2; i++ {
		nt := sg.Nodes[i].Type
		if nt != NodeDecision && nt != NodeConstraint {
			t.Errorf("node %d type %q should be decision or constraint (top priority)", i, nt)
		}
	}
	// The fact (priority 1.5) should be last.
	if sg.Nodes[2].Type != NodeFact {
		t.Errorf("expected fact to be last, got %q", sg.Nodes[2].Type)
	}
}

func TestPromptSelectorMaxNodes(t *testing.T) {
	km := NewKnowledgeModel()
	for i := 0; i < 5; i++ {
		_ = km.AddNode(&Node{
			ID:         fmt.Sprintf("dec%d", i),
			Type:       NodeDecision,
			Confidence: 0.8,
			Attributes: map[string]any{"choice": fmt.Sprintf("decision %d", i)},
		})
	}

	s := NewPromptSelector(0, 3) // cap at 3 nodes
	sg := s.Select(km)
	if len(sg.Nodes) != 3 {
		t.Errorf("expected 3 nodes under MaxNodes cap, got %d", len(sg.Nodes))
	}
}

func TestPromptSelectorMaxTokens(t *testing.T) {
	// Two scenarios with the same KM but different token budgets: the lower
	// budget must select strictly fewer (or equal) nodes.
	km := NewKnowledgeModel()
	for i := 0; i < 10; i++ {
		_ = km.AddNode(&Node{
			ID:         fmt.Sprintf("dec%d", i),
			Type:       NodeDecision,
			Confidence: 0.9,
			// Each description is 40 chars => ~10 tokens at 4 chars/token.
			Attributes: map[string]any{"description": fmt.Sprintf("decision-description-%02d-padding", i)},
		})
	}

	high := NewPromptSelector(1000, 0).Select(km)
	low := NewPromptSelector(25, 0).Select(km) // ~2-3 nodes max

	if len(high.Nodes) <= len(low.Nodes) {
		t.Errorf("expected high budget to select more nodes than low: high=%d low=%d",
			len(high.Nodes), len(low.Nodes))
	}
	if len(low.Nodes) == 0 {
		t.Error("expected low budget to still select at least one node")
	}

	// The token estimate in metadata must respect the budget.
	meta, ok := low.Metadata.(map[string]any)
	if !ok {
		t.Fatalf("expected map metadata, got %T", low.Metadata)
	}
	tokens, _ := meta["tokens_estimated"].(int)
	if tokens > 25 {
		t.Errorf("estimated tokens %d exceeds budget 25", tokens)
	}
}

// ── AKGSelector tests ────────────────────────────────────────────────────

func TestAKGSelectorName(t *testing.T) {
	s := NewAKGSelector(0.5, 0)
	if got := s.Name(); got != "akg" {
		t.Errorf("Name() = %q, want %q", got, "akg")
	}
}

func TestAKGSelectorFiltersAllByConfidence(t *testing.T) {
	km := NewKnowledgeModel()
	// Phase 1 L2: MinConfidence now applies to every node type, so a weak
	// entity below the threshold is excluded (not kept as a "backbone").
	_ = km.AddNode(&Node{ID: "ent_low", Type: NodeEntity, Confidence: 0.05})
	// Entity at/above threshold is kept.
	_ = km.AddNode(&Node{ID: "ent_ok", Type: NodeEntity, Confidence: 0.8})
	// Fact below threshold must be excluded.
	_ = km.AddNode(&Node{ID: "fact_low", Type: NodeFact, Confidence: 0.2})
	// Fact above threshold must be kept.
	_ = km.AddNode(&Node{ID: "fact_ok", Type: NodeFact, Confidence: 0.8})

	s := NewAKGSelector(0.5, 0)
	sg := s.Select(km)

	ids := subgraphIDs(sg)
	if contains(ids, "ent_low") {
		t.Errorf("ent_low (confidence 0.05 < 0.5) should be excluded; got %v", ids)
	}
	if !contains(ids, "ent_ok") {
		t.Errorf("ent_ok should be kept; got %v", ids)
	}
	if contains(ids, "fact_low") {
		t.Errorf("fact_low (confidence 0.2 < 0.5) should be excluded; got %v", ids)
	}
	if !contains(ids, "fact_ok") {
		t.Errorf("fact_ok should be kept; got %v", ids)
	}
}

func TestAKGSelectorMaxFactsKeepsHighestConfidence(t *testing.T) {
	km := NewKnowledgeModel()
	// 5 facts with increasing confidence.
	for i := 0; i < 5; i++ {
		_ = km.AddNode(&Node{
			ID:         fmt.Sprintf("fact%d", i),
			Type:       NodeFact,
			Confidence: 0.5 + float64(i)*0.1, // 0.5, 0.6, 0.7, 0.8, 0.9
		})
	}

	s := NewAKGSelector(0.5, 2) // cap facts at 2
	sg := s.Select(km)

	var facts []*Node
	for _, n := range sg.Nodes {
		if n.Type == NodeFact {
			facts = append(facts, n)
		}
	}
	if len(facts) != 2 {
		t.Fatalf("expected 2 facts under MaxFacts cap, got %d", len(facts))
	}
	// Highest-confidence facts are fact4 (0.9) and fact3 (0.8).
	got := map[string]bool{facts[0].ID: true, facts[1].ID: true}
	if !got["fact4"] || !got["fact3"] {
		t.Errorf("expected fact3 and fact4 (highest confidence), got %v", got)
	}
}

func TestAKGSelectorReferencesFiltered(t *testing.T) {
	km := NewKnowledgeModel()
	_ = km.AddNode(&Node{ID: "ref_ok", Type: NodeReference, Confidence: 0.9})
	_ = km.AddNode(&Node{ID: "ref_low", Type: NodeReference, Confidence: 0.1})

	s := NewAKGSelector(0.5, 0)
	sg := s.Select(km)

	ids := subgraphIDs(sg)
	if !contains(ids, "ref_ok") {
		t.Errorf("ref_ok should be kept; got %v", ids)
	}
	if contains(ids, "ref_low") {
		t.Errorf("ref_low should be excluded; got %v", ids)
	}
}

// ── AnalyticsSelector tests ──────────────────────────────────────────────

func TestAnalyticsSelectorName(t *testing.T) {
	s := NewAnalyticsSelector(0)
	if got := s.Name(); got != "analytics" {
		t.Errorf("Name() = %q, want %q", got, "analytics")
	}
}

func TestAnalyticsSelectorSortedAscendingByCreatedAt(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	km := NewKnowledgeModel()
	// Insert out of order to verify the selector sorts.
	times := []time.Time{
		base.Add(3 * time.Hour),
		base.Add(1 * time.Hour),
		base.Add(2 * time.Hour),
		base.Add(5 * time.Hour),
		base.Add(4 * time.Hour),
	}
	for i, ts := range times {
		_ = km.AddNode(&Node{
			ID:         fmt.Sprintf("dec%d", i),
			Type:       NodeDecision,
			Confidence: 0.8,
			CreatedAt:  ts,
		})
	}

	s := NewAnalyticsSelector(0)
	sg := s.Select(km)
	if len(sg.Nodes) != 5 {
		t.Fatalf("expected 5 nodes, got %d", len(sg.Nodes))
	}

	// Verify ascending CreatedAt ordering.
	for i := 1; i < len(sg.Nodes); i++ {
		if sg.Nodes[i].CreatedAt.Before(sg.Nodes[i-1].CreatedAt) {
			t.Errorf("node %d CreatedAt %v is before node %d CreatedAt %v (not ascending)",
				i, sg.Nodes[i].CreatedAt, i-1, sg.Nodes[i-1].CreatedAt)
		}
	}
}

func TestAnalyticsSelectorMaxNodesKeepsMostRecent(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	km := NewKnowledgeModel()
	for i := 0; i < 5; i++ {
		_ = km.AddNode(&Node{
			ID:         fmt.Sprintf("tsk%d", i),
			Type:       NodeTask,
			Confidence: 0.7,
			CreatedAt:  base.Add(time.Duration(i) * time.Hour),
		})
	}

	s := NewAnalyticsSelector(2)
	sg := s.Select(km)
	if len(sg.Nodes) != 2 {
		t.Fatalf("expected 2 nodes under cap, got %d", len(sg.Nodes))
	}
	// The most recent two are tsk3 and tsk4.
	got := map[string]bool{sg.Nodes[0].ID: true, sg.Nodes[1].ID: true}
	if !got["tsk3"] || !got["tsk4"] {
		t.Errorf("expected most recent tsk3 and tsk4, got %v", got)
	}
}

func TestAnalyticsSelectorExcludesNonTimelineTypes(t *testing.T) {
	km := NewKnowledgeModel()
	_ = km.AddNode(&Node{ID: "dec1", Type: NodeDecision, CreatedAt: time.Now()})
	_ = km.AddNode(&Node{ID: "ent1", Type: NodeEntity, CreatedAt: time.Now()})
	_ = km.AddNode(&Node{ID: "ref1", Type: NodeReference, CreatedAt: time.Now()})

	s := NewAnalyticsSelector(0)
	sg := s.Select(km)
	if len(sg.Nodes) != 1 {
		t.Fatalf("expected 1 node (decision only), got %d", len(sg.Nodes))
	}
	if sg.Nodes[0].Type != NodeDecision {
		t.Errorf("expected decision, got %q", sg.Nodes[0].Type)
	}
}

// ── SelectByType helper tests ────────────────────────────────────────────

func TestSelectByTypeNilKM(t *testing.T) {
	sg := SelectByType(nil, []NodeType{NodeDecision}, 0, nil)
	if sg == nil {
		t.Fatal("expected non-nil SubGraph for nil km")
	}
	if len(sg.Nodes) != 0 {
		t.Errorf("expected 0 nodes for nil km, got %d", len(sg.Nodes))
	}
}

func TestSelectByTypeNilScoreUsesConfidence(t *testing.T) {
	km := NewKnowledgeModel()
	_ = km.AddNode(&Node{ID: "d_low", Type: NodeDecision, Confidence: 0.2})
	_ = km.AddNode(&Node{ID: "d_high", Type: NodeDecision, Confidence: 0.95})
	_ = km.AddNode(&Node{ID: "e1", Type: NodeEntity, Confidence: 1.0}) // wrong type

	sg := SelectByType(km, []NodeType{NodeDecision}, 0, nil)
	if len(sg.Nodes) != 2 {
		t.Fatalf("expected 2 decision nodes, got %d", len(sg.Nodes))
	}
	// Higher confidence should sort first.
	if sg.Nodes[0].ID != "d_high" {
		t.Errorf("expected d_high first (higher confidence), got %q", sg.Nodes[0].ID)
	}
}

func TestSelectByTypeIncludesEdgesAmongSelected(t *testing.T) {
	km := NewKnowledgeModel()
	_ = km.AddNode(&Node{ID: "d1", Type: NodeDecision, Confidence: 0.9})
	_ = km.AddNode(&Node{ID: "d2", Type: NodeDecision, Confidence: 0.8})
	_ = km.AddNode(&Node{ID: "e1", Type: NodeEntity, Confidence: 1.0}) // not selected
	_ = km.AddEdge(Edge{ID: "edge_inner", Type: EdgeSupports, Source: "d1", Target: "d2"})
	_ = km.AddEdge(Edge{ID: "edge_out", Type: EdgeMentions, Source: "d1", Target: "e1"})

	sg := SelectByType(km, []NodeType{NodeDecision}, 0, nil)
	if len(sg.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(sg.Nodes))
	}
	if len(sg.Edges) != 1 {
		t.Fatalf("expected 1 edge among selected nodes, got %d", len(sg.Edges))
	}
	if sg.Edges[0].ID != "edge_inner" {
		t.Errorf("expected edge_inner, got %q", sg.Edges[0].ID)
	}
}

func TestSelectByTypeMaxNodes(t *testing.T) {
	km := NewKnowledgeModel()
	for i := 0; i < 5; i++ {
		_ = km.AddNode(&Node{
			ID:         fmt.Sprintf("d%d", i),
			Type:       NodeDecision,
			Confidence: float64(i) / 10.0, // 0.0, 0.1, 0.2, 0.3, 0.4
		})
	}

	sg := SelectByType(km, []NodeType{NodeDecision}, 2, nil)
	if len(sg.Nodes) != 2 {
		t.Fatalf("expected 2 nodes under cap, got %d", len(sg.Nodes))
	}
	// Highest-confidence nodes are d4 (0.4) and d3 (0.3).
	if sg.Nodes[0].ID != "d4" || sg.Nodes[1].ID != "d3" {
		t.Errorf("expected [d4, d3], got [%s, %s]", sg.Nodes[0].ID, sg.Nodes[1].ID)
	}
}

func TestSelectByTypeCustomScore(t *testing.T) {
	// Use the defaultScore helper (Confidence * (AccessCount+1)) explicitly.
	km := NewKnowledgeModel()
	_ = km.AddNode(&Node{ID: "a", Type: NodeFact, Confidence: 0.5, AccessCount: 0}) // 0.5 * 1 = 0.5
	_ = km.AddNode(&Node{ID: "b", Type: NodeFact, Confidence: 0.1, AccessCount: 9}) // 0.1 * 10 = 1.0
	_ = km.AddNode(&Node{ID: "c", Type: NodeFact, Confidence: 0.9, AccessCount: 0}) // 0.9 * 1 = 0.9

	sg := SelectByType(km, []NodeType{NodeFact}, 0, defaultScore)
	if len(sg.Nodes) != 3 {
		t.Fatalf("expected 3 nodes, got %d", len(sg.Nodes))
	}
	// Order by defaultScore desc: b (1.0), c (0.9), a (0.5).
	if sg.Nodes[0].ID != "b" || sg.Nodes[1].ID != "c" || sg.Nodes[2].ID != "a" {
		t.Errorf("expected [b, c, a], got [%s, %s, %s]",
			sg.Nodes[0].ID, sg.Nodes[1].ID, sg.Nodes[2].ID)
	}
}

func TestDefaultScore(t *testing.T) {
	cases := []struct {
		name string
		node *Node
		want float64
	}{
		{name: "nil", node: nil, want: 0},
		{name: "zero access", node: &Node{Confidence: 0.5, AccessCount: 0}, want: 0.5},
		{name: "with access", node: &Node{Confidence: 0.5, AccessCount: 9}, want: 5.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := defaultScore(tc.node); got != tc.want {
				t.Errorf("defaultScore() = %v, want %v", got, tc.want)
			}
		})
	}
}

// ── MemorySelector satisfies Selector ────────────────────────────────────

func TestMemorySelectorSatisfiesSelector(t *testing.T) {
	// Compile-time check above (var _ Selector = (*MemorySelector)(nil))
	// guarantees interface conformance. This runtime test verifies the
	// Name() method returns the documented value.
	ms := DefaultMemorySelector()
	if got := ms.Name(); got != "memory" {
		t.Errorf("MemorySelector.Name() = %q, want %q", got, "memory")
	}
	// It can also be used through the Selector interface.
	var sel Selector = ms
	if sel.Name() != "memory" {
		t.Errorf("Selector.Name() = %q, want %q", sel.Name(), "memory")
	}
	sg := sel.Select(NewKnowledgeModel())
	if sg == nil {
		t.Error("Select through Selector interface returned nil")
	}
}

// ── helpers ──────────────────────────────────────────────────────────────

// subgraphIDs returns the IDs of all nodes in a SubGraph.
func subgraphIDs(sg *SubGraph) []string {
	if sg == nil {
		return nil
	}
	out := make([]string, len(sg.Nodes))
	for i, n := range sg.Nodes {
		out[i] = n.ID
	}
	sort.Strings(out)
	return out
}

// contains reports whether the slice contains the given string.
func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
