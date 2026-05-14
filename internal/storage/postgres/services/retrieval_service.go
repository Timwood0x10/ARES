// Package services provides retrieval services for the storage system.
package services

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/sync/errgroup"
	"gopkg.in/yaml.v3"

	coreerrors "github.com/Timwood0x10/goagent/internal/core/errors"
	"github.com/Timwood0x10/goagent/internal/errors"
	"github.com/Timwood0x10/goagent/internal/experience"
	"github.com/Timwood0x10/goagent/internal/llm"
	"github.com/Timwood0x10/goagent/internal/storage/postgres"
	"github.com/Timwood0x10/goagent/internal/storage/postgres/embedding"
	storage_models "github.com/Timwood0x10/goagent/internal/storage/postgres/models"
	"github.com/Timwood0x10/goagent/internal/storage/postgres/repositories"
)

var allowedSynonymDir string

// SetAllowedSynonymDir sets the allowed directory for synonym config files.
// This is a security measure to prevent path traversal attacks.
func SetAllowedSynonymDir(dir string) {
	allowedSynonymDir = dir
}

// SearchRequest represents a search request with configuration.
type SearchRequest struct {
	Query       string          `json:"query"`           // Search query text
	TenantID    string          `json:"tenant_id"`       // Tenant ID for isolation
	TopK        int             `json:"top_k"`           // Number of results to return
	MinScore    float64         `json:"min_score"`       // Minimum similarity score
	Plan        *RetrievalPlan  `json:"plan"`            // Retrieval strategy
	EnableTrace bool            `json:"enable_trace"`    // Enable trace logging
	Trace       *RetrievalTrace `json:"trace,omitempty"` // Trace information
}

// SearchResult represents a single search result.
type SearchResult struct {
	ID        string                 `json:"id"`
	Content   string                 `json:"content"`
	Score     float64                `json:"score"`
	Source    string                 `json:"source"`     // knowledge, experience, tool, task_result
	SubSource string                 `json:"sub_source"` // vector, keyword
	Type      string                 `json:"type"`       // Result type for filtering
	Metadata  map[string]interface{} `json:"metadata"`   // Additional metadata
	CreatedAt time.Time              `json:"created_at"`

	// Query information for traceability and scoring
	Query       string  `json:"query"`        // Query that matched this result
	QueryWeight float64 `json:"query_weight"` // Weight of the query (original=1.0, rule=0.7, llm=0.5)
}

// WeightedQuery represents a query with associated weight for retrieval.
// This enables controlling the impact of query rewrites on final results.
type WeightedQuery struct {
	Query  string  `json:"query"`  // Query text
	Weight float64 `json:"weight"` // Weight for scoring (original=1.0, rule=0.7, llm=0.5)
	Source string  `json:"source"` // Source: original, rewrite_rule, rewrite_llm
}

// ResultDebugInfo contains detailed debugging information for a search result.
// This helps answer "why this result is ranked first?" and supports observability.
type ResultDebugInfo struct {
	ID           string                 `json:"id"`            // Result ID
	Score        float64                `json:"score"`         // Final score
	Query        string                 `json:"query"`         // Query that matched this result
	QueryWeight  float64                `json:"query_weight"`  // Weight of the query
	Source       string                 `json:"source"`        // Result source (knowledge/experience/tool)
	SubSource    string                 `json:"sub_source"`    // Sub-source (vector/keyword)
	SourceWeight float64                `json:"source_weight"` // Weight from source
	SubWeight    float64                `json:"sub_weight"`    // Weight from sub-source
	Signals      map[string]interface{} `json:"signals"`       // Applied signals (success, reuse_count, etc.)
	Breakdown    map[string]float64     `json:"breakdown"`     // Score breakdown for analysis
}

// QueryPriorityConfig defines priority weights for different query types.
// This controls how much influence rewrites have on retrieval results.
type QueryPriorityConfig struct {
	OriginalWeight    float64 `json:"original_weight"`     // Original query weight (default 1.0)
	RuleRewriteWeight float64 `json:"rule_rewrite_weight"` // Rule-based rewrite weight (default 0.7)
	LLMRewriteWeight  float64 `json:"llm_rewrite_weight"`  // LLM-based rewrite weight (default 0.5)
	MaxQueries        int     `json:"max_queries"`         // Maximum number of queries (default 3)
}

// RetrievalPlan defines the retrieval strategy for multi-source search.
type RetrievalPlan struct {
	SearchKnowledge   bool `json:"search_knowledge"`    // Search in knowledge base
	SearchExperience  bool `json:"search_experience"`   // Search in experiences
	SearchTools       bool `json:"search_tools"`        // Search in tools
	SearchTaskResults bool `json:"search_task_results"` // Search in task results

	KnowledgeWeight   float64 `json:"knowledge_weight"`    // Weight for knowledge results (default 0.4)
	ExperienceWeight  float64 `json:"experience_weight"`   // Weight for experience results (default 0.3)
	ToolsWeight       float64 `json:"tools_weight"`        // Weight for tool results (default 0.2)
	TaskResultsWeight float64 `json:"task_results_weight"` // Weight for task result results (default 0.1)

	EnableQueryRewrite  bool `json:"enable_query_rewrite"`  // Enable query rewriting
	EnableKeywordSearch bool `json:"enable_keyword_search"` // Enable keyword/BM25 search
	EnableTimeDecay     bool `json:"enable_time_decay"`     // Enable time-based scoring decay

	TopK int `json:"top_k"` // Maximum results per source

	// Experience-specific configuration
	ExperienceRankingEnabled  bool `json:"experience_ranking_enabled"`  // Enable experience ranking
	ExperienceConflictResolve bool `json:"experience_conflict_resolve"` // Enable conflict resolution
	ExperienceTopK            int  `json:"experience_top_k"`            // Experience recall count (default 20)
}

// RetrievalTrace contains debugging information for retrieval operations.
type RetrievalTrace struct {
	OriginalQuery   string         `json:"original_query"`
	RewrittenQuery  string         `json:"rewritten_query"`
	RewriteUsed     bool           `json:"rewrite_used"`
	VectorResults   int            `json:"vector_results"`
	KeywordResults  int            `json:"keyword_results"`
	FinalResults    int            `json:"final_results"`
	ExecutionTime   time.Duration  `json:"execution_time"`
	VectorError     error          `json:"vector_error,omitempty"`
	SearchBreakdown map[string]int `json:"search_breakdown,omitempty"` // Results per source
}

// RetrievalService provides intelligent retrieval across multiple data sources.
// It implements hybrid search (vector + keyword), query rewriting, and time-based decay.
type RetrievalService struct {
	db                       *postgres.Pool
	embeddingClient          *embedding.EmbeddingClient
	llmClient                *llm.Client
	tenantGuard              *postgres.TenantGuard
	retrievalGuard           *postgres.RetrievalGuard
	kbRepo                   *repositories.KnowledgeRepository
	expRepo                  *repositories.ExperienceRepository
	toolRepo                 *repositories.ToolRepository
	logger                   *slog.Logger
	queryPriority            *QueryPriorityConfig
	embeddingCache           map[string][]float64
	embeddingCacheMu         sync.RWMutex
	embeddingCacheSizeLimit  int      // Maximum cache entries
	embeddingCacheAccessList []string // LRU access list for eviction
	synonymRules             map[string][]string

	// Query result cache to skip redundant LLM rewrites.
	queryCache       map[string]time.Time
	queryCacheMu     sync.RWMutex
	queryCacheTTL    time.Duration // TTL for cache entries
	queryCacheMaxLen int           // Maximum cache entries

	// Experience-specific services
	distillationService *experience.DistillationService
	rankingService      *experience.RankingService
	conflictResolver    *experience.ConflictResolver
}

