// Package ares_bootstrap — RAG retriever wiring.
//
// This file closes the compression + AKG + memory-distillation loop by
// constructing the two ContextRetrievers (MemoryRetriever for distilled
// experiences, KnowledgeRetriever for AKG entries) and injecting them into
// the MemoryManager via SetRetrievers. Once wired, every BuildContext /
// BuildPromptMessages call transparently augments the LLM prompt with
// retrieved context when config.EnableRAG is true.
//
// Two adapters live here because the in-tree retriever types do not line up
// directly with the production storage types:
//
//   - pgExperienceSearcher adapts repositories.ExperienceRepositoryInterface
//     (returns *storage_models.Experience) to context.ExperienceSearcher
//     (returns distillation.Experience). The MemoryRetriever only reads, so
//     the narrow ExperienceSearcher interface is sufficient.
//   - knowledgeRetrieverAdapter adapts adapter.KnowledgeRetriever (returns
//     adapter.ContextSnippet, a local type that avoids an import cycle) to
//     memctx.ContextRetriever (returns memctx.ContextSnippet, the canonical
//     type consumed by the context builder).
package ares_bootstrap

import (
	"context"
	"fmt"

	aresconfig "github.com/Timwood0x10/ares/internal/ares_config"
	memctx "github.com/Timwood0x10/ares/internal/ares_memory/context"
	"github.com/Timwood0x10/ares/internal/ares_memory/distillation"
	memembed "github.com/Timwood0x10/ares/internal/ares_memory/embedding"
	"github.com/Timwood0x10/ares/internal/knowledge/adapter"
	knowledgeruntime "github.com/Timwood0x10/ares/internal/knowledge/runtime"
	"github.com/Timwood0x10/ares/internal/storage/postgres/embedding"
	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
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

// pgExperienceSearcher adapts the PostgreSQL experience repository to the
// context.ExperienceSearcher interface expected by MemoryRetriever.
//
// The postgres repository returns *storage_models.Experience (the storage
// DTO with backward-compat Input/Output aliases and metadata), while the
// retriever consumes distillation.Experience (the canonical api/experience
// DTO). This adapter performs the field mapping on every SearchByVector
// call so the retriever stays storage-agnostic.
//
// The underlying repository is responsible for its own concurrency safety;
// this adapter holds no mutable state and is safe for concurrent use.
type pgExperienceSearcher struct {
	repo repositories.ExperienceRepositoryInterface
}

// SearchByVector delegates to the PostgreSQL repository and converts each
// storage_models.Experience into a distillation.Experience. Entries with a
// blank ID are dropped defensively — they cannot be referenced later and
// would only add noise to the prompt.
func (s *pgExperienceSearcher) SearchByVector(
	ctx context.Context,
	vector []float64,
	tenantID string,
	limit int,
) ([]distillation.Experience, error) {
	if s == nil || s.repo == nil {
		return nil, fmt.Errorf("pg experience searcher: repository is nil")
	}
	storageExps, err := s.repo.SearchByVector(ctx, vector, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("pg experience searcher: %w", err)
	}

	out := make([]distillation.Experience, 0, len(storageExps))
	for _, se := range storageExps {
		if se == nil || se.ID == "" {
			continue
		}
		out = append(out, toDistillationExperience(se))
	}
	return out, nil
}

// toDistillationExperience maps a storage_models.Experience into the
// canonical distillation.Experience DTO. Problem/Solution fall back to the
// legacy Input/Output fields when the high-level fields are empty (the
// storage layer stores them in the 'input'/'output' columns for backward
// compat). Confidence is clamped to [0, 1] so downstream filtering operates
// on a well-defined domain.
func toDistillationExperience(e *storage_models.Experience) distillation.Experience {
	problem := e.Problem
	if problem == "" {
		problem = e.Input
	}
	solution := e.Solution
	if solution == "" {
		solution = e.Output
	}
	return distillation.Experience{
		ID:         e.ID,
		Problem:    problem,
		Solution:   solution,
		Confidence: clampScore(e.Score),
		Vector:     e.Embedding,
	}
}

// knowledgeRetrieverAdapter wraps adapter.KnowledgeRetriever and converts
// its local adapter.ContextSnippet results into the canonical
// memctx.ContextSnippet so the MemoryManager's context builder can consume
// them uniformly alongside MemoryRetriever output.
//
// The conversion is a shallow field copy — both ContextSnippet types have
// identical shapes (Source, Content, Score, Metadata). The adapter exists
// only to bridge the import boundary (knowledge/adapter cannot import
// ares_memory/context without creating a cycle through distillation).
type knowledgeRetrieverAdapter struct {
	inner *adapter.KnowledgeRetriever
}

// Retrieve delegates to the underlying KnowledgeRetriever and converts each
// adapter.ContextSnippet into a memctx.ContextSnippet. A nil inner
// retriever yields an empty slice — this keeps BuildContext resilient when
// the AKG runtime was not constructed.
func (a *knowledgeRetrieverAdapter) Retrieve(
	ctx context.Context,
	input string,
	topK int,
) ([]memctx.ContextSnippet, error) {
	if a == nil || a.inner == nil {
		return []memctx.ContextSnippet{}, nil
	}
	snippets, err := a.inner.Retrieve(ctx, input, topK)
	if err != nil {
		return nil, fmt.Errorf("knowledge retriever adapter: %w", err)
	}
	out := make([]memctx.ContextSnippet, 0, len(snippets))
	for _, s := range snippets {
		out = append(out, memctx.ContextSnippet{
			Source:   s.Source,
			Content:  s.Content,
			Score:    s.Score,
			Metadata: s.Metadata,
		})
	}
	return out, nil
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
			minScore = 0.4
		}
		pipeline, err := memembed.NewEmbeddingPipeline(embClient)
		if err != nil {
			log.Warn("bootstrap: memory retriever embedding pipeline failed; skipping",
				"error", err)
		} else {
			mr, err := memctx.NewMemoryRetriever(
				embClient,
				pipeline,
				&pgExperienceSearcher{repo: expRepo},
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
			retrievers = append(retrievers, &knowledgeRetrieverAdapter{inner: kr})
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

// clampScore normalizes a confidence value into [0, 1]. The experience
// repository is contractually expected to return scores in that range, but
// we defend against negative or >1 values so downstream sorting and
// filtering operate on a well-defined domain.
func clampScore(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
