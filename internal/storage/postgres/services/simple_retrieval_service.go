// Package services provides simplified retrieval services for the storage system.
// This service focuses on pure vector similarity search without complex weight calculations.
package services

import (
	"context"
	stderrors "errors"
	"math"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Timwood0x10/ares/internal/errors"
	memembed "github.com/Timwood0x10/ares/internal/runtime/memory/embedding"
	"github.com/Timwood0x10/ares/internal/storage/postgres/embedding"
	"github.com/Timwood0x10/ares/internal/storage/postgres/repositories"
)

// SimpleRetrievalConfig configuration for simple retrieval service
type SimpleRetrievalConfig struct {
	TopK        int     `json:"top_k"`        // Number of results to return
	MinScore    float64 `json:"min_score"`    // Minimum similarity score
	QueryPrefix string  `json:"query_prefix"` // Prefix for query embedding (e.g., "query:")
	// RerankCandidateFactor is how many candidates to recall per returned result
	// before reranking (default 5 — the value this path always used to fetch, now
	// actually reranked). 1 disables the wide recall.
	RerankCandidateFactor int `json:"rerank_candidate_factor"`
	// RerankWeights are the rerank term weights (default
	// DefaultSimpleRerankWeights). All-zero falls back to pure similarity.
	RerankWeights SimpleRerankWeights `json:"rerank_weights"`
	// RerankHalfLife is the recency term's half-life (default 30 days).
	RerankHalfLife time.Duration `json:"rerank_half_life"`
}

// normalized returns a copy with defaults filled in, so a partially-set config
// cannot silently disable the rerank or divide by a zero factor.
func (c *SimpleRetrievalConfig) normalized() SimpleRetrievalConfig {
	if c == nil {
		return SimpleRetrievalConfig{
			TopK: 5, MinScore: 0.6, QueryPrefix: "query:",
			RerankCandidateFactor: defaultRerankCandidateFactor,
			RerankWeights:         DefaultSimpleRerankWeights(),
			RerankHalfLife:        defaultRerankHalfLife,
		}
	}
	out := *c
	if out.RerankCandidateFactor <= 0 {
		out.RerankCandidateFactor = defaultRerankCandidateFactor
	}
	if out.RerankWeights == (SimpleRerankWeights{}) {
		out.RerankWeights = DefaultSimpleRerankWeights()
	}
	if out.RerankHalfLife <= 0 {
		out.RerankHalfLife = defaultRerankHalfLife
	}
	return out
}

// defaultRerankCandidateFactor is the recall width per returned result.
const defaultRerankCandidateFactor = 5

// SimpleSearchResult simple search result with only essential fields
type SimpleSearchResult struct {
	Content string  `json:"content"` // Content text
	Source  string  `json:"source"`  // Source document/file path
	Score   float64 `json:"score"`   // Cosine similarity score (1 - distance)
	// RerankScore is the score the results were ORDERED by (similarity + lexical
	// coverage + recency, see simple_rerank.go). Score keeps its original meaning
	// — callers compare it against MinScore — so the two are deliberately
	// separate: re-sorting the returned slice by Score restores the plain cosine
	// order, which is what the rerank exists to improve on.
	RerankScore float64 `json:"rerank_score"`
}

// SimpleRetrievalService provides pure vector similarity search
// This is inspired by ChromaDB's simple and direct approach:
// - Direct vector similarity search (1 - cosine_distance)
// - No complex weight calculations
// - No time decay
// - No query rewrites
// - Simple and effective for single knowledge base scenarios
type SimpleRetrievalService struct {
	mu        sync.RWMutex
	repo      *repositories.KnowledgeRepository
	embedding *embedding.EmbeddingClient
	config    *SimpleRetrievalConfig
	pipeline  memembed.EmbeddingPipeline
}

// SetEmbeddingPipeline configures the unified embedding pipeline for query embedding.
// When set, Search uses the pipeline with canonical query specs instead of calling
// the embedding client directly.
func (s *SimpleRetrievalService) SetEmbeddingPipeline(pipeline memembed.EmbeddingPipeline) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pipeline = pipeline
}

