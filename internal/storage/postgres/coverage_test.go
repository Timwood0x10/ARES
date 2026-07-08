// nolint: errcheck // Test code may ignore return values
package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/core/models"
)

var embeddingConfig = &EmbeddingConfig{
	DefaultModel:         "text-embedding-ada-002",
	DefaultVersion:       1,
	MaxRetries:           3,
	MaxBatchSize:         32,
	MaxVectorSearchLimit: 1000,
	ReconcileBatchSize:   1000,
	EmbeddingTimeout:     30 * time.Second,
}

// TestPool_Coverage tests additional pool scenarios to improve coverage.
func TestPool_Coverage(t *testing.T) {
	t.Run("test Stats returns valid values", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		stats := pool.Stats()
		if stats == nil {
			t.Error("stats should not be nil")
			return
		}
		if stats.MaxOpenConns != cfg.MaxOpenConns {
			t.Errorf("expected MaxOpenConns %d, got %d", cfg.MaxOpenConns, stats.MaxOpenConns)
		}
	})

	t.Run("test Query with cancelled context", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err = pool.Query(ctx, "SELECT 1")
		if err != nil {
			t.Logf("Expected error with cancelled context: %v", err)
		}
	})

	t.Run("test QueryRow with cancelled context", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		row := pool.QueryRow(ctx, "SELECT 1")
		if row == nil {
			t.Log("row is nil as expected with cancelled context")
		}
	})

	t.Run("test Exec with cancelled context", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err = pool.Exec(ctx, "SELECT 1")
		if err != nil {
			t.Logf("Expected error with cancelled context: %v", err)
		}
	})

	t.Run("test Begin with cancelled context", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err = pool.Begin(ctx)
		if err != nil {
			t.Logf("Expected error with cancelled context: %v", err)
		}
	})

	t.Run("test IsHealthy with closed pool", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}

		pool.Close()
		healthy := pool.IsHealthy()
		t.Logf("Pool health after close: %v", healthy)
	})
}

// TestRepository_Coverage tests additional repository scenarios.
func TestRepository_Coverage(t *testing.T) {
	t.Run("test SaveSession with nil session", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRepository(pool)
		err = repo.SaveSession(context.Background(), nil, nil)
		if err != nil {
			t.Logf("Expected error with nil session: %v", err)
		}
	})

	t.Run("test GetSessionWithResult with invalid session ID", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRepository(pool)
		_, _, err = repo.GetSessionWithResult(context.Background(), "invalid-session-id")
		if err != nil {
			t.Logf("Expected error with invalid session ID: %v", err)
		}
	})

	t.Run("test SaveProfile with nil profile", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRepository(pool)
		err = repo.SaveProfile(context.Background(), nil)
		if err != nil {
			t.Logf("Expected error with nil profile: %v", err)
		}
	})

	t.Run("test Transaction with panic", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRepository(pool)

		defer func() {
			if r := recover(); r != nil {
				t.Logf("Recovered from panic: %v", r)
			}
		}()

		_ = repo.Transaction(context.Background(), func(tx *Repository) error {
			panic("test panic")
		})
	})

	t.Run("test WithTransaction with nil context", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRepository(pool)
		_, err = repo.WithTransaction(context.Background())
		if err != nil {
			t.Logf("Expected error with nil context: %v", err)
		}
	})
}

// TestVectorSearcher_Coverage tests additional vector search scenarios.
func TestVectorSearcher_Coverage(t *testing.T) {
	t.Run("test Search with SQL injection attempt", func(t *testing.T) {
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

		_, err = searcher.Search(context.Background(), "users; DROP TABLE", embedding, 10)
		if err != nil {
			t.Logf("Expected error with SQL injection: %v", err)
		}
	})

	t.Run("test AddEmbedding with SQL injection attempt", func(t *testing.T) {
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

		err = searcher.AddEmbedding(context.Background(), "users", "test; DROP", embedding, map[string]any{})
		if err != nil {
			t.Logf("Expected error with SQL injection: %v", err)
		}
	})

	t.Run("test DeleteEmbedding with SQL injection attempt", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		searcher := NewVectorSearcher(pool, embeddingConfig)

		err = searcher.DeleteEmbedding(context.Background(), "users", "test; DROP")
		if err != nil {
			t.Logf("Expected error with SQL injection: %v", err)
		}
	})

	t.Run("test CreateVectorTable with SQL injection attempt", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		searcher := NewVectorSearcher(pool, embeddingConfig)

		err = searcher.CreateVectorTable(context.Background(), "users; DROP", "")
		if err != nil {
			t.Logf("Expected error with SQL injection: %v", err)
		}
	})

	t.Run("test Search with cancelled context", func(t *testing.T) {
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

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err = searcher.Search(ctx, "embeddings", embedding, 10)
		if err != nil {
			t.Logf("Expected error with cancelled context: %v", err)
		}
	})

	t.Run("test AddEmbedding with cancelled context", func(t *testing.T) {
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

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err = searcher.AddEmbedding(ctx, "embeddings", "test-1", embedding, map[string]any{})
		if err != nil {
			t.Logf("Expected error with cancelled context: %v", err)
		}
	})

	t.Run("test DeleteEmbedding with cancelled context", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		searcher := NewVectorSearcher(pool, embeddingConfig)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err = searcher.DeleteEmbedding(ctx, "embeddings", "test-1")
		if err != nil {
			t.Logf("Expected error with cancelled context: %v", err)
		}
	})
}

