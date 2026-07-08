// nolint: errcheck // Test code may ignore return values
package embedding

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockRedisClient is a mock implementation of RedisClient for testing.
type MockRedisClient struct {
	data     map[string]string
	failMode bool
}

func NewMockRedisClient() *MockRedisClient {
	return &MockRedisClient{
		data:     make(map[string]string),
		failMode: false,
	}
}

func (m *MockRedisClient) Get(ctx context.Context, key string) (string, error) {
	if m.failMode {
		return "", fmt.Errorf("redis failure")
	}
	val, ok := m.data[key]
	if !ok {
		return "", fmt.Errorf("key not found")
	}
	return val, nil
}

func (m *MockRedisClient) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	if m.failMode {
		return fmt.Errorf("redis failure")
	}
	val, err := json.Marshal(value)
	if err != nil {
		return err
	}
	m.data[key] = string(val)
	return nil
}

func (m *MockRedisClient) Del(ctx context.Context, keys ...string) error {
	if m.failMode {
		return fmt.Errorf("redis failure")
	}
	for _, key := range keys {
		delete(m.data, key)
	}
	return nil
}

func (m *MockRedisClient) Keys(ctx context.Context, pattern string) ([]string, error) {
	if m.failMode {
		return nil, fmt.Errorf("redis failure")
	}
	var keys []string
	for key := range m.data {
		if strings.Contains(key, pattern) {
			keys = append(keys, key)
		}
	}
	return keys, nil
}

// Scan simulates the Redis SCAN command for cursor-based key iteration.
// The mock returns all matching keys in a single batch (cursor 0) since it
// holds data in a plain map.
func (m *MockRedisClient) Scan(ctx context.Context, cursor uint64, match string, count int64) (uint64, []string, error) {
	if m.failMode {
		return 0, nil, fmt.Errorf("redis failure")
	}
	if cursor != 0 {
		return 0, nil, nil
	}
	var keys []string
	for key := range m.data {
		if strings.Contains(key, match) {
			keys = append(keys, key)
		}
	}
	return 0, keys, nil
}

func (m *MockRedisClient) SetFailMode(fail bool) {
	m.failMode = fail
}

func (m *MockRedisClient) Clear() {
	m.data = make(map[string]string)
}

// TestNewEmbeddingClient tests the constructor.
func TestNewEmbeddingClient(t *testing.T) {
	tests := []struct {
		name        string
		baseURL     string
		model       string
		redis       RedisClient
		wantError   bool
		wantEnabled bool
	}{
		{
			name:        "with redis",
			baseURL:     "http://localhost:8000",
			model:       "test-model",
			redis:       NewMockRedisClient(),
			wantError:   false,
			wantEnabled: true,
		},
		{
			name:        "without redis",
			baseURL:     "http://localhost:8000",
			model:       "test-model",
			redis:       nil,
			wantError:   false,
			wantEnabled: true,
		},
		{
			name:        "with trailing slash",
			baseURL:     "http://localhost:8000/",
			model:       "test-model",
			redis:       nil,
			wantError:   false,
			wantEnabled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewEmbeddingClient(tt.baseURL, tt.model, tt.redis, 5*time.Second)

			assert.NotNil(t, client)
			assert.Equal(t, tt.wantEnabled, client.IsEnabled())

			// Verify trailing slash is removed
			if strings.HasSuffix(tt.baseURL, "/") {
				assert.Equal(t, strings.TrimSuffix(tt.baseURL, "/"), client.baseURL)
			} else {
				assert.Equal(t, tt.baseURL, client.baseURL)
			}
		})
	}
}

// TestEmbeddingClient_EnableDisable tests enable/disable functionality.
func TestEmbeddingClient_EnableDisable(t *testing.T) {
	client := NewEmbeddingClient("http://localhost:8000", "test-model", nil, 5*time.Second)

	// Initially enabled
	assert.True(t, client.IsEnabled())

	// Disable
	client.Disable()
	assert.False(t, client.IsEnabled())

	// Re-enable
	client.Enable()
	assert.True(t, client.IsEnabled())
}

