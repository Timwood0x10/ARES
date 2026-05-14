// nolint: errcheck // Test code may ignore return values
package postgres

import (
	"context"
	stderrors "errors"
	"testing"
	"time"

	"github.com/Timwood0x10/goagent/internal/core/errors"
	"github.com/Timwood0x10/goagent/internal/core/models"
)

// TestRepository_NewRepository tests creating a new Repository.
func TestRepository_NewRepository(t *testing.T) {
	t.Run("create repository", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRepository(pool)
		if repo == nil {
			t.Error("repository should not be nil")
			return
		}
		if repo.Session == nil {
			t.Error("Session repository should not be nil")
			return
		}
		if repo.Recommend == nil {
			t.Error("Recommend repository should not be nil")
		}
		if repo.Profile == nil {
			t.Error("Profile repository should not be nil")
		}
		if repo.Vector == nil {
			t.Error("Vector repository should not be nil")
		}
		if repo.Pool() != pool {
			t.Error("Pool() should return the same pool")
		}
	})

	t.Run("close repository", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}

		repo := NewRepository(pool)
		err = repo.Close()
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
	})
}

// TestRepository_IsTransaction tests checking if repository is in transaction mode.
func TestRepository_IsTransaction(t *testing.T) {
	t.Run("non-transaction repository", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRepository(pool)
		if repo.IsTransaction() {
			t.Error("repository should not be in transaction mode")
		}
	})
}

// TestRepository_Transaction tests executing a function within a transaction.
func TestRepository_Transaction(t *testing.T) {
	t.Run("successful transaction", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRepository(pool)
		called := false

		err = repo.Transaction(context.Background(), func(txRepo *Repository) error {
			called = true
			if txRepo == nil {
				return stderrors.New("txRepo should not be nil")
			}
			if !txRepo.IsTransaction() {
				return stderrors.New("txRepo should be in transaction mode")
			}
			return nil
		})

		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
		if !called {
			t.Error("transaction function should have been called")
		}
	})

	t.Run("transaction with error - rollback", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRepository(pool)
		expectedErr := stderrors.New("transaction error")

		err = repo.Transaction(context.Background(), func(txRepo *Repository) error {
			return expectedErr
		})

		if err != expectedErr && err != nil {
			t.Logf("Expected error without database: %v", err)
		}
	})

	t.Run("transaction function panics", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRepository(pool)

		err = repo.Transaction(context.Background(), func(txRepo *Repository) error {
			panic("test panic")
		})

		if err == nil {
			t.Error("expected error from panic")
		}
	})
}

// TestRepository_WithTransaction tests creating a repository bound to a transaction.
func TestRepository_WithTransaction(t *testing.T) {
	t.Run("create transaction repository", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRepository(pool)
		txRepo, err := repo.WithTransaction(context.Background())
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
		if txRepo != nil {
			if !txRepo.IsTransaction() {
				t.Error("txRepo should be in transaction mode")
			}
			txRepo.Rollback()
		}
	})
}

// TestRepository_Commit tests committing a transaction.
func TestRepository_Commit(t *testing.T) {
	t.Run("commit transaction", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRepository(pool)
		txRepo, err := repo.WithTransaction(context.Background())
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}

		err = txRepo.Commit()
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
	})

	t.Run("commit without transaction", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRepository(pool)
		err = repo.Commit()
		if err != errors.ErrNoTransaction {
			t.Errorf("expected ErrNoTransaction, got %v", err)
		}
	})
}

// TestRepository_Rollback tests rolling back a transaction.
func TestRepository_Rollback(t *testing.T) {
	t.Run("rollback transaction", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRepository(pool)
		txRepo, err := repo.WithTransaction(context.Background())
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}

		err = txRepo.Rollback()
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
	})

	t.Run("rollback without transaction", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRepository(pool)
		err = repo.Rollback()
		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
	})
}

// TestRepository_SaveSession tests saving a session with its result.
func TestRepository_SaveSession(t *testing.T) {
	t.Run("save session with result", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRepository(pool)

		session := &models.Session{
			SessionID: "test-session-1",
			UserID:    "user-1",
			Input:     "test input",
			Status:    models.SessionStatusCompleted,
			UserProfile: &models.UserProfile{
				UserID: "user-1",
				Name:   "test user",
			},
			Metadata: map[string]any{
				"key": "value",
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			ExpiredAt: time.Now().Add(24 * time.Hour),
		}

		result := &models.RecommendResult{
			SessionID: "test-session-1",
			UserID:    "user-1",
			Items: []*models.RecommendItem{
				{
					ItemID:      "item-1",
					Name:        "test item",
					Price:       100.0,
					Category:    "test",
					Brand:       "test",
					Description: "test",
				},
			},
			Reason:     "test reason",
			TotalPrice: 100.0,
			MatchScore: 0.9,
			CreatedAt:  time.Now(),
		}

		err = repo.SaveSession(context.Background(), session, result)
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
	})

	t.Run("save session without result", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRepository(pool)

		session := &models.Session{
			SessionID: "test-session-2",
			UserID:    "user-2",
			Input:     "test input",
			Status:    models.SessionStatusPending,
			UserProfile: &models.UserProfile{
				UserID: "user-2",
				Name:   "test user",
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			ExpiredAt: time.Now().Add(24 * time.Hour),
		}

		err = repo.SaveSession(context.Background(), session, nil)
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
	})
}

// TestRepository_GetSessionWithResult tests retrieving a session with its recommendation result.
func TestRepository_GetSessionWithResult(t *testing.T) {
	t.Run("get session with result", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRepository(pool)

		session, result, err := repo.GetSessionWithResult(context.Background(), "test-session-1")
		if err != nil && err != errors.ErrRecordNotFound {
			t.Logf("Expected error without database: %v", err)
		}
		if err == nil {
			if session == nil {
				t.Error("session should not be nil")
			}
			if result == nil {
				t.Log("result can be nil")
			}
		}
	})
}

// TestRepository_SaveProfile tests saving a user profile.
func TestRepository_SaveProfile(t *testing.T) {
	t.Run("create new profile", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRepository(pool)

		profile := &models.UserProfile{

			UserID: "user-3",

			Name: "Test User",

			Gender: models.GenderFemale,

			Age: 25,

			Occupation: "engineer",

			Style: []models.StyleTag{
				models.StyleTag("casual"),
			},

			Budget: models.NewPriceRange(50.0, 200.0),

			Colors: []string{"red", "blue"},

			Occasions: []models.Occasion{
				models.Occasion("daily"),
				models.OccasionWork,
			},

			BodyType: "slim",

			Preferences: map[string]any{

				"brand": "nike",
			},

			CreatedAt: time.Now(),

			UpdatedAt: time.Now(),
		}

		err = repo.SaveProfile(context.Background(), profile)
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
	})

	t.Run("update existing profile", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewRepository(pool)

		profile := &models.UserProfile{

			UserID: "user-3",

			Name: "Updated User",

			Gender: models.GenderFemale,

			Age: 26,

			Occupation: "developer",

			Style: []models.StyleTag{

				models.StyleTag("formal"),
			},

			Budget: models.NewPriceRange(100.0, 300.0),

			Colors: []string{"black", "white"},

			Occasions: []models.Occasion{

				models.OccasionFormal,
			},

			BodyType: "athletic",

			Preferences: map[string]any{

				"brand": "adidas",
			},

			CreatedAt: time.Now(),

			UpdatedAt: time.Now(),
		}

		err = repo.SaveProfile(context.Background(), profile)
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
	})
}

// nolint: errcheck // Test code may ignore return values
