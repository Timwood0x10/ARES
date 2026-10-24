package taskfabric

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestAgingPreventsSessionStarvation pins the property the aging term exists for: a session with
// one task must not wait behind another session's many tasks purely because its priority is lower.
//
// The fixture is chosen so FIFO ALONE would produce the opposite order: B has the LOWER priority
// but a much older ReadyAt. Ordering by (priority DESC, ReadyAt ASC) without aging puts A's tasks
// first; only the aging term can lift B above them. An earlier version of this test gave B the
// same priority and an older ReadyAt, which the FIFO tiebreak already satisfied — it passed with
// aging disabled, so it could not have caught the regression it was named for.
func TestAgingPreventsSessionStarvation(t *testing.T) {
	f := NewFabric()
	clock := time.Unix(10000, 0)
	f.WithClock(func() time.Time { return clock })

	// Session A: 3 READY tasks at priority 2, ready 10s ago.
	// eff = 2 + 0.01*10 = 2.1
	for _, id := range []string{"a-1", "a-2", "a-3"} {
		require.NoError(t, f.Create(&Task{ID: id, Priority: 2}))
		f.mu.Lock()
		f.tasks[id].ReadyAt = time.Unix(9990, 0)
		f.mu.Unlock()
	}

	// Session B: 1 READY task at priority 1, ready 400s ago.
	// eff = 1 + 0.01*400 = 5.0 > 2.1, so aging — and only aging — lifts it above A.
	require.NoError(t, f.Create(&Task{ID: "b-1", Priority: 1}))
	f.mu.Lock()
	f.tasks["b-1"].ReadyAt = time.Unix(9600, 0)
	f.mu.Unlock()

	got := f.ReadyTasks()

	require.Equal(t, "b-1", got[0],
		"a much older low-priority task must overtake a fresher higher-priority batch; "+
			"without this, a busy session starves a quiet one indefinitely")
}

func TestAgingDoesNotOverrideHigherPriority(t *testing.T) {
	f := NewFabric()
	clock := time.Unix(100, 0)
	f.WithClock(func() time.Time { return clock })

	// Low priority, old.
	lowTask := &Task{ID: "low-old", Priority: 1}
	require.NoError(t, f.Create(lowTask))
	f.mu.Lock()
	f.tasks["low-old"].ReadyAt = time.Unix(0, 0) // 100s wait
	f.mu.Unlock()

	// High priority, just became ready.
	highTask := &Task{ID: "high-new", Priority: 10}
	require.NoError(t, f.Create(highTask))
	f.mu.Lock()
	f.tasks["high-new"].ReadyAt = time.Unix(99, 0) // 1s wait
	f.mu.Unlock()

	got := f.ReadyTasks()

	// effLow = 1 + 0.01 * 100 = 2.0
	// effHigh = 10 + 0.01 * 1 = 10.01
	// High should come first.
	require.Equal(t, "high-new", got[0],
		"higher priority task should still rank above lower priority despite aging")
}
