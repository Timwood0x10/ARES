# Retrieval Strategy Guide

## Overview

The GoAgent Storage module provides two retrieval strategies to accommodate different use cases:

1. **Simple Retrieval** - Pure vector similarity search
2. **Advanced Retrieval** - Multi-source hybrid search with advanced features

## Simple Retrieval (Recommended for Most Use Cases)

### Configuration

```go
req := &services.SearchRequest{
    Query:    question,
    TenantID: tenantID,
    TopK:     5,
    MinScore: 0.6,
    Plan: &services.RetrievalPlan{
        SearchKnowledge:     true,
        KnowledgeWeight:     1.0,
        EnableKeywordSearch: false,  // Disable keyword search
        EnableTimeDecay:     false, // Disable time decay
        TopK:                5,
    },
}
```

### When to Use

- ✅ **Single knowledge base** (knowledge only)
- ✅ **Simple semantic search** (RAG, Q&A)
- ✅ **Document similarity** (finding similar documents)
- ✅ **Codebase search** (finding similar code)

### Characteristics

- **Performance**: Fast (single vector search)
- **Accuracy**: High for semantic similarity
- **Complexity**: Simple (minimal configuration)
- **Resources**: Lower (no additional computations)

### Score Calculation

```
Final Score = Raw Cosine Similarity
```

The score is the raw cosine similarity from pgvector (range: -1 to 1, typically 0.6-0.9 for relevant results).

### Example Results

Query: "RAG"

