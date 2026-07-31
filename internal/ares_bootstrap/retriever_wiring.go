// The storage/knowledge adapters (experience searcher, knowledge retriever
// adapter) live in internal/ares_memory/experienceadapters and are shared
// with the sdk layer, so the field mapping has a single source of truth.
package ares_bootstrap

import (
	"context"
	"fmt"

	aresconfig "github.com/Timwood0x10/ares/internal/ares_config"
	memctx "github.com/Timwood0x10/ares/internal/ares_memory/context"
	memembed "github.com/Timwood0x10/ares/internal/ares_memory/embedding"
	"github.com/Timwood0x10/ares/internal/ares_memory/experienceadapters"
	"github.com/Timwood0x10/ares/internal/knowledge/adapter"
	knowledgeruntime "github.com/Timwood0x10/ares/internal/knowledge/runtime"
	"github.com/Timwood0x10/ares/internal/storage/postgres/embedding"
	"github.com/Timwood0x10/ares/internal/storage/postgres/repositories"
)

// retrieverSetter is the minimal interface for injecting ContextRetrievers
// into a MemoryManager. Both *memoryManager and *ProductionMemoryManager
// satisfy it, but the public MemoryManager interface does not expose
// SetRetrievers (retrieval is an optional capability), so we type-assert at
// wiring time instead of widening the interface.
type retrieverSetter interface {
	SetRetrievers(retrievers []memctx.ContextRetriever)
}

// wireRetrievers constructs the MemoryRetriever and KnowledgeRetriever from
// the wired production dependencies and injects them into the MemoryManager.
//
// Wiring is best-effort and non-fatal: if a dependency is missing (e.g. no
// embedding client, no experience repo, no knowledge runtime) the
// corresponding retriever is skipped and a warning is logged. Retrieval
// only fires at runtime when the MemoryManager's config.EnableRAG is true,
// so callers still control the feature via config regardless of whether
// retrievers are wired.
//
// Args:
//
//	ctx        - bootstrap context, used for KnowledgeRetriever construction.
//	cfg        - full config; Memory.RAGMinScore tunes the memory retriever,
//	            Knowledge.MinScore tunes the knowledge retriever.
//	mem        - the MemoryManager; type-asserted to retrieverSetter.
//	embClient  - embedding client for query embedding. Nil skips memory retriever.
//	expRepo    - PostgreSQL experience repo. Nil skips memory retriever.
//	knowRt     - AKG KnowledgeRuntime. Nil skips knowledge retriever.
func wireRetrievers(
	ctx context.Context,
	cfg *aresconfig.Config,
	mem any,
	embClient *embedding.EmbeddingClient,
	expRepo repositories.ExperienceRepositoryInterface,
	knowRt *knowledgeruntime.KnowledgeRuntime,
) {
	setter, ok := mem.(retrieverSetter)
	if !ok {
		log.Warn("bootstrap: memory manager does not expose SetRetrievers; RAG wiring skipped",
			"type", fmt.Sprintf("%T", mem))
		return
	}

	var retrievers []memctx.ContextRetriever

	// Memory retriever: surfaces distilled experiences via vector search.
	// Requires both an embedding client (to embed the query) and an
	// experience repository (to search). Skipped silently when either is
	// nil — the distillation path may be disabled in minimal configs.
	if embClient != nil && expRepo != nil {
		minScore := cfg.Memory.RAGMinScore
		if minScore <= 0 {
			minScore = memctx.DefaultMinScore
		}
		pipeline, err := memembed.NewEmbeddingPipeline(embClient)
		if err != nil {
			log.Warn("bootstrap: memory retriever embedding pipeline failed; skipping",
				"error", err)
		} else {
			mr, err := memctx.NewMemoryRetriever(
				embClient,
				pipeline,
				experienceadapters.NewExperienceSearcher(expRepo),
				defaultDistillTenant,
				minScore,
			)
			if err != nil {
				log.Warn("bootstrap: memory retriever construction failed; skipping",
					"error", err)
			} else {
				retrievers = append(retrievers, mr)
				log.Info("bootstrap: memory retriever wired (distilled experiences → RAG)",
					"tenant", defaultDistillTenant, "min_score", minScore)
			}
		}
	}

	// Knowledge retriever: surfaces AKG entries via the KnowledgeRuntime.
	// Skipped when knowRt is nil (no AKG configured). The minScore is
	// sourced from Knowledge config; adapter.DefaultMinScore (0.4) applies
	// when zero.
	if knowRt != nil {
		minScore := cfg.Knowledge.MinScore
		kr, err := adapter.NewKnowledgeRetriever(ctx, knowRt, minScore)
		if err != nil {
			log.Warn("bootstrap: knowledge retriever construction failed; skipping",
				"error", err)
		} else {
			retrievers = append(retrievers, experienceadapters.NewKnowledgeRetrieverAdapter(kr))
			log.Info("bootstrap: knowledge retriever wired (AKG → RAG)",
				"min_score", minScore)
		}
	}

	if len(retrievers) == 0 {
		log.Info("bootstrap: no RAG retrievers wired (memory/knowledge deps unavailable)")
		return
	}

	setter.SetRetrievers(retrievers)
	log.Info("bootstrap: RAG retrievers injected into memory manager",
		"count", len(retrievers))
}