// embedQuery generates a query embedding using the pipeline when available,
// falling back to direct embedder call.
func (s *SimpleRetrievalService) embedQuery(ctx context.Context, query string) ([]float64, error) {
	s.mu.RLock()
	pipeline := s.pipeline
	config := s.config
	s.mu.RUnlock()

	if pipeline != nil {
		spec, err := pipeline.BuildSpec(memembed.KindMemoryQuery, query)
		if err != nil {
			return nil, errors.Wrap(err, "build query spec")
		}
		vec, err := pipeline.Embed(ctx, spec)
		if err != nil {
			return nil, errors.Wrap(err, "embed via pipeline")
		}
		return vec, nil
	}
	if s.embedding == nil {
		return nil, errors.New("embedding client is not configured")
	}
	return s.embedding.EmbedWithPrefix(ctx, query, config.QueryPrefix)
}

// NewSimpleRetrievalService creates a new simple retrieval service
func NewSimpleRetrievalService(
	repo *repositories.KnowledgeRepository,
	embeddingClient *embedding.EmbeddingClient,
	config *SimpleRetrievalConfig,
) *SimpleRetrievalService {
	// Normalized rather than copied: a partially-set config must not silently
	// disable the rerank (a zero candidate factor would collapse the wide recall
	// back to the plain cosine order this service exists to improve on).
	normalized := config.normalized()

	return &SimpleRetrievalService{
		repo:      repo,
		embedding: embeddingClient,
		config:    &normalized,
	}
}

// Search performs intelligent retrieval with precision mode support
// Returns results with cosine similarity score (1 - distance), where:
// - 1.0 = perfect match
// - 0.0 = orthogonal (no relation)
// - -1.0 = opposite meaning
func (s *SimpleRetrievalService) Search(ctx context.Context, tenantID, query string) ([]*SimpleSearchResult, error) {
	log.Info("SimpleRetrievalService.Search",
		"tenant_id", tenantID,
		"query", query,
		"top_k", s.GetConfig().TopK,
		"min_score", s.GetConfig().MinScore)

	// Check if precision mode should be used
	if s.isPrecisionMode(query) {
		log.Info("Using precision mode", "query", query)
		return s.searchPrecision(ctx, tenantID, query)
	}

	// Generate embedding using unified pipeline when available.
	queryEmbedding, err := s.embedQuery(ctx, query)
	if err != nil {
		log.Error("Failed to embed query", "error", err)
		return nil, errors.Wrap(err, "embed query")
	}

	log.Debug("Query embedding generated", "dimension", len(queryEmbedding))

	// Perform pure vector similarity search
	// SearchByVector returns chunks with similarity in metadata
	// Use larger limit to get more candidates before filtering
	// Wide recall: fetch RerankCandidateFactor candidates per result so the
	// rerank below has something to choose from. The factor used to be a literal
	// 5 with nothing reranking the result, which bought nothing.
	cfg := s.GetConfig()
	chunks, err := s.repo.SearchByVector(ctx, queryEmbedding, tenantID, cfg.TopK*cfg.RerankCandidateFactor)
	if err != nil {
		log.Error("Vector search failed", "error", err)
		return nil, errors.Wrap(err, "vector search")
	}

	log.Debug("Raw chunks retrieved", "count", len(chunks))

	// Convert to candidates and filter by min_score, then RERANK (defect R2).
	//
	// The wide recall above is only worth its extra work if something reorders
	// it: taking the first TopK in recall order IS the cosine order, i.e. exactly
	// what a LIMIT TopK would have returned.
	now := time.Now()
	candidates := make([]rerankCandidate, 0, len(chunks))
	for _, chunk := range chunks {
		// Extract similarity from metadata (set by SearchByVector)
		// SearchByVector computes: 1 - cosine_distance
		similarity, ok := chunk.Metadata["similarity"].(float64)
		if !ok {
			log.Warn("No similarity score found for chunk", "id", chunk.ID)
			continue
		}

		// Filter by min_score threshold
		if similarity < cfg.MinScore {
			continue
		}

		candidates = append(candidates, rerankCandidate{
			Content:    chunk.Content,
			Source:     chunk.Source,
			Similarity: similarity,
			Age:        chunkAge(chunk.Metadata, now),
		})
	}

	ranked := rerankCandidates(query, candidates, cfg.TopK, cfg.RerankWeights, cfg.RerankHalfLife)
	results := make([]*SimpleSearchResult, 0, len(ranked))
	for _, c := range ranked {
		results = append(results, &SimpleSearchResult{
			Content:     c.Content,
			Source:      c.Source,
			Score:       c.Similarity,
			RerankScore: rerankScore(query, c, cfg.RerankWeights, cfg.RerankHalfLife),
		})
	}

	log.Info("Search completed",
		"results_count", len(results),
		"min_score", s.GetConfig().MinScore)

	return results, nil
}

