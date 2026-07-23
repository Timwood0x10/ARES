package ares_bootstrap

import (
	"context"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_memory/compiler"
	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/provider/store"
	memorystore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
)

// compilerAKGNamespace tags every KnowledgeObject the Conversation Compiler
// projects into the shared AKG pool, so consumers can scope queries to the
// compiler's contributions.
const compilerAKGNamespace = "conversation-compiler"

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

	// Shared AKG sink: a single KnowledgeStore instance the compiler writes
	// into. Holding it on Components makes it the shared pool that other AKG
	// consumers (prompt injection, future runtime ingestion) read from,
	// replacing the previous per-build isolated store that no other consumer
	// could see.
	sharedAKGStore := memorystore.New()
	comp.KnowledgeStore = sharedAKGStore

	// Close the read-side gap the review flagged as drift: the shared store the
	// compiler writes into was previously a dead-end cache — nothing read it
	// (the AKG data path is Provider -> Pipeline -> KnowledgeRuntime and
	// bypasses Store entirely). Registering a store-backed provider into the
	// SAME KnowledgeRuntime the agent's AKF tools and evolution system use
	// makes the compiler's persisted objects flow into live retrieval. The
	// store instance is identical to the one comp.KnowledgeStore points at, so
	// incremental compiles become visible to AKG queries. Non-fatal: a failure
	// here leaves the store writable but unread, degrading gracefully.
	if comp.KnowledgeRuntime != nil {
		storeProv, spErr := store.NewStoreProvider(sharedAKGStore, store.Config{
			Name:       "conversation-compiler-store",
			Namespace:  compilerAKGNamespace,
			IntentTags: []string{"conversation", "recall", "agent", "context", "knowledge"},
			Limit:      200,
		})
		if spErr != nil {
			log.WarnContext(ctx, "bootstrap: knowledge compiler store provider not created", "error", spErr)
		} else if err := comp.KnowledgeRuntime.RegisterProvider(storeProv); err != nil {
			log.WarnContext(ctx, "bootstrap: knowledge compiler store provider not registered", "error", err)
		}
	}

	// Reuse AKG's shared KnowledgePipeline — the exact instance the
	// KnowledgeRuntime uses — so the compiler's projections are refined by
	// AKG's Normalizer → Resolver → Summarizer and share the same entity
	// resolution pool. This closes broken-link-2 from the review: the builder
	// no longer writes coarse node summaries straight to the store. When the
	// runtime is unavailable the pipeline is nil and the builder degrades to
	// its previous build-only-direct behavior.
	var akgPipeline *knowledge.KnowledgePipeline
	if comp.KnowledgeRuntime != nil {
		akgPipeline = comp.KnowledgeRuntime.Pipeline()
	}

	// Threshold 0 → Resolver default (0.85 Jaccard). The resolver dedupes the
	// compiler's projections against what is already persisted in the shared
	// store, so the live path and any other producer stay free of near-dupes.
	akgBuilder := compiler.NewAKGBuilder(sharedAKGStore).
		WithAKGPipeline(akgPipeline).
		WithResolver(compiler.NewResolver(sharedAKGStore, 0))

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
		AKGMinConfidence:    kc.AKGMinConfidence,
		AKGMaxFacts:         kc.AKGMaxFacts,
	})
	if err != nil {
		log.WarnContext(ctx, "bootstrap: knowledge compiler lifecycle not wired", "error", err)
		return
	}

	// Close the loop the review flagged as "wired but idle": the incremental
	// lifecycle now also persists its compiled KM into the shared AKG pool via
	// the same builder the one-shot Pipeline uses, so comp.KnowledgeStore is
	// no longer left empty.
	lifecycle.SetAKGBuilder(akgBuilder, compilerAKGNamespace)

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
