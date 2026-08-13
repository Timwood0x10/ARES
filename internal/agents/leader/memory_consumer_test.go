package leader

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingMemoryManager embeds the package mock and shadows the memory-write
// methods with recording variants, so tests can assert the event→write closed
// loop. The embedded mockMemoryManager supplies all other MemoryManager methods.
type recordingMemoryManager struct {
	mockMemoryManager
	mu       sync.Mutex
	outputs  []string
	added    []string
	distills []string
}

func (m *recordingMemoryManager) UpdateTaskOutput(_ context.Context, taskID, output string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.outputs = append(m.outputs, taskID+":"+output)
	return nil
}

func (m *recordingMemoryManager) AddMessage(_ context.Context, _ string, role, content string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.added = append(m.added, role+":"+content)
	return nil
}

func (m *recordingMemoryManager) DistillTask(_ context.Context, taskID string) (*models.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.distills = append(m.distills, taskID)
	return nil, nil
}

// TestMemoryConsumer_EventDrivenClosedLoop verifies the C-phase contract: the
// leader emits EventMemoryFinalize and the memoryConsumer (started in
// Start/stopped in Stop) performs UpdateTaskOutput + AddMessage + DistillTask
// off the leader's response path.
func TestMemoryConsumer_EventDrivenClosedLoop(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	t.Cleanup(func() { _ = store.Close() })

	mem := &recordingMemoryManager{}
	c := newMemoryConsumer(store, mem, "leader-test")

	require.NoError(t, c.Start(context.Background()))
	defer c.Stop()

	// Emit the finalize request as the leader would.
	ok := ares_events.Emit(context.Background(), store, "leader-test",
		ares_events.EventMemoryFinalize, "leader", map[string]any{
			"session_id":     "sess-1",
			"task_id":        "task-1",
			"result_summary": "Generated 3 items",
		})
	require.True(t, ok, "emit should succeed")

	// Poll until the consumer performs all three writes.
	require.Eventually(t, func() bool {
		mem.mu.Lock()
		defer mem.mu.Unlock()
		return len(mem.outputs) == 1 && len(mem.added) == 1 && len(mem.distills) == 1
	}, 3*time.Second, 20*time.Millisecond, "consumer should finalize memory")

	mem.mu.Lock()
	defer mem.mu.Unlock()
	assert.Equal(t, "task-1:Generated 3 items", mem.outputs[0])
	assert.Equal(t, "assistant:Generated 3 items", mem.added[0])
	assert.Equal(t, "task-1", mem.distills[0])
}

// TestMemoryConsumer_StopDrainsInFlight verifies Stop waits for an in-flight
// write to complete (no finalization lost during shutdown).
func TestMemoryConsumer_StopDrainsInFlight(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	t.Cleanup(func() { _ = store.Close() })

	mem := &recordingMemoryManager{}
	c := newMemoryConsumer(store, mem, "leader-test")

	require.NoError(t, c.Start(context.Background()))

	ok := ares_events.Emit(context.Background(), store, "leader-test",
		ares_events.EventMemoryFinalize, "leader", map[string]any{
			"session_id":     "sess-2",
			"task_id":        "task-2",
			"result_summary": "Generated 1 items",
		})
	require.True(t, ok)

	// Stop must block until the in-flight write completes.
	c.Stop()

	mem.mu.Lock()
	defer mem.mu.Unlock()
	assert.Len(t, mem.outputs, 1, "in-flight write must not be dropped")
	assert.Equal(t, "task-2:Generated 1 items", mem.outputs[0])
}

// TestMemoryConsumer_StartIdempotent verifies Start is safe to call twice.
func TestMemoryConsumer_StartIdempotent(t *testing.T) {
	store := ares_events.NewMemoryEventStore()
	t.Cleanup(func() { _ = store.Close() })

	mem := &recordingMemoryManager{}
	c := newMemoryConsumer(store, mem, "leader-test")

	require.NoError(t, c.Start(context.Background()))
	require.NoError(t, c.Start(context.Background())) // second call is a no-op
	c.Stop()
}
