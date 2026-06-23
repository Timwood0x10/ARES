// Package memory provides unified memory management for the StyleAgent framework.
// It coordinates session memory, task memory, and distilled task storage through a single interface.
package memory

import (
	"context"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/api/core"
	memctx "github.com/Timwood0x10/ares/internal/ares_memory/context"
	truncpkg "github.com/Timwood0x10/ares/internal/ares_memory/internal/truncate"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/events"
)

// MemoryManager provides unified memory management.
// It coordinates session memory, task memory, and distilled task storage.
type MemoryManager interface {
	// CreateSession creates a new session and returns the session ID.
	CreateSession(ctx context.Context, userID string) (string, error)

	// AddMessage adds a message to the session.
	AddMessage(ctx context.Context, sessionID, role, content string) error

	// GetMessages retrieves all messages from the session.
	GetMessages(ctx context.Context, sessionID string) ([]Message, error)

	// AddStructuredMessage adds a structured message with metadata (TurnID, ToolCallID, ToolCalls)
	// to the session. This preserves the full message structure for turn-aware cleaning.
	AddStructuredMessage(ctx context.Context, sessionID string, msg Message) error

	// BuildPromptMessages returns all messages as a structured slice suitable for LLM prompt
	// construction. Unlike BuildContext, this returns typed Message structs instead of a flat string.
	BuildPromptMessages(ctx context.Context, sessionID string) ([]Message, error)

	// DeleteSession deletes a session and all its messages immediately.
	// This is different from TTL-based cleanup, which waits for expiration.
	DeleteSession(ctx context.Context, sessionID string) error

	// BuildContext builds input with conversation history context.
	BuildContext(ctx context.Context, input string, sessionID string) (string, error)

	// CreateTask creates a new task and returns the task ID.
	CreateTask(ctx context.Context, sessionID, userID, input string) (string, error)

	// UpdateTaskOutput updates the task output.
	UpdateTaskOutput(ctx context.Context, taskID, output string) error

	// DistillTask extracts key information from task for future reference.
	DistillTask(ctx context.Context, taskID string) (*models.Task, error)

	// StoreDistilledTask stores a distilled task with local vector embedding.
	// The vector is generated locally using simple hash-based algorithms.
	StoreDistilledTask(ctx context.Context, taskID string, distilled *models.Task) error

	// SearchSimilarTasks searches for similar tasks using local cosine similarity.
	SearchSimilarTasks(ctx context.Context, query string, limit int) ([]*models.Task, error)

	// GetLatestSessionForLeader retrieves the most recent session ID for a leader from checkpoint.
	// Returns ("", nil) if no checkpoint exists.
	GetLatestSessionForLeader(ctx context.Context, leaderID string) (string, error)

	// Start starts the memory manager and background workers.
	Start(ctx context.Context) error

	// Stop stops the memory manager and cleans up resources.
	Stop(ctx context.Context) error

	// SetEventStore configures an optional EventStore for emitting lifecycle events.
	// If store is nil, event emission is a no-op.
	SetEventStore(store events.EventStore, streamID string)
}

// MemoryConfig holds configuration for MemoryManager.
type MemoryConfig struct {
	// Enabled enables memory features.
	Enabled bool

	// Storage type: "memory" or "postgres".
	Storage string

	// MaxHistory is the maximum number of turns to keep in context.
	MaxHistory int

	// MaxSessions is the maximum number of sessions to store.
	MaxSessions int

	// MaxTasks is the maximum number of tasks to store.
	MaxTasks int

	// MaxDistilledTasks is the maximum number of distilled tasks to store.
	// Implements LRU eviction when limit is reached.
	MaxDistilledTasks int

	// SessionTTL is the time-to-live for sessions.
	SessionTTL time.Duration

	// TaskTTL is time-to-live for tasks.
	TaskTTL time.Duration

	// DistilledTaskTTL is time-to-live for distilled tasks.
	DistilledTaskTTL time.Duration

	// VectorDim is the dimension of the vector (for local embedding).
	VectorDim int

	// EnablePostgres enables PostgreSQL storage.
	EnablePostgres bool

	// PostgresDSN is the PostgreSQL connection string.
	PostgresDSN string

	// UseStructuredCleaning enables the new structured prompt builder (BuildPromptMessages)
	// instead of the legacy text-based BuildContext. When true, callers should use
	// BuildPromptMessages for LLM input construction. Default: false (legacy mode).
	UseStructuredCleaning bool

	// CleanOptions configures context cleaning behavior. When nil, defaults are used.
	CleanOptions *core.CleanOptions
}

// Message, ToolCall, ToolCallFunction are type aliases for the canonical
// types defined in the memctx (internal/memory/context) package.
type (
	Message          = memctx.Message
	ToolCall         = memctx.ToolCall
	ToolCallFunction = memctx.ToolCallFunction
)

// Role constants re-exported for convenience.
const (
	RoleUser       = memctx.RoleUser
	RoleAssistant  = memctx.RoleAssistant
	RoleSystem     = memctx.RoleSystem
	RoleToolCall   = memctx.RoleToolCall
	RoleToolResult = memctx.RoleToolResult
)

