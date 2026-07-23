// Package compiler — ContextLifecycle implements the Context Lifecycle (design
// §7): it tracks the current Knowledge Model version, triggers incremental
// compilation when the token-budget threshold is exceeded, and runs
// distill-and-prune to keep the KM bounded. Every operation is zero-LLM.
package compiler

import (
	"context"
	"fmt"
	"sync"
)

// LifecycleConfig configures the ContextLifecycle trigger and pruning behavior.
type LifecycleConfig struct {
	// WindowSize is the total token budget window (e.g. 128000).
	WindowSize int
	// Threshold is the compile trigger as a fraction of WindowSize (e.g. 0.7).
	Threshold float64
	// MaxNodes is the maximum nodes retained after pruning (0 = no limit).
	MaxNodes int
	// MinConfidence is the minimum confidence for node retention.
	MinConfidence float64
	// DistillAfterCompile runs distill-and-prune after each successful compile.
	DistillAfterCompile bool
}

// DefaultLifecycleConfig returns sensible defaults for LifecycleConfig.
func DefaultLifecycleConfig() LifecycleConfig {
	return LifecycleConfig{
		WindowSize:          128000,
		Threshold:           0.7,
		MaxNodes:            500,
		MinConfidence:       0.3,
		DistillAfterCompile: true,
	}
}

// ContextLifecycle manages the Context Lifecycle. It owns the current KM
// version and orchestrates incremental compilation and distill-and-prune. It is
// safe for concurrent use: the mutex serializes Compile/RenderPrompt against the
// shared model. All operations are zero-LLM.
type ContextLifecycle struct {
	compiler  *Compiler
	distiller *KMDistiller
	selector  *MemorySelector
	promptSel *PromptSelector
	builder   *PromptBuilder
	cfg       LifecycleConfig

	mu           sync.Mutex
	model        *KnowledgeModel
	compileCount int
}

// NewContextLifecycle creates a ContextLifecycle. Returns an error if compiler
// or distiller is nil.
//
// Args:
//
//	compiler - the Compiler used to compile new messages (must not be nil).
//	distiller - the KMDistiller used to distill-and-prune (must not be nil).
//	cfg - LifecycleConfig (zero values fall back to defaults).
func NewContextLifecycle(compiler *Compiler, distiller *KMDistiller, cfg LifecycleConfig) (*ContextLifecycle, error) {
	if compiler == nil {
		return nil, fmt.Errorf("context lifecycle: compiler must not be nil")
	}
	if distiller == nil {
		return nil, fmt.Errorf("context lifecycle: distiller must not be nil")
	}
	if cfg.WindowSize <= 0 {
		cfg.WindowSize = DefaultLifecycleConfig().WindowSize
	}
	if cfg.Threshold <= 0 || cfg.Threshold > 1 {
		cfg.Threshold = DefaultLifecycleConfig().Threshold
	}
	if cfg.MaxNodes <= 0 {
		cfg.MaxNodes = DefaultLifecycleConfig().MaxNodes
	}
	if cfg.MinConfidence <= 0 {
		cfg.MinConfidence = DefaultLifecycleConfig().MinConfidence
	}
	return &ContextLifecycle{
		compiler:  compiler,
		distiller: distiller,
		selector:  DefaultMemorySelector(),
		promptSel: NewPromptSelector(cfg.WindowSize, cfg.MaxNodes),
		builder:   NewPromptBuilder(DefaultPromptTemplate),
		cfg:       cfg,
		model:     NewKnowledgeModel(),
	}, nil
}

// ShouldCompile reports whether the given messages exceed the token-budget
// threshold, indicating that an incremental compile should be triggered.
func (cl *ContextLifecycle) ShouldCompile(messages []SourceMessage) bool {
	return ShouldCompile(messages, cl.cfg.WindowSize, cl.cfg.Threshold)
}

// Compile triggers an incremental compile of new messages into the current KM,
// then optionally distills-and-prunes the result. The current KM is updated to
// the compiled (and distilled) model.
//
// Args:
//
//	ctx - context for cancellation and timeout.
//	messages - new source messages to compile.
//
// Returns:
//
//	*CompileResult - the compile stats and model.
//	*DistillResult - the distill-and-prune outcome (nil when disabled).
//	error - non-nil if compile or distill fails.
func (cl *ContextLifecycle) Compile(ctx context.Context, messages []SourceMessage) (*CompileResult, *DistillResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, fmt.Errorf("context lifecycle: context cancelled: %w", err)
	}
	if len(messages) == 0 {
		return nil, nil, fmt.Errorf("context lifecycle: no messages to compile")
	}

	cl.mu.Lock()
	defer cl.mu.Unlock()

	cfg := CompileMode(cl.model, cl.cfg.MaxNodes, cl.cfg.MinConfidence)
	compileRes, err := cl.compiler.Compile(ctx, messages, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("context lifecycle: compile: %w", err)
	}
	cl.model = compileRes.Model
	cl.compileCount++

	var distillRes *DistillResult
	if cl.cfg.DistillAfterCompile {
		candidates := cl.selector.Select(cl.model)
		distillRes, err = cl.distiller.DistillSubGraph(ctx, cl.model, candidates)
		if err != nil {
			return compileRes, nil, fmt.Errorf("context lifecycle: distill: %w", err)
		}
	}
	return compileRes, distillRes, nil
}

// CurrentModel returns the current Knowledge Model. The returned reference is
// the LIVE model that Compile mutates in place during incremental compiles, so
// callers must NOT read it concurrently with a Compile call — use RenderPrompt
// (which serializes against Compile) for a concurrency-safe view of the prompt
// context. Callers must not mutate the returned model; use Compile to advance it.
func (cl *ContextLifecycle) CurrentModel() *KnowledgeModel {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return cl.model
}

// CompileCount returns the number of successful compiles since construction.
func (cl *ContextLifecycle) CompileCount() int {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	return cl.compileCount
}

// RenderPrompt renders the current KM into a prompt context block via the
// PromptSelector and PromptBuilder. The selection reflects the post-distill KM
// (memory nodes replace pruned raw nodes).
//
// Args:
//
//	ctx - context (reserved; rendering is currently synchronous).
//	format - output format (Markdown, XML, or JSON).
//
// Returns:
//
//	string - the rendered prompt context block.
//	error - non-nil if rendering fails.
func (cl *ContextLifecycle) RenderPrompt(_ context.Context, format PromptFormat) (string, error) {
	cl.mu.Lock()
	defer cl.mu.Unlock()

	// Hold the lock through Select+Render: in incremental mode Compile reuses
	// the same *KnowledgeModel pointer and mutates it in place (AddNode/AddEdge/
	// Prune write to model.Nodes/Edges). Reading those fields without the lock
	// would race with a concurrent Compile (concurrent map read+write is a fatal
	// runtime panic). Render is synchronous and pure, so holding the lock here
	// is safe and honors the documented "serializes Compile/RenderPrompt"
	// contract.
	sub := cl.promptSel.Select(cl.model)
	rendered, err := cl.builder.Render(sub, format)
	if err != nil {
		return "", fmt.Errorf("context lifecycle: render prompt: %w", err)
	}
	return rendered, nil
}
