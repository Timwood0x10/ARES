package repositories

import (
	"context"
	"database/sql"
	stderrors "errors"
	"strings"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/storage/postgres"
)

// captureDB records every statement executed through ExecContext so tenant
// contract tests can assert on the SQL text without a live database. Every
// other DBTX method panics: CleanupExpired must never touch them.
type captureDB struct {
	queries []string
	args    [][]any
}

// ExecContext records the statement and its arguments.
func (c *captureDB) ExecContext(_ context.Context, query string, args ...interface{}) (sql.Result, error) {
	c.queries = append(c.queries, query)
	c.args = append(c.args, args)
	return stubResult{}, nil
}

// PrepareContext panics: not part of the CleanupExpired contract.
func (c *captureDB) PrepareContext(context.Context, string) (*sql.Stmt, error) {
	panic("captureDB: PrepareContext must not be called by CleanupExpired")
}

// QueryContext panics: not part of the CleanupExpired contract.
func (c *captureDB) QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error) {
	panic("captureDB: QueryContext must not be called by CleanupExpired")
}

// QueryRowContext panics: not part of the CleanupExpired contract.
func (c *captureDB) QueryRowContext(context.Context, string, ...interface{}) *sql.Row {
	panic("captureDB: QueryRowContext must not be called by CleanupExpired")
}

// stubResult satisfies sql.Result without a database.
type stubResult struct{}

// LastInsertId reports zero; cleanup statements are DELETEs.
func (stubResult) LastInsertId() (int64, error) { return 0, nil }

// RowsAffected reports zero; contract tests assert on SQL text, not counts.
func (stubResult) RowsAffected() (int64, error) { return 0, nil }

// TestCleanupExpiredCarriesTenantPredicate locks the S-10 contract: every
// repository CleanupExpired must scope its DELETE by tenant_id. A tenant-less
// global DELETE purges other tenants' rows — the maintenance worker runs it on
// a schedule, so a missing predicate is a recurring cross-tenant write, not a
// one-off. SQL-text and argument assertion instead
// of a live run: the point is the predicate, not the database.
func TestCleanupExpiredCarriesTenantPredicate(t *testing.T) {
	ctx := context.Background()
	const tenant = "tenant-1"
	cases := []struct {
		name string
		run  func(db postgres.DBTX) error
	}{
		{
			"knowledge",
			func(db postgres.DBTX) error {
				_, err := NewKnowledgeRepository(db, nil).CleanupExpired(ctx, tenant, time.Now().Add(-time.Hour))
				return err
			},
		},
		{
			"conversation",
			func(db postgres.DBTX) error {
				_, err := NewConversationRepository(db).CleanupExpired(ctx, tenant)
				return err
			},
		},
		{
			"experience",
			func(db postgres.DBTX) error {
				_, err := NewExperienceRepository(db).CleanupExpired(ctx, tenant)
				return err
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := &captureDB{}
			if err := tc.run(db); err != nil {
				t.Fatalf("CleanupExpired: %v", err)
			}
			if len(db.queries) != 1 {
				t.Fatalf("CleanupExpired must execute exactly one statement, got %d", len(db.queries))
			}
			if !strings.Contains(db.queries[0], "tenant_id = $") {
				t.Fatalf("CleanupExpired DELETE must carry a tenant_id predicate, got:\n%s", db.queries[0])
			}
			if len(db.args[0]) == 0 || !containsArg(db.args[0], tenant) {
				t.Fatalf("CleanupExpired must bind the tenant as a query argument, got %v", db.args[0])
			}
		})
	}
}

// containsArg reports whether args holds the given string value.
func containsArg(args []any, want string) bool {
	for _, a := range args {
		if s, ok := a.(string); ok && s == want {
			return true
		}
	}
	return false
}

// TestCleanupExpiredRejectsEmptyTenant locks the fail-closed guard: an empty
// tenant is rejected before any database access, so a nil DBTX is safe here
// (same pattern as TestUpdateEmbeddingRejectsEmptyTenant). Without the guard
// a caller that forgets the tenant silently degrades into a cross-tenant
// purge — exactly the bug class S-10 documented.
func TestCleanupExpiredRejectsEmptyTenant(t *testing.T) {
	ctx := context.Background()

	secretRepo, err := NewSecretRepository(nil, make([]byte, 32))
	if err != nil {
		t.Fatalf("NewSecretRepository: %v", err)
	}

	cases := []struct {
		name string
		run  func() error
	}{
		{"knowledge", func() error {
			_, err := NewKnowledgeRepository(nil, nil).CleanupExpired(ctx, "", time.Now())
			return err
		}},
		{"conversation", func() error {
			_, err := NewConversationRepository(nil).CleanupExpired(ctx, "")
			return err
		}},
		{"secret", func() error {
			_, err := secretRepo.CleanupExpired(ctx, "")
			return err
		}},
		{"experience", func() error {
			_, err := NewExperienceRepository(nil).CleanupExpired(ctx, "")
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if err == nil {
				t.Fatal("CleanupExpired with an empty tenant must fail, got nil error")
			}
			if !stderrors.Is(err, postgres.ErrMissingTenantID) {
				t.Fatalf("CleanupExpired empty tenant error = %v, want ErrMissingTenantID", err)
			}
		})
	}
}
