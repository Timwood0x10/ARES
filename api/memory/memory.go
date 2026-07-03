// Package memory provides the public API for agent conversation memory management.
package memory

import (
	"context"
	"time"

	"github.com/Timwood0x10/ares/api/core"
	aresmem "github.com/Timwood0x10/ares/internal/ares_memory"
	memctx "github.com/Timwood0x10/ares/internal/ares_memory/context"
	"github.com/Timwood0x10/ares/internal/ares_memory/distillation"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
	"github.com/Timwood0x10/ares/internal/storage/postgres/embedding"
)

// Role constants.
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

// Config holds memory manager configuration.
type Config struct {
	Storage     string // "memory" | "postgres"
	MaxHistory  int    // 0 = unlimited
	MaxSessions int
	SessionTTL  time.Duration
	VectorDim   int
	MaxTasks    int
	TaskTTL     time.Duration
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		Storage:     "memory",
		MaxHistory:  10,
		MaxSessions: 100,
		SessionTTL:  24 * time.Hour,
		VectorDim:   128,
		MaxTasks:    500,
		TaskTTL:     7 * 24 * time.Hour,
	}
}

// Manager is the public contract for memory management.
type Manager interface {
	CreateSession(ctx context.Context, userID string) (string, error)
	AddMessage(ctx context.Context, sessionID, role, content string) error
	AddStructuredMessage(ctx context.Context, sessionID string, msg Message) error
	GetMessages(ctx context.Context, sessionID string) ([]Message, error)
	BuildPromptMessages(ctx context.Context, sessionID string) ([]Message, error)
	BuildContext(ctx context.Context, input, sessionID string) (string, error)
	DeleteSession(ctx context.Context, sessionID string) error
	CreateTask(ctx context.Context, sessionID, userID, input string) (string, error)
	UpdateTaskOutput(ctx context.Context, taskID, output string) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

// managerAdapter wraps aresmem.MemoryManager and converts types.
type managerAdapter struct {
	inner aresmem.MemoryManager
}

func (m *managerAdapter) CreateSession(ctx context.Context, userID string) (string, error) {
	return m.inner.CreateSession(ctx, userID)
}
func (m *managerAdapter) AddMessage(ctx context.Context, sessionID, role, content string) error {
	return m.inner.AddMessage(ctx, sessionID, role, content)
}
func (m *managerAdapter) AddStructuredMessage(ctx context.Context, sessionID string, msg Message) error {
	return m.inner.AddStructuredMessage(ctx, sessionID, toInternal(msg))
}
func (m *managerAdapter) GetMessages(ctx context.Context, sessionID string) ([]Message, error) {
	msgs, err := m.inner.GetMessages(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return fromInternalList(msgs), nil
}
func (m *managerAdapter) BuildPromptMessages(ctx context.Context, sessionID string) ([]Message, error) {
	msgs, err := m.inner.BuildPromptMessages(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return fromInternalList(msgs), nil
}
func (m *managerAdapter) BuildContext(ctx context.Context, input, sessionID string) (string, error) {
	return m.inner.BuildContext(ctx, input, sessionID)
}
func (m *managerAdapter) DeleteSession(ctx context.Context, sessionID string) error {
	return m.inner.DeleteSession(ctx, sessionID)
}
func (m *managerAdapter) CreateTask(ctx context.Context, sessionID, userID, input string) (string, error) {
	return m.inner.CreateTask(ctx, sessionID, userID, input)
}
func (m *managerAdapter) UpdateTaskOutput(ctx context.Context, taskID, output string) error {
	return m.inner.UpdateTaskOutput(ctx, taskID, output)
}
func (m *managerAdapter) Start(ctx context.Context) error {
	return m.inner.Start(ctx)
}
func (m *managerAdapter) Stop(ctx context.Context) error {
	return m.inner.Stop(ctx)
}

// toConfig converts public Config to internal MemoryConfig.
func toConfig(cfg *Config) *aresmem.MemoryConfig {
	if cfg == nil {
		return nil
	}
	return &aresmem.MemoryConfig{
		MaxSessions:      cfg.MaxSessions,
		SessionTTL:       cfg.SessionTTL,
		MaxHistory:       cfg.MaxHistory,
		VectorDim:        cfg.VectorDim,
		MaxTasks:         cfg.MaxTasks,
		TaskTTL:          cfg.TaskTTL,
		MaxDistilledTasks: 100,
		DistilledTaskTTL:   24 * 60 * 60,
	}
}

// NewManager creates an in-memory memory manager.
func NewManager(cfg *Config) (Manager, error) {
	inner, err := aresmem.NewMemoryManager(toConfig(cfg))
	if err != nil {
		return nil, err
	}
	return &managerAdapter{inner: inner}, nil
}

// NewProductionManager creates a PostgreSQL-backed memory manager.
func NewProductionManager(pool *postgres.Pool, client *embedding.EmbeddingClient, cfg *Config) (Manager, error) {
	inner, err := aresmem.NewProductionMemoryManager(pool, client, toConfig(cfg))
	if err != nil {
		return nil, err
	}
	return &managerAdapter{inner: inner}, nil
}

// NewManagerWithDistiller creates a memory manager with distillation.
func NewManagerWithDistiller(cfg *Config, embedder embedding.EmbeddingService, repo distillation.ExperienceRepository) (Manager, error) {
	inner, err := aresmem.NewMemoryManagerWithDistiller(toConfig(cfg), embedder, repo)
	if err != nil {
		return nil, err
	}
	return &managerAdapter{inner: inner}, nil
}

// ---------------------------------------------------------------------------
// Type conversion helpers
// ---------------------------------------------------------------------------

func toInternal(msg Message) memctx.Message {
	return memctx.Message{
		Role: msg.Role, Content: msg.Content, Time: msg.Time,
		TurnID: msg.TurnID, ToolCallID: msg.ToolCallID,
		ToolCalls: toInternalTCs(msg.ToolCalls),
		EventKind: msg.EventKind, ParentID: msg.ParentID,
		ArtifactRefs: msg.ArtifactRefs,
	}
}

func toInternalTCs(tcs []ToolCall) []memctx.ToolCall {
	out := make([]memctx.ToolCall, len(tcs))
	for i, t := range tcs {
		out[i] = memctx.ToolCall{ID: t.ID, Type: t.Type,
			Function: memctx.ToolCallFunction{Name: t.Function.Name, Arguments: t.Function.Arguments}}
	}
	return out
}

func fromInternal(msg memctx.Message) Message {
	return Message{
		Role: msg.Role, Content: msg.Content, Time: msg.Time,
		TurnID: msg.TurnID, ToolCallID: msg.ToolCallID,
		ToolCalls: fromInternalTCs(msg.ToolCalls),
		EventKind: msg.EventKind, ParentID: msg.ParentID,
		ArtifactRefs: msg.ArtifactRefs,
	}
}

func fromInternalList(msgs []memctx.Message) []Message {
	out := make([]Message, len(msgs))
	for i, m := range msgs {
		out[i] = fromInternal(m)
	}
	return out
}

func fromInternalTCs(tcs []memctx.ToolCall) []ToolCall {
	out := make([]ToolCall, len(tcs))
	for i, t := range tcs {
		out[i] = ToolCall{ID: t.ID, Type: t.Type,
			Function: ToolCallFunction{Name: t.Function.Name, Arguments: t.Function.Arguments}}
	}
	return out
}

// ToCoreMessage converts a Message to api/core.Message.
func ToCoreMessage(sessionID string, msg Message) *core.Message {
	return aresmem.ToCoreMessage(sessionID, toInternal(msg))
}

// FromCoreMessage converts api/core.Message to a Message.
func FromCoreMessage(sessionID string, msg *core.Message) Message {
	return fromInternal(aresmem.FromCoreMessage(sessionID, msg))
}

// ToLLMMessage converts a Message to LLM format.
func ToLLMMessage(msg Message) *core.LLMMessage {
	return aresmem.ToLLMMessage(toInternal(msg))
}

// FromLLMMessage converts LLM message back.
func FromLLMMessage(msg *core.LLMMessage) Message {
	return fromInternal(aresmem.FromLLMMessage(msg))
}