// NewRetrievalService creates a new RetrievalService instance.
// Args:
// pool - database connection pool.
// embeddingClient - embedding service client for vector search.
// llmClient - LLM client for query rewriting (optional, can be nil).
// tenantGuard - tenant isolation guard.
// retrievalGuard - rate limiting and circuit breaker for retrieval.
// kbRepo - knowledge repository for data access.
// expRepo - experience repository for experience search.
// toolRepo - tool repository for tool search.
// Returns new RetrievalService instance.
func NewRetrievalService(
	pool *postgres.Pool,
	embeddingClient *embedding.EmbeddingClient,
	llmClient *llm.Client,
	tenantGuard *postgres.TenantGuard,
	retrievalGuard *postgres.RetrievalGuard,
	kbRepo *repositories.KnowledgeRepository,
	expRepo *repositories.ExperienceRepository,
	toolRepo *repositories.ToolRepository,
) *RetrievalService {
	return &RetrievalService{
		db:                       pool,
		embeddingClient:          embeddingClient,
		llmClient:                llmClient,
		tenantGuard:              tenantGuard,
		retrievalGuard:           retrievalGuard,
		kbRepo:                   kbRepo,
		expRepo:                  expRepo,
		toolRepo:                 toolRepo,
		logger:                   slog.Default(),
		queryPriority:            DefaultQueryPriorityConfig(),
		embeddingCache:           make(map[string][]float64),
		embeddingCacheSizeLimit:  1000, // Limit cache to 1000 entries (~8-12MB)
		embeddingCacheAccessList: make([]string, 0, 1000),
		synonymRules:             loadSynonymRules(),
		queryCache:               make(map[string]time.Time),
		queryCacheTTL:            10 * time.Minute, // Queries cached for 10 minutes
		queryCacheMaxLen:         500,              // Limit cache to 500 entries
	}
}

// SetExperienceServices sets the experience-specific services.
// Args:
// distillationService - experience distillation service.
// rankingService - experience ranking service.
// conflictResolver - experience conflict resolver.
func (s *RetrievalService) SetExperienceServices(
	distillationService *experience.DistillationService,
	rankingService *experience.RankingService,
	conflictResolver *experience.ConflictResolver,
) {
	s.distillationService = distillationService
	s.rankingService = rankingService
	s.conflictResolver = conflictResolver
}

// DefaultQueryPriorityConfig returns the default query priority configuration.
func DefaultQueryPriorityConfig() *QueryPriorityConfig {
	return &QueryPriorityConfig{
		OriginalWeight:    1.0,
		RuleRewriteWeight: 0.7,
		LLMRewriteWeight:  0.5,
		MaxQueries:        3,
	}
}

// DefaultRetrievalPlan returns the default retrieval plan.
func DefaultRetrievalPlan() *RetrievalPlan {
	return &RetrievalPlan{
		SearchKnowledge:           true,
		SearchExperience:          true,
		SearchTools:               true,
		SearchTaskResults:         false,
		KnowledgeWeight:           0.4,
		ExperienceWeight:          0.3,
		ToolsWeight:               0.2,
		TaskResultsWeight:         0.1,
		EnableQueryRewrite:        true,
		EnableKeywordSearch:       true,
		EnableTimeDecay:           true,
		TopK:                      10,
		ExperienceRankingEnabled:  true,
		ExperienceConflictResolve: true,
		ExperienceTopK:            20,
	}
}

// Search performs intelligent retrieval across multiple data sources.
// This implements the core retrieval pipeline with hybrid search, query rewriting, and time decay.
// Args:
// ctx - database operation context.
// req - search request with query and configuration.
// Returns search results or error if retrieval fails.
func (s *RetrievalService) Search(ctx context.Context, req *SearchRequest) ([]*SearchResult, error) {
	startTime := time.Now()

	// Validate request
	if err := s.validateRequest(req); err != nil {
		return nil, err
	}

	// Check if precision mode should be used
	if s.isPrecisionMode(req.Query) {
		s.logger.Info("Using precision mode", "query", req.Query)
		return s.searchPrecision(ctx, req)
	}

	// Set default plan if not provided
	if req.Plan == nil {
		req.Plan = DefaultRetrievalPlan()
	}

	// Apply tenant isolation
	if err := s.tenantGuard.SetTenantContext(ctx, req.TenantID); err != nil {
		return nil, errors.Wrap(err, "set tenant context")
	}

	// Check rate limiting and circuit breaker
	if err := s.retrievalGuard.AllowRateLimit(); err != nil {
		return nil, err
	}

	// 1. Build weighted queries (original + rewrites)
	queries := s.buildQueries(ctx, req.Query, req.Plan)
	s.logger.Debug("Built weighted queries", "count", len(queries), "queries", queries)

	// 2. Execute search for each weighted query
	var allResults []*SearchResult
	for _, q := range queries {
		results := s.searchSingleQuery(ctx, q, req)
		allResults = append(allResults, results...)
	}

	s.logger.Debug("Collected results from all queries", "total", len(allResults))

	// 3. Merge and rank results
	finalResults := s.mergeAndRerank(allResults, req.Plan)

	// 4. Apply TopK limit
	if len(finalResults) > req.TopK {
		finalResults = finalResults[:req.TopK]
	}

	// 5. Apply minimum score filter
	s.logger.Info("Before score filter", "results_count", len(finalResults), "min_score", req.MinScore)
	for i, result := range finalResults {
		s.logger.Info("Result before filter", "index", i, "score", result.Score, "content", truncateForLog(result.Content, 50))
	}

	finalResults = s.filterByScore(finalResults, req.MinScore)

	s.logger.Info("After score filter", "results_count", len(finalResults))

	// 6. Generate retrieval trace (if enabled)
	if req.EnableTrace {
		req.Trace = &RetrievalTrace{
			OriginalQuery:   req.Query,
			RewrittenQuery:  "",
			RewriteUsed:     len(queries) > 1,
			VectorResults:   0,
			KeywordResults:  0,
			FinalResults:    len(finalResults),
			ExecutionTime:   time.Since(startTime),
			VectorError:     nil,
			SearchBreakdown: s.countResultsBySource(finalResults),
		}
	}

	// 7. Mark query as cached to skip redundant rewrites.
	s.markQueryCached(req.Query)

	return finalResults, nil
}

// validateRequest validates the search request.
func (s *RetrievalService) validateRequest(req *SearchRequest) error {
	if req == nil {
		return coreerrors.ErrInvalidArgument
	}
	if req.Query == "" {
		return coreerrors.ErrInvalidArgument
	}
	if req.TenantID == "" {
		return coreerrors.ErrInvalidArgument
	}
	if req.TopK <= 0 {
		req.TopK = 10
	}
	return nil
}

// isPrecisionMode determines if precision mode should be used for the query.
// Precision mode is triggered for:
// - Short queries (≤10 characters)
// - Queries containing special symbols (=+-*/:)
// This uses deterministic matching to cover semantic retrieval for precise queries.
func (s *RetrievalService) isPrecisionMode(query string) bool {
	// Short queries use exact/keyword matching for precision
	// Use rune count instead of byte length for proper Unicode support
	if utf8.RuneCountInString(query) <= 10 {
		return true
	}

	// Core expression patterns: containing equals sign or mathematical operators
	if strings.ContainsAny(query, "=+-*/:") {
		return true
	}

	return false
}

// searchPrecision executes the precision retrieval pipeline.
// It follows strict order: Exact Match -> Keyword -> Vector (fallback)
func (s *RetrievalService) searchPrecision(ctx context.Context, req *SearchRequest) ([]*SearchResult, error) {
	s.logger.Debug("Executing precision search pipeline", "query", req.Query)

	// 1. Exact Match (highest priority)
	exact, err := s.searchExact(ctx, req)
	if err != nil {
		s.logger.Error("Failed to execute exact match search", "error", err)
		return nil, errors.Wrap(err, "exact match search")
	}
	if len(exact) > 0 {
		s.logger.Debug("Precision search: exact match found", "count", len(exact))
		return exact, nil
	}

	// 2. Keyword Search (second priority)
	keyword, err := s.searchKeyword(ctx, req)
	if err != nil {
		s.logger.Error("Failed to execute keyword search", "error", err)
		return nil, errors.Wrap(err, "keyword search")
	}
	if len(keyword) > 0 {
		s.logger.Debug("Precision search: keyword match found", "count", len(keyword))
		return keyword, nil
	}

	// 3. Vector Search (fallback)
	vector, err := s.searchVector(ctx, req)
	if err != nil {
		s.logger.Error("Failed to execute vector search", "error", err)
		return nil, errors.Wrap(err, "vector search")
	}
	s.logger.Debug("Precision search: using vector fallback", "count", len(vector))

	// Mark query as cached to skip redundant rewrites on repeated short queries.
	s.markQueryCached(req.Query)

	return vector, nil
}

