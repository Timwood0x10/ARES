// Package compiler — Pipeline is the end-to-end orchestrator that ties together
// the full coordinated flow described in CONVERSATION_COMPILER.md §5:
//
//	Conversation → Compiler → KM → Selector → Consumer
//	                                  └──→ KMDistiller (distill-and-prune)
//
// One Run produces a compiled KM, a distilled/pruned KM, a rendered prompt
// context, persisted memories, and an AKG projection — the complete
// compression + knowledge-graph + distillation flow. Every stage is zero-LLM.
package compiler

import (
	"context"
	"fmt"
)

// PipelineConfig configures a Pipeline's selector and consumer parameters.
type PipelineConfig struct {
	// MaxNodes caps KM node retention after pruning (0 = no limit).
	MaxNodes int
	// MinConfidence is the minimum confidence for node retention.
	MinConfidence float64
	// PromptMaxTokens is the token budget for the rendered prompt context.
	PromptMaxTokens int
	// AKGMinConfidence filters facts/references projected to the AKG.
	AKGMinConfidence float64
	// AKGMaxFacts caps the number of facts projected to the AKG (0 = no cap).
	AKGMaxFacts int
	// DistillMinScore is the minimum score for memory creation during pruning.
	DistillMinScore float64
}

// DefaultPipelineConfig returns sensible defaults for PipelineConfig.
func DefaultPipelineConfig() PipelineConfig {
	return PipelineConfig{
		MaxNodes:         500,
		MinConfidence:    0.3,
		PromptMaxTokens:  8000,
		AKGMinConfidence: 0.4,
		AKGMaxFacts:      200,
		DistillMinScore:  0.4,
	}
}

// Pipeline orchestrates the full Compiler → KM → Selector → Consumer → Distiller
// flow. Components are optional: a nil MemoryEmitter or AKGBuilder skips that
// consumer gracefully.
type Pipeline struct {
	compiler   *Compiler
	distiller  *KMDistiller
	memSel     *MemorySelector
	promptSel  *PromptSelector
	akgSel     *AKGSelector
	emitter    *MemoryEmitter
	akgBuilder *AKGBuilder
	builder    *PromptBuilder
	cfg        PipelineConfig
}

// PipelineOption configures a Pipeline at construction.
type PipelineOption func(*Pipeline)

// WithMemoryEmitter attaches a MemoryEmitter to the pipeline.
func WithMemoryEmitter(e *MemoryEmitter) PipelineOption {
	return func(p *Pipeline) { p.emitter = e }
}

// WithAKGBuilder attaches an AKGBuilder to the pipeline.
func WithAKGBuilder(b *AKGBuilder) PipelineOption {
	return func(p *Pipeline) { p.akgBuilder = b }
}

