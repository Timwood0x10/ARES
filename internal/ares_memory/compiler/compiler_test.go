// Package compiler tests for the Knowledge Model types and Compiler pipeline.
package compiler

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// ── KnowledgeModel tests ─────────────────────────────────────────────────

func TestNewKnowledgeModel(t *testing.T) {
	km := NewKnowledgeModel()
	if km == nil {
		t.Fatal("NewKnowledgeModel returned nil")
	}
	if km.NodeCount() != 0 {
		t.Errorf("expected 0 nodes, got %d", km.NodeCount())
	}
	if km.EdgeCount() != 0 {
		t.Errorf("expected 0 edges, got %d", km.EdgeCount())
	}
}

func TestAddNode(t *testing.T) {
	km := NewKnowledgeModel()
	node := &Node{ID: "n1", Type: NodeEntity, Attributes: map[string]any{"name": "ARES"}}

	if err := km.AddNode(node); err != nil {
		t.Fatalf("AddNode failed: %v", err)
	}
	if km.NodeCount() != 1 {
		t.Errorf("expected 1 node, got %d", km.NodeCount())
	}

	// Duplicate ID should fail.
	if err := km.AddNode(node); err == nil {
		t.Error("expected error for duplicate node ID")
	}

	// Nil node should fail.
	if err := km.AddNode(nil); err == nil {
		t.Error("expected error for nil node")
	}

	// Empty ID should fail.
	if err := km.AddNode(&Node{ID: ""}); err == nil {
		t.Error("expected error for empty node ID")
	}
}

func TestAddEdge(t *testing.T) {
	km := NewKnowledgeModel()
	_ = km.AddNode(&Node{ID: "n1", Type: NodeEntity})
	_ = km.AddNode(&Node{ID: "n2", Type: NodeFact})

	edge := Edge{ID: "e1", Type: EdgeMentions, Source: "n1", Target: "n2"}
	if err := km.AddEdge(edge); err != nil {
		t.Fatalf("AddEdge failed: %v", err)
	}
	if km.EdgeCount() != 1 {
		t.Errorf("expected 1 edge, got %d", km.EdgeCount())
	}

	// Edge to non-existent source should fail.
	if err := km.AddEdge(Edge{ID: "e2", Source: "nonexistent", Target: "n2"}); err == nil {
		t.Error("expected error for non-existent source")
	}

	// Edge to non-existent target should fail.
	if err := km.AddEdge(Edge{ID: "e3", Source: "n1", Target: "nonexistent"}); err == nil {
		t.Error("expected error for non-existent target")
	}

	// Empty edge ID should fail.
	if err := km.AddEdge(Edge{ID: "", Source: "n1", Target: "n2"}); err == nil {
		t.Error("expected error for empty edge ID")
	}
}

func TestGetNodesByType(t *testing.T) {
	km := NewKnowledgeModel()
	_ = km.AddNode(&Node{ID: "e1", Type: NodeEntity})
	_ = km.AddNode(&Node{ID: "e2", Type: NodeEntity})
	_ = km.AddNode(&Node{ID: "f1", Type: NodeFact})

	entities := km.GetNodesByType(NodeEntity)
	if len(entities) != 2 {
		t.Errorf("expected 2 entity nodes, got %d", len(entities))
	}

	facts := km.GetNodesByType(NodeFact)
	if len(facts) != 1 {
		t.Errorf("expected 1 fact node, got %d", len(facts))
	}

	none := km.GetNodesByType(NodeDecision)
	if len(none) != 0 {
		t.Errorf("expected 0 decision nodes, got %d", len(none))
	}
}

func TestGetEdgesByType(t *testing.T) {
	km := NewKnowledgeModel()
	_ = km.AddNode(&Node{ID: "n1", Type: NodeEntity})
	_ = km.AddNode(&Node{ID: "n2", Type: NodeEntity})
	_ = km.AddNode(&Node{ID: "n3", Type: NodeFact})
	_ = km.AddEdge(Edge{ID: "e1", Type: EdgeMentions, Source: "n1", Target: "n2"})
	_ = km.AddEdge(Edge{ID: "e2", Type: EdgeDependsOn, Source: "n2", Target: "n3"})

	mentions := km.GetEdgesByType(EdgeMentions)
	if len(mentions) != 1 {
		t.Errorf("expected 1 mention edge, got %d", len(mentions))
	}

	depends := km.GetEdgesByType(EdgeDependsOn)
	if len(depends) != 1 {
		t.Errorf("expected 1 depends_on edge, got %d", len(depends))
	}
}