// searchExact performs exact substring matching.
func (s *RetrievalService) searchExact(ctx context.Context, req *SearchRequest) ([]*SearchResult, error) {
	s.logger.Debug("Running exact match search", "query", req.Query)

	chunks, err := s.kbRepo.SearchBySubstring(ctx, req.Query, req.TenantID, 5)
	if err != nil {
		s.logger.Error("Exact match search failed", "error", err)
		return nil, errors.Wrap(err, "exact match search")
	}

	if len(chunks) == 0 {
		return []*SearchResult{}, nil
	}

	results := make([]*SearchResult, 0, len(chunks))
	for _, chunk := range chunks {
		results = append(results, &SearchResult{
			ID:        chunk.ID,
			Content:   chunk.Content,
			Score:     1.0, // Fixed highest score for exact matches
			Source:    "knowledge",
			SubSource: "exact",
			CreatedAt: chunk.CreatedAt,
		})
	}

	return results, nil
}

// searchKeyword performs BM25 keyword search with simplified scoring.
func (s *RetrievalService) searchKeyword(ctx context.Context, req *SearchRequest) ([]*SearchResult, error) {
	s.logger.Debug("Running keyword search", "query", req.Query)

	chunks, err := s.kbRepo.SearchByKeyword(ctx, req.Query, req.TenantID, req.TopK)
	if err != nil {
		s.logger.Error("Keyword search failed", "error", err)
		return nil, errors.Wrap(err, "keyword search")
	}

	if len(chunks) == 0 {
		return []*SearchResult{}, nil
	}

	results := make([]*SearchResult, 0, len(chunks))
	for _, chunk := range chunks {
		score := 1.0
		if chunk.Metadata != nil {
			if keywordScore, ok := chunk.Metadata["keyword_score"].(float64); ok {
				score = math.Min(keywordScore, 1.0)
			}
		}

		results = append(results, &SearchResult{
			ID:        chunk.ID,
			Content:   chunk.Content,
			Score:     score,
			Source:    "knowledge",
			SubSource: "keyword",
			CreatedAt: chunk.CreatedAt,
		})
	}

	return results, nil
}

// searchVector performs vector similarity search.
func (s *RetrievalService) searchVector(ctx context.Context, req *SearchRequest) ([]*SearchResult, error) {
	s.logger.Debug("Running vector search", "query", req.Query)

	embedding := s.getEmbedding(ctx, req.Query)
	if len(embedding) == 0 {
		s.logger.Warn("No embedding available for vector search")
		return []*SearchResult{}, nil
	}

	chunks, err := s.kbRepo.SearchByVector(ctx, embedding, req.TenantID, req.TopK)
	if err != nil {
		s.logger.Error("Vector search failed", "error", err)
		return nil, errors.Wrap(err, "vector search")
	}

	if len(chunks) == 0 {
		return []*SearchResult{}, nil
	}

	results := make([]*SearchResult, 0, len(chunks))
	for _, chunk := range chunks {
		score := 0.0
		if chunk.Metadata != nil {
			if similarity, ok := chunk.Metadata["similarity"].(float64); ok {
				score = similarity
			}
		}

		results = append(results, &SearchResult{
			ID:        chunk.ID,
			Content:   chunk.Content,
			Score:     score,
			Source:    "knowledge",
			SubSource: "vector",
			CreatedAt: chunk.CreatedAt,
		})
	}

	return results, nil
}

// getEmbedding retrieves embedding for a query with caching.
func (s *RetrievalService) getEmbedding(ctx context.Context, query string) []float64 {
	if query == "" {
		return nil
	}

	if s.embeddingClient == nil {
		s.logger.Warn("Embedding client is nil, cannot get embedding")
		return nil
	}

	embedding, err := s.embeddingClient.Embed(ctx, query)
	if err != nil {
		s.logger.Warn("Failed to get embedding", "query", query, "error", err)
		return nil
	}

	// Note: embedding service already returns normalized vectors, so no need to normalize again
	return embedding
}

// getEmbeddingCached retrieves embedding with caching to reduce LLM calls.
// This can reduce 50-75% of embedding computations for repeated queries.
//
// Thread-safety: Uses read-write mutex to protect cache access.
// Implements LRU eviction to prevent unbounded memory growth.
//
// Args:
// ctx - operation context.
// query - query text.
// Returns embedding vector or nil if failed.
func (s *RetrievalService) getEmbeddingCached(ctx context.Context, query string) []float64 {
	if query == "" {
		return nil
	}

	// 1. Check cache (read lock)
	s.embeddingCacheMu.RLock()
	if embedding, ok := s.embeddingCache[query]; ok {
		s.embeddingCacheMu.RUnlock()
		s.logger.Debug("Embedding cache hit", "query", truncateForLog(query, 30))
		return embedding
	}
	s.embeddingCacheMu.RUnlock()

	// 2. Compute embedding
	embedding := s.getEmbedding(ctx, query)
	if len(embedding) == 0 {
		return nil
	}

	// 3. Store in cache with LRU eviction (write lock)
	s.embeddingCacheMu.Lock()
	defer s.embeddingCacheMu.Unlock()

	// Check if eviction is needed
	if len(s.embeddingCache) >= s.embeddingCacheSizeLimit {
		if len(s.embeddingCacheAccessList) > 0 {
			oldestKey := s.embeddingCacheAccessList[0]
			delete(s.embeddingCache, oldestKey)
			s.embeddingCacheAccessList = s.embeddingCacheAccessList[1:]
			s.logger.Debug("Embedding cache eviction", "evicted_key", truncateForLog(oldestKey, 30))
		}
	}

	s.embeddingCache[query] = embedding
	s.embeddingCacheAccessList = append(s.embeddingCacheAccessList, query)
	s.logger.Debug("Embedding cache miss, stored in cache", "query", truncateForLog(query, 30), "cache_size", len(s.embeddingCache))

	return embedding
}

// shouldRewriteQuery determines if a query should be rewritten.
func (s *RetrievalService) shouldRewriteQuery(query string) bool {
	// Skip very short queries (byte-level check).
	// Note: isPrecisionMode uses rune count for different semantics (precision trigger).
	// Here we skip only trivially short inputs that cannot benefit from rewriting.
	if len(query) < 10 {
		return false
	}

	// Skip if query is in cache (simple check)
	if s.isQueryInCache(query) {
		return false
	}

	// Complex query patterns that benefit from rewriting
	complexPatterns := []string{
		"如何", "怎么", "什么", "why", "为什么",
		"what", "how", "explain", "解释", "describe", "描述",
	}

	for _, pattern := range complexPatterns {
		if contains(query, pattern) {
			return true
		}
	}

	return false
}

// isQueryInCache checks if query results are already cached within TTL.
// Uses RLock for the read path to minimize contention; expired entry cleanup
// is deferred to markQueryCached to avoid write lock overhead on read-heavy paths.
func (s *RetrievalService) isQueryInCache(query string) bool {
	if len(query) == 0 {
		return false
	}

	normalized := s.normalizeQueryForCache(query)

	s.queryCacheMu.RLock()
	defer s.queryCacheMu.RUnlock()

	cachedAt, exists := s.queryCache[normalized]
	if !exists {
		return false
	}

	// Expired entries are lazily cleaned up by markQueryCached.
	// Here we simply report the cache status.
	return time.Since(cachedAt) <= s.queryCacheTTL
}

// markQueryCached records a query as processed to skip future rewrites.
// Thread-safe: acquires write lock for the entire operation.
// Eviction strategy: first remove expired entries, then remove oldest if still at capacity.
func (s *RetrievalService) markQueryCached(query string) {
	if len(query) == 0 {
		return
	}

	normalized := s.normalizeQueryForCache(query)

	s.queryCacheMu.Lock()
	defer s.queryCacheMu.Unlock()

	// Evict expired entries if cache is at capacity.
	if len(s.queryCache) >= s.queryCacheMaxLen {
		now := time.Now()
		for key, ts := range s.queryCache {
			if now.Sub(ts) > s.queryCacheTTL {
				delete(s.queryCache, key)
			}
		}
	}

	// Fallback: if still at capacity after expired eviction, remove oldest entry.
	if len(s.queryCache) >= s.queryCacheMaxLen {
		var oldestKey string
		var oldestTime time.Time
		for key, ts := range s.queryCache {
			if oldestKey == "" || ts.Before(oldestTime) {
				oldestKey = key
				oldestTime = ts
			}
		}
		if oldestKey != "" {
			delete(s.queryCache, oldestKey)
		}
	}

	s.queryCache[normalized] = time.Now()
}

