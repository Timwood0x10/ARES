package taskfabric

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
)

// pgRestoreTestPool connects to the TEST_POSTGRES_DSN database (skipping when
// unset — the pg_store_test.go skip convention) and returns a pool the test
// can build PostgresEventStores over. The DSN is parsed into a
// postgres.Config instead of using DefaultConfig so the test runs against
// whatever database the environment provides.
func pgRestoreTestPool(t *testing.T) *postgres.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set, skipping integration test")
	}
	u, err := url.Parse(dsn)
	require.NoError(t, err, "TEST_POSTGRES_DSN must be a postgres:// URL")
	port := 5432
	if p := u.Port(); p != "" {
		port, err = strconv.Atoi(p)
		require.NoError(t, err, "TEST_POSTGRES_DSN port must be numeric")
	}
	password, _ := u.User.Password()
	sslMode := "disable"
	if s := u.Query().Get("sslmode"); s != "" {
		sslMode = s
	}
	pool, err := postgres.NewPool(&postgres.Config{
		Host:     u.Hostname(),
		Port:     port,
		User:     u.User.Username(),
		Password: password,
		Database: strings.TrimPrefix(u.Path, "/"),
		SSLMode:  sslMode,
	})
	require.NoError(t, err, "connect to TEST_POSTGRES_DSN")
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}

// pgCleanupEvents empties the events table so one test's rows never leak into
// another's restore fold.
func pgCleanupEvents(t *testing.T, pool *postgres.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), "DELETE FROM events")
	require.NoError(t, err, "cleanup events table")
}

// TestRestoreFromStorePostgresPartialDependency is the M3 acceptance ④ contract
// over real Postgres: an AllowPartial task degraded by a permanently failed
// predecessor folds back with its policy, its recorded gaps, and still
// schedulable. The restart must not turn a gap the cascade recorded back into
// "waiting on a dependency that can never complete".
func TestRestoreFromStorePostgresPartialDependency(t *testing.T) {
	pool := pgRestoreTestPool(t)
	pgCleanupEvents(t, pool)
	ctx := context.Background()

	store1, err := ares_events.NewPostgresEventStore(pool)
	require.NoError(t, err)
	f1 := NewFabric().WithEventStore(store1)

	require.NoError(t, f1.Create(&Task{ID: "t2", Capability: "rust", RetryPolicy: RetryPolicy{MaxRetries: 0}}))
	require.NoError(t, f1.Create(&Task{ID: "t3", Capability: "rust"}))
	require.NoError(t, f1.Create(&Task{
		ID:           "t4",
		Capability:   "rust",
		Dependencies: []string{"t2", "t3"},
		AllowPartial: true,
		BackoffBase:  time.Second,
		BackoffMax:   4 * time.Second,
	}))

	// t2 dies for good; t3 lands. t4 is left degraded but fully satisfied.
	epoch2, err := f1.Acquire("t2", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f1.Start("t2", "agent-a", epoch2))
	require.NoError(t, f1.Fail("t2", "agent-a", epoch2, errors.New("root cause: tool unavailable")))
	epoch3, err := f1.Acquire("t3", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f1.Start("t3", "agent-a", epoch3))
	require.NoError(t, f1.Complete("t3", "agent-a", epoch3))
	require.Contains(t, f1.ResumableTasks(), "t4", "fixture sanity: schedulable before the restart")

	// "Restart": fresh store instance over the same pool, fresh fabric.
	store2, err := ares_events.NewPostgresEventStore(pool)
	require.NoError(t, err)
	f2 := NewFabric().WithEventStore(store2)
	require.NoError(t, f2.RestoreFromStore(ctx))

	got, err := f2.Task("t4")
	require.NoError(t, err)
	require.True(t, got.AllowPartial, "AllowPartial must survive the PG round-trip")
	require.Equal(t, []string{"t2"}, got.DegradedInputs, "recorded gaps must survive the PG round-trip")
	require.Equal(t, time.Second, got.BackoffBase, "the retry policy rides the same events")
	require.Equal(t, 4*time.Second, got.BackoffMax)
	require.Equal(t, StateReady, got.State)
	require.Contains(t, f2.ResumableTasks(), "t4", "the rebuilt fabric must still schedule the degraded task")
	require.True(t, mustIsReady(t, f2, "t4"), "IsReady must agree with the scheduler's view")

	// The failed root stays terminal: a restart never revives it.
	root, err := f2.Task("t2")
	require.NoError(t, err)
	require.Equal(t, StateFailed, root.State)
}