func TestGetOutgoingEdges(t *testing.T) {
	km := NewKnowledgeModel()
	_ = km.AddNode(&Node{ID: "n1", Type: NodeEntity})
	_ = km.AddNode(&Node{ID: "n2", Type: NodeEntity})
	_ = km.AddNode(&Node{ID: "n3", Type: NodeEntity})
	_ = km.AddEdge(Edge{ID: "e1", Type: EdgeMentions, Source: "n1", Target: "n2"})
	_ = km.AddEdge(Edge{ID: "e2", Type: EdgeMentions, Source: "n1", Target: "n3"})

	outgoing := km.GetOutgoingEdges("n1")
	if len(outgoing) != 2 {
		t.Errorf("expected 2 outgoing edges, got %d", len(outgoing))
	}

	outgoing = km.GetOutgoingEdges("n2")
	if len(outgoing) != 0 {
		t.Errorf("expected 0 outgoing edges, got %d", len(outgoing))
	}
}

func TestGetIncomingEdges(t *testing.T) {
	km := NewKnowledgeModel()
	_ = km.AddNode(&Node{ID: "n1", Type: NodeEntity})
	_ = km.AddNode(&Node{ID: "n2", Type: NodeEntity})
	_ = km.AddEdge(Edge{ID: "e1", Type: EdgeMentions, Source: "n1", Target: "n2"})

	incoming := km.GetIncomingEdges("n2")
	if len(incoming) != 1 {
		t.Errorf("expected 1 incoming edge, got %d", len(incoming))
	}

	incoming = km.GetIncomingEdges("n1")
	if len(incoming) != 0 {
		t.Errorf("expected 0 incoming edges, got %d", len(incoming))
	}
}

func TestToSubGraph(t *testing.T) {
	km := NewKnowledgeModel()
	_ = km.AddNode(&Node{ID: "e1", Type: NodeEntity})
	_ = km.AddNode(&Node{ID: "f1", Type: NodeFact})
	_ = km.AddNode(&Node{ID: "d1", Type: NodeDecision})
	_ = km.AddEdge(Edge{ID: "e1", Type: EdgeMentions, Source: "e1", Target: "f1"})

	sg := km.ToSubGraph(NodeEntity, NodeFact)
	if len(sg.Nodes) != 2 {
		t.Errorf("expected 2 nodes in subgraph, got %d", len(sg.Nodes))
	}
	if len(sg.Edges) != 1 {
		t.Errorf("expected 1 edge in subgraph, got %d", len(sg.Edges))
	}
}

func TestPrune(t *testing.T) {
	km := NewKnowledgeModel()
	_ = km.AddNode(&Node{ID: "m1", Type: NodeMemory, Confidence: 0.9, AccessCount: 10})
	_ = km.AddNode(&Node{ID: "e1", Type: NodeEntity, Confidence: 0.8, AccessCount: 5})
	_ = km.AddNode(&Node{ID: "e2", Type: NodeEntity, Confidence: 0.3, AccessCount: 0})
	_ = km.AddNode(&Node{ID: "e3", Type: NodeEntity, Confidence: 0.2, AccessCount: 0})

	pruned := km.Prune(2, 0.5)
	if pruned < 1 {
		t.Errorf("expected at least 1 pruned node, got %d", pruned)
	}
	// Memory nodes should always be kept.
	if km.Nodes["m1"] == nil {
		t.Error("memory node was pruned, should have been kept")
	}
}

// ── Compiler tests ───────────────────────────────────────────────────────

func TestCompilerNoop(t *testing.T) {
	ext := NewNoopExtractor()
	norm := NewNoopNormalizer()
	cfg := DefaultCompileConfig()
	comp := NewCompiler(ext, norm, cfg)

	msgs := []SourceMessage{
		{ID: "m1", Role: "user", Content: "Hello", TurnID: "t1", Timestamp: time.Now()},
	}

	result, err := comp.Compile(context.Background(), msgs)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}
	if result == nil {
		t.Fatal("CompileResult is nil")
	}
	if result.Model == nil {
		t.Fatal("KnowledgeModel is nil")
	}
	if result.Stats.MessagesIn != 1 {
		t.Errorf("expected 1 message, got %d", result.Stats.MessagesIn)
	}
}

