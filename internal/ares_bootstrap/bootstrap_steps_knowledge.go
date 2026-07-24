package ares_bootstrap

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_memory/compiler"
	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/provider/store"
	memorystore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
	postgresstore "github.com/Timwood0x10/ares/internal/knowledge/store/postgres"
	sqlitestore "github.com/Timwood0x10/ares/internal/knowledge/store/sqlite"
)

// compilerAKGNamespace tags every KnowledgeObject the Conversation Compiler
// projects into the shared AKG pool, so consumers can scope queries to the
// compiler's contributions.
const compilerAKGNamespace = "conversation-compiler"

// storageTypePostgres is the cfg.Storage.Type value selecting PostgreSQL.
const storageTypePostgres = "postgres"

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
func wireKnowledgeCompiler(ctx context.Context, cfg *ares_config.Config, comp *Components, cleanups *[]func()) {
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
	//
	// Phase 2 (AKG_CLOSURE_PLAN.md): the sink is now selected by config
	// (cfg.KnowledgeCompiler.AKGStore) — postgres for durable cross-replica
	// persistence, sqlite for a lightweight durable node, or in-memory for a
	// session-scoped graph. The DB handle (when any) is registered for
	// graceful shutdown via cleanups so connections are not leaked.
	sharedAKGStore, storeCleanup, storeErr := newSharedAKGStore(ctx, cfg)
	if storeErr != nil {
		// Graceful degradation: a persistence backend that fails to open must
		// never crash bootstrap. Fall back to in-memory so the pipeline still
		// runs (graph survives the process but is lost on restart).
		log.WarnContext(ctx, "bootstrap: knowledge compiler AKG store unavailable, using in-memory fallback", "error", storeErr)
		sharedAKGStore = memorystore.New()
	}
	if storeCleanup != nil {
		*cleanups = append(*cleanups, storeCleanup)
	}
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

	// TODO(tech-debt): vector-backed GraphProvider for the AKF knowledge graph.
	// The removed internal/knowledge/provider/vector package was meant to wrap
	// storage.VectorStore into a GraphProvider so a configured vector backend
	// (pgvector / Qdrant / Milvus / …) could flow its contents into AKF
	// semantic context via Runtime.RegisterProvider. It was a stub (random
	// query vectors, no real embeddings) and was never wired into any serve
	// path, so it was deleted. The pluggable VectorStore contract itself
	// (storage.VectorStore interface + the compat/vector registry) is
	// untouched and still lets external users swap vector backends freely.
	// A future iteration should add a real provider here (with real
	// embeddings) IF AKF needs vector-store-derived context.

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

	// L3 observability: a single shared quality-gate collector fed by the
	// selector, builder, and resolver of this pipeline, so a run produces one
	// coherent snapshot (akg_objects_in / dropped_lowconf / dropped_structural
	// / dedup_hits / objects_built + confidence histogram + signal tiers).
	akgMetrics := compiler.NewAKGMetrics()

	// Threshold 0 → Resolver default (0.85 Jaccard). The resolver dedupes the
	// compiler's projections against what is already persisted in the shared
	// store, so the live path and any other producer stay free of near-dupes.
	akgBuilder := compiler.NewAKGBuilder(sharedAKGStore).
		WithAKGPipeline(akgPipeline).
		WithResolver(compiler.NewResolver(sharedAKGStore, 0).WithMetrics(akgMetrics)).
		WithQualityGate(true).  // drop structurally invalid nodes from the AKG graph
		WithMetrics(akgMetrics) // L3: record structural drops + object histogram

	pipeline, err := compiler.NewPipeline(
		kmCompiler, distiller, pipelineCfg,
		compiler.WithMemoryEmitter(memEmitter),
		compiler.WithAKGBuilder(akgBuilder),
		compiler.WithAKGMetrics(akgMetrics),
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
	lifecycle.SetAKGMetrics(akgMetrics) // L3: shared collector for incremental compiles

	comp.KnowledgeCompiler = &KnowledgeCompilerComponents{
		Pipeline:   pipeline,
		Lifecycle:  lifecycle,
		AKGMetrics: akgMetrics,
	}
	log.InfoContext(ctx, "bootstrap: knowledge compiler pipeline wired",
		"max_nodes", kc.MaxNodes,
		"prompt_max_tokens", kc.PromptMaxTokens,
		"window_size", kc.WindowSize,
		"distill_after_compile", kc.DistillAfterCompile)
}

// newSharedAKGStore selects and constructs the shared KnowledgeStore the
// Conversation Compiler projects its AKG objects into (plan Phase 2).
//
// Backend selection (cfg.KnowledgeCompiler.AKGStore):
//   - "auto" (default): postgres when cfg.Storage is a ready PostgreSQL
//     deployment, otherwise in-memory (preserves prior behavior).
//   - "memory":   session-scoped in-memory pool.
//   - "sqlite":   durable single-node pool at cfg.KnowledgeCompiler.AKGSQLitePath.
//   - "postgres": durable shared pool in the akf_objects table.
//
// The returned cleanup closes the underlying *sql.DB when a durable backend
// was opened; it is nil for the in-memory backend. On any backend failure the
// caller is expected to fall back to in-memory (graceful degradation).
func newSharedAKGStore(ctx context.Context, cfg *ares_config.Config) (knowledge.KnowledgeStore, func(), error) {
	kc := cfg.KnowledgeCompiler
	mode := kc.AKGStore
	// An empty mode means the config skipped normalization (e.g. constructed
	// directly in tests); treat it as "auto" instead of guessing.
	if mode == "" || mode == ares_config.AKGStoreAuto {
		// Infer from the global storage deployment: a ready PostgreSQL
		// deployment makes the AKG graph durable and cross-replica.
		if cfg.Storage.Enabled && cfg.Storage.Type == storageTypePostgres && cfg.Storage.Host != "" {
			mode = ares_config.AKGStorePostgres
		} else {
			mode = ares_config.AKGStoreMemory
		}
	}

	switch mode {
	case ares_config.AKGStoreMemory:
		return memorystore.New(), nil, nil
	case ares_config.AKGStorePostgres:
		return newPostgresAKGStore(ctx, cfg)
	case ares_config.AKGStoreSQLite:
		return newSQLiteAKGStore(kc.AKGSQLitePath)
	default:
		// Defensive: an unknown mode should never reach here (validated at
		// config load), but degrade gracefully instead of crashing bootstrap.
		log.WarnContext(ctx, "bootstrap: unknown akg_store mode, falling back to memory", "mode", mode)
		return memorystore.New(), nil, nil
	}
}

// newPostgresAKGStore opens the shared PostgreSQL KnowledgeStore for the AKG
// pool. It reuses the global storage DSN and registers the akf_objects schema
// via the store's own migration (postgresstore.New). The cleanup closes the
// *sql.DB on shutdown.
func newPostgresAKGStore(ctx context.Context, cfg *ares_config.Config) (knowledge.KnowledgeStore, func(), error) {
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Storage.Host, cfg.Storage.Port, cfg.Storage.Username,
		cfg.Storage.Password, cfg.Storage.Database, cfg.Storage.SSLMode)
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("akg postgres store: open db: %w", err)
	}
	pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
	defer pingCancel()
	if err := db.PingContext(pingCtx); err != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, nil, fmt.Errorf("akg postgres store: ping: %w (also close db: %v)", err, closeErr)
		}
		return nil, nil, fmt.Errorf("akg postgres store: ping: %w", err)
	}
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(5 * time.Minute)

	store, err := postgresstore.New(db)
	if err != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, nil, fmt.Errorf("akg postgres store: init: %w (also close db: %v)", err, closeErr)
		}
		return nil, nil, fmt.Errorf("akg postgres store: init: %w", err)
	}
	cleanup := func() {
		if cErr := db.Close(); cErr != nil {
			log.WarnContext(context.Background(), "bootstrap: close akg postgres store", "error", cErr)
		}
	}
	return store, cleanup, nil
}

