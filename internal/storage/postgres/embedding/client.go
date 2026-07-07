// nolint: errcheck // Operations may ignore return values
package embedding

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/Timwood0x10/ares/internal/errors"

	"golang.org/x/crypto/blake2b"
)

// HTTPError represents an HTTP request error.
type HTTPError struct {
	StatusCode int
	Message    string
}

// Error returns the error message.
func (e *HTTPError) Error() string {
	return e.Message
}

// EmbeddingClient provides vector embedding functionality with caching.

type EmbeddingClient struct {
	httpClient *http.Client

	redis RedisClient

	baseURL string

	model string

	timeout time.Duration

	cacheTTL time.Duration

	enabled atomic.Bool
}

// GetModel returns the embedding model name.
func (c *EmbeddingClient) GetModel() string {
	return c.model
}

// GetTimeout returns the embedding timeout.
func (c *EmbeddingClient) GetTimeout() time.Duration {
	return c.timeout
}

// NewEmbeddingClient creates a new embedding client.

// redisClient is optional. If nil, the client will work without Redis.

func NewEmbeddingClient(baseURL, model string, redisClient RedisClient, timeout time.Duration) *EmbeddingClient {
	c := &EmbeddingClient{

		httpClient: &http.Client{

			Timeout: timeout,

			Transport: &http.Transport{

				MaxIdleConns: 100,

				MaxIdleConnsPerHost: 10,

				IdleConnTimeout: 90 * time.Second,
			},
		},

		redis: redisClient,

		baseURL: strings.TrimSuffix(baseURL, "/"),

		model: model,

		timeout: timeout,

		cacheTTL: 24 * time.Hour,
	}
	c.enabled.Store(true)
	return c
}

// Embed generates vector embedding for a query text (uses "query" prefix).
// For document storage, use EmbedWithPrefix with "passage:" prefix.
// Args:
// ctx - operation context.
// text - text to embed.
// Returns embedding vector or error.
func (c *EmbeddingClient) Embed(ctx context.Context, text string) ([]float64, error) {
	return c.EmbedWithPrefix(ctx, text, "query:")
}

// EmbedWithPrefix generates vector embedding with custom prefix.
// Use "query:" for search queries and "passage:" for document storage.
// Args:
// ctx - operation context.
// text - text to embed.
// prefix - prefix to add before text (e.g., "query:", "passage:").
// Returns embedding vector or error.
func (c *EmbeddingClient) EmbedWithPrefix(ctx context.Context, text, prefix string) ([]float64, error) {
	if !c.enabled.Load() {
		return nil, fmt.Errorf("embedding client is disabled")
	}

	// Normalize text to avoid cache miss explosion
	normalizedText := c.normalizeText(text)

	// Generate cache key
	cacheKey := c.getCacheKey(normalizedText, prefix)

	// Try to get from Redis cache
	if c.redis != nil {
		cached, err := c.redis.Get(ctx, cacheKey)
		if err == nil {
			var embedding []float64
			if err := json.Unmarshal([]byte(cached), &embedding); err == nil {
				return embedding, nil
			}
		}
	}

	// Call embedding service with specified prefix
	embedding, err := c.callEmbeddingService(ctx, normalizedText, prefix)
	if err != nil {
		return nil, err
	}

	// Cache the result
	if c.redis != nil {
		if data, err := json.Marshal(embedding); err == nil {
			if err := c.redis.Set(ctx, cacheKey, data, c.cacheTTL); err != nil {
				log.Warn("embedding client: cache set", "key", cacheKey, "error", err)
			}
		}
	}

	return embedding, nil
}

// EmbedBatch generates vector embeddings for multiple texts.
func (c *EmbeddingClient) EmbedBatch(ctx context.Context, texts []string) ([][]float64, error) {
	if !c.enabled.Load() {
		return nil, fmt.Errorf("embedding client is disabled")
	}

	// Normalize all texts
	normalizedTexts := make([]string, len(texts))
	for i, text := range texts {
		normalizedTexts[i] = c.normalizeText(text)
	}

	// Try to get from cache for each text
	embeddings := make([][]float64, len(texts))
	uncachedIndices := []int{}
	uncachedTexts := []string{}

	for i, text := range normalizedTexts {
		cacheKey := c.getCacheKey(text, "query")

		if c.redis != nil {
			cached, err := c.redis.Get(ctx, cacheKey)
			if err == nil {
				if err := json.Unmarshal([]byte(cached), &embeddings[i]); err == nil {
					continue
				}
			}
		}
		uncachedIndices = append(uncachedIndices, i)
		uncachedTexts = append(uncachedTexts, text)
	}

	// Batch call for uncached texts
	if len(uncachedTexts) > 0 {
		batchEmbeddings, err := c.callEmbeddingBatchService(ctx, uncachedTexts, "query")
		if err != nil {
			return nil, err
		}

		// Assign batch results and cache them
		for i, idx := range uncachedIndices {
			embeddings[idx] = batchEmbeddings[i]

			if c.redis != nil {
				cacheKey := c.getCacheKey(uncachedTexts[i], "query")
				if data, err := json.Marshal(batchEmbeddings[i]); err == nil {
					if err := c.redis.Set(ctx, cacheKey, data, c.cacheTTL); err != nil {
						log.Warn("embedding client: set embedding cache", "key", cacheKey, "error", err)
					}
				}
			}
		}
	}

	return embeddings, nil
}

