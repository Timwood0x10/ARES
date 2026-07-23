// Package compiler integration tests for ContextLifecycle and Pipeline — the
// end-to-end Compiler → KM → Selector → Consumer → Distiller flow.
package compiler

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// sampleMessages returns a small conversation that the rule-based AKGExtractor
// can mine for entities, facts, decisions, and constraints (zero LLM).
func sampleMessages() []SourceMessage {
	now := time.Now()
	return []SourceMessage{
		{
			ID: "m1", Role: "user", Timestamp: now,
			Content: "We chose Patch to fix errors in ARES. ARES uses Patch for runtime updates.",
		},
		{
			ID: "m2", Role: "assistant", Timestamp: now,
			Content: "Understood. The ARES system must not use hot reload. This is a requirement.",
		},
	}
}

// newTestCompiler builds a real Compiler wired with the zero-LLM AKG extractor
// and rule normalizer.
func newTestCompiler(t *testing.T) *Compiler {
	t.Helper()
	c := NewCompiler(NewAKGExtractor(), NewRuleNormalizer(), DefaultCompileConfig())
	return c
}

func TestContextLifecycleNilDepsError(t *testing.T) {
	if _, err := NewContextLifecycle(nil, NewKMDistiller(), DefaultLifecycleConfig()); err == nil {
		t.Error("expected error for nil compiler")
	}
	if _, err := NewContextLifecycle(newTestCompiler(t), nil, DefaultLifecycleConfig()); err == nil {
		t.Error("expected error for nil distiller")
	}
}

func TestContextLifecycleShouldCompile(t *testing.T) {
	cl, err := NewContextLifecycle(newTestCompiler(t), NewKMDistiller(WithMinScore(0.3)), LifecycleConfig{
		WindowSize: 100, Threshold: 0.7,
	})
	if err != nil {
		t.Fatalf("NewContextLifecycle: %v", err)
	}
	// Tiny message: ~3 tokens, far below 70-token threshold.
	if cl.ShouldCompile(sampleMessages()[:1]) {
		t.Error("expected ShouldCompile=false for tiny message")
	}
	// Large message: exceeds threshold.
	big := []SourceMessage{{ID: "big", Role: "user", Content: strings.Repeat("ARES uses Patch ", 200), Timestamp: time.Now()}}
	if !cl.ShouldCompile(big) {
		t.Error("expected ShouldCompile=true for large message")
	}
}

func TestContextLifecycleCompileAndRender(t *testing.T) {
	cl, err := NewContextLifecycle(newTestCompiler(t), NewKMDistiller(WithMinScore(0.3)), LifecycleConfig{
		WindowSize: 1, Threshold: 0.0, MaxNodes: 500, MinConfidence: 0.3, DistillAfterCompile: true,
	})
	if err != nil {
		t.Fatalf("NewContextLifecycle: %v", err)
	}

	compileRes, distillRes, err := cl.Compile(context.Background(), sampleMessages())
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if compileRes == nil || compileRes.Stats.NodesCreated == 0 {
		t.Fatalf("expected compiled nodes, got %+v", compileRes)
	}
	if distillRes == nil {
		t.Fatal("expected distill result when DistillAfterCompile=true")
	}
	if cl.CompileCount() != 1 {
		t.Errorf("expected compile count 1, got %d", cl.CompileCount())
	}
	if cl.CurrentModel() == nil {
		t.Error("expected non-nil current model")
	}

	prompt, err := cl.RenderPrompt(context.Background(), FormatMarkdown)
	if err != nil {
		t.Fatalf("RenderPrompt: %v", err)
	}
	if !strings.Contains(prompt, "## Context") {
		t.Errorf("expected markdown context header, got %q", prompt)
	}
}

func TestContextLifecycleIncrementalMergesModel(t *testing.T) {
	cl, err := NewContextLifecycle(newTestCompiler(t), NewKMDistiller(WithMinScore(0.3)), LifecycleConfig{
		WindowSize: 1, Threshold: 0.0, MaxNodes: 500, MinConfidence: 0.3, DistillAfterCompile: false,
	})
	if err != nil {
		t.Fatalf("NewContextLifecycle: %v", err)
	}

	// First compile.
	if _, _, err := cl.Compile(context.Background(), sampleMessages()); err != nil {
		t.Fatalf("first compile: %v", err)
	}
	firstCount := cl.CurrentModel().NodeCount()

	// Second compile with new messages merges into the existing model.
	more := []SourceMessage{{ID: "m3", Role: "user", Content: "Go implements concurrency. Go uses goroutines.", Timestamp: time.Now()}}
	if _, _, err := cl.Compile(context.Background(), more); err != nil {
		t.Fatalf("second compile: %v", err)
	}
	if cl.CompileCount() != 2 {
		t.Errorf("expected compile count 2, got %d", cl.CompileCount())
	}
	// Model should still have nodes (incremental, not reset).
	if cl.CurrentModel().NodeCount() == 0 {
		t.Error("expected non-empty model after incremental compile")
	}
	_ = firstCount
}

