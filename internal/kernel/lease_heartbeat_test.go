package kernel

import (
	"testing"
	"time"
)

// TestLeaseHeartbeatIntervalFitsTTL locks F-04: the heartbeat cadence must
// ALWAYS fire inside the lease window. The old code floored the interval at
// 5s unconditionally, so any kernel.lease_ttl < 5s got its first heartbeat
// after the lease had already expired — recovery requeued a still-running
// quantum (duplicate side effects despite epoch fencing).
func TestLeaseHeartbeatIntervalFitsTTL(t *testing.T) {
	cases := []struct {
		ttl  time.Duration
		want time.Duration
		note string
	}{
		{30 * time.Second, 10 * time.Second, "normal ttl: ttl/3"},
		{15 * time.Second, 5 * time.Second, "boundary: ttl/3 exactly hits the floor"},
		{12 * time.Second, 4 * time.Second, "short ttl: keeps ttl/3, no 5s floor"},
		{6 * time.Second, 2 * time.Second, "short ttl: keeps ttl/3"},
		{2 * time.Second, 2 * time.Second / 3, "chaos demo ttl: ttl/3 stays sub-second"},
		{1 * time.Second, 1 * time.Second / 3, "minimal ttl"},
		{0, 5 * time.Second, "degenerate ttl: legacy floor"},
		{-1 * time.Second, 5 * time.Second, "invalid ttl: legacy floor"},
	}
	for _, tc := range cases {
		got := leaseHeartbeatInterval(tc.ttl)
		if got != tc.want {
			t.Errorf("leaseHeartbeatInterval(%v) = %v, want %v (%s)", tc.ttl, got, tc.want, tc.note)
		}
		// The core invariant, checked independently of the table: a fresh
		// lease always outlives the first heartbeat.
		if tc.ttl > 0 && got >= tc.ttl {
			t.Errorf("interval %v must be < ttl %v — first renewal must land inside the lease", got, tc.ttl)
		}
	}
}
