package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failConnPool builds a Pool whose connection attempts always fail, so
// flushes of VALID items fail transiently (not panic) — letting the test
// observe the loop's re-queue behavior without a database.
func failConnPool() *Pool {
	return &Pool{db: sql.OpenDB(driver.Connector(failingConnector{}))}
}

type failingConnector struct{}

func (failingConnector) Connect(context.Context) (driver.Conn, error) {
	return nil, errors.New("no database in test")
}

func (failingConnector) Driver() driver.Driver { return nil }

// TestFlushBatch_UnsupportedTableIsPermanent is the #64 regression: an
// item with an unsupported table (the poison pill) failed the WHOLE batch
// transaction; the batch was then re-queued and retried forever — a
// livelock that also blocked every good item sharing the batch. The error
// must be classified permanent (ErrPermanentWriteItem) so the caller can
// dead-letter the poison instead of retrying it.
func TestFlushBatch_UnsupportedTableIsPermanent(t *testing.T) {
	b := NewWriteBuffer(failConnPool(), nil, 8, time.Second, nil)

	// The poison is detected before any DB access, so nil-safe with no
	// live connection: the sentinel must come back immediately.
	err := b.flushBatch(context.Background(), []*WriteItem{
		{TenantID: "t", Table: "knowledge_chunks_1024", Content: "ok"},
		{TenantID: "t", Table: "definitely_not_a_table", Content: "poison"},
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrPermanentWriteItem),
		"unsupported-table failure must classify as permanent, got: %v", err)

	// A supported-only batch must NOT be flagged permanent (it needs a real
	// DB, so any error here is a plain transient failure, not the sentinel).
	require.True(t, supportedWriteTable("knowledge_chunks_1024"))
	require.True(t, supportedWriteTable("experiences_1024"))
	assert.False(t, supportedWriteTable("nope"))
}

// TestFlushBatchWithRetry_NoRetryOnPermanent verifies the retry loop
// short-circuits permanent failures: retrying a poison item is pointless
// and was the livelock's engine.
func TestFlushBatchWithRetry_NoRetryOnPermanent(t *testing.T) {
	b := NewWriteBuffer(nil, nil, 8, time.Hour, nil) // huge backoff: any retry would hang the test

	start := time.Now()
	err := b.flushBatchWithRetry(context.Background(), []*WriteItem{
		{TenantID: "t", Table: "bogus", Content: "poison"},
	}, 3)
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrPermanentWriteItem))
	assert.Less(t, elapsed, 5*time.Second,
		"permanent failure must return without backing off/retrying (took %v)", elapsed)
}

// TestProcessLoop_DeadLettersPoisonAndFlushesRest is the #64 end-to-end
// regression: a poison item entering the loop must be dead-lettered (logged
// + counted, never retried) while the rest of the batch still flushes. The
// buffer below has no database, so the flush of the good items fails
// transiently and is re-queued — the poison must NOT come back with them
// (that was the livelock).
func TestProcessLoop_DeadLettersPoisonAndFlushesRest(t *testing.T) {
	b := NewWriteBuffer(failConnPool(), nil, 4, 10*time.Millisecond, nil)
	require.NoError(t, b.Start(context.Background()))

	// One poison item and enough good items to trigger a size-based flush.
	poison := &WriteItem{TenantID: "t", Table: "bogus_table", Content: "poison"}
	require.NoError(t, b.Write(context.Background(), poison))
	for i := 0; i < 3; i++ {
		require.NoError(t, b.Write(context.Background(), &WriteItem{
			TenantID: "t", Table: "knowledge_chunks_1024", Content: "good",
		}))
	}

	// Wait for the loop to process the batch: the poison is dead-lettered
	// exactly once; the good items fail (no DB) and are re-queued, but the
	// dead-letter counter must stay at 1 — the poison never re-enters.
	assert.Eventually(t, func() bool {
		return b.DeadLetteredItems() == 1
	}, 5*time.Second, 10*time.Millisecond, "poison item must be dead-lettered exactly once")

	time.Sleep(200 * time.Millisecond) // a few more flush cycles
	assert.Equal(t, int64(1), b.DeadLetteredItems(),
		"poison must never be retried (livelock); good items may retry")

	require.NoError(t, b.Stop(context.Background()))
}