// normalizeQueryForCache normalizes query text for cache key usage.
// Strips whitespace and converts to lowercase for case-insensitive matching.
func (s *RetrievalService) normalizeQueryForCache(query string) string {
	trimmed := strings.TrimSpace(query)
	return toLower(trimmed)
}

// queryRewrite rewrites a query for better retrieval.
// This uses LLM to expand and refine the query.
func (s *RetrievalService) queryRewrite(ctx context.Context, query string) (string, error) {
	// Use LLM-based rewrite for backward compatibility
	rewrites, err := s.llmBasedRewrite(ctx, query)
	if err != nil {
		s.logger.Warn("LLM rewrite failed, returning original query", "error", err)
		return query, nil
	}

	// Return the best rewrite or original
	if len(rewrites) > 0 {
		return rewrites[0], nil
	}

	return query, nil
}

// buildQueries constructs a list of weighted queries based on the original query and rewrites.
// This implements the converged version with weight control to prevent rewrites from dominating.
// Args:
// ctx - operation context.
// original - original query text.
// plan - retrieval plan with rewrite configuration.
// Returns list of weighted queries ordered by priority.
func (s *RetrievalService) buildQueries(ctx context.Context, original string, plan *RetrievalPlan) []WeightedQuery {
	queries := []WeightedQuery{
		{Query: original, Weight: s.queryPriority.OriginalWeight, Source: "original"},
	}

	// 1. Rule-based rewriting (low cost, stable)
	if plan.EnableQueryRewrite {
		ruleRewrites := s.ruleBasedRewrite(original)

		for _, r := range ruleRewrites {
			queries = append(queries, WeightedQuery{
				Query:  r,
				Weight: s.queryPriority.RuleRewriteWeight,
				Source: "rewrite_rule",
			})
		}
	}

	// 2. LLM-based rewriting (optional, high quality but lower weight + fail-safe)
	if plan.EnableQueryRewrite {
		llmRewrites, err := s.llmBasedRewrite(ctx, original)
		if err != nil {
			s.logger.Warn("LLM rewrite failed, using rule-based only", "error", err)
		} else {
			// Validate rewrite quality
			validated := s.validateRewrites(original, llmRewrites)

			// Deduplicate
			uniqueRewrites := s.uniqueRewrites(validated)

			// Limit count (critical to prevent explosion, max 2)
			maxLLMRewrites := 2
			if len(uniqueRewrites) > maxLLMRewrites {
				uniqueRewrites = uniqueRewrites[:maxLLMRewrites]
			}

			for _, r := range uniqueRewrites {
				queries = append(queries, WeightedQuery{
					Query:  r,
					Weight: s.queryPriority.LLMRewriteWeight,
					Source: "rewrite_llm",
				})
			}
		}
	}

	// 3. Limit total count (critical to prevent explosion)
	maxQueries := s.queryPriority.MaxQueries
	if len(queries) > maxQueries {
		queries = queries[:maxQueries]
	}

	return queries
}

// loadSynonymRules loads synonym rules from configuration file.
// This provides better maintainability and allows runtime configuration.
// Returns map of original terms to their synonyms.
// Uses CONFIG_PATH environment variable if set, otherwise uses relative path.
func loadSynonymRules() map[string][]string {
	// Default rules if config file not found
	defaultRules := map[string][]string{
		"how to":   {"how do i", "what is the best way to", "how can i"},
		"what is":  {"define", "explain", "describe"},
		"编程":       {"开发", "写代码", "编码", "程序设计"},
		"并发":       {"并行", "多线程", "异步"},
		"database": {"db", "data storage"},
		"api":      {"interface", "web service"},
	}

	// Use environment variable if set, otherwise fall back to relative path
	configPath := os.Getenv("SYNONYM_CONFIG_PATH")
	if configPath == "" {
		// Try to get the absolute path based on executable location
		execPath, err := os.Executable()
		if err == nil {
			configPath = filepath.Join(filepath.Dir(execPath), "..", "..", "configs", "synonyms.yaml")
		} else {
			configPath = "configs/synonyms.yaml"
		}
	}

	// Security: validate path is within allowed directory
	if allowedSynonymDir != "" {
		absPath, err := filepath.Abs(configPath)
		if err != nil {
			return defaultRules
		}
		absDir, err := filepath.Abs(allowedSynonymDir)
		if err != nil {
			return defaultRules
		}
		if !strings.HasPrefix(absPath, absDir) {
			return defaultRules
		}
	}

	if _, err := os.Stat(configPath); err != nil { // #nosec G703
		return defaultRules
	}

	data, err := os.ReadFile(configPath) // #nosec G304, G703
	if err != nil {
		return defaultRules
	}

	// Parse YAML config
	var config map[string][]string
	if err := yaml.Unmarshal(data, &config); err != nil {
		return defaultRules
	}

	return config
}

// ruleBasedRewrite performs rule-based query rewriting.
// This uses predefined rules for query expansion without LLM overhead.
// Args:
// original - original query text.
// Returns list of rewritten queries.
func (s *RetrievalService) ruleBasedRewrite(original string) []string {
	rewrites := []string{}

	// Normalize English queries (expand contractions, standardize format)
	normalized := normalizeEnglishQuery(original)

	// Use synonym rules loaded from configuration file
	queryLower := toLower(normalized)
	for key, synonyms := range s.synonymRules {
		if contains(queryLower, key) {
			for _, synonym := range synonyms {
				rewrites = append(rewrites, replaceCaseInsensitive(normalized, key, synonym))
			}
		}
	}

	return rewrites
}

// validateRewrites validates the quality of rewritten queries.
// This filters out rewrites that are too different or malformed.
// Args:
// original - original query text.
// rewrites - list of rewritten queries.
// Returns list of valid rewrites.
func (s *RetrievalService) validateRewrites(original string, rewrites []string) []string {
	valid := []string{}

	for _, r := range rewrites {
		// Rule 1: Similarity to original cannot be too low
		if s.calculateSimilarity(original, r) < 0.6 {
			s.logger.Debug("Rewrite too different from original", "original", original, "rewrite", r)
			continue
		}

		// Rule 2: Length cannot exceed 2x original
		if len(r) > 2*len(original) {
			s.logger.Debug("Rewrite too long", "original", original, "rewrite", r)
			continue
		}

		// Rule 3: Cannot be empty
		if r == "" {
			continue
		}

		valid = append(valid, r)
	}

	return valid
}

// uniqueRewrites removes duplicate queries from the list.
// Args:
// rewrites - list of rewritten queries.
// Returns list of unique queries.
func (s *RetrievalService) uniqueRewrites(rewrites []string) []string {
	seen := make(map[string]bool)
	unique := []string{}

	for _, r := range rewrites {
		if !seen[r] {
			seen[r] = true
			unique = append(unique, r)
		}
	}

	return unique
}

// llmBasedRewrite performs LLM-based query rewriting.
// This uses LLM to generate high-quality query variations.
// Args:
// ctx - operation context.
// query - original query text.
// Returns list of rewritten queries or error.
func (s *RetrievalService) llmBasedRewrite(ctx context.Context, query string) ([]string, error) {
	// Check if LLM client is available and enabled
	if s.llmClient == nil || !s.llmClient.IsEnabled() {
		s.logger.Debug("LLM client not available or disabled, skipping LLM rewrite")
		return []string{}, nil
	}

	// Build prompt for query rewriting
	prompt := fmt.Sprintf(`You are a search query optimization assistant. Your task is to rewrite the given search query to improve retrieval results.

Rules:
1. Keep the original intent but use different wording
2. Generate up to 3 alternative queries
3. Return each query on a separate line
4. Be concise and clear
5. Focus on semantic similarity rather than exact matches

Original Query: %s

Rewritten Queries (one per line):`, query)

	// Call LLM API with timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	response, err := s.llmClient.Generate(timeoutCtx, prompt)
	if err != nil {
		s.logger.Warn("LLM rewrite failed", "error", err, "provider", s.llmClient.GetProvider())
		return []string{}, nil // Don't fail the whole process, just return empty
	}

	// Parse response into individual queries
	rewrites := s.parseLLMResponse(response)

	s.logger.Info("LLM rewrite completed", "original", query, "rewrites_count", len(rewrites), "provider", s.llmClient.GetProvider())

	return rewrites, nil
}

