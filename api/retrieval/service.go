// Package retrieval provides high-level APIs for knowledge retrieval operations.
package retrieval

import (
	"context"

	"goagentx/internal/errors"
	"goagentx/internal/storage/postgres"
	"goagentx/internal/storage/postgres/embedding"
	"goagentx/internal/storage/postgres/repositories"
	"goagentx/internal/storage/postgres/services"
)

// Service provides retrieval operations for knowledge base.
type Service struct {
	simpleRetrieval   *services.SimpleRetrievalService
	advancedRetrieval *services.RetrievalService
	pool              *postgres.Pool
}

// Config configuration for retrieval service.
type Config struct {
	UseSimpleRetrieval bool
	TopK               int
	MinScore           float64
}

// NewService creates a new retrieval service instance.
// Args:
// pool - database connection pool.
// embeddingClient - embedding service client.
// kbRepo - knowledge repository for data access.
// config - retrieval configuration.
// Returns new retrieval service instance or error.
func NewService(
	pool *postgres.Pool,
	embeddingClient *embedding.EmbeddingClient,
	kbRepo *repositories.KnowledgeRepository,
	config *Config,
) (*Service, error) {
	if config == nil {
		config = &Config{
			UseSimpleRetrieval: true,
			TopK:               10,
			MinScore:           0.4,
		}
	}

	var simpleRetrieval *services.SimpleRetrievalService
	var advancedRetrieval *services.RetrievalService

	if config.UseSimpleRetrieval {
		simpleRetrieval = services.NewSimpleRetrievalService(
			kbRepo,
			embeddingClient,
			&services.SimpleRetrievalConfig{
				TopK:        config.TopK,
				MinScore:    config.MinScore,
				QueryPrefix: "query:",
			},
		)
	}

	return &Service{
		simpleRetrieval:   simpleRetrieval,
		advancedRetrieval: advancedRetrieval,
		pool:              pool,
	}, nil
}

// Search performs knowledge base search.
// Args:
// ctx - operation context.
// tenantID - tenant identifier for isolation.
// query - search query text.
// Returns search results or error if search fails.
func (s *Service) Search(ctx context.Context, tenantID, query string) ([]*Result, error) {
	if tenantID == "" {
		return nil, ErrInvalidTenantID
	}
	if query == "" {
		return nil, ErrInvalidQuery
	}

	if s.simpleRetrieval != nil {
		results, err := s.simpleRetrieval.Search(ctx, tenantID, query)
		if err != nil {
			return nil, errors.Wrap(err, "simple retrieval search")
		}

		// Convert internal results to API results
		apiResults := make([]*Result, 0, len(results))
		for _, res := range results {
			apiResults = append(apiResults, &Result{
				Content:   res.Content,
				Source:    res.Source,
				Score:     res.Score,
				SubSource: "simple",
			})
		}
		return apiResults, nil
	}

	return nil, ErrNoRetrievalService
}

// SearchWithConfig performs search with custom configuration.
// Args:
// ctx - operation context.
// tenantID - tenant identifier for isolation.
// query - search query text.
// config - custom search configuration.
// Returns search results or error if search fails.
func (s *Service) SearchWithConfig(ctx context.Context, tenantID, query string, config *Config) ([]*Result, error) {
	if tenantID == "" {
		return nil, ErrInvalidTenantID
	}
	if query == "" {
		return nil, ErrInvalidQuery
	}

	// If config is nil, use default search
	if config == nil {
		return s.Search(ctx, tenantID, query)
	}

	// If simple retrieval is configured, use it with custom parameters
	if s.simpleRetrieval != nil {
		// Create a new retrieval service with custom config
		// Note: This is a simplified implementation. In production, you might want
		// to update the existing service's config or use a more sophisticated approach
		simpleResults, err := s.simpleRetrieval.Search(ctx, tenantID, query)
		if err != nil {
			return nil, errors.Wrap(err, "simple retrieval search with config")
		}

		// Convert internal results to API results
		apiResults := make([]*Result, 0, len(simpleResults))
		for _, res := range simpleResults {
			// Filter results based on min score if specified
			if config.MinScore > 0 && res.Score < config.MinScore {
				continue
			}
			apiResults = append(apiResults, &Result{
				Content:   res.Content,
				Source:    res.Source,
				Score:     res.Score,
				SubSource: "simple",
			})
		}

		// Limit results based on TopK if specified
		if config.TopK > 0 && len(apiResults) > config.TopK {
			apiResults = apiResults[:config.TopK]
		}

		return apiResults, nil
	}

	return nil, ErrNoRetrievalService
}

// Result represents a single retrieval result.
type Result struct {
	Content   string  `json:"content"`
	Source    string  `json:"source"`
	Score     float64 `json:"score"`
	SubSource string  `json:"sub_source"`
}
