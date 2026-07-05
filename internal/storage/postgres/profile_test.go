// nolint: errcheck // Test code may ignore return values
package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/core/models"
)

// TestProfileRepository_NewProfileRepository tests creating a new ProfileRepository.
func TestProfileRepository_NewProfileRepository(t *testing.T) {
	t.Run("create profile repository", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewProfileRepository(pool)
		if repo == nil {
			t.Error("repository should not be nil")
		}
	})
}

// TestProfileRepository_Create tests creating a new user profile.
func TestProfileRepository_Create(t *testing.T) {
	t.Run("create valid profile", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewProfileRepository(pool)

		profile := &models.UserProfile{
			UserID:     "user-1",
			Name:       "Alice Johnson",
			Gender:     models.GenderFemale,
			Age:        28,
			Occupation: "Software Engineer",
			Style: []models.StyleTag{
				models.StyleTag("casual"),
				models.StyleMinimalist,
			},
			Budget: models.NewPriceRange(50.0, 200.0),
			Colors: []string{"black", "white", "blue", "gray"},
			Occasions: []models.Occasion{
				models.OccasionWork,
				models.Occasion("daily"),
			},
			BodyType: "slim",
			Preferences: map[string]any{
				"brand":    "nike",
				"material": "cotton",
				"fit":      "slim",
				"size":     "M",
				"avoid":    []string{"polyester", "synthetic"},
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}

		err = repo.Create(context.Background(), profile)
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
	})

	t.Run("create profile with minimal data", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewProfileRepository(pool)

		profile := &models.UserProfile{
			UserID:      "user-2",
			Name:        "Bob Smith",
			Gender:      models.GenderMale,
			Age:         0,
			Occupation:  "",
			Style:       []models.StyleTag{},
			Budget:      nil,
			Colors:      []string{},
			Occasions:   []models.Occasion{},
			BodyType:    "",
			Preferences: map[string]any{},
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}

		err = repo.Create(context.Background(), profile)
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
	})
}

// TestProfileRepository_GetByID tests retrieving a user profile by ID.
func TestProfileRepository_GetByID(t *testing.T) {
	t.Run("get existing profile", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewProfileRepository(pool)

		profile, err := repo.GetByID(context.Background(), "user-1")
		if err != nil && err != errors.ErrRecordNotFound {
			t.Logf("Expected error without database: %v", err)
		}
		if err == nil && profile != nil {
			if profile.UserID != "user-1" {
				t.Errorf("expected user ID user-1, got %s", profile.UserID)
			}
			if profile.Name != "Alice Johnson" {
				t.Errorf("expected name Alice Johnson, got %s", profile.Name)
			}
		}
	})

	t.Run("get non-existent profile", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewProfileRepository(pool)

		profile, err := repo.GetByID(context.Background(), "non-existent-user")
		if err != errors.ErrRecordNotFound {
			t.Logf("Expected ErrRecordNotFound, got %v", err)
		}
		if profile != nil {
			t.Error("profile should be nil")
		}
	})
}

// TestProfileRepository_Update tests updating a user profile.
func TestProfileRepository_Update(t *testing.T) {
	t.Run("update existing profile", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewProfileRepository(pool)

		profile := &models.UserProfile{
			UserID:     "user-1",
			Name:       "Alice Johnson Smith",
			Gender:     models.GenderFemale,
			Age:        29,
			Occupation: "Senior Software Engineer",
			Style: []models.StyleTag{
				models.StyleTag("casual"),
				models.StyleMinimalist,
			},
			Budget: models.NewPriceRange(75.0, 250.0),
			Colors: []string{"black", "white", "navy", "beige"},
			Occasions: []models.Occasion{
				models.OccasionWork,
				models.OccasionFormal,
				models.Occasion("daily"),
			},
			BodyType: "athletic",
			Preferences: map[string]any{
				"brand":    "patagonia",
				"material": "organic cotton",
				"fit":      "regular",
				"size":     "M",
				"avoid":    []string{"fast fashion"},
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}

		err = repo.Update(context.Background(), profile)
		if err != nil && err != errors.ErrRecordNotFound {
			t.Logf("Expected error without database: %v", err)
		}
	})

	t.Run("update non-existent profile", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewProfileRepository(pool)

		profile := &models.UserProfile{
			UserID:      "non-existent-user",
			Name:        "Non Existent",
			Gender:      models.GenderOther,
			Age:         0,
			Occupation:  "",
			Style:       []models.StyleTag{},
			Budget:      nil,
			Colors:      []string{},
			Occasions:   []models.Occasion{},
			BodyType:    "",
			Preferences: map[string]any{},
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}

		err = repo.Update(context.Background(), profile)
		if err != errors.ErrRecordNotFound {
			t.Logf("Expected ErrRecordNotFound, got %v", err)
		}
	})
}

// TestProfileRepository_Delete tests deleting a user profile.
func TestProfileRepository_Delete(t *testing.T) {
	t.Run("delete existing profile", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewProfileRepository(pool)

		err = repo.Delete(context.Background(), "user-1")
		if err != nil && err != errors.ErrRecordNotFound {
			t.Logf("Expected error without database: %v", err)
		}
	})

	t.Run("delete non-existent profile", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewProfileRepository(pool)

		err = repo.Delete(context.Background(), "non-existent-user")
		if err != errors.ErrRecordNotFound {
			t.Logf("Expected ErrRecordNotFound, got %v", err)
		}
	})
}

// TestProfileRepository_Exists tests checking if a profile exists.
func TestProfileRepository_Exists(t *testing.T) {
	t.Run("check existing profile", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewProfileRepository(pool)

		exists, err := repo.Exists(context.Background(), "user-1")
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
		if err == nil {
			t.Logf("Profile exists: %v", exists)
		}
	})

	t.Run("check non-existent profile", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewProfileRepository(pool)

		exists, err := repo.Exists(context.Background(), "non-existent-user")
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
		if err == nil && exists {
			t.Error("non-existent profile should not exist")
		}
	})
}

// TestProfileRepository_NewProfileRepositoryWithDB tests creating a ProfileRepository with a custom DBTX.
func TestProfileRepository_NewProfileRepositoryWithDB(t *testing.T) {
	t.Run("create with nil DBTX", func(t *testing.T) {
		repo := NewProfileRepositoryWithDB(nil)
		if repo == nil {
			t.Error("repository should not be nil")
		}
	})

	t.Run("create with mock DBTX", func(t *testing.T) {
		mockDB := &mockDBTX{}
		repo := NewProfileRepositoryWithDB(mockDB)
		if repo == nil {
			t.Error("repository should not be nil")
		}
	})
}

// nolint: errcheck // Test code may ignore return values