// parseLLMResponse parses LLM response into individual query lines.
// Args:
// response - LLM response text.
// Returns list of parsed queries.
func (s *RetrievalService) parseLLMResponse(response string) []string {
	queries := []string{}

	// Split by lines and filter empty lines
	lines := strings.Split(response, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			queries = append(queries, line)
		}
	}

	// Limit to 3 queries
	if len(queries) > 3 {
		queries = queries[:3]
	}

	return queries
}

// calculateSimilarity calculates similarity between two strings.
// Args:
// s1 - first string.
// s2 - second string.
// Returns similarity score between 0 and 1.
func (s *RetrievalService) calculateSimilarity(s1, s2 string) float64 {
	if s1 == s2 {
		return 1.0
	}

	// Simple Jaccard similarity based on word overlap
	words1 := make(map[string]bool)
	words2 := make(map[string]bool)

	for _, word := range tokenize(toLower(s1)) {
		if word != "" {
			words1[word] = true
		}
	}

	for _, word := range tokenize(toLower(s2)) {
		if word != "" {
			words2[word] = true
		}
	}

	intersection := 0
	for word := range words1 {
		if words2[word] {
			intersection++
		}
	}

	union := len(words1) + len(words2) - intersection
	if union == 0 {
		return 0.0
	}

	return float64(intersection) / float64(union)
}

// replaceCaseInsensitive replaces all occurrences of old substring with new string, ignoring case.
// This correctly handles multi-byte UTF-8 characters by using strings.Contains with lowercasing.
//
// Args:
// s - original string.
// old - substring to replace.
// new - replacement string.
// Returns string with replacement applied.
func replaceCaseInsensitive(s, old, new string) string {
	if old == "" {
		return s
	}

	sLower := toLower(s)
	oldLower := toLower(old)

	result := strings.Builder{}
	i := 0
	for i < len(s) {
		// Find next occurrence of old substring
		if i <= len(s)-len(old) && sLower[i:i+len(old)] == oldLower {
			result.WriteString(new)
			i += len(old)
		} else {
			// Write one rune at a time to handle multi-byte characters
			_, size := utf8.DecodeRuneInString(s[i:])
			result.WriteString(s[i : i+size])
			i += size
		}
	}

	return result.String()
}

// tokenize splits a string into words.
// Args:
// s - string to tokenize.
// Returns list of words.
func tokenize(s string) []string {
	words := []string{}
	currentWord := ""

	for _, ch := range s {
		if isWordChar(ch) {
			currentWord += string(ch)
		} else {
			if currentWord != "" {
				words = append(words, currentWord)
				currentWord = ""
			}
		}
	}

	if currentWord != "" {
		words = append(words, currentWord)
	}

	return words
}

// isWordChar checks if a character is a word character.
// Args:
// ch - rune to check.
// Returns true if character is alphanumeric.
func isWordChar(ch rune) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9')
}

// searchSingleQuery executes retrieval for a single weighted query.
// This is the unified entry point for both vector and keyword search.
// It attaches query information to results for traceability.
// Args:
// ctx - operation context.
// q - weighted query to search.
// req - search request with configuration.
// Returns search results for this query.
func (s *RetrievalService) searchSingleQuery(ctx context.Context, q WeightedQuery, req *SearchRequest) []*SearchResult {
	var vectorResults, keywordResults []*SearchResult

	// Set 2 second timeout for search
	searchCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	// Use database timeout from retrieval guard
	searchCtx, dbCancel := s.retrievalGuard.WithDBTimeout(searchCtx)
	defer dbCancel() // FIX: Preserve and call cancel to prevent resource leak

	// Create errgroup for parallel search (per design standard)
	eg, ctx := errgroup.WithContext(searchCtx)
	eg.SetLimit(2) // Vector and keyword in parallel

	var mu sync.Mutex

	// Vector search (parallel)
	eg.Go(func() error {
		if s.embeddingClient != nil && s.embeddingClient.IsEnabled() {
			// Check embedding circuit breaker
			if err := s.retrievalGuard.CheckEmbeddingCircuitBreaker(); err == nil {
				embedding := s.getEmbeddingCached(ctx, q.Query)
				if len(embedding) > 0 {
					results := s.searchAllVectorSources(ctx, embedding, q.Query, req)
					mu.Lock()
					vectorResults = append(vectorResults, results...)
					mu.Unlock()
				}
				s.retrievalGuard.RecordEmbeddingSuccess()
			} else {
				s.retrievalGuard.RecordEmbeddingFailure()
				s.logger.Warn("Embedding circuit breaker open", "query", q.Query, "error", err)
			}
		}
		return nil
	})

	// Keyword search (parallel)
	eg.Go(func() error {
		if req.Plan.EnableKeywordSearch {
			results := s.searchAllKeywordSources(ctx, q.Query, req.TenantID, req.Plan.TopK)
			mu.Lock()
			keywordResults = append(keywordResults, results...)
			mu.Unlock()
		}
		return nil
	})

	// Wait for both searches to complete
	if err := eg.Wait(); err != nil {
		s.logger.Warn("Some searches failed", "error", err)
	}

	// Merge vector and keyword results
	allResults := make([]*SearchResult, 0, len(vectorResults)+len(keywordResults))
	allResults = append(allResults, vectorResults...)
	allResults = append(allResults, keywordResults...)

	// Attach query information to all results
	for _, result := range allResults {
		result.Query = q.Query
		result.QueryWeight = q.Weight
	}

	return allResults
}

// searchAllVectorSources performs vector search across all enabled sources.
// Args:
// ctx - operation context.
// embedding - query embedding vector.
// query - original query text for logging.
// req - search request with configuration.
// Returns vector search results from all sources.
func (s *RetrievalService) searchAllVectorSources(ctx context.Context, embedding []float64, query string, req *SearchRequest) []*SearchResult {
	var results []*SearchResult

	// Search knowledge base
	if req.Plan.SearchKnowledge {
		kbResults := s.searchKnowledgeVector(ctx, embedding, req)
		results = append(results, kbResults...)
	}

	// Search experiences
	if req.Plan.SearchExperience {
		expResults := s.searchExperienceVector(ctx, embedding, req)
		results = append(results, expResults...)
	}

	// Search tools
	if req.Plan.SearchTools {
		toolResults := s.searchToolsVector(ctx, embedding, req)
		results = append(results, toolResults...)
	}

	return results
}

// searchAllKeywordSources performs keyword search across all enabled sources.
// Args:
// ctx - operation context.
// query - query text.
// tenantID - tenant identifier.
// limit - maximum results per source.
// Returns keyword search results from all sources.
func (s *RetrievalService) searchAllKeywordSources(ctx context.Context, query, tenantID string, limit int) []*SearchResult {
	var results []*SearchResult

	// Search knowledge base
	kbResults := s.bm25SearchKnowledge(ctx, query, tenantID, limit)
	results = append(results, kbResults...)

	// Search experiences
	expResults := s.bm25SearchExperience(ctx, query, tenantID, limit)
	results = append(results, expResults...)

	// Search tools
	toolResults := s.bm25SearchTools(ctx, query, tenantID, limit)
	results = append(results, toolResults...)

	return results
}

// searchKnowledgeVector performs vector search on knowledge base using pgvector.
// This uses cosine similarity to find the most relevant knowledge chunks.
func (s *RetrievalService) searchKnowledgeVector(ctx context.Context, embedding []float64, req *SearchRequest) []*SearchResult {
	if len(embedding) == 0 {
		return []*SearchResult{}
	}

	// Use Repository layer to search knowledge base
	chunks, err := s.kbRepo.SearchByVector(ctx, embedding, req.TenantID, req.Plan.TopK)
	if err != nil {
		s.logger.Error("Knowledge vector search failed", "error", err)
		return []*SearchResult{}
	}

	// Convert KnowledgeChunk to SearchResult
	results := make([]*SearchResult, 0, len(chunks))
	for _, chunk := range chunks {
		result := &SearchResult{
			ID:        chunk.ID,
			Content:   chunk.Content,
			Source:    chunk.SourceType,
			SubSource: "vector",
			Type:      "knowledge",
			Metadata:  chunk.Metadata,
			CreatedAt: chunk.CreatedAt,
		}

		// Extract similarity score from metadata if available
		if similarity, ok := chunk.Metadata["similarity"].(float64); ok {
			result.Score = similarity
		}

		results = append(results, result)
	}

	return results
}