// newSQLiteAKGStore opens a durable single-node SQLite KnowledgeStore for the
// AKG pool. The directory is created on demand so a relative AKGSQLitePath
// (e.g. "data/akg.db") works from the process working directory. The cleanup
// closes the *sql.DB on shutdown.
func newSQLiteAKGStore(dbPath string) (knowledge.KnowledgeStore, func(), error) {
	if dbPath == "" {
		return nil, nil, fmt.Errorf("akg sqlite store: path must not be empty")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o750); err != nil {
		return nil, nil, fmt.Errorf("akg sqlite store: make dir %q: %w", filepath.Dir(dbPath), err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, nil, fmt.Errorf("akg sqlite store: open %q: %w", dbPath, err)
	}
	db.SetMaxOpenConns(1) // SQLite only supports a single writer.
	db.SetMaxIdleConns(1)

	store, err := sqlitestore.NewWithDB(db)
	if err != nil {
		if closeErr := db.Close(); closeErr != nil {
			return nil, nil, fmt.Errorf("akg sqlite store: init: %w (also close db: %v)", err, closeErr)
		}
		return nil, nil, fmt.Errorf("akg sqlite store: init: %w", err)
	}
	cleanup := func() {
		if cErr := db.Close(); cErr != nil {
			log.WarnContext(context.Background(), "bootstrap: close akg sqlite store", "error", cErr)
		}
	}
	return store, cleanup, nil
}
