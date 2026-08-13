package leader

import (
	"context"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/ares_protocol/ahp"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// capturingSender records every AHPMessage it is asked to send so tests can
// assert on the sender identity stamped by getAgentID.
type capturingSender struct {
	sent []*ahp.AHPMessage
	err  error
}

func (s *capturingSender) Send(_ context.Context, _ string, msg *ahp.AHPMessage) error {
	s.sent = append(s.sent, msg)
	return s.err
}

// TestDispatcher_DefaultAgentID verifies that a dispatcher constructed
// without WithDispatcherAgentID falls back to DefaultDispatcherAgentID.
// This locks in the legacy default for tests that never use the message
// sender path.
func TestDispatcher_DefaultAgentID(t *testing.T) {
	d, err := NewTaskDispatcher(map[models.AgentType]string{}, 1, 1, nil)
	require.NoError(t, err)

	td := d.(*taskDispatcher)
	assert.Equal(t, DefaultDispatcherAgentID, td.getAgentID(),
		"default agent ID should be the legacy constant")
}

// TestDispatcher_WithAgentID verifies that WithDispatcherAgentID injects
// the real agent ID, so distributed messages carry the correct sender
// instead of the hardcoded "leader".
func TestDispatcher_WithAgentID(t *testing.T) {
	const realLeaderID = "leader-prod-42"
	d, err := NewTaskDispatcher(
		map[models.AgentType]string{},
		1, 1, nil,
		WithDispatcherAgentID(realLeaderID),
	)
	require.NoError(t, err)

	td := d.(*taskDispatcher)
	assert.Equal(t, realLeaderID, td.getAgentID(),
		"getAgentID must return the configured real ID, not 'leader'")
	assert.NotEqual(t, "leader", td.getAgentID(),
		"getAgentID must not return the legacy 'leader' constant")
}

// TestDispatcher_WithAgentIDEmptyIgnored verifies that an empty agent ID
// option is ignored and the default is retained.
func TestDispatcher_WithAgentIDEmptyIgnored(t *testing.T) {
	d, err := NewTaskDispatcher(
		map[models.AgentType]string{},
		1, 1, nil,
		WithDispatcherAgentID(""),
	)
	require.NoError(t, err)

	td := d.(*taskDispatcher)
	assert.Equal(t, DefaultDispatcherAgentID, td.getAgentID(),
		"empty agent ID should fall back to the default")
}

// TestDispatcher_DispatchStampsConfiguredAgentID verifies end-to-end that
// when a task is dispatched via the message sender, the outgoing AHPMessage
// carries the configured agent ID, not the hardcoded "leader".
func TestDispatcher_DispatchStampsConfiguredAgentID(t *testing.T) {
	const realLeaderID = "leader-real-99"
	sender := &capturingSender{}

	// Register a target agent address but no local executor, forcing the
	// dispatcher down the message-sender path.
	registry := map[models.AgentType]string{
		models.AgentTypeTop: "agent-top-addr",
	}
	d, err := NewTaskDispatcher(
		registry,
		1, 30, sender,
		WithDispatcherAgentID(realLeaderID),
	)
	require.NoError(t, err)

	tasks := []*models.Task{
		models.NewTask("task-1", models.AgentTypeTop, &models.UserProfile{}),
	}

	results, err := d.Dispatch(context.Background(), tasks)
	require.NoError(t, err)
	require.Len(t, results, 1)

	require.Len(t, sender.sent, 1, "sender should have received one message")
	assert.Equal(t, realLeaderID, sender.sent[0].AgentID,
		"outgoing message AgentID must be the configured real ID")
	assert.NotEqual(t, "leader", sender.sent[0].AgentID,
		"outgoing message AgentID must not be the hardcoded 'leader'")
}

// TestDispatcher_EventDriven_DuplicateResultNotDoubleCounted is a regression
// test for the dispatchViaEvents result-collection dedup fix.
//
// A sub-agent may publish a duplicate EventSubTaskResult for the same task
// (e.g. a retry or an event-store replay). Before the fix, every received
// result bumped the `collected` counter, so a duplicate could make the
// collection loop think it was done while some tasks' results were still nil,
// or overwrite a result with a stale duplicate. The fix tracks collected
// taskIDs so each published task is collected exactly once.
func TestDispatcher_EventDriven_DuplicateResultNotDoubleCounted(t *testing.T) {
	const subAddr = "sub-addr-dup"

	store := ares_events.NewMemoryEventStore()
	t.Cleanup(func() { _ = store.Close() })

	registry := map[models.AgentType]string{
		models.AgentTypeTop: subAddr,
	}
	d, err := NewTaskDispatcher(registry, 1, 30, nil,
		WithDispatcherEventStore(store))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Two tasks to the same sub-agent. The sub publishes a DUPLICATE result
	// for the first task and a single result for the second. Pre-fix, the
	// duplicate bumped `collected` past `published`, terminating the loop
	// before the second task's result arrived — leaving its result nil.
	taskA := models.NewTask("dup-task-a", models.AgentTypeTop, &models.UserProfile{})
	taskB := models.NewTask("dup-task-b", models.AgentTypeTop, &models.UserProfile{})

	// Simulate the sub-agent: subscribe to scheduled events, then publish
	// results — task A twice (duplicate), task B once.
	subCtx, subCancel := context.WithCancel(context.Background())
	defer subCancel()
	subCh, err := store.Subscribe(subCtx, ares_events.EventFilter{
		Types: []ares_events.EventType{ares_events.EventSubTaskScheduled},
	})
	require.NoError(t, err)

	go func() {
		for {
			select {
			case ev, ok := <-subCh:
				if !ok {
					return
				}
				taskID, _ := ev.Payload["task_id"].(string)
				payload := map[string]any{
					"task_id":    taskID,
					"agent_type": string(taskA.AgentType),
					"success":    true,
				}
				ares_events.Emit(ctx, store, subAddr, ares_events.EventSubTaskResult, "sub", payload)
				if taskID == taskA.TaskID {
					// Duplicate result for task A only.
					ares_events.Emit(ctx, store, subAddr, ares_events.EventSubTaskResult, "sub", payload)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	results, err := d.Dispatch(ctx, []*models.Task{taskA, taskB})
	require.NoError(t, err)
	require.Len(t, results, 2, "expected exactly two results")

	// Both tasks must have a non-nil, successful result — the duplicate for A
	// must not terminate collection before B's result arrives.
	assert.True(t, results[0].Success, "task A must be successful")
	assert.True(t, results[1].Success, "task B must be collected despite A's duplicate result")
}