// searchExperienceVector performs vector search on experiences using pgvector.
// This uses cosine similarity to find the most relevant agent experiences.
// Supports ranking and conflict resolution if enabled in the plan.
func (s *RetrievalService) searchExperienceVector(ctx context.Context, embedding []float64, req *SearchRequest) []*SearchResult {
	if len(embedding) == 0 {
		return []*SearchResult{}
	}

	if s.expRepo == nil {
		s.logger.Debug("ExperienceRepository not available, skipping experience search")
		return []*SearchResult{}
	}

	// Determine topK for vector search (default 20 for ranking)
	topK := req.Plan.TopK
	if req.Plan.ExperienceTopK > 0 {
		topK = req.Plan.ExperienceTopK
	}

	// Use Repository layer to search experiences
	experiences, err := s.expRepo.SearchByVector(ctx, embedding, req.TenantID, topK)
	if err != nil {
		s.logger.Error("Experience vector search failed", "error", err)
		return []*SearchResult{}
	}

	// If ranking is enabled, apply ranking and conflict resolution
	if req.Plan.ExperienceRankingEnabled && s.rankingService != nil {
		return s.applyExperienceRanking(ctx, experiences, req)
	}

	// Otherwise, convert directly to SearchResult
	return s.convertExperiencesToResults(experiences)
}

// applyExperienceRanking applies ranking and conflict resolution to experiences.
// Args:
// ctx - operation context.
// experiences - experiences to rank and resolve.
// req - search request containing configuration.
// Returns ranked and resolved search results.
func (s *RetrievalService) applyExperienceRanking(ctx context.Context, experiences []*storage_models.Experience, req *SearchRequest) []*SearchResult {
	if len(experiences) == 0 {
		return []*SearchResult{}
	}

	// Extract base semantic scores from metadata
	baseScores := make([]float64, len(experiences))
	apiExperiences := make([]*experience.Experience, len(experiences))

	for i, exp := range experiences {
		// Get semantic similarity from metadata (stored by SearchByVector)
		semanticScore := 0.5 // default score
		if exp.Metadata != nil {
			if score, ok := exp.Metadata["similarity"].(float64); ok {
				semanticScore = score
			}
		}
		baseScores[i] = semanticScore

		// Convert to API model
		apiExperiences[i] = &experience.Experience{
			ID:               exp.ID,
			TenantID:         exp.TenantID,
			Type:             exp.Type,
			Problem:          exp.Problem,
			Solution:         exp.Solution,
			Constraints:      exp.Constraints,
			Embedding:        exp.Embedding,
			EmbeddingModel:   exp.EmbeddingModel,
			EmbeddingVersion: exp.EmbeddingVersion,
			Score:            exp.Score,
			Success:          exp.Success,
			AgentID:          exp.AgentID,
			UsageCount:       exp.UsageCount,
			DecayAt:          exp.DecayAt,
			CreatedAt:        exp.CreatedAt,
		}
	}

	// Apply ranking
	ranked := s.rankingService.Rank(ctx, apiExperiences, baseScores)

	// Apply conflict resolution if enabled
	var resolved []*experience.Experience
	if req.Plan.ExperienceConflictResolve && s.conflictResolver != nil {
		resolved = s.conflictResolver.Resolve(ctx, ranked)
	} else {
		// Extract experiences from ranked results
		resolved = make([]*experience.Experience, len(ranked))
		for i, r := range ranked {
			resolved[i] = r.Experience
		}
	}

	// Limit to TopK results
	if len(resolved) > req.Plan.TopK {
		resolved = resolved[:req.Plan.TopK]
	}

	// Convert to SearchResult
	return s.convertAPIExperiencesToResults(resolved)
}

// convertExperiencesToResults converts storage model experiences to search results.
// Args:
// experiences - storage model experiences.
// Returns search results.
func (s *RetrievalService) convertExperiencesToResults(experiences []*storage_models.Experience) []*SearchResult {
	results := make([]*SearchResult, 0, len(experiences))
	for _, exp := range experiences {
		result := &SearchResult{
			ID:        exp.ID,
			Content:   exp.Output,
			Source:    "experience",
			SubSource: "vector",
			Type:      "experience",
			Metadata:  exp.Metadata,
			CreatedAt: exp.CreatedAt,
		}

		// Extract similarity score from metadata if available
		if exp.Metadata != nil {
			if similarity, ok := exp.Metadata["similarity"].(float64); ok {
				result.Score = similarity
			}
		}

		results = append(results, result)
	}

	return results
}

// convertAPIExperiencesToResults converts API model experiences to search results.
// Args:
// experiences - API model experiences.
// Returns search results with ranking scores.
func (s *RetrievalService) convertAPIExperiencesToResults(experiences []*experience.Experience) []*SearchResult {
	results := make([]*SearchResult, 0, len(experiences))
	for _, exp := range experiences {
		result := &SearchResult{
			ID:        exp.ID,
			Content:   exp.Solution,
			Source:    "experience",
			SubSource: "vector",
			Type:      "experience",
			Metadata: map[string]interface{}{
				"problem":     exp.Problem,
				"constraints": exp.Constraints,
				"usage_count": exp.UsageCount,
				"success":     exp.Success,
				"agent_id":    exp.AgentID,
			},
			CreatedAt: exp.CreatedAt,
			Score:     exp.Score, // Use ranked score
		}

		results = append(results, result)
	}

	return results
}

// searchToolsVector performs vector search on tools using pgvector.
// This combines semantic search with usage statistics for tool ranking.
func (s *RetrievalService) searchToolsVector(ctx context.Context, embedding []float64, req *SearchRequest) []*SearchResult {
	if len(embedding) == 0 {
		return []*SearchResult{}
	}

	if s.toolRepo == nil {
		s.logger.Debug("ToolRepository not available, skipping tool search")
		return []*SearchResult{}
	}

	// Limit tool recommendations to avoid overwhelming results
	maxTools := 5
	if req.Plan.TopK < maxTools {
		maxTools = req.Plan.TopK
	}

	// Use Repository layer to search tools
	tools, err := s.toolRepo.SearchByVector(ctx, embedding, req.TenantID, maxTools)
	if err != nil {
		s.logger.Error("Tool vector search failed", "error", err)
		return []*SearchResult{}
	}

	// Convert Tool to SearchResult
	results := make([]*SearchResult, 0, len(tools))
	for _, tool := range tools {
		result := &SearchResult{
			ID:        tool.ID,
			Content:   tool.Description,
			Source:    "tool",
			SubSource: "vector",
			Type:      "tool",
			Metadata: map[string]interface{}{
				"name":         tool.Name,
				"agent_type":   tool.AgentType,
				"tags":         tool.Tags,
				"usage_count":  tool.UsageCount,
				"success_rate": tool.SuccessRate,
			},
			CreatedAt: tool.CreatedAt,
		}

		// Extract similarity score from metadata if available
		if similarity, ok := tool.Metadata["similarity"].(float64); ok {
			result.Score = similarity
		} else {
			// Default score based on success rate and usage
			result.Score = tool.SuccessRate*0.7 + float64(tool.UsageCount)/100.0*0.3
		}

		results = append(results, result)
	}

	return results
}

// bm25Search performs BM25 full-text search using PostgreSQL tsvector.
// This serves as a fallback when vector search fails or is disabled.
func (s *RetrievalService) bm25Search(ctx context.Context, req *SearchRequest) []*SearchResult {
	if req.Query == "" {
		return []*SearchResult{}
	}

	var results []*SearchResult

	// Search knowledge base using BM25
	knowledgeResults := s.bm25SearchKnowledge(ctx, req.Query, req.TenantID, req.Plan.TopK)
	results = append(results, knowledgeResults...)

	// Search experiences using BM25
	experienceResults := s.bm25SearchExperience(ctx, req.Query, req.TenantID, req.Plan.TopK)
	results = append(results, experienceResults...)

	// Search tools using BM25
	toolResults := s.bm25SearchTools(ctx, req.Query, req.TenantID, req.Plan.TopK)
	results = append(results, toolResults...)

	return results
}

