// Package services provides simplified retrieval services for the storage system.
// This service focuses on pure vector similarity search without complex weight calculations.
package services

import (
	"context"
	"log/slog"
	"math"
	"strings"

	"github.com/Timwood0x10/goagent/internal/errors"
	"github.com/Timwood0x10/goagent/internal/storage/postgres/embedding"
	"github.com/Timwood0x10/goagent/internal/storage/postgres/repositories"
)

// SimpleRetrievalConfig configuration for simple retrieval service
type SimpleRetrievalConfig struct {
	TopK        int     `json:"top_k"`        // Number of results to return
	MinScore    float64 `json:"min_score"`    // Minimum similarity score
	QueryPrefix string  `json:"query_prefix"` // Prefix for query embedding (e.g., "query:")
}

// SimpleSearchResult simple search result with only essential fields
type SimpleSearchResult struct {
	Content string  `json:"content"` // Content text
	Source  string  `json:"source"`  // Source document/file path
	Score   float64 `json:"score"`   // Cosine similarity score (1 - distance)
}

// SimpleRetrievalService provides pure vector similarity search
// This is inspired by ChromaDB's simple and direct approach:
// - Direct vector similarity search (1 - cosine_distance)
// - No complex weight calculations
// - No time decay
// - No query rewrites
// - Simple and effective for single knowledge base scenarios
type SimpleRetrievalService struct {
	repo      *repositories.KnowledgeRepository
	embedding *embedding.EmbeddingClient
	config    *SimpleRetrievalConfig
}

// NewSimpleRetrievalService creates a new simple retrieval service
func NewSimpleRetrievalService(
	repo *repositories.KnowledgeRepository,
	embeddingClient *embedding.EmbeddingClient,
	config *SimpleRetrievalConfig,
) *SimpleRetrievalService {
	if config == nil {
		config = &SimpleRetrievalConfig{
			TopK:        5,
			MinScore:    0.6,
			QueryPrefix: "query:",
		}
	}

	return &SimpleRetrievalService{
		repo:      repo,
		embedding: embeddingClient,
		config:    config,
	}
}

// Search performs intelligent retrieval with precision mode support
// Returns results with cosine similarity score (1 - distance), where:
// - 1.0 = perfect match
// - 0.0 = orthogonal (no relation)
// - -1.0 = opposite meaning
func (s *SimpleRetrievalService) Search(ctx context.Context, tenantID, query string) ([]*SimpleSearchResult, error) {
	slog.Info("SimpleRetrievalService.Search",
		"tenant_id", tenantID,
		"query", query,
		"top_k", s.config.TopK,
		"min_score", s.config.MinScore)

	// Check if precision mode should be used
	if s.isPrecisionMode(query) {
		slog.Info("Using precision mode", "query", query)
		return s.searchPrecision(ctx, tenantID, query), nil
	}

	// Generate embedding for the query with optional prefix
	fullQuery := query
	if s.config.QueryPrefix != "" {
		fullQuery = s.config.QueryPrefix + query
	}

	queryEmbedding, err := s.embedding.EmbedWithPrefix(ctx, fullQuery, s.config.QueryPrefix)
	if err != nil {
		slog.Error("Failed to embed query", "error", err)
		return nil, errors.Wrap(err, "embed query")
	}

	slog.Debug("Query embedding generated", "dimension", len(queryEmbedding))

	// Perform pure vector similarity search
	// SearchByVector returns chunks with similarity in metadata
	// Use larger limit to get more candidates before filtering
	chunks, err := s.repo.SearchByVector(ctx, queryEmbedding, tenantID, s.config.TopK*5)
	if err != nil {
		slog.Error("Vector search failed", "error", err)
		return nil, errors.Wrap(err, "vector search")
	}

	slog.Debug("Raw chunks retrieved", "count", len(chunks))

	// Convert to simple results and filter by min_score
	var results []*SimpleSearchResult
	for _, chunk := range chunks {
		// Extract similarity from metadata (set by SearchByVector)
		// SearchByVector computes: 1 - cosine_distance
		similarity, ok := chunk.Metadata["similarity"].(float64)
		if !ok {
			slog.Warn("No similarity score found for chunk", "id", chunk.ID)
			continue
		}

		// Filter by min_score threshold
		if similarity < s.config.MinScore {
			continue
		}

		results = append(results, &SimpleSearchResult{
			Content: chunk.Content,
			Source:  chunk.Source,
			Score:   similarity,
		})
	}

	// Limit to TopK results
	if len(results) > s.config.TopK {
		results = results[:s.config.TopK]
	}

	slog.Info("Search completed",
		"results_count", len(results),
		"min_score", s.config.MinScore)

	return results, nil
}