// NewPipeline creates a Pipeline with the given compiler, distiller, and config.
// Returns an error if compiler or distiller is nil.
//
// Args:
//
//	compiler - the Compiler used to compile messages (must not be nil).
//	distiller - the KMDistiller used to distill-and-prune (must not be nil).
//	cfg - PipelineConfig (zero values fall back to defaults).
//	opts - optional WithMemoryEmitter / WithAKGBuilder.
func NewPipeline(compiler *Compiler, distiller *KMDistiller, cfg PipelineConfig, opts ...PipelineOption) (*Pipeline, error) {
	if compiler == nil {
		return nil, fmt.Errorf("pipeline: compiler must not be nil")
	}
	if distiller == nil {
		return nil, fmt.Errorf("pipeline: distiller must not be nil")
	}
	if cfg.MaxNodes <= 0 {
		cfg = DefaultPipelineConfig()
	}
	p := &Pipeline{
		compiler:  compiler,
		distiller: distiller,
		memSel:    DefaultMemorySelector(),
		promptSel: NewPromptSelector(cfg.PromptMaxTokens, cfg.MaxNodes),
		akgSel:    NewAKGSelector(cfg.AKGMinConfidence, cfg.AKGMaxFacts),
		builder:   NewPromptBuilder(DefaultPromptTemplate),
		cfg:       cfg,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p, nil
}

// PipelineResult holds the outcome of a single Pipeline.Run.
type PipelineResult struct {
	Compile         *CompileResult
	Distill         *DistillResult
	PromptContext   string
	EmittedMemories int
	AKGObjects      int
	AKGRelations    int
	KM              *KnowledgeModel
}

// Run executes the full coordinated flow on a batch of source messages.
//
// Pipeline:
//  1. Compile messages into a KM (Extract → Normalize → Compile).
//  2. Select memory candidates and distill-and-prune the KM (zero-LLM).
//  3. Render the post-distill KM into a prompt context block.
//  4. Emit memory nodes to the MemoryStore (when configured).
//  5. Project entities/facts into the AKG (when configured).
//
// Args:
//
//	ctx - context for cancellation and timeout.
//	messages - source messages to process.
//	tenantID - tenant ID for memory emission.
//	userID - user ID for memory emission.
//	namespace - AKG namespace for projected objects.
//
// Returns:
//
//	*PipelineResult - the full outcome.
//	error - non-nil if a mandatory stage (compile/distill) fails.
func (p *Pipeline) Run(ctx context.Context, messages []SourceMessage, tenantID, userID, namespace string) (*PipelineResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("pipeline: context cancelled: %w", err)
	}
	if len(messages) == 0 {
		return nil, fmt.Errorf("pipeline: no messages to process")
	}

	res := &PipelineResult{}

	// Stage 1: Compile messages into a raw KM.
	cfg := CompileMode(nil, p.cfg.MaxNodes, p.cfg.MinConfidence)
	compileRes, err := p.compiler.Compile(ctx, messages, cfg)
	if err != nil {
		return nil, fmt.Errorf("pipeline: compile: %w", err)
	}
	res.Compile = compileRes
	km := compileRes.Model

	// Stage 2: Project to AKG on the RAW km — entities/facts are the backbone
	// and must be projected before distillation prunes them into memory nodes.
	if p.akgBuilder != nil {
		akgSub := p.akgSel.Select(km)
		akgRes, err := p.akgBuilder.Build(ctx, akgSub, namespace)
		if err != nil {
			return nil, fmt.Errorf("pipeline: build akg: %w", err)
		}
		res.AKGObjects = len(akgRes.Objects)
		res.AKGRelations = len(akgRes.Relations)
	}

	// Stage 3: Distill-and-prune (select candidates, then compress+prune).
	candidates := p.memSel.Select(km)
	distillRes, err := p.distiller.DistillSubGraph(ctx, km, candidates)
	if err != nil {
		return nil, fmt.Errorf("pipeline: distill: %w", err)
	}
	res.Distill = distillRes

	// Stage 4: Render prompt from the post-distill KM (memory nodes replace
	// pruned raw nodes, keeping the prompt compact).
	promptSub := p.promptSel.Select(km)
	promptCtx, err := p.builder.Render(promptSub, FormatMarkdown)
	if err != nil {
		return nil, fmt.Errorf("pipeline: render prompt: %w", err)
	}
	res.PromptContext = promptCtx

	// Stage 5: Emit memory nodes to the store (optional consumer).
	if p.emitter != nil {
		memorySub := selectMemoryNodes(km)
		emitted, err := p.emitter.Emit(ctx, memorySub, tenantID, userID)
		if err != nil {
			return nil, fmt.Errorf("pipeline: emit memories: %w", err)
		}
		res.EmittedMemories = emitted
	}

	res.KM = km
	return res, nil
}

// selectMemoryNodes returns a SubGraph containing only the NodeMemory nodes of
// a KM. Used by the emitter stage, which persists the compressed memories.
func selectMemoryNodes(km *KnowledgeModel) *SubGraph {
	if km == nil {
		return &SubGraph{}
	}
	var nodes []*Node
	for _, n := range km.Nodes {
		if n.Type == NodeMemory {
			nodes = append(nodes, n)
		}
	}
	return &SubGraph{
		Nodes: nodes,
		Metadata: map[string]any{
			attrSelector: "memory_nodes",
			"count":      len(nodes),
		},
	}
}
