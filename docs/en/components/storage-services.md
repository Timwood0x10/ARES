# Services Module API Documentation

## Overview

The Services module provides the business logic layer, encapsulating complex data access operations and retrieval logic. This module implements advanced features such as multi-source retrieval, query rewriting, and time-based decay.

## Core Services

### RetrievalService

Intelligent retrieval service supporting hybrid search across multiple data sources.

#### Main Features

- **Hybrid Search**: Combines vector search and keyword search (BM25)
- **Multi-Source Retrieval**: Supports multiple data sources including knowledge base, experience repository, tools, and task results
- **Query Rewriting**: Automatically optimizes queries for better retrieval results
- **Time Decay**: Time-based scoring decay to prioritize recent content
- **Result Fusion**: Intelligently merges and ranks results from different sources
- **Tenant Isolation**: All operations support multi-tenant isolation

#### Core Data Structures

##### SearchRequest

Search request configuration:

```go
type SearchRequest struct {
    Query       string          // Search query text
    TenantID    string          // Tenant ID for isolation
    TopK        int             // Number of results to return
    MinScore    float64         // Minimum similarity score
    Plan        *RetrievalPlan  // Retrieval strategy
    EnableTrace bool            // Enable trace logging
    Trace       *RetrievalTrace // Trace information
}
```

##### RetrievalPlan

Retrieval strategy configuration:

```go
type RetrievalPlan struct {
    // Data source configuration
    SearchKnowledge   bool    // Search in knowledge base
    SearchExperience  bool    // Search in experience repository
    SearchTools       bool    // Search in tools
    SearchTaskResults bool    // Search in task results

    // Weight configuration
    KnowledgeWeight   float64 // Weight for knowledge results (default 0.4)
    ExperienceWeight  float64 // Weight for experience results (default 0.3)
    ToolsWeight       float64 // Weight for tool results (default 0.2)
    TaskResultsWeight float64 // Weight for task result results (default 0.1)

    // Feature configuration
    EnableQueryRewrite  bool // Enable query rewriting
    EnableKeywordSearch bool // Enable keyword/BM25 search
    EnableTimeDecay     bool // Enable time-based scoring decay

    TopK int // Maximum results per source
}
```

##### SearchResult

Search result:

```go
type SearchResult struct {
    ID        string                 // Result ID
    Content   string                 // Result content
    Score     float64                // Similarity score
    Source    string                 // Source (knowledge, experience, tool, task_result)
    Type      string                 // Result type for filtering
    Metadata  map[string]interface{} // Additional metadata
    CreatedAt time.Time              // Creation time
}
```

##### RetrievalTrace

Retrieval trace information:

```go
type RetrievalTrace struct {
    OriginalQuery   string        // Original query
    RewrittenQuery  string        // Rewritten query
    RewriteUsed     bool          // Whether query rewrite was used
    VectorResults   int           // Number of vector search results
    KeywordResults  int           // Number of keyword search results
    FinalResults    int           // Number of final results
    ExecutionTime   time.Duration // Execution time
    VectorError     error         // Vector search error
    SearchBreakdown map[string]int // Results per source
}
```

#### Main Methods

| Method | Description |
|--------|-------------|
| `NewRetrievalService(...)` | Create new retrieval service instance |
| `Search(ctx, req)` | Execute intelligent retrieval |
| `validateRequest(req)` | Validate search request |
| `getEmbedding(ctx, query)` | Get vector embedding for query |
| `shouldRewriteQuery(query)` | Determine if query should be rewritten |
| `isQueryInCache(query)` | Check if query is in cache |
| `queryRewrite(ctx, query)` | Execute query rewriting |
| `parallelVectorSearch(...)` | Parallel vector search |
| `searchKnowledgeVector(...)` | Vector search in knowledge base |
| `searchExperienceVector(...)` | Vector search in experience repository |
| `searchToolsVector(...)` | Vector search in tools |
| `bm25Search(...)` | Execute BM25 keyword search |
| `bm25SearchKnowledge(...)` | BM25 search in knowledge base |
| `bm25SearchExperience(...)` | BM25 search in experience repository |
| `bm25SearchTools(...)` | BM25 search in tools |
| `mergeAndRank(...)` | Merge and rank results |
| `calculateTimeDecay(createdAt)` | Calculate time decay factor |
| `filterByScore(results, minScore)` | Filter results by score |
| `countResultsBySource(results)` | Count results by source |

#### Usage Examples

##### Basic Search

```go
service := services.NewRetrievalService(
    pool,
    embeddingClient,
    tenantGuard,
    retrievalGuard,
    kbRepo,
)

// Create search request
req := &services.SearchRequest{
    Query:    "How to use Go for concurrent programming",
    TenantID: "tenant-1",
    TopK:     10,
    Plan:     services.DefaultRetrievalPlan(),
}

// Execute search
results, err := service.Search(ctx, req)
if err != nil {
    // Handle error
}

// Process results
for _, result := range results {
    fmt.Printf("Score: %.2f, Content: %s\n", result.Score, result.Content)
}
```

##### Custom Retrieval Strategy