// TestEmbeddingClient_Embed_Disabled tests embedding when client is disabled.
func TestEmbeddingClient_Embed_Disabled(t *testing.T) {
	client := NewEmbeddingClient("http://localhost:8000", "test-model", nil, 5*time.Second)
	client.Disable()

	ctx := context.Background()
	_, err := client.Embed(ctx, "test text")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "disabled")
}

// TestEmbeddingClient_Embed_WithMockServer tests embedding with mock HTTP server.
func TestEmbeddingClient_Embed_WithMockServer(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/embed", r.URL.Path)
		assert.Equal(t, "POST", r.Method)

		var reqBody map[string]interface{}
		err := json.NewDecoder(r.Body).Decode(&reqBody)
		require.NoError(t, err)

		assert.Equal(t, "test text", reqBody["text"])
		assert.Equal(t, "query:", reqBody["prefix"]) // Updated: e5-large-v2 uses "query:" prefix

		// Return mock embedding
		embedding := make([]float64, 1024)
		for i := range embedding {
			embedding[i] = float64(i) / 1024.0
		}

		resp := map[string]interface{}{
			"embedding": embedding,
			"dimension": 1024,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Create client with mock server URL
	client := NewEmbeddingClient(server.URL, "test-model", nil, 5*time.Second)

	ctx := context.Background()
	embedding, err := client.Embed(ctx, "test text")

	require.NoError(t, err)
	assert.NotNil(t, embedding)
	assert.Equal(t, 1024, len(embedding))
}

// TestEmbeddingClient_Embed_WithCache tests embedding with Redis cache.
func TestEmbeddingClient_Embed_WithCache(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody map[string]interface{}
		err := json.NewDecoder(r.Body).Decode(&reqBody)
		require.NoError(t, err)

		assert.Equal(t, "test text", reqBody["text"])

		// Return mock embedding
		embedding := make([]float64, 1024)
		for i := range embedding {
			embedding[i] = float64(i) / 1024.0
		}

		resp := map[string]interface{}{
			"embedding": embedding,
			"dimension": 1024,
		}

		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	redis := NewMockRedisClient()
	client := NewEmbeddingClient(server.URL, "test-model", redis, 5*time.Second)

	ctx := context.Background()

	// First call - should hit server
	embedding1, err := client.Embed(ctx, "test text")
	require.NoError(t, err)
	assert.NotNil(t, embedding1)
	assert.Equal(t, 1024, len(embedding1))

	// Second call - should hit cache (if Redis works)
	embedding2, err := client.Embed(ctx, "test text")
	require.NoError(t, err)
	assert.NotNil(t, embedding2)
	assert.Equal(t, 1024, len(embedding2))

	// Both calls should return the same embedding
	assert.Equal(t, embedding1, embedding2)
}

// TestEmbeddingClient_Embed_EmptyText tests embedding with empty text.
func TestEmbeddingClient_Embed_EmptyText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqBody map[string]interface{}
		err := json.NewDecoder(r.Body).Decode(&reqBody)
		require.NoError(t, err)

		// Verify empty text is normalized to empty string
		assert.Equal(t, "", reqBody["text"])

		embedding := make([]float64, 1024)
		resp := map[string]interface{}{
			"embedding": embedding,
			"dimension": 1024,
		}

		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewEmbeddingClient(server.URL, "test-model", nil, 5*time.Second)

	ctx := context.Background()
	embedding, err := client.Embed(ctx, "")

	require.NoError(t, err)
	assert.NotNil(t, embedding)
	assert.Equal(t, 1024, len(embedding))
}

