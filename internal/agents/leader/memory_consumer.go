// Package leader provides the Leader Agent implementation for multi-agent orchestration.
package leader

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	memory "github.com/Timwood0x10/ares/internal/ares_memory"
)

// memoryConsumer is a dedicated, event-driven consumer for post-result memory
// finalization. It decouples memory writes (update task output, record assistant
// message, distill) from the leader's Process/ProcessStream loop: the leader
// emits EventMemoryFinalize and the consumer performs the writes asynchronously.
//
// It owns its own goroutine and lifecycle (Start/Stop) so the leader does not
// manage distillation errgroups directly (leader/sub decoupling, C phase).
type memoryConsumer struct {
	mu       sync.Mutex
	store    ares_events.EventStore
	mem      memory.MemoryManager
	streamID string // leader ID used as the event stream

	wg      sync.WaitGroup
	cancel  context.CancelFunc
	started bool
}

func newMemoryConsumer(store ares_events.EventStore, mem memory.MemoryManager, streamID string) *memoryConsumer {
	return &memoryConsumer{
		store:    store,
		mem:      mem,
		streamID: streamID,
	}
}

// Start subscribes to EventMemoryFinalize and consumes memory writes in a
// background goroutine. The subscription is established synchronously before
// Start returns, so an Emit that happens after Start is guaranteed to reach the
// consumer (otherwise a subscriber that is still starting could silently drop
// the event). It is idempotent and safe to call multiple times.
func (c *memoryConsumer) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started || c.store == nil || c.mem == nil {
		return nil
	}
	subCtx, cancel := context.WithCancel(ctx)
	ch, err := c.store.Subscribe(subCtx, ares_events.EventFilter{
		Types: []ares_events.EventType{ares_events.EventMemoryFinalize},
	})
	if err != nil {
		cancel()
		return fmt.Errorf("memory consumer: subscribe: %w", err)
	}
	c.cancel = cancel
	c.started = true

	c.wg.Add(1)
	go c.consume(subCtx, ch)
	return nil
}

// Stop cancels the subscription and waits for in-flight memory writes to finish
// (with a bounded wait) so no write is lost during shutdown.
func (c *memoryConsumer) Stop() {
	c.mu.Lock()
	if !c.started {
		c.mu.Unlock()
		return
	}
	c.started = false
	cancel := c.cancel
	c.cancel = nil
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	c.wg.Wait()
}

func (c *memoryConsumer) consume(ctx context.Context, ch <-chan *ares_events.Event) {
	defer c.wg.Done()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			c.handleFinalize(ev)
		case <-ctx.Done():
			// Drain any events already delivered to the buffered channel before
			// returning. Append→notifySubscribers is synchronous under the
			// store lock, so an Emit that completed before Stop will have its
			// event sitting in the buffer; without this drain, Stop would drop
			// an in-flight finalization (TestMemoryConsumer_StopDrainsInFlight).
			for {
				select {
				case ev, ok := <-ch:
					if !ok {
						return
					}
					c.handleFinalize(ev)
				default:
					return
				}
			}
		}
	}
}

// handleFinalize performs the async memory writes for one EventMemoryFinalize.
// Uses a detached context with a bounded timeout so writes complete even after
// the request context is cancelled (code_rules_v2 §4.3: non-blocking I/O).
func (c *memoryConsumer) handleFinalize(ev *ares_events.Event) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("memory consumer panic recovered", "stream", c.streamID, "panic", r)
		}
	}()

	sessionID, _ := ev.Payload["session_id"].(string)
	taskID, _ := ev.Payload["task_id"].(string)
	resultStr, _ := ev.Payload["result_summary"].(string)
	if sessionID == "" {
		return
	}

	memCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if taskID != "" {
		if err := c.mem.UpdateTaskOutput(memCtx, taskID, resultStr); err != nil {
			log.Warn("memory operation failed, proceeding without", "operation", "UpdateTaskOutput", "error", err)
		}
	}
	if err := c.mem.AddMessage(memCtx, sessionID, "assistant", resultStr); err != nil {
		log.Warn("memory operation failed, proceeding without", "operation", "AddMessage", "error", err)
	}

	// Best-effort emit; the payload is informational.
	_ = ares_events.Emit(memCtx, c.store, c.streamID, ares_events.EventMessageAdded, "leader", map[string]any{
		"session_id": sessionID,
		"role":       "assistant",
	})
	if taskID != "" {
		_ = ares_events.Emit(memCtx, c.store, c.streamID, ares_events.EventTaskCompleted, "leader", map[string]any{
			"task_id": taskID,
			"status":  "completed",
		})
	}

	if taskID != "" {
		if _, err := c.mem.DistillTask(memCtx, taskID); err != nil {
			log.Warn("memory operation failed, proceeding without", "operation", "DistillTask", "error", err)
		}
	}
}