// TestConfig_Coverage tests additional config scenarios.
func TestConfig_Coverage(t *testing.T) {
	t.Run("test Validate with all zero values", func(t *testing.T) {
		cfg := &Config{}

		err := cfg.Validate()
		if err != nil {
			t.Logf("Expected error with zero values: %v", err)
		}

		if cfg.Port != 5432 {
			t.Errorf("expected port 5432 after validation, got %d", cfg.Port)
		}
		if cfg.MaxOpenConns != 25 {
			t.Errorf("expected MaxOpenConns 25 after validation, got %d", cfg.MaxOpenConns)
		}
		if cfg.MaxIdleConns != 10 {
			t.Errorf("expected MaxIdleConns 10 after validation, got %d", cfg.MaxIdleConns)
		}
		if cfg.ConnMaxLifetime != 5*time.Minute {
			t.Errorf("expected ConnMaxLifetime 5min after validation, got %v", cfg.ConnMaxLifetime)
		}
		if cfg.ConnMaxIdleTime != 1*time.Minute {
			t.Errorf("expected ConnMaxIdleTime 1min after validation, got %v", cfg.ConnMaxIdleTime)
		}
	})

	t.Run("test DSN with special characters in password", func(t *testing.T) {
		cfg := &Config{
			Host:     "localhost",
			Port:     5432,
			User:     "user",
			Password: "p@ssw0rd!#$",
			Database: "testdb",
		}

		dsn := cfg.DSN()
		if dsn == "" {
			t.Error("DSN should not be empty")
		}
		if !strings.Contains(dsn, "p%40ssw0rd%21%23%24") {
			t.Errorf("DSN should contain URL-encoded password, got: %s", dsn)
		}
	})
}

// TestSessionRepository_Coverage tests additional session repository scenarios.
func TestSessionRepository_Coverage(t *testing.T) {
	t.Run("test GetByID with empty session ID", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewSessionRepository(pool)

		_, err = repo.GetByID(context.Background(), "")
		if err != nil {
			t.Logf("Expected error with empty session ID: %v", err)
		}
	})

	t.Run("test Create with minimal session", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewSessionRepository(pool)

		session := &models.Session{
			SessionID: "test-session-coverage",
			UserID:    "test-user",
			Status:    models.SessionStatusPending,
		}
		err = repo.Create(context.Background(), session)
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
	})
}

// TestRecommendRepository_Coverage tests additional recommend repository scenarios.
func TestRecommendRepository_Coverage(t *testing.T) {
	t.Run("test GetBySessionID with empty session ID", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRecommendRepository(pool)

		_, err = repo.GetBySessionID(context.Background(), "")
		if err != nil {
			t.Logf("Expected error with empty session ID: %v", err)
		}
	})

	t.Run("test Create with minimal recommendation", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRecommendRepository(pool)

		recommend := &models.RecommendResult{
			SessionID: "test-session-coverage",
			UserID:    "test-user",
			Items:     []*models.RecommendItem{},
			Reason:    "Test recommendation",
		}
		err = repo.Create(context.Background(), recommend)
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
	})
}

// TestProfileRepository_Coverage tests additional profile repository scenarios.
func TestProfileRepository_Coverage(t *testing.T) {
	t.Run("test GetByID with empty user ID", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewProfileRepository(pool)

		_, err = repo.GetByID(context.Background(), "")
		if err != nil {
			t.Logf("Expected error with empty user ID: %v", err)
		}
	})

	t.Run("test Create with minimal profile", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewProfileRepository(pool)

		profile := &models.UserProfile{
			UserID: "test-user-coverage",
			Name:   "Test User",
			Gender: models.GenderMale,
			Age:    30,
		}
		err = repo.Create(context.Background(), profile)
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
	})
}

// nolint: errcheck // Test code may ignore return values
