// nolint: errcheck // Test code may ignore return values
package postgres

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/errors"
)

// TestSessionRepository_NewSessionRepository tests creating a new SessionRepository.
func TestSessionRepository_NewSessionRepository(t *testing.T) {
	t.Run("create session repository", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewSessionRepository(pool)
		if repo == nil {
			t.Error("repository should not be nil")
		}
	})
}

// TestSessionRepository_Create tests creating a new session.
func TestSessionRepository_Create(t *testing.T) {
	t.Run("create valid session", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewSessionRepository(pool)

		session := &models.Session{
			SessionID: "test-session-1",
			UserID:    "user-1",
			Input:     "test input",
			Status:    models.SessionStatusPending,
			UserProfile: &models.UserProfile{
				UserID: "user-1",
				Name:   "test user",
				Age:    25,
			},
			Metadata: map[string]any{
				"key": "value",
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			ExpiredAt: time.Now().Add(24 * time.Hour),
		}

		err = repo.Create(context.Background(), session)
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
	})

	t.Run("create session with nil profile", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewSessionRepository(pool)

		session := &models.Session{
			SessionID:   "test-session-2",
			UserID:      "user-2",
			Input:       "test input",
			Status:      models.SessionStatusPending,
			UserProfile: nil,
			Metadata:    nil,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
			ExpiredAt:   time.Now().Add(24 * time.Hour),
		}

		err = repo.Create(context.Background(), session)
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
	})
}

// TestSessionRepository_GetByID tests retrieving a session by ID.
func TestSessionRepository_GetByID(t *testing.T) {
	t.Run("get existing session", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewSessionRepository(pool)

		session, err := repo.GetByID(context.Background(), "test-session-1")
		if err != nil && err != errors.ErrRecordNotFound {
			t.Logf("Expected error without database: %v", err)
		}
		if err == nil && session != nil {
			if session.SessionID != "test-session-1" {
				t.Errorf("expected session ID test-session-1, got %s", session.SessionID)
			}
		}
	})

	t.Run("get non-existent session", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewSessionRepository(pool)

		session, err := repo.GetByID(context.Background(), "non-existent-session")
		if err != errors.ErrRecordNotFound {
			t.Logf("Expected ErrRecordNotFound, got %v", err)
		}
		if session != nil {
			t.Error("session should be nil")
		}
	})
}

// TestSessionRepository_Update tests updating a session.
func TestSessionRepository_Update(t *testing.T) {
	t.Run("update existing session", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewSessionRepository(pool)

		session := &models.Session{
			SessionID: "test-session-1",
			UserID:    "user-1",
			Input:     "updated input",
			Status:    models.SessionStatusCompleted,
			UserProfile: &models.UserProfile{
				UserID: "user-1",
				Name:   "updated user",
				Age:    26,
			},
			Metadata: map[string]any{
				"updated": true,
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			ExpiredAt: time.Now().Add(24 * time.Hour),
		}

		err = repo.Update(context.Background(), session)
		if err != nil && err != errors.ErrRecordNotFound {
			t.Logf("Expected error without database: %v", err)
		}
	})

	t.Run("update non-existent session", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewSessionRepository(pool)

		session := &models.Session{
			SessionID: "non-existent-session",
			UserID:    "user-1",
			Input:     "test input",
			Status:    models.SessionStatusPending,
			UserProfile: &models.UserProfile{
				UserID: "user-1",
				Name:   "test user",
			},
			Metadata:  map[string]any{},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			ExpiredAt: time.Now().Add(24 * time.Hour),
		}

		err = repo.Update(context.Background(), session)
		if err != errors.ErrRecordNotFound {
			t.Logf("Expected ErrRecordNotFound, got %v", err)
		}
	})
}

// TestSessionRepository_Delete tests deleting a session.
func TestSessionRepository_Delete(t *testing.T) {
	t.Run("delete existing session", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewSessionRepository(pool)

		err = repo.Delete(context.Background(), "test-session-1")
		if err != nil && err != errors.ErrRecordNotFound {
			t.Logf("Expected error without database: %v", err)
		}
	})

	t.Run("delete non-existent session", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewSessionRepository(pool)

		err = repo.Delete(context.Background(), "non-existent-session")
		if err != errors.ErrRecordNotFound {
			t.Logf("Expected ErrRecordNotFound, got %v", err)
		}
	})
}

// TestSessionRepository_ListByUserID tests listing sessions by user ID.
func TestSessionRepository_ListByUserID(t *testing.T) {
	t.Run("list sessions with pagination", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewSessionRepository(pool)

		sessions, err := repo.ListByUserID(context.Background(), "user-1", 10, 0)
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
		if sessions == nil {
			t.Error("sessions should not be nil")
		}
	})

	t.Run("list sessions with offset", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewSessionRepository(pool)

		sessions, err := repo.ListByUserID(context.Background(), "user-1", 10, 5)
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
		if sessions == nil {
			t.Error("sessions should not be nil")
		}
	})

	t.Run("list sessions with zero limit", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewSessionRepository(pool)

		sessions, err := repo.ListByUserID(context.Background(), "user-1", 0, 0)
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
		if sessions == nil {
			t.Error("sessions should not be nil")
		}
	})
}

