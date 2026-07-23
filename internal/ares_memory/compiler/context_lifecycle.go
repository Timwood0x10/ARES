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
	// AKGMinConfidence filters facts/references projected to the shared AKG
	// pool (0 = no filter). Used only when an AKGBuilder is attached.
	AKGMinConfidence float64
	// AKGMaxFacts caps the number of facts projected to the shared AKG pool
	// (0 = no cap). Used only when an AKGBuilder is attached.
	AKGMaxFacts int
}

// DefaultLifecycleConfig returns sensible defaults for LifecycleConfig.
func DefaultLifecycleConfig() LifecycleConfig {
	return LifecycleConfig{
		WindowSize:          128000,
		Threshold:           0.7,
		MaxNodes:            500,
		MinConfidence:       0.3,
		DistillAfterCompile: true,
		AKGMinConfidence:    0.6,
		AKGMaxFacts:         200,
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

	akgBuilder *AKGBuilder
	akgSel     *AKGSelector
	namespace  string

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
	if cfg.AKGMinConfidence < 0 {
		cfg.AKGMinConfidence = DefaultLifecycleConfig().AKGMinConfidence
	}
	if cfg.AKGMaxFacts < 0 {
		cfg.AKGMaxFacts = DefaultLifecycleConfig().AKGMaxFacts
	}
	return &ContextLifecycle{
		compiler:  compiler,
		distiller: distiller,
		selector:  DefaultMemorySelector(),
		promptSel: NewPromptSelector(cfg.WindowSize, cfg.MaxNodes),
		builder:   NewPromptBuilder(DefaultPromptTemplate),
		akgSel:    NewAKGSelector(cfg.AKGMinConfidence, cfg.AKGMaxFacts),
		cfg:       cfg,
		model:     NewKnowledgeModel(),
	}, nil
}

// SetAKGBuilder optionally enables AKG projection: after each successful
// incremental compile, the compiled KM is projected into AKG KnowledgeObjects
// and persisted to the shared KnowledgeStore via the given builder. This closes
// the loop between the Conversation Compiler and the shared AKG pool — without
// it the lifecycle only kept an in-memory KM for prompt injection and the
// shared store stayed empty (a "wired but idle" gap). The namespace tags every
// projected object. Pass nil to disable AKG projection. Call once during wiring,
// before the lifecycle serves requests. It is guarded by the lifecycle mutex so
// it is safe to call before the first Compile.
func (cl *ContextLifecycle) SetAKGBuilder(b *AKGBuilder, namespace string) {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	cl.akgBuilder = b
	cl.namespace = namespace
}

// SetAKGMetrics attaches the L3 quality-gate metrics collector to the
// lifecycle's AKG selector. The same instance should be shared with the
// builder passed to SetAKGBuilder so a Compile accumulates one coherent
// snapshot. Call once during wiring, before the lifecycle serves requests.
// It is guarded by the lifecycle mutex so it is safe to call before the first
// Compile.
func (cl *ContextLifecycle) SetAKGMetrics(m *AKGMetrics) {
	if m == nil {
		return
	}
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.akgSel != nil {
		cl.akgSel.WithAKGMetrics(m)
	}
}

// Metrics returns the lifecycle's L3 quality-gate collector, or nil when none
// was configured. Callers read Snapshot() from it after a Compile to observe
// what the gate dropped.
func (cl *ContextLifecycle) Metrics() *AKGMetrics {
	cl.mu.Lock()
	defer cl.mu.Unlock()
	if cl.akgSel == nil {
		return nil
	}
	return cl.akgSel.metrics
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

	// Phase 5 (opt-in): project the compiled KM into the shared AKG pool. This
	// runs under the lifecycle mutex, so it serializes with concurrent Compile
	// and RenderPrompt calls, and the in-memory store write is itself
	// RWMutex-guarded. Failures are best-effort: the in-memory KM (used for
	// prompt injection) is already updated above, so AKG persistence must not
	// fail the compile. The AKG pipeline is concurrency-safe (shallow-copies
	// its input and locks its candidate pool), so sharing it with the runtime
	// is safe even if both call Process concurrently.
	if cl.akgBuilder != nil {
		akgSub := cl.akgSel.Select(cl.model)
		if _, bErr := cl.akgBuilder.Build(ctx, akgSub, cl.namespace); bErr != nil {
			el.WarnContext(ctx, "context lifecycle: akg projection failed", "error", bErr)
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