```go
// Create custom retrieval plan
plan := &services.RetrievalPlan{
    SearchKnowledge:   true,
    SearchExperience:  true,
    SearchTools:       true,
    SearchTaskResults: false,

    KnowledgeWeight:   0.5,  // Increase knowledge base weight
    ExperienceWeight:  0.3,
    ToolsWeight:       0.2,
    TaskResultsWeight: 0.0,

    EnableQueryRewrite:  true,  // Enable query rewriting
    EnableKeywordSearch: true,  // Enable keyword search
    EnableTimeDecay:     true,  // Enable time decay

    TopK: 15,  // Return 15 results per source
}

req := &services.SearchRequest{
    Query:    "Machine learning best practices",
    TenantID: "tenant-1",
    TopK:     20,
    Plan:     plan,
    MinScore: 0.7,  // Minimum similarity score
}

results, err := service.Search(ctx, req)
```

##### Enable Tracing

```go
req := &services.SearchRequest{
    Query:       "API security design",
    TenantID:    "tenant-1",
    TopK:        10,
    Plan:        services.DefaultRetrievalPlan(),
    EnableTrace: true,  // Enable tracing
}

results, err := service.Search(ctx, req)

// Access trace information
if req.Trace != nil {
    fmt.Printf("Original query: %s\n", req.Trace.OriginalQuery)
    fmt.Printf("Rewritten query: %s\n", req.Trace.RewrittenQuery)
    fmt.Printf("Execution time: %v\n", req.Trace.ExecutionTime)
    fmt.Printf("Result distribution: %v\n", req.Trace.SearchBreakdown)
}
```

#### Default Configuration

```go
// Default retrieval plan
plan := services.DefaultRetrievalPlan()

// Default configuration:
// - SearchKnowledge: true
// - SearchExperience: true
// - SearchTools: true
// - SearchTaskResults: false
// - KnowledgeWeight: 0.4
// - ExperienceWeight: 0.3
// - ToolsWeight: 0.2
// - TaskResultsWeight: 0.1
// - EnableQueryRewrite: false
// - EnableKeywordSearch: true
// - EnableTimeDecay: true
// - TopK: 10
```

#### Retrieval Algorithms

##### 1. Hybrid Search

RetrievalService combines two search methods:

- **Vector Search**: Uses embedding vectors for semantic similarity matching
- **Keyword Search**: Uses BM25 algorithm for keyword matching

Results are fused through weighted averaging and re-ranking.

##### 2. Time Decay

To prioritize recent content, RetrievalService implements time decay:

```go
// Time decay formula
decayFactor = 0.9 ^ (daysOld / 30)

// Minimum decay factor is 0.1
finalScore = baseScore * decayFactor
```

##### 3. Query Rewriting

Query rewriting optimizes queries for better retrieval results:

- Identify synonyms
- Expand queries
- Correct spelling errors
- Add related terms

#### Error Handling

Service returns the following error types:

- `errors.ErrInvalidArgument`: Invalid search request
- `errors.ErrEmbeddingFailed`: Embedding generation failure
- `errors.ErrTenantIsolation`: Tenant isolation validation failure

#### Performance Optimization

- **Parallel Search**: Multiple data sources searched in parallel
- **Result Caching**: Embedding vectors cached to reduce computation
- **Batch Processing**: Supports batch retrieval operations
- **Rate Limiting**: Prevents excessive requests

#### Test Coverage

Current test coverage: 52.4%

Tested functionality:
- Default retrieval plan configuration
- Time decay calculation
- Score filtering
- Result merging and ranking
- Search request validation
- Query rewrite decision logic
- Result source counting
- Helper functions (string manipulation, log truncation, etc.) - 100% coverage
- Retrieval service constructor
- Vector search (knowledge base)
- BM25 search (knowledge base)
- Query rewrite functionality
- Query cache checking
- Experience repository search (returns empty results) - 100% coverage
- Tool repository search (returns empty results) - 100% coverage
- Result fusion (including different sources and time decay)
- Edge case handling (empty results, unknown sources, etc.)
- Unicode character handling
- Time decay edge cases (future time, zero time)
- Result deduplication and score combination
- Weight configuration validation
- Query length and special character handling
- Large dataset processing (50+ results)
- All data source type testing
- Exact threshold matching
- Specific time point time decay
- Various score ranges (zero, high, negative)
- Validation edge cases (TopK=1, very large TopK)
- Special character and number processing
- Case-insensitive matching

## Best Practices

### 1. Choose Appropriate Retrieval Strategy

Select appropriate retrieval plan based on use case:

```go
// Academic research scenario: focus on knowledge base and experience
plan := &services.RetrievalPlan{
    SearchKnowledge:   true,
    SearchExperience:  true,
    SearchTools:       false,
    SearchTaskResults: false,
    KnowledgeWeight:   0.6,
    ExperienceWeight:  0.4,
}
```

### 2. Set Appropriate TopK

```go
// Recommended TopK values:
// - Quick response: 5-10
// - General search: 10-20
// - Deep search: 20-50
```

### 3. Enable Tracing for Debugging

```go
// Enable tracing during development and debugging
if os.Getenv("ENV") == "development" {
    req.EnableTrace = true
}
```

### 4. Handle Errors Properly

```go
results, err := service.Search(ctx, req)
if err != nil {
    if errors.Is(err, errors.ErrInvalidArgument) {
        // Handle invalid argument
    } else if errors.Is(err, errors.ErrEmbeddingFailed) {
        // Handle embedding failure
    } else {
        // Handle other errors
    }
}
```