// bm25SearchKnowledge performs BM25 search on knowledge base.
func (s *RetrievalService) bm25SearchKnowledge(ctx context.Context, query string, tenantID string, limit int) []*SearchResult {
	// Use Repository layer for keyword search
	chunks, err := s.kbRepo.SearchByKeyword(ctx, query, tenantID, limit)
	if err != nil {
		s.logger.Error("Knowledge BM25 search failed", "error", err)
		return []*SearchResult{}
	}

	// Convert KnowledgeChunk to SearchResult
	results := make([]*SearchResult, 0, len(chunks))
	for _, chunk := range chunks {
		result := &SearchResult{
			ID:        chunk.ID,
			Content:   chunk.Content,
			Source:    "knowledge",
			SubSource: "keyword",
			Type:      "knowledge",
			Metadata:  chunk.Metadata,
			CreatedAt: chunk.CreatedAt,
		}

		// Extract keyword score from metadata if available
		if score, ok := chunk.Metadata["keyword_score"].(float64); ok {
			result.Score = score
		}

		results = append(results, result)
	}

	return results
}

// bm25SearchExperience performs BM25 search on experiences.
func (s *RetrievalService) bm25SearchExperience(ctx context.Context, query string, tenantID string, limit int) []*SearchResult {
	if s.expRepo == nil {
		return []*SearchResult{}
	}

	experiences, err := s.expRepo.SearchByKeyword(ctx, query, tenantID, limit)
	if err != nil {
		s.logger.Error("Experience BM25 search failed", "error", err)
		return []*SearchResult{}
	}

	results := make([]*SearchResult, 0, len(experiences))
	for _, exp := range experiences {
		result := &SearchResult{
			ID:          exp.ID,
			Content:     exp.Output,
			Score:       exp.Score * 0.5, // BM25 scores are typically lower
			Source:      "experience",
			SubSource:   "keyword",
			Query:       query,
			QueryWeight: 1.0,
			Metadata: map[string]interface{}{
				"task_type": exp.Type,
				"success":   exp.Success,
				"agent_id":  exp.AgentID,
				"lessons":   exp.Input,
			},
			CreatedAt: exp.CreatedAt,
		}
		results = append(results, result)
	}

	return results
}

// bm25SearchTools performs BM25 search on tools.
func (s *RetrievalService) bm25SearchTools(ctx context.Context, query string, tenantID string, limit int) []*SearchResult {
	if s.toolRepo == nil {
		return []*SearchResult{}
	}

	tools, err := s.toolRepo.SearchByKeyword(ctx, query, tenantID, limit)
	if err != nil {
		s.logger.Error("Tool BM25 search failed", "error", err)
		return []*SearchResult{}
	}

	// Convert Tool to SearchResult
	results := make([]*SearchResult, 0, len(tools))
	for _, tool := range tools {
		result := &SearchResult{
			ID:        tool.ID,
			Content:   tool.Description,
			Source:    "tool",
			SubSource: "keyword",
			Type:      "tool",
			Metadata: map[string]interface{}{
				"name":         tool.Name,
				"agent_type":   tool.AgentType,
				"tags":         tool.Tags,
				"usage_count":  tool.UsageCount,
				"success_rate": tool.SuccessRate,
			},
			CreatedAt: tool.CreatedAt,
		}

		// Default score based on success rate and usage
		result.Score = tool.SuccessRate*0.7 + float64(tool.UsageCount)/100.0*0.3

		results = append(results, result)
	}

	return results
}

// mergeAndRerank merges and reranks results using deduplication with score accumulation.
// This implements the converged version where all weights are applied in rerank only.
// Args:
// results - all results from all queries.
// plan - retrieval plan with configuration.
// Returns merged and reranked results.
func (s *RetrievalService) mergeAndRerank(results []*SearchResult, plan *RetrievalPlan) []*SearchResult {
	if len(results) == 0 {
		return results
	}

	// 1. Deduplicate with score accumulation (improved version)
	deduped := s.deduplicateResults(results)

	// 2. Unified reranking (all weights applied here)
	reranked := s.rerankResults(deduped, plan)

	return reranked
}

// deduplicateResults removes duplicate results by ID and accumulates scores.
// This is the improved version that preserves "multi-hit signals" by accumulating scores.
// Multi-query hits get naturally higher scores without extra features.
// Args:
// results - results to deduplicate.
// Returns deduplicated results with accumulated scores.
func (s *RetrievalService) deduplicateResults(results []*SearchResult) []*SearchResult {
	seen := make(map[string]*SearchResult)

	for _, result := range results {
		if existing, exists := seen[result.ID]; exists {
			// Accumulate scores (30% of new score added)
			// This preserves multi-hit signals without over-weighting
			existing.Score += result.Score * 0.3

			// Update query info if this one has higher weight
			if result.QueryWeight > existing.QueryWeight {
				existing.Query = result.Query
				existing.QueryWeight = result.QueryWeight
			}
		} else {
			seen[result.ID] = result
		}
	}

	deduped := make([]*SearchResult, 0, len(seen))
	for _, result := range seen {
		deduped = append(deduped, result)
	}

	return deduped
}

// rerankResults performs unified reranking as the single scoring entry point.
// This applies all weights (query, source, subSource, signals) in one place.
// This fixes the double-application bug from the original design.
// Args:
// results - results to rerank.
// plan - retrieval plan with configuration.
// Returns reranked results sorted by final score.
func (s *RetrievalService) rerankResults(results []*SearchResult, plan *RetrievalPlan) []*SearchResult {
	if len(results) == 0 {
		return results
	}

	// Check if multiple sources are actually being searched
	// Only apply source weight if multiple sources are enabled
	multipleSourcesEnabled := false
	activeSources := 0
	if plan.SearchKnowledge {
		activeSources++
	}
	if plan.SearchExperience {
		activeSources++
	}
	if plan.SearchTools {
		activeSources++
	}
	if plan.SearchTaskResults {
		activeSources++
	}
	if activeSources > 1 {
		multipleSourcesEnabled = true
	}

	// Apply all weights here (unified scoring entry point)
	for _, result := range results {
		baseScore := result.Score

		// 1. Query weight (only applied here, not in merge)
		baseScore *= result.QueryWeight

		// 2. Source weight - only apply if multiple sources are being searched
		// This avoids reducing scores in single-source retrieval
		if multipleSourcesEnabled {
			baseScore *= s.sourceWeight(result.Source, plan)
		}

		// 3. SubSource weight (vector vs keyword)
		baseScore *= s.subSourceWeight(result.SubSource)

		// 4. Source-specific signals
		baseScore = s.applySourceSignals(baseScore, result)

		// 5. Time decay (if enabled)
		if plan.EnableTimeDecay {
			baseScore *= s.calculateTimeDecay(result.CreatedAt)
		}

		result.Score = baseScore
	}

	// Sort by score (descending)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results
}

// sourceWeight calculates weight based on result source.
// Args:
// source - result source (knowledge, experience, tool).
// plan - retrieval plan with source weights.
// Returns weight multiplier.
func (s *RetrievalService) sourceWeight(source string, plan *RetrievalPlan) float64 {
	switch source {
	case "experience":
		return plan.ExperienceWeight
	case "tool":
		return plan.ToolsWeight
	case "knowledge":
		return plan.KnowledgeWeight
	case "task_result":
		return plan.TaskResultsWeight
	default:
		return 1.0
	}
}

// subSourceWeight calculates weight based on sub-source (vector vs keyword).
// Vector search gets 1.0, keyword search gets 0.8 to avoid contamination.
// Args:
// sub - sub-source (vector, keyword).
// Returns weight multiplier.
func (s *RetrievalService) subSourceWeight(sub string) float64 {
	switch sub {
	case "vector":
		return 1.0 // Vector search is baseline
	case "keyword":
		return 0.8 // Keyword search has lower weight
	default:
		return 1.0
	}
}

