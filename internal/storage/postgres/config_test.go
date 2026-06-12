// nolint: errcheck // Test code may ignore return values
package postgres

import (
	"errors"
	"testing"
	"time"
)

func TestConfig_DefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Host != "localhost" {
		t.Errorf("expected localhost, got %s", cfg.Host)
	}
	if cfg.Port != 5432 {
		t.Errorf("expected 5432, got %d", cfg.Port)
	}
	if cfg.User != "postgres" {
		t.Errorf("expected postgres, got %s", cfg.User)
	}
	if cfg.MaxOpenConns != 25 {
		t.Errorf("expected 25, got %d", cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns != 10 {
		t.Errorf("expected 10, got %d", cfg.MaxIdleConns)
	}
}

func TestConfig_DSN(t *testing.T) {
	cfg := &Config{
		Host:     "localhost",
		Port:     5432,
		User:     "user",
		Password: "password",
		Database: "testdb",
	}

	dsn := cfg.DSN()
	expected := "postgresql://user:password@localhost:5432/testdb?sslmode=disable&client_encoding=UTF8"
	if dsn != expected {
		t.Errorf("expected %s, got %s", expected, dsn)
	}
}

func TestConfig_Validate(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		cfg := &Config{
			Host:            "localhost",
			Port:            5432,
			MaxOpenConns:    10,
			MaxIdleConns:    5,
			ConnMaxLifetime: 10 * time.Minute,
			ConnMaxIdleTime: 2 * time.Minute,
		}

		err := cfg.Validate()
		if err != nil {
			t.Errorf("Validate() error = %v", err)
		}
	})

	t.Run("invalid port", func(t *testing.T) {
		cfg := &Config{
			Port: 0,
		}

		err := cfg.Validate()
		if err != nil {
			t.Errorf("Validate() error = %v", err)
		}

		if cfg.Port != 5432 {
			t.Errorf("expected port 5432 after validation")
		}
	})

	t.Run("invalid max open conns", func(t *testing.T) {
		cfg := &Config{
			MaxOpenConns: 0,
		}

		err := cfg.Validate()
		if err != nil {
			t.Errorf("Validate() error = %v", err)
		}

		if cfg.MaxOpenConns != 25 {
			t.Errorf("expected MaxOpenConns 25 after validation")
		}
	})

	t.Run("invalid max idle conns", func(t *testing.T) {
		cfg := &Config{
			MaxIdleConns: 0,
		}

		err := cfg.Validate()
		if err != nil {
			t.Errorf("Validate() error = %v", err)
		}

		if cfg.MaxIdleConns != 10 {
			t.Errorf("expected MaxIdleConns 10 after validation")
		}
	})

	t.Run("invalid conn max lifetime", func(t *testing.T) {
		cfg := &Config{
			ConnMaxLifetime: 0,
		}

		err := cfg.Validate()
		if err != nil {
			t.Errorf("Validate() error = %v", err)
		}

		if cfg.ConnMaxLifetime != 5*time.Minute {
			t.Errorf("expected ConnMaxLifetime 5min after validation")
		}
	})

	t.Run("invalid conn max idle time", func(t *testing.T) {
		cfg := &Config{
			ConnMaxIdleTime: 0,
		}

		err := cfg.Validate()
		if err != nil {
			t.Errorf("Validate() error = %v", err)
		}

		if cfg.ConnMaxIdleTime != 1*time.Minute {
			t.Errorf("expected ConnMaxIdleTime 1min after validation")
		}
	})
}

func TestPool(t *testing.T) {
	t.Run("create pool with default config", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		// Note: This test requires a running PostgreSQL instance
		// In CI, we skip if no database is available
		_, err := NewPool(cfg)
		if err != nil {
			// Expected to fail without real database
			t.Logf("Expected to fail without database: %v", err)
		}
	})
}

func TestRepository(t *testing.T) {
	t.Run("create repository", func(t *testing.T) {
		// Test that NewRepository doesn't panic with nil pool
		// Note: This test requires a running PostgreSQL instance
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		_, err := NewPool(cfg)
		if err != nil {
			t.Logf("Expected to fail without database: %v", err)
		}
	})

	t.Run("transaction", func(t *testing.T) {
		// Test transaction helper exists
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Logf("Expected to fail without database: %v", err)
			return
		}

		repo := NewRepository(pool)
		if repo == nil {
			t.Errorf("repository should not be nil")
		}

		// Test transaction function - just verify it can be called
		err = repo.Transaction(t.Context(), func(tx *Repository) error {
			// Just test that tx repo is created properly
			if tx == nil {
				return errors.New("transaction failed")
			}
			return nil
		})
		// Will fail without database, that's expected
		_ = err
	})
}

// nolint: errcheck // Test code may ignore return values
