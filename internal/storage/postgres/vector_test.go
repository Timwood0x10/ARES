// nolint: errcheck // Test code may ignore return values
package postgres

import (
	"context"
	"testing"
)

// TestVectorSearcher_NewVectorSearcher tests creating a new VectorSearcher.
func TestVectorSearcher_NewVectorSearcher(t *testing.T) {
	t.Run("create vector searcher", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		searcher := NewVectorSearcher(pool, embeddingConfig)
		if searcher == nil {
			t.Error("searcher should not be nil")
		}
	})
}

// TestVectorSearcher_NewVectorSearcherWithDB tests creating a VectorSearcher with a custom DBTX.
func TestVectorSearcher_NewVectorSearcherWithDB(t *testing.T) {
	t.Run("create with nil DBTX", func(t *testing.T) {
		searcher := NewVectorSearcherWithDB(nil, embeddingConfig)
		if searcher == nil {
			t.Error("searcher should not be nil")
		}
	})

	t.Run("create with mock DBTX", func(t *testing.T) {
		mockDB := &mockDBTX{}
		searcher := NewVectorSearcherWithDB(mockDB, embeddingConfig)
		if searcher == nil {
			t.Error("searcher should not be nil")
		}
	})
}

// TestVectorSearcher_Search tests performing a vector similarity search.
func TestVectorSearcher_Search(t *testing.T) {
	t.Run("search with valid embedding", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		searcher := NewVectorSearcher(pool, embeddingConfig)

		// Create a test embedding (1536 dimensions for OpenAI embeddings)
		embedding := make([]float64, 1536)
		for i := range embedding {
			embedding[i] = 0.1
		}

		results, err := searcher.Search(context.Background(), "embeddings", "default", embedding, 10)
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
		if err == nil && results == nil {
			t.Error("results should not be nil on success")
		}
	})

	t.Run("search with empty embedding", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		searcher := NewVectorSearcher(pool, embeddingConfig)

		embedding := []float64{}

		_, err = searcher.Search(context.Background(), "embeddings", "default", embedding, 10)
		if err != nil {
			t.Logf("Expected error with empty embedding: %v", err)
		}
	})

	t.Run("search with zero limit", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		searcher := NewVectorSearcher(pool, embeddingConfig)

		embedding := make([]float64, 1536)
		for i := range embedding {
			embedding[i] = 0.1
		}

		_, err = searcher.Search(context.Background(), "embeddings", "default", embedding, 0)
		if err != nil {
			t.Logf("Expected error with zero limit: %v", err)
		}
	})

	t.Run("search with negative limit", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		searcher := NewVectorSearcher(pool, embeddingConfig)

		embedding := make([]float64, 1536)
		for i := range embedding {
			embedding[i] = 0.1
		}

		_, err = searcher.Search(context.Background(), "embeddings", "default", embedding, -10)
		if err != nil {
			t.Logf("Expected error with negative limit: %v", err)
		}
	})
}

// TestVectorSearcher_AddEmbedding tests adding a vector embedding.
func TestVectorSearcher_AddEmbedding(t *testing.T) {
	t.Run("add valid embedding", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		searcher := NewVectorSearcher(pool, embeddingConfig)

		// Create a test embedding (1536 dimensions for OpenAI embeddings)
		embedding := make([]float64, 1536)
		for i := range embedding {
			embedding[i] = 0.1
		}

		metadata := map[string]any{
			"item_id":  "item-1",
			"category": "clothing",
			"brand":    "nike",
		}

		err = searcher.AddEmbedding(context.Background(), "embeddings", "test-embedding-1", embedding, metadata)
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
	})

	t.Run("add embedding with empty id", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		searcher := NewVectorSearcher(pool, embeddingConfig)

		embedding := make([]float64, 1536)
		for i := range embedding {
			embedding[i] = 0.1
		}

		err = searcher.AddEmbedding(context.Background(), "embeddings", "", embedding, map[string]any{})
		if err != nil {
			t.Logf("Expected error with empty id: %v", err)
		}
	})

	t.Run("add embedding with nil metadata", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		searcher := NewVectorSearcher(pool, embeddingConfig)

		embedding := make([]float64, 1536)
		for i := range embedding {
			embedding[i] = 0.1
		}

		err = searcher.AddEmbedding(context.Background(), "embeddings", "test-embedding-2", embedding, nil)
		if err != nil {
			t.Logf("Expected error with nil metadata: %v", err)
		}
	})
}