func TestContextLifecycleCancelledContext(t *testing.T) {
	cl, err := NewContextLifecycle(newTestCompiler(t), NewKMDistiller(), DefaultLifecycleConfig())
	if err != nil {
		t.Fatalf("NewContextLifecycle: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := cl.Compile(ctx, sampleMessages()); err == nil {
		t.Error("expected error for cancelled context")
	}
}

func TestPipelineNilDepsError(t *testing.T) {
	if _, err := NewPipeline(nil, NewKMDistiller(), DefaultPipelineConfig()); err == nil {
		t.Error("expected error for nil compiler")
	}
	if _, err := NewPipeline(newTestCompiler(t), nil, DefaultPipelineConfig()); err == nil {
		t.Error("expected error for nil distiller")
	}
}

func TestPipelineRunFullFlow(t *testing.T) {
	p, err := NewPipeline(newTestCompiler(t), NewKMDistiller(WithMinScore(0.3)), PipelineConfig{
		MaxNodes: 500, MinConfidence: 0.3, PromptMaxTokens: 8000,
		AKGMinConfidence: 0.3, AKGMaxFacts: 100, DistillMinScore: 0.3,
	})
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}

	res, err := p.Run(context.Background(), sampleMessages(), "tenant-1", "user-1", "default")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Compile == nil || res.Compile.Stats.NodesCreated == 0 {
		t.Fatalf("expected compiled nodes, got %+v", res.Compile)
	}
	if res.Distill == nil {
		t.Error("expected distill result")
	}
	if res.PromptContext == "" {
		t.Error("expected non-empty prompt context")
	}
	if res.KM == nil {
		t.Error("expected non-nil KM")
	}
}

func TestPipelineRunWithConsumers(t *testing.T) {
	store := NewInMemoryMemoryStore()
	emitter := NewMemoryEmitter(store)
	builder := NewAKGBuilder(nil) // build-only, no backing store

	p, err := NewPipeline(newTestCompiler(t), NewKMDistiller(WithMinScore(0.3)), DefaultPipelineConfig(),
		WithMemoryEmitter(emitter), WithAKGBuilder(builder))
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}

	res, err := p.Run(context.Background(), sampleMessages(), "tenant-1", "user-1", "default")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// At least one memory should be emitted when distill produces memory nodes.
	if res.EmittedMemories == 0 && res.Distill.MemoryNodesCreated > 0 {
		t.Errorf("expected emitted memories > 0 (created=%d), got %d",
			res.Distill.MemoryNodesCreated, res.EmittedMemories)
	}
	// AKG projection should produce objects when entities survive.
	if res.AKGObjects == 0 {
		t.Error("expected AKG objects > 0")
	}
	// The in-memory store should reflect emissions.
	if store.All() == nil {
		t.Error("expected in-memory store to hold memories")
	}
}

func TestPipelineRunEmptyMessagesError(t *testing.T) {
	p, err := NewPipeline(newTestCompiler(t), NewKMDistiller(), DefaultPipelineConfig())
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	if _, err := p.Run(context.Background(), nil, "t", "u", "ns"); err == nil {
		t.Error("expected error for empty messages")
	}
}

// TestContextLifecycleConcurrentCompileAndRender guards against the data race
// where RenderPrompt reads the shared model while Compile mutates it in place.
// Run with -race, this test fails (fatal "concurrent map read and map write")
// if RenderPrompt releases the lock before reading the model. It builds a
// non-empty model first so subsequent compiles take the incremental in-place
// mutation path (CompileMode sets PreviousModel = cl.model).
func TestContextLifecycleConcurrentCompileAndRender(t *testing.T) {
	t.Parallel()
	cl, err := NewContextLifecycle(newTestCompiler(t), NewKMDistiller(WithMinScore(0.3)), LifecycleConfig{
		WindowSize:          100,
		Threshold:           0.01,
		MaxNodes:            1000,
		MinConfidence:       0.0,
		DistillAfterCompile: true,
	})
	if err != nil {
		t.Fatalf("NewContextLifecycle: %v", err)
	}

	// Seed the model so the second compile takes the incremental (in-place) path.
	if _, _, err := cl.Compile(context.Background(), sampleMessages()); err != nil {
		t.Fatalf("seed compile: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)

	// Writer: repeatedly compiles new messages, mutating cl.model in place.
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			msgs := []SourceMessage{{
				ID: "c", Role: "user", Timestamp: time.Now(),
				Content: "ARES uses Patch for runtime updates. Go implements concurrency.",
			}}
			_, _, _ = cl.Compile(ctx, msgs)
		}
	}()

	// Reader: repeatedly renders the prompt, reading cl.model under the lock.
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			if _, err := cl.RenderPrompt(ctx, FormatMarkdown); err != nil {
				t.Errorf("RenderPrompt: %v", err)
				return
			}
		}
	}()

	wg.Wait()
}
