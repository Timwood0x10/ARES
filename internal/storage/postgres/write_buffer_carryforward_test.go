package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func items(n int) []*WriteItem {
	out := make([]*WriteItem, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, &WriteItem{
			TenantID: "t",
			Table:    "knowledge_chunks_1024",
			Content:  fmt.Sprintf("item-%d", i),
		})
	}
	return out
}

// TestCapCarriedForward pins the overflow policy applied to a batch that
// survived a failed flush. Left unbounded, the carried batch grew by up to a
// channel's worth per retry cycle and a sustained database outage accumulated
// every item ever written into one in-memory batch. The cap keeps the OLDEST
// items (they have waited longest and drain first on recovery) and counts the
// drop so the loss is observable.
func TestCapCarriedForward(t *testing.T) {
	// batchSize 2 => maxCarriedForward = 2*2 = 4 (the channel capacity).
	const cap = 4

	cases := []struct {
		name     string
		leftover int
		wantKept int
		wantDrop int64
	}{
		{"empty_is_untouched", 0, 0, 0},
		{"under_cap_is_untouched", 3, 3, 0},
		{"at_cap_is_untouched", cap, cap, 0},
		{"over_cap_drops_newest", cap + 3, cap, 3},
		{"far_over_cap_still_bounded", 100, cap, 100 - cap},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := NewWriteBuffer(failConnPool(), nil, 2, time.Hour, nil)
			require.Equal(t, cap, b.maxCarriedForward,
				"maxCarriedForward must track the channel capacity")

			in := items(tc.leftover)
			kept := b.capCarriedForward(in)

			assert.Len(t, kept, tc.wantKept)
			assert.Equal(t, tc.wantDrop, b.DroppedOverflowItems())
			assert.Equal(t, int64(0), b.DeadLetteredItems(),
				"overflow drops are not poison pills and must not be counted as such")

			// The SURVIVORS must be the oldest items, not an arbitrary slice.
			for i := range kept {
				assert.Equal(t, fmt.Sprintf("item-%d", i), kept[i].Content,
					"cap must keep the oldest items first")
			}
		})
	}
}

// TestCapCarriedForward_IsIdempotentOnSuccess pins that a successful flush
// (which returns nil) never triggers the cap or the counter — the bound only
// engages on the failure path.
func TestCapCarriedForward_IsIdempotentOnSuccess(t *testing.T) {
	b := NewWriteBuffer(failConnPool(), nil, 2, time.Hour, nil)
	assert.Nil(t, b.capCarriedForward(nil))
	assert.Equal(t, int64(0), b.DroppedOverflowItems())
}

// TestProcessLoop_CarriedForwardStaysBoundedUnderOutage is the integration
// half of the same contract: against a pool that can never accept a write,
// the processLoop must keep dropping the overflow instead of letting the
// local batch grow without limit. Before the cap, each failed flush handed
// its whole batch back and the loop appended more on top, so the batch grew
// monotonically for the lifetime of the outage.
//
// The loop is driven by a background writer rather than a fixed write count:
// Write is non-blocking and rejects once the channel is full, so a burst
// alone cannot push the carried batch past the cap — only sustained pressure
// across several failed flush cycles can. Each cycle costs one full retry
// backoff (~700ms), which is why the poll budget below is generous.
func TestProcessLoop_CarriedForwardStaysBoundedUnderOutage(t *testing.T) {
	// batchSize 2 => maxCarriedForward 4, channel capacity 4. A short flush
	// interval keeps the loop cycling once the channel drains.
	ctx, cancel := context.WithCancel(context.Background())
	b := NewWriteBuffer(failConnPool(), nil, 2, 20*time.Millisecond, nil)
	require.NoError(t, b.Start(ctx))

	// Keep pressure on the buffer for the whole test. Writes fail fast when
	// the channel is full (see Write), so a tight retry loop is the only way
	// to refill it the moment the processLoop drains.
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		for i := 0; ctx.Err() == nil; i++ {
			_ = b.Write(context.Background(), &WriteItem{
				TenantID: "t",
				Table:    "knowledge_chunks_1024",
				Content:  fmt.Sprintf("item-%d", i),
			})
		}
	}()

	require.Eventually(t, func() bool {
		return b.DroppedOverflowItems() > 0
	}, 20*time.Second, 25*time.Millisecond,
		"sustained flush failure must drop overflow rather than grow the batch unbounded")

	cancel()
	<-writeDone
	require.NoError(t, b.Stop(context.Background()))

	assert.Equal(t, int64(0), b.DeadLetteredItems(),
		"every item used a supported table; none should be dead-lettered")
}
