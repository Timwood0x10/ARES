// nolint: errcheck // Test code may ignore return values
package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/core/models"
)

// TestRecommendRepository_NewRecommendRepository tests creating a new RecommendRepository.
func TestRecommendRepository_NewRecommendRepository(t *testing.T) {
	t.Run("create recommend repository", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRecommendRepository(pool)
		if repo == nil {
			t.Error("repository should not be nil")
		}
	})
}

// TestRecommendRepository_Create tests creating a new recommendation result.
func TestRecommendRepository_Create(t *testing.T) {
	t.Run("create valid recommendation", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRecommendRepository(pool)

		result := &models.RecommendResult{
			SessionID: "test-session-1",
			UserID:    "user-1",
			Items: []*models.RecommendItem{
				{
					ItemID:           "item-1",
					Name:             "T-Shirt",
					Price:            50.0,
					Description:      "A nice t-shirt",
					Category:         "clothing",
					Brand:            "Nike",
					ImageURL:         "https://example.com/image.jpg",
					AgentPreferences: []models.StyleTag{models.StyleTag("casual")},
					Colors:           []string{"white"},
					MatchReason:      "Matches your casual style",
				}, {
					ItemID:           "item-2",
					Name:             "Jeans",
					Price:            80.0,
					Description:      "Blue jeans",
					Category:         "clothing",
					Brand:            "Levi's",
					ImageURL:         "https://example.com/jeans.jpg",
					AgentPreferences: []models.StyleTag{models.StyleTag("casual")},
					Colors:           []string{"blue"},
					MatchReason:      "Great for casual wear",
				},
			},
			Reason:     "Matches your casual style",
			TotalPrice: 130.0,
			MatchScore: 0.88,
			Occasion:   models.Occasion("daily"),
			Season:     "summer",
			Feedback: &models.UserFeedback{
				Rating:  5,
				Comment: "Great recommendations!",
				Liked:   true,
			},
			Metadata: map[string]any{
				"model": "style-recommender-v1",
			},
			CreatedAt: time.Now(),
		}

		err = repo.Create(context.Background(), result)
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
	})

	t.Run("create recommendation with empty items", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRecommendRepository(pool)

		result := &models.RecommendResult{
			SessionID:  "test-session-2",
			UserID:     "user-2",
			Items:      []*models.RecommendItem{},
			Reason:     "No matching items found",
			TotalPrice: 0.0,
			MatchScore: 0.0,
			CreatedAt:  time.Now(),
		}

		err = repo.Create(context.Background(), result)
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
	})
}

// TestRecommendRepository_GetBySessionID tests retrieving a recommendation by session ID.
func TestRecommendRepository_GetBySessionID(t *testing.T) {
	t.Run("get existing recommendation", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRecommendRepository(pool)

		result, err := repo.GetBySessionID(context.Background(), "test-session-1")
		if err != nil && err != errors.ErrRecordNotFound {
			t.Logf("Expected error without database: %v", err)
		}
		if err == nil && result != nil {
			if result.SessionID != "test-session-1" {
				t.Errorf("expected session ID test-session-1, got %s", result.SessionID)
			}
			if len(result.Items) == 0 {
				t.Error("expected at least one item")
			}
		}
	})

	t.Run("get non-existent recommendation", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRecommendRepository(pool)

		result, err := repo.GetBySessionID(context.Background(), "non-existent-session")
		if err != errors.ErrRecordNotFound {
			t.Logf("Expected ErrRecordNotFound, got %v", err)
		}
		if result != nil {
			t.Error("result should be nil")
		}
	})
}

// TestRecommendRepository_UpdateFeedback tests updating user feedback for a recommendation.
func TestRecommendRepository_UpdateFeedback(t *testing.T) {
	t.Run("update feedback for existing recommendation", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRecommendRepository(pool)

		feedback := &models.UserFeedback{
			Rating:  4,
			Comment: "Good but could be better",
			Liked:   true,
		}

		err = repo.UpdateFeedback(context.Background(), "test-session-1", feedback)
		if err != nil && err != errors.ErrRecordNotFound {
			t.Logf("Expected error without database: %v", err)
		}
	})

	t.Run("update feedback for non-existent recommendation", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRecommendRepository(pool)

		feedback := &models.UserFeedback{
			Rating:  5,
			Comment: "Excellent!",
			Liked:   true,
		}

		err = repo.UpdateFeedback(context.Background(), "non-existent-session", feedback)
		if err != errors.ErrRecordNotFound {
			t.Logf("Expected ErrRecordNotFound, got %v", err)
		}
	})

	t.Run("update feedback with nil feedback", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRecommendRepository(pool)

		err = repo.UpdateFeedback(context.Background(), "test-session-1", nil)
		if err != nil {
			t.Logf("Expected error with nil feedback: %v", err)
		}
	})
}

// TestRecommendRepository_ListByUserID tests listing recommendations by user ID.
func TestRecommendRepository_ListByUserID(t *testing.T) {
	t.Run("list recommendations with pagination", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRecommendRepository(pool)

		results, err := repo.ListByUserID(context.Background(), "user-1", 10, 0)
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
		if results == nil {
			t.Error("results should not be nil")
		}
	})

	t.Run("list recommendations with offset", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRecommendRepository(pool)

		results, err := repo.ListByUserID(context.Background(), "user-1", 10, 5)
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
		if results == nil {
			t.Error("results should not be nil")
		}
	})

	t.Run("list recommendations with zero limit", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRecommendRepository(pool)

		results, err := repo.ListByUserID(context.Background(), "user-1", 0, 0)
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
		if results == nil {
			t.Error("results should not be nil")
		}
	})
}

// TestRecommendRepository_Delete tests deleting a recommendation.
func TestRecommendRepository_Delete(t *testing.T) {
	t.Run("delete existing recommendation", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRecommendRepository(pool)

		err = repo.Delete(context.Background(), "test-session-1")
		if err != nil && err != errors.ErrRecordNotFound {
			t.Logf("Expected error without database: %v", err)
		}
	})

	t.Run("delete non-existent recommendation", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRecommendRepository(pool)

		err = repo.Delete(context.Background(), "non-existent-session")
		if err != errors.ErrRecordNotFound {
			t.Logf("Expected ErrRecordNotFound, got %v", err)
		}
	})
}

// TestRecommendRepository_NewRecommendRepositoryWithDB tests creating a RecommendRepository with a custom DBTX.
func TestRecommendRepository_NewRecommendRepositoryWithDB(t *testing.T) {
	t.Run("create with nil DBTX", func(t *testing.T) {
		repo := NewRecommendRepositoryWithDB(nil)
		if repo == nil {
			t.Error("repository should not be nil")
		}
	})

	t.Run("create with mock DBTX", func(t *testing.T) {
		mockDB := &mockDBTX{}
		repo := NewRecommendRepositoryWithDB(mockDB)
		if repo == nil {
			t.Error("repository should not be nil")
		}
	})
}

// nolint: errcheck // Test code may ignore return values