func TestCompilerEmptyMessages(t *testing.T) {
	comp := NewCompiler(NewNoopExtractor(), NewNoopNormalizer(), DefaultCompileConfig())

	_, err := comp.Compile(context.Background(), nil)
	if err == nil {
		t.Error("expected error for nil messages")
	}

	_, err = comp.Compile(context.Background(), []SourceMessage{})
	if err == nil {
		t.Error("expected error for empty messages")
	}
}

func TestCompilerContextCancelled(t *testing.T) {
	comp := NewCompiler(NewNoopExtractor(), NewNoopNormalizer(), DefaultCompileConfig())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := comp.Compile(ctx, []SourceMessage{{ID: "m1", Content: "test"}})
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}

// ── ShouldCompile tests ─────────────────────────────────────────────────

func TestShouldCompile(t *testing.T) {
	msgs := []SourceMessage{
		{Content: "This is a test message with enough content to trigger compilation."},
	}

	if ShouldCompile(msgs, 100, 0.7) {
		t.Error("should not compile with small messages and large window")
	}

	// Create a large message.
	largeContent := make([]byte, 2000)
	for i := range largeContent {
		largeContent[i] = 'a'
	}
	largeMsgs := []SourceMessage{{Content: string(largeContent)}}

	if !ShouldCompile(largeMsgs, 1000, 0.5) {
		t.Error("should compile with large messages and small window")
	}

	// Edge cases.
	if ShouldCompile(nil, 0, 0.7) {
		t.Error("should not compile with zero window")
	}
	if ShouldCompile(nil, 100, 0) {
		t.Error("should not compile with zero threshold")
	}
}

// ── GraphCompiler tests ─────────────────────────────────────────────────

func TestGraphCompilerEmpty(t *testing.T) {
	gc := NewGraphCompiler()
	model, err := gc.Compile(context.Background(), nil, nil, DefaultCompileConfig())
	if err != nil {
		t.Fatalf("Compile with empty input failed: %v", err)
	}
	if model.NodeCount() != 0 {
		t.Errorf("expected 0 nodes, got %d", model.NodeCount())
	}
}

func TestGraphCompilerWithEntities(t *testing.T) {
	gc := NewGraphCompiler()
	entities := []ExtractedEntity{
		{Name: "ARES", Type: "system", Confidence: 0.9, SourceID: "m1"},
		{Name: "RuntimePatch", Type: "concept", Confidence: 0.8, SourceID: "m1"},
	}

	model, err := gc.Compile(context.Background(), entities, nil, DefaultCompileConfig())
	if err != nil {
		t.Fatalf("Compile with entities failed: %v", err)
	}
	if model.NodeCount() != 2 {
		t.Errorf("expected 2 nodes, got %d", model.NodeCount())
	}
}

func TestGraphCompilerWithFacts(t *testing.T) {
	gc := NewGraphCompiler()
	facts := []ExtractedFact{
		{Subject: "ARES", Predicate: "uses", Object: "RuntimePatch", Confidence: 0.9},
	}

	model, err := gc.Compile(context.Background(), nil, facts, DefaultCompileConfig())
	if err != nil {
		t.Fatalf("Compile with facts failed: %v", err)
	}
	if model.NodeCount() != 1 {
		t.Errorf("expected 1 fact node, got %d", model.NodeCount())
	}
}

// ── AKGExtractor tests ──────────────────────────────────────────────────

func TestAKGExtractorEmpty(t *testing.T) {
	ext := NewAKGExtractor()
	entities, facts, err := ext.Extract(context.Background(), nil)
	if err != nil {
		t.Fatalf("Extract with nil failed: %v", err)
	}
	if len(entities) != 0 {
		t.Errorf("expected 0 entities, got %d", len(entities))
	}
	if len(facts) != 0 {
		t.Errorf("expected 0 facts, got %d", len(facts))
	}
}

func TestAKGExtractorCodeBlocks(t *testing.T) {
	ext := NewAKGExtractor()
	msgs := []SourceMessage{
		{
			ID:      "m1",
			Role:    "assistant",
			Content: "Here is the implementation:\n```go\npackage main\nfunc main() {}\n```\nAnd also:\n```rust\nfn main() {}\n```",
			TurnID:  "t1",
		},
	}

	entities, facts, err := ext.Extract(context.Background(), msgs)
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	// Should extract "go" and "rust" as language entities.
	hasGo := false
	hasRust := false
	for _, e := range entities {
		if e.Name == "go" || e.Name == "Go" {
			hasGo = true
		}
		if e.Name == "rust" || e.Name == "Rust" {
			hasRust = true
		}
	}
	if !hasGo {
		t.Error("expected 'go' language entity")
	}
	if !hasRust {
		t.Error("expected 'rust' language entity")
	}

	// No facts expected from code blocks.
	if len(facts) != 0 {
		t.Errorf("expected 0 facts, got %d", len(facts))
	}
}