// isPrecisionMode determines if precision mode should be used for the query.
// Precision mode is triggered for:
// - Short queries (≤10 characters)
// - Queries containing special symbols (=+-*/:)
// This uses deterministic matching to cover semantic retrieval for precise queries.
func (s *SimpleRetrievalService) isPrecisionMode(query string) bool {
	// Short queries use exact/keyword matching for precision
	if len(query) <= 10 {
		return true
	}

	// Core expression patterns: containing equals sign or mathematical operators
	if strings.ContainsAny(query, "=+-*/:") {
		return true
	}

	return false
}

// searchPrecision executes the precision retrieval pipeline for SimpleRetrievalService.
func (s *SimpleRetrievalService) searchPrecision(ctx context.Context, tenantID, query string) []*SimpleSearchResult {
	slog.Debug("Executing precision search pipeline", "query", query)

	// 1. Exact Match (highest priority)
	exact, err := s.searchExact(ctx, tenantID, query)
	if err != nil {
		slog.Error("Failed to execute exact match search", "error", err)
		return []*SimpleSearchResult{}
	}
	if len(exact) > 0 {
		slog.Debug("Precision search: exact match found", "count", len(exact))
		return exact
	}

	// 2. Keyword Search (second priority)
	keyword, err := s.searchKeyword(ctx, tenantID, query)
	if err != nil {
		slog.Error("Failed to execute keyword search", "error", err)
		return []*SimpleSearchResult{}
	}
	if len(keyword) > 0 {
		slog.Debug("Precision search: keyword match found", "count", len(keyword))
		return keyword
	}

	// 3. Vector Search (fallback)
	vector, err := s.searchVector(ctx, tenantID, query)
	if err != nil {
		slog.Error("Failed to execute vector search", "error", err)
		return []*SimpleSearchResult{}
	}
	slog.Debug("Precision search: using vector fallback", "count", len(vector))

	return vector
}

// searchExact performs exact substring matching.
func (s *SimpleRetrievalService) searchExact(ctx context.Context, tenantID, query string) ([]*SimpleSearchResult, error) {
	slog.Debug("Running exact match search", "query", query)

	chunks, err := s.repo.SearchBySubstring(ctx, query, tenantID, 5)
	if err != nil {
		slog.Error("Exact match search failed", "error", err)
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
	slog.Debug("Running keyword search", "query", query)

	chunks, err := s.repo.SearchByKeyword(ctx, query, tenantID, s.config.TopK)
	if err != nil {
		slog.Error("Keyword search failed", "error", err)
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
	slog.Debug("Running vector search", "query", query)

	// Generate embedding for the query with optional prefix
	fullQuery := query
	if s.config.QueryPrefix != "" {
		fullQuery = s.config.QueryPrefix + query
	}

	queryEmbedding, err := s.embedding.EmbedWithPrefix(ctx, fullQuery, s.config.QueryPrefix)
	if err != nil {
		slog.Error("Failed to embed query", "error", err)
		return nil, errors.Wrap(err, "embed query")
	}

	chunks, err := s.repo.SearchByVector(ctx, queryEmbedding, tenantID, s.config.TopK)
	if err != nil {
		slog.Error("Vector search failed", "error", err)
		return nil, errors.Wrap(err, "vector search")
	}

	if len(chunks) == 0 {
		return []*SimpleSearchResult{}, nil
	}

	results := make([]*SimpleSearchResult, 0, len(chunks))
	for _, chunk := range chunks {
		score := 0.0
		if chunk.Metadata != nil {
			if similarity, ok := chunk.Metadata["similarity"].(float64); ok {
				score = similarity
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

// SetConfig updates the retrieval configuration
func (s *SimpleRetrievalService) SetConfig(config *SimpleRetrievalConfig) {
	s.config = config
}

// GetConfig returns the current configuration
func (s *SimpleRetrievalService) GetConfig() *SimpleRetrievalConfig {
	return s.config
}
