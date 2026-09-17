package context

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Memory errors.
var (
	ErrSessionNotFound = errors.New("session not found")
	ErrTaskNotFound    = errors.New("task not found")
)

// defaultMaxSessionMessages bounds how many messages one session may STORE.
// MaxHistory (the closed-loop context window) only truncates at BuildContext
// read time, so without a storage bound a long-lived session's message slice
// — and every GetMessages/BuildContext copy of it — grew without bound across
// a server lifetime. The default is comfortably above every configured
// read-side window (MaxHistory defaults to 10–50) so the newest history the
// context builder draws from is always available.
const defaultMaxSessionMessages = 500

// SessionMemory stores conversation context for a session.
type SessionMemory struct {
	sessions     map[string]*SessionData
	mu           sync.RWMutex
	maxSize      int
	maxMessages  int
	ttl          time.Duration
	cleanupTick  time.Duration
	stopCleanup  chan struct{}
	stopOnce     sync.Once
	cleanupStart sync.Once
	wg           sync.WaitGroup
}

// SessionData holds session information.
type SessionData struct {
	SessionID  string
	UserID     string
	Messages   []Message
	Context    map[string]interface{}
	AccessedAt time.Time
	CreatedAt  time.Time
}

// Standard role constants for typed message routing.
const (
	RoleUser       = "user"
	RoleAssistant  = "assistant"
	RoleSystem     = "system"
	RoleToolCall   = "tool_call"
	RoleToolResult = "tool_result"
)

// ToolCallFunction holds the function details of a tool invocation.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolCall represents a single tool invocation.
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

