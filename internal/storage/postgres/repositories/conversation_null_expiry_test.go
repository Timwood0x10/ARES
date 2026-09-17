package repositories

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// newConversationTestDB creates an in-memory SQLite database with a
// conversations table and one row per expiry shape.
func newConversationTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE conversations (
		id TEXT PRIMARY KEY, session_id TEXT, tenant_id TEXT, user_id TEXT,
		agent_id TEXT, role TEXT, content TEXT, metadata TEXT,
		expires_at TIMESTAMP, created_at TIMESTAMP)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	now := time.Now()
	rows := []struct {
		id     string
		expiry any
	}{
		{"c-null", nil},
		{"c-set", now.Add(time.Hour)},
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO conversations
			(id, session_id, tenant_id, user_id, agent_id, role, content, metadata, expires_at, created_at)
			VALUES (?, 'sess-1', 'tenant-1', 'user-1', 'agent-1', 'user', 'hello', '{}', ?, ?)`,
			r.id, r.expiry, now); err != nil {
			t.Fatalf("insert %s: %v", r.id, err)
		}
	}
	return db
}

// TestConversationRepository_ListMethodsNullExpiresAt locks REVIEW 3.7:
// expires_at is nullable, but GetBySession/GetByUser/GetByAgent scanned it
// into a bare time.Time — every NULL-expiry row failed its scan and was
// silently skipped (continue), so messages vanished from listings.
func TestConversationRepository_ListMethodsNullExpiresAt(t *testing.T) {
	db := newConversationTestDB(t)
	repo := NewConversationRepository(db)

	bySession, err := repo.GetBySession(context.Background(), "sess-1", "tenant-1", 10)
	if err != nil {
		t.Fatalf("GetBySession: %v", err)
	}
	if len(bySession) != 2 {
		t.Fatalf("GetBySession must return both rows incl. NULL expiry, got %d", len(bySession))
	}

	byUser, err := repo.GetByUser(context.Background(), "user-1", "tenant-1", 10)
	if err != nil {
		t.Fatalf("GetByUser: %v", err)
	}
	if len(byUser) != 2 {
		t.Fatalf("GetByUser must return both rows incl. NULL expiry, got %d", len(byUser))
	}

	byAgent, err := repo.GetByAgent(context.Background(), "agent-1", "tenant-1", 10)
	if err != nil {
		t.Fatalf("GetByAgent: %v", err)
	}
	if len(byAgent) != 2 {
		t.Fatalf("GetByAgent must return both rows incl. NULL expiry, got %d", len(byAgent))
	}

	// The set expiry must round-trip, the NULL one must stay zero.
	for _, conv := range bySession {
		switch conv.ID {
		case "c-set":
			if conv.ExpiresAt.IsZero() {
				t.Error("c-set: set expiry lost")
			}
		case "c-null":
			if !conv.ExpiresAt.IsZero() {
				t.Errorf("c-null: NULL expiry must stay zero, got %v", conv.ExpiresAt)
			}
		}
	}
}