// isPrecisionMode determines if precision mode should be used for the query.
// Precision mode is triggered for:
// - Short queries (≤10 characters)
// - Queries containing special symbols (= or mathematical expressions)
// This uses deterministic matching to cover semantic retrieval for precise queries.
func (s *SimpleRetrievalService) isPrecisionMode(query string) bool {
	// Short queries use exact/keyword matching for precision
	if utf8.RuneCountInString(query) <= 10 {
		return true
	}

	// Core expression patterns: containing equals sign or mathematical operators
	// Note: - is intentionally excluded to avoid matching hyphens in compound words (e.g., "go-agent")
	// Note: +, *, / are checked via regex requiring digit adjacency to avoid matching
	//       programming symbols like "C++", "*args", "**kwargs"
	if strings.Contains(query, "=") || mathExprPattern.MatchString(query) {
		return true
	}

	return false
}

// searchPrecision executes the precision retrieval pipeline for SimpleRetrievalService.
// The exact→keyword→vector chain degrades gracefully per stage, but a total
// failure (every stage errored AND the vector fallback produced nothing) is
// propagated as an error: an outage must not be indistinguishable from empty
// knowledge (3.7#8).
func (s *SimpleRetrievalService) searchPrecision(ctx context.Context, tenantID, query string) ([]*SimpleSearchResult, error) {
	log.Debug("Executing precision search pipeline", "query", query)

	var stageErrs []error

	// 1. Exact Match (highest priority)
	exact, err := s.searchExact(ctx, tenantID, query)
	if err != nil {
		log.Error("Failed to execute exact match search, falling back to keyword", "error", err)
		stageErrs = append(stageErrs, err)
		exact = nil
	}
	if len(exact) > 0 {
		log.Debug("Precision search: exact match found", "count", len(exact))
		return exact, nil
	}

	// 2. Keyword Search (second priority)
	keyword, err := s.searchKeyword(ctx, tenantID, query)
	if err != nil {
		log.Error("Failed to execute keyword search, falling back to vector", "error", err)
		stageErrs = append(stageErrs, err)
		keyword = nil
	}
	if len(keyword) > 0 {
		log.Debug("Precision search: keyword match found", "count", len(keyword))
		return keyword, nil
	}

	// 3. Vector Search (fallback)
	vector, err := s.searchVector(ctx, tenantID, query)
	if err != nil {
		log.Error("Failed to execute vector search", "error", err)
		stageErrs = append(stageErrs, err)
		return nil, errors.Wrap(stderrors.Join(stageErrs...), "precision search: all stages failed")
	}
	log.Debug("Precision search: using vector fallback", "count", len(vector))

	return vector, nil
}

