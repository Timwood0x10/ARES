package leader

import (
	"context"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/core/models"

	"github.com/stretchr/testify/require"
)

// eventTestExecutor is a function type for test executors.
type eventTestExecutor func(ctx context.Context, task *models.Task) (*models.TaskResult, error)

// newEventDrivenDispatcher creates a dispatcher wired to an in-memory event
// store with mock sub-agent listeners that execute tasks via the provided
// executor function. Returns the dispatcher and a cleanup function.
//
// This replaces the old RegisterExecutor pattern in tests with the
// event-driven dispatch path (code_rules_v2 §5.1: single execution path).
func newEventDrivenDispatcher(
	t *testing.T,
	registry map[models.AgentType]string,
	execFn eventTestExecutor,
) (TaskDispatcher, func()) {
	t.Helper()

	store := ares_events.NewMemoryEventStore()
	dispatcher, err := NewTaskDispatcher(
		registry, 2, 30, nil,
		WithDispatcherEventStore(store),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	for _, agentAddr := range registry {
		addr := agentAddr
		subCh, err := store.Subscribe(ctx, ares_events.EventFilter{
			StreamIDs: []string{addr},
			Types:     []ares_events.EventType{ares_events.EventSubTaskScheduled},
		})
		require.NoError(t, err)

		go func() {
			for {
				select {
				case ev, ok := <-subCh:
					if !ok {
						return
					}
					task, _ := ev.Payload["task"].(*models.Task)
					if task == nil {
						continue
					}
					result, execErr := execFn(ctx, task)

					payload := map[string]any{
						"task_id":    task.TaskID,
						"agent_type": string(task.AgentType),
					}
					if execErr != nil {
						payload["error"] = execErr.Error()
						payload["success"] = false
					} else if result != nil {
						payload["success"] = result.Success
						if result.Items != nil {
							payload["items"] = result.Items
						}
						if !result.Success && result.Error != "" {
							payload["error"] = result.Error
						}
					}
					ares_events.Emit(ctx, store, addr, ares_events.EventSubTaskResult, "test", payload)

				case <-ctx.Done():
					return
				}
			}
		}()
	}

	cleanup := func() {
		cancel()
		_ = store.Close()
	}

	return dispatcher, cleanup
}

// defaultTestExecutor returns a simple executor that succeeds with one item.
func defaultTestExecutor(_ context.Context, task *models.Task) (*models.TaskResult, error) {
	result := models.NewTaskResult(task.TaskID, task.AgentType)
	result.SetSuccess([]*models.RecommendItem{{ItemID: "item1", Name: "test item"}}, "ok")
	return result, nil
}

// prevent unused import warning.
var _ = time.Second
