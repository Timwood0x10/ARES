package ares_bootstrap

import (
	"context"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_memory/compiler"
	memorystore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
)

// wireKnowledgeCompiler conditionally wires the opt-in Conversation Compiler
// pipeline (design: CONVERSATION_COMPILER.md). It is a no-op unless
// cfg.KnowledgeCompiler.Enabled is true. Failures are non-fatal: they are
// logged and skipped, leaving the system running without the compiler pipeline
// (graceful degradation), mirroring wireDistillation's contract.
//
// The assembled pipeline is zero-LLM and deeply binds the existing modules:
//   - AKGExtractor   — rule-based extraction sharing AKG patterns.
//   - RuleNormalizer — rule-based alias / coreference / dedup normalization.
//   - KMDistiller    — reuses distillation.MemoryClassifier + ImportanceScorer.
//   - MemoryEmitter  — emits distillation.Memory records via a MemoryStore.
//   - AKGBuilder     — projects into knowledge.KnowledgeObject graphs.
//
// Stores default to in-memory implementations (graceful, no DB required) and
// remain pluggable via the Pipeline option pattern so a future iteration can
// swap in the shared AKG store or a persistent memory store without touching
// this wiring. No LLM chat/completion call is made anywhere in this path.
func wireKnowledgeCompiler(ctx context.Context, cfg *ares_config.Config, comp *Components) {
	if cfg == nil || !cfg.KnowledgeCompiler.Enabled {
		return
	}
	kc := cfg.KnowledgeCompiler

	// Compiler: AKG rule-based extractor + rule-based normalizer. Both are
	// entirely rule-based (zero LLM, zero embedding).
	kmCompiler := compiler.NewCompiler(
		compiler.NewAKGExtractor(),
		compiler.NewRuleNormalizer(),
		compiler.DefaultCompileConfig(),
	)

	// KMDistiller: zero-LLM bridge that reuses the distillation package's
	// classifier + scorer and clusters KM nodes into memory nodes.
	distiller := compiler.NewKMDistiller(compiler.WithMinScore(kc.DistillMinScore))

	pipelineCfg := compiler.PipelineConfig{
		MaxNodes:         kc.MaxNodes,
		MinConfidence:    kc.MinConfidence,
		PromptMaxTokens:  kc.PromptMaxTokens,
		AKGMaxFacts:      kc.AKGMaxFacts,
		AKGMinConfidence: kc.AKGMinConfidence,
		DistillMinScore:  kc.DistillMinScore,
	}

	// Consumers: in-memory stores as graceful defaults. The MemoryEmitter
	// persists distillation.Memory records; the AKGBuilder persists
	// knowledge.KnowledgeObject graphs. Both are swappable via options.
	memEmitter := compiler.NewMemoryEmitter(compiler.NewInMemoryMemoryStore())
	akgBuilder := compiler.NewAKGBuilder(memorystore.New())

	pipeline, err := compiler.NewPipeline(
		kmCompiler, distiller, pipelineCfg,
		compiler.WithMemoryEmitter(memEmitter),
		compiler.WithAKGBuilder(akgBuilder),
	)
	if err != nil {
		log.WarnContext(ctx, "bootstrap: knowledge compiler pipeline not wired", "error", err)
		return
	}

	lifecycle, err := compiler.NewContextLifecycle(kmCompiler, distiller, compiler.LifecycleConfig{
		WindowSize:          kc.WindowSize,
		Threshold:           kc.Threshold,
		MaxNodes:            kc.MaxNodes,
		MinConfidence:       kc.MinConfidence,
		DistillAfterCompile: kc.DistillAfterCompile,
	})
	if err != nil {
		log.WarnContext(ctx, "bootstrap: knowledge compiler lifecycle not wired", "error", err)
		return
	}

	comp.KnowledgeCompiler = &KnowledgeCompilerComponents{
		Pipeline:  pipeline,
		Lifecycle: lifecycle,
	}
	log.InfoContext(ctx, "bootstrap: knowledge compiler pipeline wired",
		"max_nodes", kc.MaxNodes,
		"prompt_max_tokens", kc.PromptMaxTokens,
		"window_size", kc.WindowSize,
		"distill_after_compile", kc.DistillAfterCompile)
}