// ToCoreMessage converts a memory Message to an api/core Message.
func ToCoreMessage(sessionID string, msg Message) *core.Message {
	return &core.Message{
		SessionID: sessionID,
		Role:      core.MessageRole(msg.Role),
		Content:   msg.Content,
		Time:      msg.Time,
		Metadata: core.Metadata{
			"turn_id":      msg.TurnID,
			"tool_call_id": msg.ToolCallID,
			"tool_calls":   msg.ToolCalls,
		},
	}
}

// ToLLMMessage converts a memory Message to an api/core LLMMessage.
func ToLLMMessage(msg Message) *core.LLMMessage {
	tcs := make([]core.ToolCall, len(msg.ToolCalls))
	for i, tc := range msg.ToolCalls {
		tcs[i] = core.ToolCall{
			ID:   tc.ID,
			Type: tc.Type,
			Function: core.FunctionCall{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		}
	}
	return &core.LLMMessage{
		Role:       msg.Role,
		Content:    msg.Content,
		ToolCalls:  tcs,
		ToolCallID: msg.ToolCallID,
	}
}

// FromCoreMessage converts an api/core Message to a memory Message.
func FromCoreMessage(sessionID string, msg *core.Message) Message {
	if msg == nil {
		return Message{}
	}
	m := Message{
		Role:    string(msg.Role),
		Content: msg.Content,
		Time:    msg.Time,
	}
	if msg.Metadata != nil {
		if tid, ok := msg.Metadata["turn_id"].(string); ok {
			m.TurnID = tid
		}
		if tcid, ok := msg.Metadata["tool_call_id"].(string); ok {
			m.ToolCallID = tcid
		}
		if tcs, ok := msg.Metadata["tool_calls"]; ok {
			switch v := tcs.(type) {
			case []ToolCall:
				m.ToolCalls = v
			case []interface{}:
				m.ToolCalls = convertRawToToolCalls(v)
			}
		}
	}
	_ = sessionID
	return m
}

// FromLLMMessage converts an api/core LLMMessage to a memory Message.
func FromLLMMessage(msg *core.LLMMessage) Message {
	if msg == nil {
		return Message{}
	}
	tcs := make([]ToolCall, len(msg.ToolCalls))
	for i, tc := range msg.ToolCalls {
		tcs[i] = ToolCall{
			ID:   tc.ID,
			Type: tc.Type,
			Function: ToolCallFunction{
				Name:      tc.Function.Name,
				Arguments: tc.Function.Arguments,
			},
		}
	}
	return Message{
		Role:       msg.Role,
		Content:    msg.Content,
		ToolCalls:  tcs,
		ToolCallID: msg.ToolCallID,
	}
}

// convertRawToToolCalls converts a raw []interface{} from JSON metadata into typed ToolCalls.
func convertRawToToolCalls(raw []interface{}) []ToolCall {
	calls := make([]ToolCall, 0, len(raw))
	for _, r := range raw {
		m, ok := r.(map[string]interface{})
		if !ok {
			continue
		}
		var tc ToolCall
		if id, ok := m["id"].(string); ok {
			tc.ID = id
		}
		if typ, ok := m["type"].(string); ok {
			tc.Type = typ
		}
		if fn, ok := m["function"].(map[string]interface{}); ok {
			tcf := ToolCallFunction{}
			if name, ok := fn["name"].(string); ok {
				tcf.Name = name
			}
			if args, ok := fn["arguments"].(string); ok {
				tcf.Arguments = args
			}
			tc.Function = tcf
		}
		calls = append(calls, tc)
	}
	return calls
}

// ToBuildContextFormat converts a slice of cleaned Messages into a flat string
// suitable for legacy BuildContext output. This allows BuildContext to delegate
// to BuildPromptMessages and then render the result as text.
func ToBuildContextFormat(messages []Message) string {
	if len(messages) == 0 {
		return ""
	}
	var b string
	b = "Previous conversation history:\n\n"
	for _, msg := range messages {
		label := msg.Role
		switch label {
		case memctx.RoleToolCall:
			label = "Tool call"
		case memctx.RoleToolResult:
			label = "Tool result"
		case memctx.RoleUser:
			label = "User"
		case memctx.RoleAssistant:
			label = "Assistant"
		case memctx.RoleSystem:
			label = "System"
		}
		b += fmt.Sprintf("%s: %s\n", label, truncpkg.WithEllipsis(msg.Content, 100))
	}
	return b
}

// DefaultMemoryConfig returns default configuration for MemoryManager.
func DefaultMemoryConfig() *MemoryConfig {
	opts := core.DefaultCleanOptions()
	return &MemoryConfig{
		Enabled:               true,
		Storage:               "memory",
		MaxHistory:            10,
		MaxSessions:           100,
		MaxTasks:              1000,
		MaxDistilledTasks:     5000,
		SessionTTL:            24 * time.Hour,
		TaskTTL:               7 * 24 * time.Hour,
		VectorDim:             128,
		EnablePostgres:        false,
		UseStructuredCleaning: false,
		CleanOptions:          &opts,
	}
}