// TestSessionRepository_CleanupExpired tests cleaning up expired sessions.
func TestSessionRepository_CleanupExpired(t *testing.T) {
	t.Run("cleanup expired sessions", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Host = "localhost"

		pool, err := NewPool(cfg)
		if err != nil {
			t.Skipf("Skipping test without database: %v", err)
		}
		defer pool.Close()

		repo := NewSessionRepository(pool)

		// Create an expired session
		expiredSession := &models.Session{
			SessionID:   "expired-session-1",
			UserID:      "user-1",
			Input:       "test input",
			Status:      models.SessionStatusPending,
			UserProfile: nil,
			Metadata:    map[string]any{},
			CreatedAt:   time.Now().Add(-48 * time.Hour),
			UpdatedAt:   time.Now().Add(-48 * time.Hour),
			ExpiredAt:   time.Now().Add(-24 * time.Hour),
		}

		_ = repo.Create(context.Background(), expiredSession)

		count, err := repo.CleanupExpired(context.Background())
		if err != nil {
			t.Logf("Expected error without database: %v", err)
		}
		if count < 0 {
			t.Errorf("expected non-negative count, got %d", count)
		}
	})
}

// TestSessionRepository_NewSessionRepositoryWithDB tests creating a SessionRepository with a custom DBTX.
func TestSessionRepository_NewSessionRepositoryWithDB(t *testing.T) {
	t.Run("create with nil DBTX", func(t *testing.T) {
		repo := NewSessionRepositoryWithDB(nil)
		if repo == nil {
			t.Error("repository should not be nil")
		}
	})

	t.Run("create with mock DBTX", func(t *testing.T) {
		// Create a simple mock that implements DBTX
		mockDB := &mockDBTX{}
		repo := NewSessionRepositoryWithDB(mockDB)
		if repo == nil {
			t.Error("repository should not be nil")
		}
	})
}

// mockDBTX is a simple mock implementation of DBTX for testing.
type mockDBTX struct{}

func (m *mockDBTX) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	return nil, errors.New("not implemented")
}

func (m *mockDBTX) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return nil, errors.New("not implemented")
}

func (m *mockDBTX) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	return nil, errors.New("not implemented")
}

func (m *mockDBTX) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	return nil
}

// nolint: errcheck // Test code may ignore return values

// TestSessionRepository_ListByUserIDNullExpiredAt locks REVIEW 3.5b/3.7:
// Create binds NULL for a zero ExpiredAt, but ListByUserID scanned
// expired_at into a bare time.Time — every row with a NULL expiry failed
// the scan and killed the whole listing ("scan session" error).
func TestSessionRepository_ListByUserIDNullExpiredAt(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE sessions (
		session_id TEXT PRIMARY KEY, user_id TEXT, input TEXT, status TEXT,
		user_profile TEXT, metadata TEXT, created_at TIMESTAMP, updated_at TIMESTAMP, expired_at TIMESTAMP)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	now := time.Now()
	// One row with a NULL expired_at (what Create writes for a zero
	// ExpiredAt) and one with a set expiry, for the same user.
	if _, err := db.Exec(`INSERT INTO sessions (session_id, user_id, input, status, user_profile, metadata, created_at, updated_at, expired_at)
		VALUES ('s-null', 'u1', 'in', 'pending', '{}', '{}', ?, ?, NULL)`, now, now); err != nil {
		t.Fatalf("insert null-expiry row: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO sessions (session_id, user_id, input, status, user_profile, metadata, created_at, updated_at, expired_at)
		VALUES ('s-set', 'u1', 'in', 'pending', '{}', '{}', ?, ?, ?)`, now, now, now.Add(time.Hour)); err != nil {
		t.Fatalf("insert set-expiry row: %v", err)
	}

	repo := NewSessionRepositoryWithDB(db)
	sessions, err := repo.ListByUserID(context.Background(), "u1", 10, 0)
	if err != nil {
		t.Fatalf("ListByUserID must tolerate NULL expired_at, got: %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}
	byID := map[string]*models.Session{}
	for _, s := range sessions {
		byID[s.SessionID] = s
	}
	if _, ok := byID["s-null"]; !ok {
		t.Fatal("session with NULL expired_at missing from listing")
	}
	if got := byID["s-set"]; !got.ExpiredAt.Equal(now.Add(time.Hour)) {
		t.Errorf("set expiry did not round-trip: got %v", got.ExpiredAt)
	}
}