// applySourceSignals applies source-specific signals to the score.
// Args:
// baseScore - current score.
// result - search result with metadata.
// Returns adjusted score.
func (s *RetrievalService) applySourceSignals(baseScore float64, result *SearchResult) float64 {
	// Experience-specific signals
	if result.Source == "experience" {
		if success, ok := result.Metadata["success"].(bool); ok {
			if success {
				baseScore *= 1.2 // Successful experiences get boost
			} else {
				baseScore *= 0.7 // Failed experiences get penalty
			}
		}

		// Execution time signal (faster experiences get preference)
		if executionTime, ok := result.Metadata["execution_time"].(float64); ok {
			// Normalize execution time: < 1s = 1.2x, 1-5s = 1.0x, > 5s = 0.8x
			if executionTime < 1.0 {
				baseScore *= 1.2 // Very fast experiences get boost
			} else if executionTime < 5.0 {
				baseScore *= 1.0 // Normal speed, no change
			} else {
				baseScore *= 0.8 // Slow experiences get penalty
			}
		}

		// Reuse count signal (highly reusable experiences get boost)
		if reuseCount, ok := result.Metadata["reuse_count"].(int); ok && reuseCount > 3 {
			baseScore *= 1.1
		}

		// Lessons learned signal (experiences with lessons get boost)
		if lessons, ok := result.Metadata["lessons"].(string); ok && lessons != "" {
			baseScore *= 1.05 // Experiences with documented lessons get slight boost
		}
	}

	// Tool-specific signals
	if result.Source == "tool" {
		if requiresAuth, ok := result.Metadata["requires_auth"].(bool); ok && requiresAuth {
			baseScore *= 0.9 // Tools requiring auth get slight penalty
		}

		// Success rate signal
		if successRate, ok := result.Metadata["success_rate"].(float64); ok {
			if successRate < 0.5 {
				baseScore *= 0.8 // Low success rate tools get penalty
			} else if successRate > 0.8 {
				baseScore *= 1.1 // High success rate tools get boost
			}
		}
	}

	return baseScore
}

// GenerateDebugInfo generates detailed debugging information for a search result.
// This helps answer "why this result is ranked first?" and supports observability.
// Args:
// result - search result to generate debug info for.
// plan - retrieval plan with weight configuration (optional, can be nil for default weights).
// Returns ResultDebugInfo with scoring breakdown and signals.
func (s *RetrievalService) GenerateDebugInfo(result *SearchResult, plan *RetrievalPlan) *ResultDebugInfo {
	sourceWeight := 1.0
	if plan != nil {
		switch result.Source {
		case "experience":
			sourceWeight = plan.ExperienceWeight
		case "tool":
			sourceWeight = plan.ToolsWeight
		case "knowledge":
			sourceWeight = plan.KnowledgeWeight
		case "task_result":
			sourceWeight = plan.TaskResultsWeight
		}
	} else {
		// Use default weights when plan is not provided
		switch result.Source {
		case "experience":
			sourceWeight = 1.2
		case "tool":
			sourceWeight = 1.1
		case "knowledge":
			sourceWeight = 1.0
		default:
			sourceWeight = 1.0
		}
	}

	info := &ResultDebugInfo{
		ID:           result.ID,
		Score:        result.Score,
		Query:        result.Query,
		QueryWeight:  result.QueryWeight,
		Source:       result.Source,
		SubSource:    result.SubSource,
		SourceWeight: sourceWeight,
		SubWeight:    s.subSourceWeight(result.SubSource),
		Signals:      make(map[string]interface{}),
		Breakdown:    make(map[string]float64),
	}

	// Collect source-specific signals
	if result.Source == "experience" {
		if success, ok := result.Metadata["success"].(bool); ok {
			info.Signals["success"] = success
		}
		if reuseCount, ok := result.Metadata["reuse_count"].(int); ok {
			info.Signals["reuse_count"] = reuseCount
		}
		if executionTime, ok := result.Metadata["execution_time"].(float64); ok {
			info.Signals["execution_time"] = executionTime
		}
		if lessons, ok := result.Metadata["lessons"].(string); ok {
			info.Signals["lessons"] = lessons
		}
	}

	if result.Source == "tool" {
		if requiresAuth, ok := result.Metadata["requires_auth"].(bool); ok {
			info.Signals["requires_auth"] = requiresAuth
		}
		if successRate, ok := result.Metadata["success_rate"].(float64); ok {
			info.Signals["success_rate"] = successRate
		}
	}

	// Score breakdown for analysis
	info.Breakdown["query"] = result.QueryWeight
	info.Breakdown["source"] = info.SourceWeight
	info.Breakdown["sub_source"] = info.SubWeight

	return info
}

// calculateTimeDecay calculates time-based decay factor for scoring.
// Newer content gets higher scores to prevent old data from dominating.
func (s *RetrievalService) calculateTimeDecay(createdAt time.Time) float64 {
	ageHours := time.Since(createdAt).Hours()
	lambda := 0.01 // Decay coefficient (configurable)

	// Exponential decay: older content has lower weight
	decay := math.Exp(-lambda * ageHours)

	// Ensure minimum decay factor to avoid completely ignoring old data
	if decay < 0.1 {
		decay = 0.1
	}

	return decay
}

// filterByScore filters results by minimum score threshold.
func (s *RetrievalService) filterByScore(results []*SearchResult, minScore float64) []*SearchResult {
	// Filter by minimum score (negative minScore means no filtering)
	filtered := make([]*SearchResult, 0, len(results))
	for _, result := range results {
		if result.Score >= minScore {
			filtered = append(filtered, result)
		}
	}

	return filtered
}

// countResultsBySource counts results by source for trace information.
func (s *RetrievalService) countResultsBySource(results []*SearchResult) map[string]int {
	counts := make(map[string]int)
	for _, result := range results {
		counts[result.Source]++
	}
	return counts
}

// Helper functions for string manipulation

// truncateForLog truncates string for logging
func truncateForLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

func toLower(s string) string {
	return strings.ToLower(s)
}

// contains reports whether substr is within s, using case-insensitive matching.
// Uses strings.ToLower for Unicode-safe comparison.
func contains(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// normalizeEnglishQuery normalizes English queries by expanding contractions and standardizing format.
// This improves query matching by converting common contractions to their full forms.
// Args:
// query - original query text.
// Returns normalized query text.
func normalizeEnglishQuery(query string) string {
	// Define common English contractions and their expansions
	contractions := map[string]string{
		"i'm":       "i am",
		"you're":    "you are",
		"he's":      "he is",
		"she's":     "she is",
		"it's":      "it is",
		"we're":     "we are",
		"they're":   "they are",
		"don't":     "do not",
		"doesn't":   "does not",
		"didn't":    "did not",
		"won't":     "will not",
		"wouldn't":  "would not",
		"shouldn't": "should not",
		"can't":     "cannot",
		"couldn't":  "could not",
		"mightn't":  "might not",
		"mustn't":   "must not",
		"let's":     "let us",
		"that's":    "that is",
		"what's":    "what is",
		"where's":   "where is",
		"who's":     "who is",
		"how's":     "how is",
	}

	// Normalize to lowercase for matching
	queryLower := toLower(query)

	// Replace contractions with their full forms
	for contraction, expansion := range contractions {
		queryLower = replaceAllIgnoreCase(queryLower, contraction, expansion)
	}

	// Trim extra spaces
	queryLower = trimSpaces(queryLower)

	return queryLower
}

// replaceAllIgnoreCase replaces all occurrences of a substring case-insensitively.
// Args:
// s - original string.
// old - substring to replace.
// new - replacement string.
// Returns string with all replacements applied.
func replaceAllIgnoreCase(s, old, new string) string {
	sLower := toLower(s)
	oldLower := toLower(old)

	result := ""
	i := 0
	for i < len(sLower) {
		if i <= len(sLower)-len(oldLower) && sLower[i:i+len(oldLower)] == oldLower {
			result += new
			i += len(oldLower)
		} else {
			result += string(s[i])
			i++
		}
	}

	return result
}

// trimSpaces removes extra spaces from a string, keeping only single spaces.
// Args:
// s - string to trim.
// Returns string with normalized spacing.
func trimSpaces(s string) string {
	// Trim leading and trailing spaces
	s = strings.TrimSpace(s)

	// Replace multiple spaces with single space
	var result strings.Builder
	prevSpace := false

	for _, ch := range s {
		if ch == ' ' {
			if !prevSpace {
				result.WriteRune(ch)
				prevSpace = true
			}
		} else {
			result.WriteRune(ch)
			prevSpace = false
		}
	}

	return result.String()
}