| Rank | Similarity | Content |
|------|-----------|---------|
| 1 | 0.79 | Configuration parameters for chunk size and overlap |
| 2 | 0.72 | [RAG Best Practices](https://docs.anthropic.com) |
| 3 | 0.68 | Hybrid retrieval flow documentation |

## Advanced Retrieval (For Complex Multi-Source Scenarios)

### Configuration

```go
req := &services.SearchRequest{
    Query:    question,
    TenantID: tenantID,
    TopK:     10,
    MinScore: 0.5,
    Plan: &services.RetrievalPlan{
        SearchKnowledge:     true,
		SearchExperience:    true,
		SearchTools:         true,
		SearchTaskResults:   false,
		KnowledgeWeight:     0.4,
		ExperienceWeight:    0.3,
		ToolsWeight:         0.2,
		TaskResultsWeight:   0.1,
		EnableQueryRewrite:  true,
		EnableKeywordSearch: true,
		EnableTimeDecay:     true,
		TopK:                10,
    },
}
```

### When to Use

- ✅ **Multi-source retrieval** (knowledge + experiences + tools)
- ✅ **Hybrid search** (vector + keyword/BM25)
- ✅ **Query rewriting** (semantic expansion)
- ✅ **Time-sensitive data** (prioritize recent information)
- ✅ **Complex enterprise systems** (multiple data sources)

### Characteristics

- **Performance**: Slower (multiple searches + reranking)
- **Accuracy**: High (multi-source, intelligent reranking)
- **Complexity**: Complex (many configurable features)
- **Resources**: Higher (additional embeddings, computations)

### Score Calculation

```
Final Score = Raw Similarity × Query Weight × Source Weight × Sub-Source Weight × Time Decay × Signals
```

#### Score Components

1. **Raw Similarity**: Cosine similarity from pgvector (0.6-0.9)
2. **Query Weight**: 
   - Original query: 1.0
   - Rule-based rewrite: 0.7
   - LLM-based rewrite: 0.5
3. **Source Weight**:
   - Knowledge: 0.4
   - Experience: 0.3
   - Tools: 0.2
   - Task Results: 0.1
4. **Sub-Source Weight**:
   - Vector search: 1.0
   - Keyword search: 0.8
5. **Time Decay**: 
   - Exponential decay: `exp(-0.01 × age_in_hours)`
   - Minimum value: 0.1
6. **Source-Specific Signals**:
   - Experience: Success rate (1.2×), execution time (0.8-1.2×)
   - Tools: Success rate (0.8-1.1×), auth required (0.9×)

### Example Results

Query: "How to configure chunk size?"

| Rank | Raw Score | Final Score | Source | Content |
|------|-----------|-------------|--------|---------|
| 1 | 0.85 | 0.34 | Knowledge | Chunk size configuration guide |
| 2 | 0.72 | 0.22 | Knowledge | Parameter tuning best practices |
| 3 | 0.65 | 0.13 | Experience | Previous configuration issues |
| 4 | 0.58 | 0.12 | Knowledge | Advanced configuration options |

## Performance Comparison

| Metric | Simple Retrieval | Advanced Retrieval |
|--------|-----------------|-------------------|
| Query Time | ~50ms | ~200-500ms |
| Memory Usage | Low | Medium |
| CPU Usage | Low | Medium-High |
| Retrieval Quality | High (single source) | High (multi-source) |

## Choosing the Right Strategy

### Use Simple Retrieval If:

1. You only need to search one data source (e.g., knowledge base)
2. You want maximum performance
3. You need straightforward semantic similarity
4. You have a single-purpose application (e.g., document Q&A)
5. Your data doesn't require time-based prioritization

### Use Advanced Retrieval If:

1. You need to search multiple data sources (knowledge + experiences + tools)
2. You need keyword matching in addition to semantic search
3. You want query expansion/rewriting
4. You have time-sensitive data that needs prioritization
5. You're building a complex enterprise system with multiple retrieval needs

## Configuration Examples

### Example 1: RAG System (Simple)

```go
req := &services.SearchRequest{
    Query:    "What is RAG?",
    TenantID: "user-123",
    TopK:     5,
    MinScore: 0.6,
    Plan: &services.RetrievalPlan{
        SearchKnowledge:     true,
        KnowledgeWeight:     1.0,
        EnableKeywordSearch: false,
        EnableTimeDecay:     false,
        TopK:                5,
    },
}
```

### Example 2: Multi-Source Enterprise System (Advanced)

```go
req := &services.SearchRequest{
    Query:    "How do I use the storage module?",
    TenantID: "company-xyz",
    TopK:     10,
    MinScore: 0.5,
    Plan: &services.RetrievalPlan{
        SearchKnowledge:     true,
        SearchExperience:    true,
        SearchTools:         true,
        SearchTaskResults:   true,
        KnowledgeWeight:     0.5,
        ExperienceWeight:    0.3,
        ToolsWeight:         0.15,
		TaskResultsWeight:   0.05,
        EnableQueryRewrite:  true,
        EnableKeywordSearch: true,
        EnableTimeDecay:     true,
        TopK:                10,
    },
}
```

### Example 3: Document Similarity (Simple)

```go
req := &services.SearchRequest{
    Query:    "Similar to my last question",
    TenantID: "user-456",
    TopK:     3,
    MinScore: 0.7,
    Plan: &services.RetrievalPlan{
        SearchKnowledge:     true,
        KnowledgeWeight:     1.0,
        EnableKeywordSearch: false,
        EnableTimeDecay:     false,
        TopK:                3,
    },
}
```

## Tuning Tips

### Simple Retrieval

1. **MinScore**:
   - 0.7-0.8: High precision, fewer results
   - 0.6-0.7: Balance precision and recall
   - 0.5-0.6: More results, lower precision

2. **TopK**:
   - 3-5: Focused answers
   - 5-10: Comprehensive answers (recommended)
   - 10+: Broad exploration

3. **Chunk Size** (document ingestion):
   - 200-300: High precision, may lose context
   - 500-700: Balance precision and context (recommended)
   - 1000+: More context, lower precision

### Advanced Retrieval

1. **Source Weights**: Adjust based on data importance
   - Increase KnowledgeWeight if documents are primary source
   - Increase ExperienceWeight if past solutions are valuable
   - Increase ToolsWeight if tool recommendations are key

2. **Query Rewriting**: Enable for ambiguous queries
   - Helps expand semantic understanding
   - Can improve recall at cost of latency

3. **Time Decay**: Enable for frequently updated content
   - Lambda 0.01 is default
   - Adjust lambda for faster/slower decay

## Migration Guide

### From Advanced to Simple

If you find advanced retrieval too complex or slow, simplify your configuration:

```go
// Before (Advanced)
Plan: &services.RetrievalPlan{
    SearchKnowledge:     true,
    KnowledgeWeight:     0.4,
    EnableKeywordSearch: true,
    EnableTimeDecay:     true,
    TopK:                10,
}

// After (Simple)
Plan: &services.RetrievalPlan{
    SearchKnowledge:     true,
    KnowledgeWeight:     1.0,
    EnableKeywordSearch: false,
    EnableTimeDecay:     false,
    TopK:                5,
}
```

### Performance Impact

Switching from Advanced to Simple Retrieval:

- ⚡ **4-10x faster** (50ms → 200-500ms)
- 💾 **30% less memory** (no keyword results to process)
- 🔍 **Simpler results** (pure semantic similarity)

## Best Practices

1. **Start Simple**: Begin with simple retrieval, add features as needed
2. **Measure Performance**: Monitor query times and result quality
3. **A/B Test**: Test both strategies with your data
4. **Adjust Gradually**: Change one parameter at a
5. **Monitor Scores**: Ensure MinScore threshold is appropriate

## Troubleshooting

### Issue: No Results Returned

**Simple Retrieval:**
- Check MinScore is not too high (try 0.5)
- Verify documents are indexed
- Check embedding model is working

**Advanced Retrieval:**
- Check all source weights are not 0
- Verify at least one source is enabled
- Check MinScore after all weight calculations

### Issue: Results Not Relevant

**Simple Retrieval:**
- Increase MinScore (0.5 → 0.7)
- Check chunk size (try smaller chunks)
- Verify embedding model quality

**Advanced Retrieval:**
- Adjust source weights
- Disable unnecessary features (time decay, query rewrite)
- Check if keyword search is adding noise

### Issue: Slow Performance

**Simple Retrieval:**
- Reduce TopK (10 → 5)
- Check database indexes
- Verify pgvector is optimized

**Advanced Retrieval:**
- Disable query rewriting
- Disable keyword search
- Reduce number of enabled sources
- Reduce TopK

## References

- [pgvector Documentation](https://github.com/pgvector/pgvector)
- [ChromaDB Best Practices](https://docs.trychroma.com/guides)
- [RAG Best Practices](https://docs.anthropic.com/claude/docs/retrieval-augmented-generation)
- [Vector Search Optimization](https://www.pinecone.io/learn/vector-search-optimization/)