func TestAKGExtractorTripleExtraction(t *testing.T) {
	ext := NewAKGExtractor()
	msgs := []SourceMessage{
		{
			ID:      "m1",
			Role:    "assistant",
			Content: "ARES uses RuntimePatch for evolution. The system implements a patch-based mechanism.",
			TurnID:  "t1",
		},
	}

	_, facts, err := ext.Extract(context.Background(), msgs)
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	if len(facts) == 0 {
		t.Fatal("expected at least 1 fact")
	}

	// Check for "ARES uses RuntimePatch" fact.
	hasARESUses := false
	for _, f := range facts {
		if f.Subject == "ARES" && f.Predicate == "uses" && f.Object == "RuntimePatch" {
			hasARESUses = true
		}
	}
	if !hasARESUses {
		// Debug: print all facts.
		t.Logf("extracted facts: %+v", facts)
		t.Error("expected fact: ARES uses RuntimePatch")
	}
}

func TestAKGExtractorCancelledContext(t *testing.T) {
	ext := NewAKGExtractor()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	msgs := []SourceMessage{
		{ID: "m1", Content: "Test message", TurnID: "t1"},
	}

	_, _, err := ext.Extract(ctx, msgs)
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}

// ── Benchmark ────────────────────────────────────────────────────────────

func BenchmarkCompilerEmpty(b *testing.B) {
	ext := NewNoopExtractor()
	norm := NewNoopNormalizer()
	comp := NewCompiler(ext, norm, DefaultCompileConfig())
	msgs := []SourceMessage{{ID: "m1", Content: "test", TurnID: "t1"}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = comp.Compile(context.Background(), msgs)
	}
}

