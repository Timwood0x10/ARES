package ahp

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// MaxRetriesUnlimited indicates that a DLQ entry should be retried indefinitely.
const MaxRetriesUnlimited = 0

// DLQ represents a Dead Letter Queue for failed messages.
type DLQ struct {
	mu       sync.Mutex
	messages []*DLQEntry
	maxSize  int
}

// DLQEntry represents an entry in the dead letter queue.
type DLQEntry struct {
	Message    *AHPMessage
	Error      error
	Reason     string
	Timestamp  time.Time
	Retries    int
	MaxRetries int `json:"max_retries"`
}

// NewDLQ creates a new DLQ.
func NewDLQ(maxSize int) *DLQ {
	if maxSize <= 0 {
		maxSize = 10000
	}
	return &DLQ{
		messages: make([]*DLQEntry, 0, maxSize),
		maxSize:  maxSize,
	}
}

// Add adds a message to the dead letter queue.
func (d *DLQ) Add(msg *AHPMessage, err error, reason string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	entry := &DLQEntry{
		Message:   msg,
		Error:     err,
		Reason:    reason,
		Timestamp: time.Now(),
		Retries:   0,
	}

	// Remove oldest if full
	if len(d.messages) >= d.maxSize {
		d.messages[0] = nil
		d.messages = d.messages[1:]
	}

	d.messages = append(d.messages, entry)
}

// GetAll returns all entries in the DLQ.
func (d *DLQ) GetAll() []*DLQEntry {
	d.mu.Lock()
	defer d.mu.Unlock()

	entries := make([]*DLQEntry, len(d.messages))
	copy(entries, d.messages)
	return entries
}

// GetByAgent returns entries for a specific agent.
func (d *DLQ) GetByAgent(agentID string) []*DLQEntry {
	d.mu.Lock()
	defer d.mu.Unlock()

	var entries []*DLQEntry
	for _, entry := range d.messages {
		if entry.Message.AgentID == agentID {
			entries = append(entries, entry)
		}
	}
	return entries
}

// GetBySession returns entries for a specific session.
func (d *DLQ) GetBySession(sessionID string) []*DLQEntry {
	d.mu.Lock()
	defer d.mu.Unlock()

	var entries []*DLQEntry
	for _, entry := range d.messages {
		if entry.Message.SessionID == sessionID {
			entries = append(entries, entry)
		}
	}
	return entries
}

// Size returns the number of entries in the DLQ.
func (d *DLQ) Size() int {
	d.mu.Lock()
	defer d.mu.Unlock()

	return len(d.messages)
}

// Clear removes all entries from the DLQ and releases the backing array.
func (d *DLQ) Clear() {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.messages = make([]*DLQEntry, 0, d.maxSize)
}

// Remove removes an entry from the DLQ and nils the trailing slot
// to avoid leaking the pointer through the underlying array.
func (d *DLQ) Remove(entry *DLQEntry) {
	d.mu.Lock()
	defer d.mu.Unlock()

	for i, e := range d.messages {
		if e == entry {
			copy(d.messages[i:], d.messages[i+1:])
			d.messages[len(d.messages)-1] = nil
			d.messages = d.messages[:len(d.messages)-1]
			return
		}
	}
}

// RemoveBySession removes entries for a specific session.
func (d *DLQ) RemoveBySession(sessionID string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var newMessages []*DLQEntry
	for _, entry := range d.messages {
		if entry.Message.SessionID != sessionID {
			newMessages = append(newMessages, entry)
		}
	}
	d.messages = newMessages
}

// DLQProcessor handles processing of dead letter queue messages.
type DLQProcessor struct {
	dlq           *DLQ
	handlers      map[string]DLQHandler
	mu            sync.RWMutex
	processed     int
	failed        int
	retryInterval time.Duration
}

// DLQHandler handles a dead letter queue entry.
type DLQHandler func(ctx context.Context, entry *DLQEntry) error

// NewDLQProcessor creates a new DLQProcessor.
func NewDLQProcessor(dlq *DLQ) *DLQProcessor {
	return &DLQProcessor{
		dlq:      dlq,
		handlers: make(map[string]DLQHandler),
	}
}

// RegisterHandler registers a handler for a specific error type.
func (p *DLQProcessor) RegisterHandler(errorType string, handler DLQHandler) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.handlers[errorType] = handler
}

// Process processes all entries in the DLQ.
func (p *DLQProcessor) Process(ctx context.Context) error {
	entries := p.dlq.GetAll()

	for _, entry := range entries {
		// Skip entries that have exhausted their retry budget.
		if entry.MaxRetries > 0 && entry.Retries >= entry.MaxRetries {
			continue
		}

		entry.Retries++

		if err := p.processEntry(ctx, entry); err != nil {
			p.mu.Lock()
			p.failed++
			p.mu.Unlock()
			continue
		}

		p.dlq.Remove(entry)

		p.mu.Lock()
		p.processed++
		p.mu.Unlock()
	}

	return nil
}

// StartAutoRetry starts a background loop that retries all pending DLQ entries
// at the given interval. It exits when ctx is cancelled. The caller must use
// errgroup or a cancellable context to manage the goroutine lifecycle.
func (p *DLQProcessor) StartAutoRetry(ctx context.Context, interval time.Duration) {
	p.retryInterval = interval

	g, gCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-gCtx.Done():
				return nil
			case <-ticker.C:
				if err := p.Process(gCtx); err != nil {
					slog.Error("DLQ auto-retry tick failed", "error", err)
				}
			}
		}
	})

	// Block until context is cancelled; ignore the error since the
	// goroutine returns nil on context cancellation.
	_ = g.Wait()
}

// processEntry processes a single DLQ entry.
func (p *DLQProcessor) processEntry(ctx context.Context, entry *DLQEntry) error {
	p.mu.RLock()
	handler, ok := p.handlers[entry.Reason]
	p.mu.RUnlock()

	if !ok {
		// No specific handler, try default
		return p.defaultHandler(ctx, entry)
	}

	return handler(ctx, entry)
}

// defaultHandler is the default handler for DLQ entries.
func (p *DLQProcessor) defaultHandler(ctx context.Context, entry *DLQEntry) error {
	slog.Warn("DLQ entry processed by default handler",
		"session_id", entry.Message.SessionID,
		"reason", entry.Reason,
		"retries", entry.Retries,
		"error", entry.Error,
	)
	return nil
}

// Stats returns processing statistics.
func (p *DLQProcessor) Stats() (processed, failed int) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.processed, p.failed
}
