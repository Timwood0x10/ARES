package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFlushBatchFiltered_ReturnsLeftoverOnTransientFailure is the #P0-2
// regression: flushBatchFiltered returned nil on every path, so the caller's
// `len(leftover) > 0` incomplete detection was dead code and a failed final
// flush was reported as success. On a transient failure (no database) the
// valid items must come back so the caller can report them as unwritten.
func TestFlushBatchFiltered_ReturnsLeftoverOnTransientFailure(t *testing.T) {
	b := NewWriteBuffer(failConnPool(), nil, 8, time.Hour, nil)

	batch := []*WriteItem{
		{TenantID: "t", Table: "knowledge_chunks_1024", Content: "a"},
		{TenantID: "t", Table: "experiences_1024", Content: "b"},
	}
	leftover := b.flushBatchFiltered(context.Background(), batch, 0)

	assert.Len(t, leftover, 2,
		"transient flush failure must return the unwritten items, not nil")
}

// TestProcessLoop_CtxDoneDrainsQueuedItems is the #P0-1 regression: the
// ctx.Done() branch flushed only the local batch and abandoned items still
// sitting in the channel (ProductionMemoryManager.Stop cancels the context
// before closing the channel). A poison item queued — not yet received — at
// cancel time must still be dead-lettered by the shutdown drain, proving the
// channel was emptied instead of dropped.
func TestProcessLoop_CtxDoneDrainsQueuedItems(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	b := NewWriteBuffer(failConnPool(), nil, 4, time.Hour, nil)
	require.NoError(t, b.Start(ctx))

	// Fill a size-based flush (4 valid items) so the loop is busy retrying
	// against the failing pool while the poison is enqueued behind it.
	for i := 0; i < 4; i++ {
		require.NoError(t, b.Write(context.Background(), &WriteItem{
			TenantID: "t", Table: "knowledge_chunks_1024", Content: "good",
		}))
	}
	require.NoError(t, b.Write(context.Background(), &WriteItem{
		TenantID: "t", Table: "bogus_table", Content: "poison",
	}))

	// Cancel while the loop is inside the failing flush: the poison is still
	// queued in the channel (never received into the local batch).
	cancel()

	// Stop closes the channel promptly (the production sequence), so the
	// drain loop exits on the close rather than on the 5s grace deadline.
	require.NoError(t, b.Stop(context.Background()))

	assert.Equal(t, int64(1), b.DeadLetteredItems(),
		"ctx.Done() must drain the channel: the queued poison item was dropped before the fix")
}