func BenchmarkGraphCompilerWithEntities(b *testing.B) {
	gc := NewGraphCompiler()
	entities := make([]ExtractedEntity, 100)
	for i := range entities {
		entities[i] = ExtractedEntity{
			Name:       fmt.Sprintf("Entity%d", i),
			Type:       "concept",
			Confidence: 0.8,
			SourceID:   "m1",
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = gc.Compile(context.Background(), entities, nil, DefaultCompileConfig())
	}
}

func BenchmarkAKGExtractor(b *testing.B) {
	ext := NewAKGExtractor()
	msgs := []SourceMessage{
		{
			ID:   "m1",
			Role: "assistant",
			Content: "ARES uses RuntimePatch for evolution. The system implements a patch-based mechanism. " +
				"Here is the code:\n```go\nfunc main() {}\n```\nAnd:\n```rust\nfn main() {}\n```",
			TurnID: "t1",
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _ = ext.Extract(context.Background(), msgs)
	}
}

func BenchmarkKnowledgeModelAddNodes(b *testing.B) {
	km := NewKnowledgeModel()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = km.AddNode(&Node{
			ID:   fmt.Sprintf("n%d", i),
			Type: NodeEntity,
			Attributes: map[string]any{
				"name": fmt.Sprintf("Entity%d", i),
			},
		})
	}
}

func BenchmarkKnowledgeModelPrune(b *testing.B) {
	km := NewKnowledgeModel()
	for i := 0; i < 1000; i++ {
		_ = km.AddNode(&Node{
			ID:          fmt.Sprintf("n%d", i),
			Type:        NodeEntity,
			Confidence:  float64(i) / 1000.0,
			AccessCount: int64(i % 10),
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		km.Prune(500, 0.5)
	}
}

// ── PromptBuilder tests ─────────────────────────────────────────────────

func TestPromptBuilderDefaultTemplate(t *testing.T) {
	km := NewKnowledgeModel()
	_ = km.AddNode(&Node{ID: "d1", Type: NodeDecision, Confidence: 0.9,
		Attributes: map[string]any{"choice": "采用 Patch", "rejection": "热更新"}})
	_ = km.AddNode(&Node{ID: "c1", Type: NodeConstraint, Confidence: 0.8,
		Attributes: map[string]any{"name": "SaaS 成本必须可控"}})
	_ = km.AddNode(&Node{ID: "q1", Type: NodeQuestion, Confidence: 0.5,
		Attributes: map[string]any{"name": "如何实现增量编译"}})

	subGraph := km.ToSubGraph(NodeDecision, NodeConstraint, NodeQuestion)
	result, err := DefaultPromptContext(subGraph)
	if err != nil {
		t.Fatalf("DefaultPromptContext failed: %v", err)
	}
	if !strings.Contains(result, "Decision") {
		t.Error("expected Decision section in prompt context")
	}
	if !strings.Contains(result, "采用 Patch") {
		t.Error("expected decision content in prompt context")
	}
	if !strings.Contains(result, "SaaS") {
		t.Error("expected constraint content in prompt context")
	}
}

func TestPromptBuilderEmpty(t *testing.T) {
	subGraph := &SubGraph{}
	result, err := DefaultPromptContext(subGraph)
	if err != nil {
		t.Fatalf("DefaultPromptContext with empty subGraph failed: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result even for empty subGraph")
	}
}

func TestPromptBuilderNil(t *testing.T) {
	_, err := DefaultPromptContext(nil)
	if err == nil {
		t.Error("expected error for nil subGraph")
	}
}

func TestPromptBuilderFormats(t *testing.T) {
	km := NewKnowledgeModel()
	_ = km.AddNode(&Node{ID: "d1", Type: NodeDecision, Confidence: 0.9,
		Attributes: map[string]any{"choice": "采用 Patch"}})
	subGraph := km.ToSubGraph(NodeDecision)
	pb := NewPromptBuilder(DefaultPromptTemplate)

	// Markdown
	md, err := pb.Render(subGraph, FormatMarkdown)
	if err != nil {
		t.Fatalf("Markdown render failed: %v", err)
	}
	if !strings.Contains(md, "采用 Patch") {
		t.Error("Markdown render missing decision content")
	}

	// XML
	xml, err := pb.Render(subGraph, FormatXML)
	if err != nil {
		t.Fatalf("XML render failed: %v", err)
	}
	if !strings.Contains(xml, "<decisions>") {
		t.Error("XML render missing decisions section")
	}

	// JSON
	json, err := pb.Render(subGraph, FormatJSON)
	if err != nil {
		t.Fatalf("JSON render failed: %v", err)
	}
	if !strings.Contains(json, "Decisions") {
		t.Error("JSON render missing Decisions section")
	}
}

// ── MemorySelector tests ────────────────────────────────────────────────

func TestMemorySelectorEmpty(t *testing.T) {
	ms := DefaultMemorySelector()
	result := ms.Select(NewKnowledgeModel())
	if result == nil {
		t.Fatal("Select returned nil")
	}
	if len(result.Nodes) != 0 {
		t.Errorf("expected 0 candidates, got %d", len(result.Nodes))
	}
}

func TestMemorySelectorNil(t *testing.T) {
	ms := DefaultMemorySelector()
	result := ms.Select(nil)
	if result == nil {
		t.Fatal("Select returned nil for nil input")
	}
}

func TestMemorySelectorFilters(t *testing.T) {
	km := NewKnowledgeModel()
	_ = km.AddNode(&Node{ID: "d1", Type: NodeDecision, Confidence: 0.9})
	_ = km.AddNode(&Node{ID: "e1", Type: NodeEntity, Confidence: 0.3}) // Low confidence
	_ = km.AddNode(&Node{ID: "m1", Type: NodeMemory, Confidence: 0.9}) // Already stored

	ms := DefaultMemorySelector()
	result := ms.Select(km)
	if len(result.Nodes) != 1 {
		t.Errorf("expected 1 candidate (only decision), got %d: %+v", len(result.Nodes), result.Nodes)
	}
}

func TestMemorySelectorPriority(t *testing.T) {
	km := NewKnowledgeModel()
	_ = km.AddNode(&Node{ID: "d1", Type: NodeDecision, Confidence: 0.9, AccessCount: 5})
	_ = km.AddNode(&Node{ID: "f1", Type: NodeFact, Confidence: 0.8, AccessCount: 2})
	_ = km.AddNode(&Node{ID: "e1", Type: NodeEntity, Confidence: 0.7, AccessCount: 1})

	ms := DefaultMemorySelector()
	// Decision should score higher than Entity.
	scoreD := ms.Score(km.Nodes["d1"])
	scoreE := ms.Score(km.Nodes["e1"])
	if scoreD <= scoreE {
		t.Errorf("expected decision score (%f) > entity score (%f)", scoreD, scoreE)
	}
}

func TestMemorySelectorMaxCandidates(t *testing.T) {
	km := NewKnowledgeModel()
	for i := 0; i < 10; i++ {
		_ = km.AddNode(&Node{
			ID:         fmt.Sprintf("d%d", i),
			Type:       NodeDecision,
			Confidence: 0.8,
		})
	}

	ms := DefaultMemorySelector()
	ms.MaxCandidates = 3
	result := ms.Select(km)
	if len(result.Nodes) > 3 {
		t.Errorf("expected at most 3 candidates, got %d", len(result.Nodes))
	}
}

// ── Extended extraction tests ───────────────────────────────────────────

func TestAKGExtractorDecisions(t *testing.T) {
	ext := NewAKGExtractor()
	msgs := []SourceMessage{
		{
			ID:      "m1",
			Role:    "assistant",
			Content: "We chose RuntimePatch for the evolution system. We rejected hot-reload due to complexity.",
			TurnID:  "t1",
		},
	}
	entities, _, err := ext.Extract(context.Background(), msgs)
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	hasDecision := false
	hasRejection := false
	for _, e := range entities {
		if e.Type == "decision_choice" {
			hasDecision = true
		}
		if e.Type == "decision_rejection" {
			hasRejection = true
		}
	}
	if !hasDecision {
		t.Error("expected decision_choice entity")
	}
	if !hasRejection {
		t.Error("expected decision_rejection entity")
	}
}

func TestAKGExtractorConstraints(t *testing.T) {
	ext := NewAKGExtractor()
	msgs := []SourceMessage{
		{
			ID:      "m1",
			Role:    "assistant",
			Content: "The system must be secure. Cost cannot exceed budget. Latency is a requirement.",
			TurnID:  "t1",
		},
	}
	entities, _, err := ext.Extract(context.Background(), msgs)
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	hasConstraint := false
	for _, e := range entities {
		if e.Type == "constraint" {
			hasConstraint = true
			break
		}
	}
	if !hasConstraint {
		t.Error("expected constraint entity")
	}
}

func TestAKGExtractorTradeoffs(t *testing.T) {
	ext := NewAKGExtractor()
	msgs := []SourceMessage{
		{
			ID:      "m1",
			Role:    "assistant",
			Content: "There is a tradeoff between performance and memory usage. Using caching improves speed at the cost of RAM.",
			TurnID:  "t1",
		},
	}
	entities, _, err := ext.Extract(context.Background(), msgs)
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	hasTradeoff := false
	for _, e := range entities {
		if e.Type == "tradeoff" {
			hasTradeoff = true
			break
		}
	}
	if !hasTradeoff {
		t.Error("expected tradeoff entity")
	}
}

func TestAKGExtractorOpenQuestions(t *testing.T) {
	ext := NewAKGExtractor()
	msgs := []SourceMessage{
		{
			ID:      "m1",
			Role:    "assistant",
			Content: "TODO: implement the resolver. This is an open question about conflict resolution strategy.",
			TurnID:  "t1",
		},
	}
	entities, _, err := ext.Extract(context.Background(), msgs)
	if err != nil {
		t.Fatalf("Extract failed: %v", err)
	}

	hasQuestion := false
	for _, e := range entities {
		t.Logf("entity: %+v", e)
		if e.Type == "question" {
			hasQuestion = true
			break
		}
	}
	if !hasQuestion {
		t.Error("expected question entity")
	}
}

// ── Phase 2 Benchmarks ──────────────────────────────────────────────────

func BenchmarkPromptBuilder(b *testing.B) {
	km := NewKnowledgeModel()
	for i := 0; i < 50; i++ {
		_ = km.AddNode(&Node{
			ID:         fmt.Sprintf("d%d", i),
			Type:       NodeDecision,
			Confidence: 0.8,
			Attributes: map[string]any{"choice": fmt.Sprintf("Decision %d", i)},
		})
	}
	subGraph := km.ToSubGraph(NodeDecision)
	pb := NewPromptBuilder(DefaultPromptTemplate)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = pb.Render(subGraph, FormatMarkdown)
	}
}

func BenchmarkMemorySelector(b *testing.B) {
	km := NewKnowledgeModel()
	for i := 0; i < 100; i++ {
		_ = km.AddNode(&Node{
			ID:         fmt.Sprintf("n%d", i),
			Type:       NodeDecision,
			Confidence: 0.5 + float64(i)/200.0,
		})
	}
	ms := DefaultMemorySelector()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ms.Select(km)
	}
}