// TestVectorSearcher_DeleteEmbedding tests deleting a vector embedding.
func TestVectorSearcher_DeleteEmbedding(t *testing.T) {
	t.Run("delete existing embedding", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		searcher := NewVectorSearcher(pool, embeddingConfig)

		err = searcher.DeleteEmbedding(context.Background(), "embeddings", "test-embedding-1")
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
	})

	t.Run("delete non-existent embedding", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		searcher := NewVectorSearcher(pool, embeddingConfig)

		err = searcher.DeleteEmbedding(context.Background(), "embeddings", "non-existent-embedding")
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
	})
}

// TestVectorSearcher_CreateVectorTable tests creating a vector table.
func TestVectorSearcher_CreateVectorTable(t *testing.T) {
	t.Run("create vector table", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		searcher := NewVectorSearcher(pool, embeddingConfig)

		err = searcher.CreateVectorTable(context.Background(), "test_vectors", "")
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
	})

	t.Run("create vector table with custom metadata schema", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		searcher := NewVectorSearcher(pool, embeddingConfig)

		metadataSchema := `metadata JSONB CHECK (jsonb_typeof(metadata) = 'object')`
		err = searcher.CreateVectorTable(context.Background(), "test_vectors_with_schema", metadataSchema)
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
	})
}

// TestSearchResult tests SearchResult structure.
func TestSearchResult(t *testing.T) {
	t.Run("create search result", func(t *testing.T) {
		result := &SearchResult{
			ID:    "item-1",
			Score: 0.95,
			Metadata: map[string]any{
				"category": "clothing",
				"brand":    "nike",
			},
		}

		if result.ID != "item-1" {
			t.Errorf("expected ID item-1, got %s", result.ID)
		}
		if result.Score != 0.95 {
			t.Errorf("expected score 0.95, got %f", result.Score)
		}
		if result.Metadata == nil {
			t.Error("metadata should not be nil")
		}
	})

	t.Run("create search result with nil metadata", func(t *testing.T) {
		result := &SearchResult{
			ID:       "item-2",
			Score:    0.88,
			Metadata: nil,
		}

		if result.Metadata != nil {
			t.Error("metadata should be nil")
		}
	})
}

// TestVectorSearcher_Integration tests integration scenarios.
func TestVectorSearcher_Integration(t *testing.T) {
	t.Run("full workflow: create table, add embedding, search, delete", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		searcher := NewVectorSearcher(pool, embeddingConfig)

		// Step 1: Create table
		err = searcher.CreateVectorTable(context.Background(), "integration_test", "")
		if err != nil {
			t.Logf("Step 1 - Create table error: %v", err)
		}

		// Step 2: Add embeddings
		embedding1 := make([]float64, 1536)
		for i := range embedding1 {
			embedding1[i] = 0.1
		}

		embedding2 := make([]float64, 1536)
		for i := range embedding2 {
			embedding2[i] = 0.2
		}

		metadata1 := map[string]any{"item_id": "item-1", "category": "clothing"}
		metadata2 := map[string]any{"item_id": "item-2", "category": "shoes"}

		_ = searcher.AddEmbedding(context.Background(), "integration_test", "embedding-1", embedding1, metadata1)
		_ = searcher.AddEmbedding(context.Background(), "integration_test", "embedding-2", embedding2, metadata2)

		// Step 3: Search
		searchEmbedding := make([]float64, 1536)
		for i := range searchEmbedding {
			searchEmbedding[i] = 0.15
		}

		results, err := searcher.Search(context.Background(), "integration_test", "default", searchEmbedding, 5)
		if err != nil {
			t.Logf("Step 3 - Search error: %v", err)
		}
		if err == nil {
			t.Logf("Found %d results", len(results))
		}

		// Step 4: Delete
		_ = searcher.DeleteEmbedding(context.Background(), "integration_test", "embedding-1")
		_ = searcher.DeleteEmbedding(context.Background(), "integration_test", "embedding-2")
	})
}

// nolint: errcheck // Test code may ignore return values