// TestEmbeddingClient_EmbedBatch tests batch embedding.
func TestEmbeddingClient_EmbedBatch(t *testing.T) {
	texts := []string{"text1", "text2", "text3"}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/embed_batch", r.URL.Path)

		var reqBody map[string]interface{}
		err := json.NewDecoder(r.Body).Decode(&reqBody)
		require.NoError(t, err)

		textsFromBody, ok := reqBody["texts"].([]interface{})
		require.True(t, ok)
		assert.Equal(t, 3, len(textsFromBody))

		// Return mock embeddings
		embeddings := make([][]float64, 3)
		for i := range embeddings {
			embeddings[i] = make([]float64, 1024)
			for j := range embeddings[i] {
				embeddings[i][j] = float64(i*1024+j) / 3072.0
			}
		}

		resp := map[string]interface{}{
			"embeddings": embeddings,
			"dimension":  1024,
		}

		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewEmbeddingClient(server.URL, "test-model", nil, 5*time.Second)

	ctx := context.Background()
	embeddings, err := client.EmbedBatch(ctx, texts)

	require.NoError(t, err)
	assert.NotNil(t, embeddings)
	assert.Equal(t, 3, len(embeddings))

	for _, emb := range embeddings {
		assert.Equal(t, 1024, len(emb))
	}
}

// TestEmbeddingClient_EmbedBatch_EmptySlice tests batch embedding with empty slice.
func TestEmbeddingClient_EmbedBatch_EmptySlice(t *testing.T) {
	client := NewEmbeddingClient("http://localhost:8000", "test-model", nil, 5*time.Second)

	ctx := context.Background()
	embeddings, err := client.EmbedBatch(ctx, []string{})

	// Should return empty slice without error
	require.NoError(t, err)
	assert.NotNil(t, embeddings)
	assert.Equal(t, 0, len(embeddings))
}

// TestEmbeddingClient_EmbedBatch_Disabled tests batch embedding when disabled.
func TestEmbeddingClient_EmbedBatch_Disabled(t *testing.T) {
	client := NewEmbeddingClient("http://localhost:8000", "test-model", nil, 5*time.Second)
	client.Disable()

	ctx := context.Background()
	_, err := client.EmbedBatch(ctx, []string{"text1", "text2"})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "disabled")
}

// TestEmbeddingClient_HealthCheck tests health check functionality.
func TestEmbeddingClient_HealthCheck(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantError  bool
	}{
		{
			name:       "healthy",
			statusCode: http.StatusOK,
			wantError:  false,
		},
		{
			name:       "unhealthy",
			statusCode: http.StatusInternalServerError,
			wantError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/health", r.URL.Path)
				w.WriteHeader(tt.statusCode)
			}))
			defer server.Close()

			client := NewEmbeddingClient(server.URL, "test-model", nil, 5*time.Second)

			ctx := context.Background()
			err := client.HealthCheck(ctx)

			if tt.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestEmbeddingClient_HealthCheck_Disabled tests health check when disabled.
func TestEmbeddingClient_HealthCheck_Disabled(t *testing.T) {
	client := NewEmbeddingClient("http://localhost:8000", "test-model", nil, 5*time.Second)
	client.Disable()

	ctx := context.Background()
	err := client.HealthCheck(ctx)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "disabled")
}

// TestEmbeddingClient_CallEmbeddingService_Error tests error handling in embedding service call.
func TestEmbeddingClient_CallEmbeddingService_Error(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		response   string
		wantError  bool
		errorMsg   string
	}{
		{
			name:       "server error 500",
			statusCode: http.StatusInternalServerError,
			response:   "internal server error",
			wantError:  true,
			errorMsg:   "500",
		},
		{
			name:       "server error 503",
			statusCode: http.StatusServiceUnavailable,
			response:   "service unavailable",
			wantError:  true,
			errorMsg:   "503",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				w.Write([]byte(tt.response))
			}))
			defer server.Close()

			client := NewEmbeddingClient(server.URL, "test-model", nil, 5*time.Second)

			ctx := context.Background()
			_, err := client.Embed(ctx, "test text")

			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.errorMsg)
		})
	}
}

