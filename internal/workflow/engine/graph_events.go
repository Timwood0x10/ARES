package engine

import (
	"fmt"
	"sync"
	"time"
)

// ChangeType classifies a graph mutation type.
type ChangeType int

const (
	// ChangeAddNode indicates a node was added to the DAG.
	ChangeAddNode ChangeType = iota
	// ChangeRemoveNode indicates a node was removed from the DAG.
	ChangeRemoveNode
	// ChangeAddEdge indicates an edge was added to the DAG.
	ChangeAddEdge
	// ChangeRemoveEdge indicates an edge was removed from the DAG.
	ChangeRemoveEdge
	// ChangeReplaceNode indicates a node was replaced (swap migration).
	ChangeReplaceNode
)

// GraphChange describes a single mutation to the DAG.
type GraphChange struct {
	Type      ChangeType
	NodeID    string
	OldNodeID string // populated for ChangeReplaceNode
	FromID    string
	ToID      string
	Step      *Step
	Timestamp time.Time
}

// GraphEvent is emitted when a mutation is applied.
type GraphEvent struct {
	Change  GraphChange
	Success bool
	Error   error
}

// graphEventBufferSize is the channel buffer size per subscriber.
const graphEventBufferSize = 64

// GraphEventHub provides pub/sub for graph change events.
type GraphEventHub struct {
	mu          sync.RWMutex
	subscribers map[string]chan GraphEvent
	nextID      int
}

// NewGraphEventHub creates a GraphEventHub.
func NewGraphEventHub() *GraphEventHub {
	return &GraphEventHub{
		subscribers: make(map[string]chan GraphEvent),
	}
}

// Subscribe returns a read-only event channel and a subscription ID.
func (h *GraphEventHub) Subscribe() (string, <-chan GraphEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.nextID++
	id := fmt.Sprintf("sub-%d", h.nextID)
	ch := make(chan GraphEvent, graphEventBufferSize)
	h.subscribers[id] = ch

	return id, ch
}

// Unsubscribe removes a subscriber and closes its channel.
func (h *GraphEventHub) Unsubscribe(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	ch, exists := h.subscribers[id]
	if !exists {
		return
	}

	delete(h.subscribers, id)
	close(ch)
}

// Publish sends an event to all subscribers. Non-blocking (drops if buffer full).
func (h *GraphEventHub) Publish(event GraphEvent) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, ch := range h.subscribers {
		select {
		case ch <- event:
		default:
			// Buffer full, drop event for this subscriber.
		}
	}
}

// SubscriberCount returns the number of active subscribers.
func (h *GraphEventHub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return len(h.subscribers)
}