// Message represents a chat message with optional tool call metadata.
type Message struct {
	Role         string     `json:"role"`
	Content      string     `json:"content"`
	Time         time.Time  `json:"time"`
	TurnID       string     `json:"turn_id,omitempty"`
	ToolCallID   string     `json:"tool_call_id,omitempty"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	EventKind    string     `json:"event_kind,omitempty"`
	ParentID     string     `json:"parent_id,omitempty"`
	ArtifactRefs []string   `json:"artifact_refs,omitempty"`
}

// NewSessionMemory creates a new SessionMemory. Per-session message storage
// is bounded by defaultMaxSessionMessages; use WithMaxMessages to tune it.
func NewSessionMemory(maxSize int, ttl time.Duration) *SessionMemory {
	return &SessionMemory{
		sessions:    make(map[string]*SessionData),
		maxSize:     maxSize,
		maxMessages: defaultMaxSessionMessages,
		ttl:         ttl,
		cleanupTick: ttl / 2, // Cleanup every half TTL period
		stopCleanup: make(chan struct{}),
	}
}

// WithMaxMessages sets the per-session stored-message cap (0 or negative
// restores the default). Returns the receiver for chaining.
// Reconfigure pushes updated limits from a runtime config patch into the
// already-constructed live store. maxSize and ttl are captured at
// NewSessionMemory time, so without this push the patch executor updated the
// stored config while the live store kept enforcing boot-time values.
// Non-positive values leave the corresponding limit unchanged. The cleanup
// tick keeps its original cadence (a coarser tick only delays the sweep;
// lazy expiry in Get/GetMessages is the backstop).
func (m *SessionMemory) Reconfigure(maxSize int, ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if maxSize > 0 {
		m.maxSize = maxSize
	}
	if ttl > 0 {
		m.ttl = ttl
	}
}

func (m *SessionMemory) WithMaxMessages(n int) *SessionMemory {
	if n <= 0 {
		n = defaultMaxSessionMessages
	}
	m.maxMessages = n
	return m
}

// StartCleanup starts the background cleanup task.
func (m *SessionMemory) StartCleanup() {
	m.cleanupStart.Do(func() {
		if m.cleanupTick <= 0 {
			return
		}

		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			ticker := time.NewTicker(m.cleanupTick)
			defer ticker.Stop()

			for {
				select {
				case <-ticker.C:
					removed := m.Cleanup(context.Background())
					if removed > 0 {
						log.Debug("Session memory cleanup completed", "removed_sessions", removed)
					}
				case <-m.stopCleanup:
					return
				}
			}
		}()
	})
}

// StopCleanup stops the background cleanup task.
func (m *SessionMemory) StopCleanup() {
	m.stopOnce.Do(func() {
		close(m.stopCleanup)
	})
	m.wg.Wait()
}

// Cleanup removes all expired sessions and returns the count of removed sessions.
// Limits cleanup to avoid long lock holding that blocks other operations.
func (m *SessionMemory) Cleanup(ctx context.Context) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	removed := 0

	// Limit cleanup to avoid long lock holding
	const maxCleanupPerCall = 100
	for sessionID, session := range m.sessions {
		if removed >= maxCleanupPerCall {
			// Stop early to avoid blocking other operations
			break
		}
		if now.Sub(session.AccessedAt) > m.ttl {
			delete(m.sessions, sessionID)
			removed++
		}
	}

	return removed
}

// Get retrieves session data and updates access time.
// A deep copy is returned so callers cannot mutate the stored session
// (messages slice and context map are copied, not shared).
func (m *SessionMemory) Get(ctx context.Context, sessionID string) (*SessionData, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, exists := m.sessions[sessionID]
	if !exists {
		return nil, false
	}

	if time.Since(session.AccessedAt) > m.ttl {
		delete(m.sessions, sessionID)
		return nil, false
	}

	// Update access time (requires write lock).
	session.AccessedAt = time.Now()

	// Deep copy messages and context so the caller cannot mutate the
	// internal session state.
	messages := make([]Message, len(session.Messages))
	copy(messages, session.Messages)
	contextCopy := make(map[string]interface{}, len(session.Context))
	for k, v := range session.Context {
		contextCopy[k] = v
	}
	return &SessionData{
		SessionID:  session.SessionID,
		UserID:     session.UserID,
		Messages:   messages,
		Context:    contextCopy,
		AccessedAt: session.AccessedAt,
		CreatedAt:  session.CreatedAt,
	}, true
}

// Set stores session data. The messages slice is copied so the caller
// cannot mutate stored state through the original backing array, and
// truncated to the newest maxMessages so the storage bound holds on this
// path too (AddMessage is the growth path; Set is the invariant).
func (m *SessionMemory) Set(ctx context.Context, sessionID, userID string, messages []Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.sessions) >= m.maxSize {
		m.evictOldest()
	}

	msgCopy := make([]Message, len(messages))
	copy(msgCopy, messages)
	if m.maxMessages > 0 && len(msgCopy) > m.maxMessages {
		msgCopy = msgCopy[len(msgCopy)-m.maxMessages:]
	}

	session := &SessionData{
		SessionID:  sessionID,
		UserID:     userID,
		Messages:   msgCopy,
		Context:    make(map[string]interface{}),
		AccessedAt: time.Now(),
		CreatedAt:  time.Now(),
	}

	m.sessions[sessionID] = session
	return nil
}

// AddMessage adds a message to the session, keeping only the newest
// maxMessages entries (the oldest are dropped). The bound is a STORAGE
// bound, not the context window: MaxHistory truncates at BuildContext read
// time and defaults far below this cap, so the newest history the context
// builder needs is always present — while a long-lived session can no longer
// grow its slice (and every copy of it) without limit.
func (m *SessionMemory) AddMessage(ctx context.Context, sessionID string, msg Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, exists := m.sessions[sessionID]
	if !exists {
		return ErrSessionNotFound
	}

	session.Messages = append(session.Messages, msg)
	if m.maxMessages > 0 && len(session.Messages) > m.maxMessages {
		// Drop from the front, preserving recency. Re-slice rather than
		// copy: the append above already owns a backing array that no
		// caller holds a reference to (GetMessages/Get always copy out).
		session.Messages = session.Messages[len(session.Messages)-m.maxMessages:]
	}
	session.AccessedAt = time.Now()

	return nil
}

// GetMessages returns session messages. A live read refreshes AccessedAt
// (proof of life, same as Get) — but an EXPIRED session is deleted here too,
// matching Get's lazy-expiry semantics. The pre-fix version refreshed
// AccessedAt without any TTL check, so a session reached only through this
// path (BuildPromptMessages/BuildContext, the memory tool) was immortal —
// the TTL sweep could never reap it.
func (m *SessionMemory) GetMessages(ctx context.Context, sessionID string) ([]Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, exists := m.sessions[sessionID]
	if !exists {
		return nil, ErrSessionNotFound
	}
	if time.Since(session.AccessedAt) > m.ttl {
		delete(m.sessions, sessionID)
		return nil, ErrSessionNotFound
	}
	session.AccessedAt = time.Now()

	// Return a copy to prevent concurrent modification of internal slice
	messages := make([]Message, len(session.Messages))
	copy(messages, session.Messages)
	return messages, nil
}

// Delete removes a session.
func (m *SessionMemory) Delete(ctx context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.sessions, sessionID)
	return nil
}

// Clear removes all sessions.
func (m *SessionMemory) Clear(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sessions = make(map[string]*SessionData)
	return nil
}

// Size returns the number of sessions.
func (m *SessionMemory) Size() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return len(m.sessions)
}

// Close stops the background cleanup task and clears all sessions.
func (m *SessionMemory) Close(ctx context.Context) error {
	// Stop background cleanup
	m.StopCleanup()

	// Clear all sessions
	return m.Clear(ctx)
}

// evictOldest removes the oldest session.
func (m *SessionMemory) evictOldest() {
	var oldest *SessionData
	var oldestID string

	for id, session := range m.sessions {
		if oldest == nil || session.AccessedAt.Before(oldest.AccessedAt) {
			oldest = session
			oldestID = id
		}
	}

	if oldestID != "" {
		delete(m.sessions, oldestID)
	}
}