// TestEmbeddingClient_CallEmbeddingService_InvalidJSON tests invalid JSON response.
func TestEmbeddingClient_CallEmbeddingService_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("invalid json"))
	}))
	defer server.Close()

	client := NewEmbeddingClient(server.URL, "test-model", nil, 5*time.Second)

	ctx := context.Background()
	_, err := client.Embed(ctx, "test text")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "decode")
}

// TestEmbeddingClient_Timeout tests timeout handling.
func TestEmbeddingClient_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate slow response
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"embedding": make([]float64, 1024),
			"dimension": 1024,
		})
	}))
	defer server.Close()

	// Create client with very short timeout
	client := NewEmbeddingClient(server.URL, "test-model", nil, 10*time.Millisecond)

	ctx := context.Background()
	_, err := client.Embed(ctx, "test text")

	assert.Error(t, err)
	errMsg := err.Error()
	assert.True(t, strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "deadline exceeded"),
		"Expected timeout or deadline exceeded error, got: %v", err)
}

// TestNormalizeText_EdgeCases tests normalizeText with edge cases.
func TestNormalizeText_EdgeCases(t *testing.T) {
	client := &EmbeddingClient{}

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "unicode spaces",
			input:    "hello\u00A0world",
			expected: "hello world",
		},
		{
			name:     "multiple newlines",
			input:    "hello\n\nworld",
			expected: "hello world",
		},
		{
			name:     "mixed tabs and spaces",
			input:    "hello\t\tworld",
			expected: "hello world",
		},
		{
			name:     "leading and trailing unicode spaces",
			input:    "\u200Bhello world\u200B",
			expected: "\u200Bhello world\u200B", // \u200B (zero-width space) is not considered a space by unicode.IsSpace()
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "only spaces",
			input:    "     ",
			expected: "",
		},
		{
			name:     "only tabs",
			input:    "\t\t\t",
			expected: "",
		},
		{
			name:     "mixed whitespace",
			input:    "  \t\n  ",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.normalizeText(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestGetCacheKey_Deterministic tests that cache key generation is deterministic.
func TestGetCacheKey_Deterministic(t *testing.T) {
	client := &EmbeddingClient{
		model: "test-model",
	}

	key1 := client.getCacheKey("hello world", "query")
	key2 := client.getCacheKey("hello world", "query")

	assert.Equal(t, key1, key2)
}

// TestGetCacheKey_DifferentInputs tests that different inputs produce different keys.
func TestGetCacheClient_DifferentInputs(t *testing.T) {
	client := &EmbeddingClient{
		model: "test-model",
	}

	key1 := client.getCacheKey("hello world", "query")
	key2 := client.getCacheKey("hello world", "passage")
	key3 := client.getCacheKey("hello world!", "query")

	assert.NotEqual(t, key1, key2)
	assert.NotEqual(t, key1, key3)
}

// TestEmbeddingCache_GetSet tests cache get/set operations.
func TestEmbeddingCache_GetSet(t *testing.T) {
	ctx := context.Background()
	redis := NewMockRedisClient()
	cache := NewEmbeddingCache(redis, time.Hour)

	key := &CacheKey{
		Text:   "test text",
		Model:  "test-model",
		Method: "query",
	}

	embedding := []float64{0.1, 0.2, 0.3, 0.4, 0.5}

	// Initially not found
	_, found := cache.Get(ctx, key)
	assert.False(t, found)

	// Set value
	err := cache.Set(ctx, key, embedding)
	require.NoError(t, err)

	// Now should be found
	result, found := cache.Get(ctx, key)
	assert.True(t, found)
	assert.Equal(t, embedding, result)
}

// TestEmbeddingCache_WithMemoryFallback tests memory cache fallback.
func TestEmbeddingCache_WithMemoryFallback(t *testing.T) {
	ctx := context.Background()
	redis := NewMockRedisClient()
	redis.SetFailMode(true) // Redis fails

	cache := NewEmbeddingCache(redis, time.Hour)

	key := &CacheKey{
		Text:   "test text",
		Model:  "test-model",
		Method: "query",
	}

	embedding := []float64{0.1, 0.2, 0.3, 0.4, 0.5}

	// Set should work despite Redis failure (memory fallback)
	err := cache.Set(ctx, key, embedding)
	require.NoError(t, err)

	// Get should work from memory
	result, found := cache.Get(ctx, key)
	assert.True(t, found)
	assert.Equal(t, embedding, result)
}

// TestMemoryCache_GetSet tests memory cache operations.
func TestMemoryCache_GetSet(t *testing.T) {
	cache := NewMemoryCache()

	// Get non-existent key
	_, found := cache.Get("key1")
	assert.False(t, found)

	// Set value
	cache.Set("key1", []byte("value1"), time.Hour)

	// Get existing key
	val, found := cache.Get("key1")
	assert.True(t, found)
	assert.Equal(t, []byte("value1"), val)
}

// TestMemoryCache_Expiration tests cache expiration.
func TestMemoryCache_Expiration(t *testing.T) {
	cache := NewMemoryCache()

	// Set value with short TTL
	cache.Set("key1", []byte("value1"), 10*time.Millisecond)

	// Immediately available
	_, found := cache.Get("key1")
	assert.True(t, found)

	// Wait for expiration
	time.Sleep(15 * time.Millisecond)

	// Should be expired
	_, found = cache.Get("key1")
	assert.False(t, found)
}

// TestMemoryCache_Delete tests cache deletion.
func TestMemoryCache_Delete(t *testing.T) {
	cache := NewMemoryCache()

	cache.Set("key1", []byte("value1"), time.Hour)
	cache.Set("key2", []byte("value2"), time.Hour)

	// Delete one key
	cache.Delete("key1")

	// key1 should be gone
	_, found1 := cache.Get("key1")
	assert.False(t, found1)

	// key2 should still exist
	_, found2 := cache.Get("key2")
	assert.True(t, found2)
}

// TestMemoryCache_Keys tests key pattern matching.
func TestMemoryCache_Keys(t *testing.T) {
	cache := NewMemoryCache()

	cache.Set("embed:key1", []byte("value1"), time.Hour)
	cache.Set("embed:key2", []byte("value2"), time.Hour)
	cache.Set("other:key3", []byte("value3"), time.Hour)

	// Get keys matching pattern
	keys := cache.Keys("embed:*")

	assert.Equal(t, 2, len(keys))
	assert.Contains(t, keys, "embed:key1")
	assert.Contains(t, keys, "embed:key2")
	assert.NotContains(t, keys, "other:key3")
}

// TestMemoryCache_ConcurrentAccess tests concurrent access to cache.
func TestMemoryCache_ConcurrentAccess(t *testing.T) {
	cache := NewMemoryCache()

	// Concurrent writes
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(index int) {
			key := fmt.Sprintf("key%d", index)
			cache.Set(key, []byte(fmt.Sprintf("value%d", index)), time.Hour)
			done <- true
		}(i)
	}

	// Wait for all writes
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify all values
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("key%d", i)
		val, found := cache.Get(key)
		assert.True(t, found, fmt.Sprintf("Key %s should exist", key))
		expected := []byte(fmt.Sprintf("value%d", i))
		assert.Equal(t, expected, val, fmt.Sprintf("Value for key %s should match", key))
	}
}

// TestMemoryCache_Close tests cleanup and goroutine termination.
func TestMemoryCache_Close(t *testing.T) {
	cache := NewMemoryCache()

	// Add some items
	cache.Set("key1", []byte("value1"), time.Hour)
	cache.Set("key2", []byte("value2"), time.Hour)

	// Close cache
	cache.Close()

	// Items should be cleared
	_, found1 := cache.Get("key1")
	_, found2 := cache.Get("key2")

	assert.False(t, found1)
	assert.False(t, found2)
}