// normalizeText normalizes text to avoid cache miss explosion.
// This includes: Unicode normalization, lowercase, trim spaces, remove extra spaces.
func (c *EmbeddingClient) normalizeText(text string) string {
	// 1. Lowercase
	text = strings.ToLower(text)

	// 2. Trim spaces
	text = strings.TrimSpace(text)

	// 3. Remove extra spaces (including unicode spaces)
	var result strings.Builder
	prevSpace := false
	for _, r := range text {
		if unicode.IsSpace(r) {
			if !prevSpace {
				result.WriteRune(' ')
				prevSpace = true
			}
		} else {
			result.WriteRune(r)
			prevSpace = false
		}
	}

	text = result.String()
	text = strings.TrimSpace(text)

	return text
}

// getCacheKey generates a standardized cache key using BLAKE2b-128 hash.
// BLAKE2b provides:
// - Security: Cryptographically secure hash function
// - Performance: 20-30% faster than SHA256
// - Efficiency: 128-bit output is sufficient for cache key collision resistance
func (c *EmbeddingClient) getCacheKey(text, method string) string {
	// Normalize text first
	normalized := c.normalizeText(text)

	// Combine multiple factors for unique key
	// method is the prefix (e.g., "query:" or "passage:")
	keyData := fmt.Sprintf("%s|%s|%s", normalized, c.model, method)

	// Use BLAKE2b-256 and truncate to 128 bits for security and performance
	hash := blake2b.Sum256([]byte(keyData))

	// Convert first 16 bytes (128 bits) to hex string
	return fmt.Sprintf("embed:%s", hex.EncodeToString(hash[:16]))
}

// callEmbeddingService calls the embedding service for a single text.
func (c *EmbeddingClient) callEmbeddingService(ctx context.Context, text, prefix string) ([]float64, error) {
	reqBody := map[string]interface{}{
		"text":   text,
		"prefix": prefix,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, errors.Wrap(err, "marshal request")
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/embed", strings.NewReader(string(jsonData)))
	if err != nil {
		return nil, errors.Wrap(err, "create request")
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "call service")
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			// nolint: errcheck // Response body close error is logged but not critical
			log.Error("Failed to close response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, &HTTPError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("service returned status %d: %s", resp.StatusCode, string(body)),
		}
	}

	var result struct {
		Embedding []float64 `json:"embedding"`
		Dimension int       `json:"dimension"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, errors.Wrap(err, "decode response")
	}

	return result.Embedding, nil
}

// callEmbeddingBatchService calls the embedding service for multiple texts.
func (c *EmbeddingClient) callEmbeddingBatchService(ctx context.Context, texts []string, prefix string) ([][]float64, error) {
	reqBody := map[string]interface{}{
		"texts":  texts,
		"prefix": prefix,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, errors.Wrap(err, "marshal request")
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/embed_batch", strings.NewReader(string(jsonData)))
	if err != nil {
		return nil, errors.Wrap(err, "create request")
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, errors.Wrap(err, "call service")
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			// Response body close error is logged but not critical
			log.Error("Failed to close response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, &HTTPError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("service returned status %d: %s", resp.StatusCode, string(body)),
		}
	}

	var result struct {
		Embeddings [][]float64 `json:"embeddings"`
		Dimension  int         `json:"dimension"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, errors.Wrap(err, "decode response")
	}

	return result.Embeddings, nil
}

// HealthCheck checks if the embedding service is healthy.
func (c *EmbeddingClient) HealthCheck(ctx context.Context) error {
	if !c.enabled.Load() {
		return fmt.Errorf("embedding client is disabled")
	}

	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/health", nil)
	if err != nil {
		return errors.Wrap(err, "create request")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return errors.Wrap(err, "call service")
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			// Response body close error is logged but not critical
			log.Error("Failed to close response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return &HTTPError{
			StatusCode: resp.StatusCode,
			Message:    fmt.Sprintf("service returned status %d", resp.StatusCode),
		}
	}

	return nil
}

// Enable enables the embedding client.
func (c *EmbeddingClient) Enable() {
	c.enabled.Store(true)
}

// Disable disables the embedding client.
func (c *EmbeddingClient) Disable() {
	c.enabled.Store(false)
}

// IsEnabled returns whether the embedding client is enabled.
func (c *EmbeddingClient) IsEnabled() bool {
	return c.enabled.Load()
}