// searchExact performs exact substring matching.
func (s *SimpleRetrievalService) searchExact(ctx context.Context, tenantID, query string) ([]*SimpleSearchResult, error) {
	log.Debug("Running exact match search", "query", query)

	chunks, err := s.repo.SearchBySubstring(ctx, query, tenantID, s.GetConfig().TopK)
	if err != nil {
		log.Error("Exact match search failed", "error", err)
		return nil, errors.Wrap(err, "exact match search")
	}

	if len(chunks) == 0 {
		return []*SimpleSearchResult{}, nil
	}

	results := make([]*SimpleSearchResult, 0, len(chunks))
	for _, chunk := range chunks {
		results = append(results, &SimpleSearchResult{
			Content: chunk.Content,
			Source:  chunk.Source,
			Score:   1.0, // Fixed highest score for exact matches
		})
	}

	return results, nil
}

// searchKeyword performs BM25 keyword search with simplified scoring.
func (s *SimpleRetrievalService) searchKeyword(ctx context.Context, tenantID, query string) ([]*SimpleSearchResult, error) {
	log.Debug("Running keyword search", "query", query)

	chunks, err := s.repo.SearchByKeyword(ctx, query, tenantID, s.GetConfig().TopK)
	if err != nil {
		log.Error("Keyword search failed", "error", err)
		return nil, errors.Wrap(err, "keyword search")
	}

	if len(chunks) == 0 {
		return []*SimpleSearchResult{}, nil
	}

	results := make([]*SimpleSearchResult, 0, len(chunks))
	for _, chunk := range chunks {
		score := 1.0
		if chunk.Metadata != nil {
			if keywordScore, ok := chunk.Metadata["keyword_score"].(float64); ok {
				score = math.Min(keywordScore, 1.0)
			}
		}

		results = append(results, &SimpleSearchResult{
			Content: chunk.Content,
			Source:  chunk.Source,
			Score:   score,
		})
	}

	return results, nil
}

// searchVector performs vector similarity search.
func (s *SimpleRetrievalService) searchVector(ctx context.Context, tenantID, query string) ([]*SimpleSearchResult, error) {
	log.Debug("Running vector search", "query", query)

	// Generate embedding using unified pipeline when available.
	queryEmbedding, err := s.embedQuery(ctx, query)
	if err != nil {
		log.Error("Failed to embed query", "error", err)
		return nil, errors.Wrap(err, "embed query")
	}

	cfg := s.GetConfig()
	chunks, err := s.repo.SearchByVector(ctx, queryEmbedding, tenantID, cfg.TopK*cfg.RerankCandidateFactor)
	if err != nil {
		log.Error("Vector search failed", "error", err)
		return nil, errors.Wrap(err, "vector search")
	}

	if len(chunks) == 0 {
		return []*SimpleSearchResult{}, nil
	}

	// Same wide-recall-then-rerank shape as Search, so the vector fallback of the
	// precision path does not quietly answer with the plain cosine order.
	now := time.Now()
	candidates := make([]rerankCandidate, 0, len(chunks))
	for _, chunk := range chunks {
		score := 0.0
		if chunk.Metadata != nil {
			if similarity, ok := chunk.Metadata["similarity"].(float64); ok {
				score = similarity
			}
		}

		// Apply min_score filter, consistent with normal Search path behavior
		if score < cfg.MinScore {
			continue
		}

		candidates = append(candidates, rerankCandidate{
			Content:    chunk.Content,
			Source:     chunk.Source,
			Similarity: score,
			Age:        chunkAge(chunk.Metadata, now),
		})
	}

	ranked := rerankCandidates(query, candidates, cfg.TopK, cfg.RerankWeights, cfg.RerankHalfLife)
	results := make([]*SimpleSearchResult, 0, len(ranked))
	for _, c := range ranked {
		results = append(results, &SimpleSearchResult{
			Content:     c.Content,
			Source:      c.Source,
			Score:       c.Similarity,
			RerankScore: rerankScore(query, c, cfg.RerankWeights, cfg.RerankHalfLife),
		})
	}

	return results, nil
}

// SetConfig updates the retrieval configuration (normalized, so a partial config
// keeps the rerank active).
func (s *SimpleRetrievalService) SetConfig(config *SimpleRetrievalConfig) {
	normalized := config.normalized()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config = &normalized
}

// GetConfig returns the current configuration
func (s *SimpleRetrievalService) GetConfig() *SimpleRetrievalConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config
}
