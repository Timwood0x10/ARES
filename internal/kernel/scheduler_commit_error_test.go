package kernel

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ares_events "github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/aresrecovery"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// TestQuantumResult_StorageOutcomeIsNotAnExecutorVerdict pins the split the attribution depends on.
//
// Two different failures used to look identical to the scheduler: the step succeeded and the
// completion was refused, versus the step failed. Reading a single error forced a guess, and the
// guess charged the storage refusal to an agent whose execution had succeeded.
func TestQuantumResult_StorageOutcomeIsNotAnExecutorVerdict(t *testing.T) {
	refused := errors.Join(taskfabric.ErrCommitRefused, errors.New("disk full"))

	cases := []struct {
		name string
		qr   taskfabric.QuantumResult
		want bool
	}{
		{
			name: "step succeeded, completion refused",
			qr:   taskfabric.QuantumResult{Executed: true, CommitErr: refused},
			want: true,
		},
		{
			name: "step succeeded, completion durable",
			qr:   taskfabric.QuantumResult{Executed: true},
			want: true,
		},
		{
			name: "step failed, failure not even recordable",
			qr:   taskfabric.QuantumResult{Executed: true, StepErr: errors.New("tool exploded"), CommitErr: refused},
			want: false,
		},
		{
			name: "never executed",
			qr:   taskfabric.QuantumResult{Executed: false, StepErr: taskfabric.ErrNotOwner},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.qr.Succeeded())
		})
	}
}

// failingFailedAppendStore refuses the task.failed append, so a real step failure has an unrecordable
// failure on top of it — the exact shape that used to report zero retries.
type failingFailedAppendStore struct{}

func (failingFailedAppendStore) Append(_ context.Context, streamID string, events []*ares_events.Event, _ int64) error {
	for _, ev := range events {
		if string(ev.Type) == string(taskfabric.EventTaskFailed) {
			return errors.Join(taskfabric.ErrCommitRefused, errors.New("disk full"))
		}
	}
	return nil
}
func (failingFailedAppendStore) Read(context.Context, string, ares_events.ReadOptions) ([]*ares_events.Event, error) {
	return nil, nil
}
func (failingFailedAppendStore) ReadAll(context.Context, ares_events.ReadOptions) ([]*ares_events.Event, error) {
	return nil, nil
}
func (failingFailedAppendStore) Subscribe(context.Context, ares_events.EventFilter) (<-chan *ares_events.Event, error) {
	return nil, nil
}
func (failingFailedAppendStore) StreamVersion(context.Context, string) (int64, error) { return 0, nil }

// TestRetryAttribution_SurvivesARefusedFailureAppend drives the production path end to end:
// RunQuantumResult → failWithAttempts → endQuantumOutcome → attribution.
//
// The previous version of this test set RetriesConsumed by hand and then asserted it was 1, which
// would keep passing even if the production code always returned 0. Here the retry has to survive a
// commit failure riding on the same result, and the assertion is on what the attribution recorded.
func TestRetryAttribution_SurvivesARefusedFailureAppend(t *testing.T) {
	fabric := taskfabric.NewFabric().WithEventStore(failingFailedAppendStore{})
	task := &taskfabric.Task{ID: "t-retry-commit", Capability: "code"}
	require.NoError(t, fabric.Create(task))

	epoch, err := fabric.Acquire("t-retry-commit", "coder", time.Minute)
	require.NoError(t, err)

	qr := fabric.RunQuantumResult("t-retry-commit", "coder", epoch, func() (any, bool, error) {
		return nil, false, errors.New("tool exploded")
	})

	require.Equal(t, 1, qr.RetriesConsumed, "the retry the fabric spent must be reported")
	require.Error(t, qr.CommitErr, "and the failure it could not record must be reported separately")
	assert.False(t, qr.Succeeded())

	attribution := aresrecovery.NewExecutionAttribution()
	tracker := NewLoadTracker()
	sched := New(fabric, map[string]CapabilityExecutor{}, tracker)
	sched.WithAttribution(attribution)

	tracker.Begin("coder")
	sched.endQuantumOutcome(context.Background(), "coder", "code", "t-retry-commit", qr, time.Millisecond)

	snap := attribution.Snapshot()
	require.Len(t, snap.PerCapability, 1)
	cr := snap.PerCapability[0]
	assert.Equal(t, 1, cr.Fail, "the step really did fail")
	assert.Equal(t, 1.0, cr.AvgRetries,
		"and the retry it consumed must survive the commit failure on the same result")
}