// TestRestoreFromStorePostgresRoundTrip is the M4.1 persistence contract,
// end to end: a task lifecycle persisted through a PostgresEventStore by one
// fabric is folded back by a FRESH fabric + FRESH store instance over the
// same database — exactly the shape of a serve restart with storage.enabled.
// It also exercises the JSON-round-tripped payload forms (float64 numbers,
// []any dependencies) that the in-memory restore tests never hit.
func TestRestoreFromStorePostgresRoundTrip(t *testing.T) {
	pool := pgRestoreTestPool(t)
	pgCleanupEvents(t, pool)
	ctx := context.Background()

	// "Previous boot": a fabric with the PG-backed store persists a task that
	// yielded mid-execution with a checkpoint (crash mid-quantum) plus a task
	// that completed.
	store1, err := ares_events.NewPostgresEventStore(pool)
	require.NoError(t, err)
	f1 := NewFabric().WithEventStore(store1)

	require.NoError(t, f1.Create(&Task{
		ID:         "peer-plan-7",
		Capability: "ares/plan",
		Priority:   3,
		Origin:     "agent-root",
		RetryPolicy: RetryPolicy{
			MaxRetries: 2,
			Attempts:   1,
		},
	}))
	epoch1, err := f1.Acquire("peer-plan-7", "agent-a", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f1.Start("peer-plan-7", "agent-a", epoch1))
	require.NoError(t, f1.Yield("peer-plan-7", "agent-a", epoch1, EncodeCheckpoint(DecodedCheckpoint{
		Payload:        map[string]any{"input": "audit the events table"},
		StepCheckpoint: map[string]any{"step": 3},
	})))

	require.NoError(t, f1.Create(&Task{ID: "peer-plan-8", Capability: "tool/echo"}))
	epoch8, err := f1.Acquire("peer-plan-8", "agent-a", time.Minute)
	require.NoError(t, err)
	// Start first: the state machine only allows Leased → Running → Completed
	// (state.go canTransition), so Acquire → Complete directly is ErrIllegalState.
	// This ran only in CI (the test skips without TEST_POSTGRES_DSN), which is
	// why the missing hop survived locally.
	require.NoError(t, f1.Start("peer-plan-8", "agent-a", epoch8))
	require.NoError(t, f1.Complete("peer-plan-8", "agent-a", epoch8))

	// "Restart": a fresh store instance over the same pool, a fresh fabric.
	// The store construction itself re-ensures the schema idempotently.
	store2, err := ares_events.NewPostgresEventStore(pool)
	require.NoError(t, err)
	f2 := NewFabric().WithEventStore(store2)
	require.NoError(t, f2.RestoreFromStore(ctx))

	// Non-terminal task folds back to READY, unowned, with its checkpoint.
	got, err := f2.Task("peer-plan-7")
	require.NoError(t, err)
	require.Equal(t, StateReady, got.State, "non-terminal task must fold to READY")
	require.Empty(t, got.Owner, "lease must never be restored")
	require.Equal(t, "ares/plan", got.Capability)
	require.Equal(t, 3, got.Priority)
	require.Equal(t, "agent-root", got.Origin)
	require.Equal(t, RetryPolicy{MaxRetries: 2, Attempts: 1}, got.RetryPolicy)
	require.NotNil(t, got.Checkpoint, "checkpoint must survive the PG round-trip")
	dc, err := DecodeCheckpoint(got.Checkpoint)
	require.NoError(t, err)
	require.Equal(t, CurrentCheckpointSchemaVersion, dc.SchemaVersion)
	require.Equal(t, map[string]any{"input": "audit the events table"}, dc.Payload)
	require.Equal(t, map[string]any{"step": float64(3)}, dc.StepCheckpoint)

	// Terminal task stays terminal — a completed task is never revived.
	done, err := f2.Task("peer-plan-8")
	require.NoError(t, err)
	require.Equal(t, StateCompleted, done.State)

	// Fencing: the rebuilt fabric's first token must strictly dominate every
	// token the previous boot handed out, and the stale pre-crash holder must
	// be rejected.
	epoch2, err := f2.Acquire("peer-plan-7", "agent-b", time.Minute)
	require.NoError(t, err)
	require.Greater(t, epoch2, epoch1, "restored epoch must exceed all pre-restart epochs")
	// ownerLocked checks ownership BEFORE the epoch (fabric.go:309 then :312),
	// so the stale pre-restart holder trips the owner guard first...
	err = f2.Complete("peer-plan-7", "agent-a", epoch1)
	require.ErrorIs(t, err, ErrNotOwner, "pre-restart holder must not be accepted after restore")
	// ...and epoch fencing is what the CURRENT owner hits when it presents a
	// pre-restart epoch.
	err = f2.Complete("peer-plan-7", "agent-b", epoch1)
	require.ErrorIs(t, err, ErrEpochMismatch, "pre-restart epoch must not be accepted even by the current owner")
}
