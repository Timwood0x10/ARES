# LLM Query Rewrite Integration

This document describes how to integrate and use LLM-based query rewriting in the retrieval service.

## Overview

The retrieval service now supports LLM-based query rewriting to improve search results. The system supports two LLM providers:

1. **OpenRouter** - Cloud-based LLM API with multiple models
2. **Ollama** - Local LLM server for privacy and performance

## Configuration

### 1. Using Ollama (Recommended for Local Development)

```yaml
llm:
  provider: "ollama"
  base_url: "http://localhost:11434"
  model: "llama3"
  timeout: 30
```

**Setup Ollama:**
```bash
# Install Ollama
curl -fsSL https://ollama.com/install.sh | sh

# Start Ollama server
ollama serve

# Pull a model (optional, will download automatically)
ollama pull llama3
```

### 2. Using OpenRouter

```yaml
llm:
  provider: "openrouter"
  api_key: "sk-or-v1-your-api-key"
  base_url: "https://openrouter.ai/api/v1"
  model: "minimax/minimax-m2-her"
  timeout: 30
```

**Get API Key:**
- Visit [OpenRouter](https://openrouter.ai/)
- Sign up and get your API key
- Replace in configuration

## Query Priority Configuration

Control the influence of different query types:

```yaml
query_priority:
  original_weight: 1.0    # Original query (strongest)
  rule_rewrite_weight: 0.7  # Rule-based rewrite
  llm_rewrite_weight: 0.5   # LLM rewrite (weakest)
  max_queries: 3            # Total queries to use
```

## Usage

### Automatic Integration

The LLM client is automatically integrated into the retrieval service:

```go
// Create retrieval service with LLM client
llmClient, err := llm.NewClientFromEnv()
if err != nil {
    log.Warn("Failed to create LLM client", "error", err)
    llmClient = nil // Will fall back to rule-based rewriting only
}

retrievalService := services.NewRetrievalService(
    pool,
    embeddingClient,
    llmClient, // LLM client for query rewriting
    tenantGuard,
    retrievalGuard,
    kbRepo,
    expRepo,
    toolRepo,
)
```

### Environment Variables

You can configure LLM via environment variables:

```bash
# Provider
export LLM_PROVIDER="ollama"

# Ollama
export LLM_BASE_URL="http://localhost:11434"
export LLM_MODEL="llama3"

# OpenRouter
export LLM_PROVIDER="openrouter"
export LLM_API_KEY="sk-or-v1-your-key"
export LLM_BASE_URL="https://openrouter.ai/api/v1"
export LLM_MODEL="minimax/minimax-m2-her"
```

## Query Rewrite Process

1. **Original Query** (weight: 1.0)
   - Always included
   - Strongest influence

2. **Rule-Based Rewrite** (weight: 0.7)
   - Uses synonym rules from `configs/synonyms.yaml`
   - Fast and predictable
   - Always available

3. **LLM Rewrite** (weight: 0.5)
   - Uses LLM to generate variations
   - High quality but slower
   - Falls back gracefully if unavailable

## LLM Prompt

The system uses the following prompt for query rewriting:

```
You are a search query optimization assistant. Your task is to rewrite the given search query to improve retrieval results.

Rules:
1. Keep the original intent but use different wording
2. Generate up to 3 alternative queries
3. Return each query on a separate line
4. Be concise and clear
5. Focus on semantic similarity rather than exact matches

Original Query: {query}

Rewritten Queries (one per line):
```

## Synonym Configuration

Edit `configs/synonyms.yaml` to add or modify synonym rules:

```yaml
programming:
  - "coding"
  - "development"
  - "software development"

database:
  - "db"
  - "data storage"
  - "sql"
  - "nosql"
```

## Error Handling

The system gracefully handles LLM failures:

- If LLM client is not configured → Uses rule-based rewriting only
- If LLM call fails → Returns empty list, logs warning
- If LLM timeout → Returns empty list, logs warning
- System continues to work with available methods

## Performance

- **Rule-based rewriting**: < 1ms
- **LLM rewriting**: 500ms - 3s (depends on provider)
- **Embedding cache**: Reduces repeated LLM calls by 50-75%

## Testing

Test LLM integration:

```go
// Test with actual LLM
client, _ := llm.NewClient(&llm.Config{
    Provider: "ollama",
    BaseURL:  "http://localhost:11434",
    Model:    "llama3",
    Timeout:  10,
})

response, err := client.Generate(ctx, "Say 'hello'")
if err != nil {
    log.Error("LLM call failed", "error", err)
}
```

## Troubleshooting

### LLM Not Working

1. Check if LLM is running:
   ```bash
   # For Ollama
   curl http://localhost:11434/api/tags

   # For OpenRouter
   curl -H "Authorization: Bearer YOUR_KEY" https://openrouter.ai/api/v1/models
   ```

2. Check logs for error messages

3. Verify configuration in config file

### Poor Rewrite Quality

1. Adjust the prompt in `llmBasedRewrite` function
2. Try different models
3. Add more rules to `synonyms.yaml`
4. Adjust weights in `query_priority` config

### Performance Issues

1. Increase `llm_rewrite_weight` to reduce reliance on LLM
2. Use faster models
3. Increase timeout value
4. Consider local Ollama instead of cloud API

## Best Practices

1. **Start with rule-based**: Ensure synonyms.yaml is comprehensive
2. **Test locally first**: Use Ollama before cloud APIs
3. **Monitor performance**: Track LLM call latency
4. **Set reasonable timeouts**: 30s is a good starting point
5. **Log warnings**: Monitor LLM failures for debugging
6. **Provide fallbacks**: Always ensure rule-based rewriting works

## Future Enhancements

Potential improvements:

1. **Query caching**: Cache LLM rewrite results
2. **Batch rewriting**: Rewrite multiple queries together
3. **Model selection**: Choose model based on query complexity
4. **Confidence scoring**: Rate LLM rewrite quality
5. **Feedback loop**: Learn from user clicks on results