package agentipc

import (
	"sync"
	"time"
)

// DeadLetter is one failed IPC request preserved for diagnosis and manual or
// automatic redelivery. The bus records every request
// that could not be delivered or timed out, so multi-agent messaging failures
// are observable instead of vanishing at the error-return boundary.
type DeadLetter struct {
	// ID is a monotonically increasing sequence within the store.
	ID uint64
	// From / To / Topic / Payload mirror the failed request.
	From    string
	To      string
	Topic   string
	Payload any
	// TraceID is the causal chain the failed request belonged to:
	// join it against logs and collaboration receipts for the full story.
	TraceID string
	// Reason is why delivery failed (e.g. ErrAgentNotRegistered / ErrTimeout).
	Reason string
	// At is when the failure was recorded.
	At time.Time
}

// DeadLetterStore is a bounded FIFO of failed IPC requests. Thread-safe.
// Capacity is capped so a long-running process cannot grow it without limit
// (bounded aggregation — same ring policy as the flight aggregates).
type DeadLetterStore struct {
	mu       sync.Mutex
	next     uint64
	capacity int
	// entries is a FIXED ring once full: the oldest entry sits at head, and the next write
	// overwrites the slot at (head+count)%capacity.
	//
	// The previous implementation appended and then sliced the front off (`entries = entries[1:]`),
	// which advances the slice header but keeps the whole backing array alive — so every evicted
	// DeadLetter, Payload included, stayed reachable from the array the slice pointed into. In a
	// long-lived process with steady failures that is an unbounded leak growing at one entry per
	// eviction, and the payloads are message bodies.
	entries []DeadLetter
	head    int
	count   int
}

// NewDeadLetterStore creates a store capped at capacity entries (oldest
// evicted first). A capacity <= 0 falls back to the default (1024).
func NewDeadLetterStore(capacity int) *DeadLetterStore {
	if capacity <= 0 {
		capacity = 1024
	}
	return &DeadLetterStore{capacity: capacity}
}

// Record appends a failed request, evicting the oldest entry when full.
func (s *DeadLetterStore) Record(from, to, topic string, payload any, reason, traceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.capacity <= 0 {
		s.capacity = 1024 // zero-value store: same default as the constructor
	}
	s.next++
	dl := DeadLetter{
		ID:      s.next,
		From:    from,
		To:      to,
		Topic:   topic,
		Payload: payload,
		TraceID: traceID,
		Reason:  reason,
		At:      time.Now(),
	}
	if s.count < s.capacity {
		s.entries = append(s.entries, dl)
		s.count = len(s.entries)
		return
	}
	// Full: overwrite the oldest slot in place. Assigning over it drops the previous value's only
	// reference, which is the point — the array never moves and never grows past capacity.
	s.entries[s.head] = dl
	s.head = (s.head + 1) % s.capacity
	s.count = s.capacity
}

// Snapshot returns a copy of the dead letters (oldest first).
func (s *DeadLetterStore) Snapshot() []DeadLetter {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]DeadLetter, 0, s.count)
	if s.count < len(s.entries) {
		// Not full yet: the entries are in write order.
		return append(out, s.entries[:s.count]...)
	}
	// Full: oldest first means head..end, then 0..head.
	out = append(out, s.entries[s.head:]...)
	out = append(out, s.entries[:s.head]...)
	return out
}

// Count returns the number of dead letters currently retained.
func (s *DeadLetterStore) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count
}
