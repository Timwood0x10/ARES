package lease

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManager_AcquireRelease(t *testing.T) {
	m := NewManager()
	ctx := context.Background()

	l, err := m.Acquire(ctx, "sess-1", "worker-a", time.Minute)
	require.NoError(t, err)
	assert.Equal(t, "sess-1", l.SessionID)
	assert.Equal(t, "worker-a", l.Owner)
	assert.True(t, m.Held("sess-1"))
	assert.Equal(t, 1, m.Count())

	require.NoError(t, m.Release(ctx, "sess-1", "worker-a"))
	assert.False(t, m.Held("sess-1"))
	assert.Equal(t, 0, m.Count())
}

func TestManager_AcquireConflictingOwnerRejected(t *testing.T) {
	m := NewManager()
	ctx := context.Background()

	_, err := m.Acquire(ctx, "sess-1", "worker-a", time.Minute)
	require.NoError(t, err)

	_, err = m.Acquire(ctx, "sess-1", "worker-b", time.Minute)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrLeaseHeld)
}

func TestManager_RenewAndErrors(t *testing.T) {
	m := NewManager()
	ctx := context.Background()

	_, err := m.Acquire(ctx, "sess-1", "worker-a", time.Minute)
	require.NoError(t, err)

	// Non-owner renew rejected.
	err = m.Renew(ctx, "sess-1", "worker-b", time.Minute)
	require.ErrorIs(t, err, ErrLeaseOwnerMismatch)

	// Owner renew succeeds.
	require.NoError(t, m.Renew(ctx, "sess-1", "worker-a", time.Minute))

	// Release by non-owner rejected.
	err = m.Release(ctx, "sess-1", "worker-b")
	require.ErrorIs(t, err, ErrLeaseOwnerMismatch)

	// Unknown session errors.
	err = m.Renew(ctx, "ghost", "worker-a", time.Minute)
	require.ErrorIs(t, err, ErrLeaseNotFound)
}

func TestManager_ExpiredLeaseReacquireable(t *testing.T) {
	m := NewManager()
	ctx := context.Background()
	now := time.Now()
	m.timeNow = func() time.Time { return now }

	_, err := m.Acquire(ctx, "sess-1", "worker-a", 10*time.Millisecond)
	require.NoError(t, err)

	// Advance clock past TTL; lease is expired and Held reports false.
	m.timeNow = func() time.Time { return now.Add(time.Second) }
	assert.False(t, m.Held("sess-1"))

	// A new owner can acquire once the previous lease expired.
	_, err = m.Acquire(ctx, "sess-1", "worker-b", time.Minute)
	require.NoError(t, err, "expired lease must be re-acquirable")
}

func TestManager_ConcurrentAcquireSingleWinner(t *testing.T) {
	m := NewManager()
	ctx := context.Background()

	const workers = 16
	var wg sync.WaitGroup
	winners := make(chan string, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			owner := "worker-" + string(rune('a'+id%26))
			if _, err := m.Acquire(ctx, "sess-race", owner, time.Minute); err == nil {
				winners <- owner
			}
		}(i)
	}
	wg.Wait()
	close(winners)

	count := 0
	for range winners {
		count++
	}
	assert.Equal(t, 1, count, "exactly one concurrent acquirer must win")
}

func TestManager_Validation(t *testing.T) {
	m := NewManager()
	ctx := context.Background()
	_, err := m.Acquire(ctx, "", "worker", time.Minute)
	require.Error(t, err)
	_, err = m.Acquire(ctx, "s", "", time.Minute)
	require.Error(t, err)
	_, err = m.Acquire(ctx, "s", "w", 0)
	require.Error(t, err)
	_, ok := m.Get("missing")
	assert.False(t, ok)
	_ = errors.Is(ErrLeaseHeld, ErrLeaseHeld)
}
