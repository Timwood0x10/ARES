// Package distillation extracts structured knowledge from conversations.
package distillation

import (
	"context"

	"github.com/Timwood0x10/ares/internal/ares_memory/distillation"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/storage/postgres/embedding"
)

// MemoryType classifies distilled knowledge.
type MemoryType string

const (
	MemoryKnowledge   MemoryType = "knowledge"
	MemoryPreference  MemoryType = "preference"
	MemoryInteraction MemoryType = "interaction"
	MemoryProfile     MemoryType = "profile"
)

// Memory is a single distilled knowledge fragment.
type Memory struct {
	ID         string                 `json:"id"`
	Type       MemoryType             `json:"type"`
	Content    string                 `json:"content"`
	Importance float64                `json:"importance"`
	Source     string                 `json:"source"`
	Vector     []float64              `json:"vector,omitempty"`
	TTL        int64                  `json:"ttl,omitempty"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

// Config controls the distillation pipeline.
type Config struct {
	MinImportance             float64
	ConflictThreshold         float64
	MaxMemoriesPerDistillation int
	MaxSolutionsPerTenant     int
	EnableCodeFilter          bool
	EnableCrossTurnExtraction bool
	PrecisionOverRecall       bool
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		MinImportance:              0.6,
		ConflictThreshold:          0.85,
		MaxMemoriesPerDistillation: 3,
		MaxSolutionsPerTenant:      5000,
		EnableCodeFilter:           true,
		EnableCrossTurnExtraction:  true,
		PrecisionOverRecall:        true,
	}
}

// Distiller extracts structured memories from conversations.
type Distiller interface {
	DistillConversation(ctx context.Context, conversationID string, messages []Message, tenantID, userID string) ([]Memory, error)
	SubscribeAndDistill(ctx context.Context, eventStore ares_events.EventStore) error
}

// Message represents a conversation message for distillation input.
type Message struct {
	Role       string      `json:"role"`
	Content    string      `json:"content"`
	ToolCallID string      `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall  `json:"tool_calls,omitempty"`
	TurnID     string      `json:"turn_id,omitempty"`
}

// ToolCall represents a tool invocation in a message.
type ToolCall struct {
	ID       string            `json:"id"`
	Type     string            `json:"type"`
	Function ToolCallFunction  `json:"function"`
}

// ToolCallFunction holds tool function details.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ---------------------------------------------------------------------------
// Adapter
// ---------------------------------------------------------------------------

type distillerAdapter struct {
	inner *distillation.Distiller
}

func New(config *Config, embedder embedding.EmbeddingService, repo distillation.ExperienceRepository) Distiller {
	icfg := distillation.DefaultDistillationConfig()
	if config != nil {
		icfg.MinImportance = config.MinImportance
		icfg.ConflictThreshold = config.ConflictThreshold
		icfg.MaxMemoriesPerDistillation = config.MaxMemoriesPerDistillation
		icfg.MaxSolutionsPerTenant = config.MaxSolutionsPerTenant
		icfg.EnableCodeFilter = config.EnableCodeFilter
		icfg.EnableCrossTurnExtraction = config.EnableCrossTurnExtraction
		icfg.PrecisionOverRecall = config.PrecisionOverRecall
	}
	return &distillerAdapter{
	 inner: distillation.NewDistiller(icfg, embedder, repo),
	}
}

func (d *distillerAdapter) DistillConversation(ctx context.Context, conversationID string, messages []Message, tenantID, userID string) ([]Memory, error) {
	imsgs := make([]distillation.Message, len(messages))
	for i, m := range messages {
		imsgs[i] = distillation.Message{
			Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID,
			TurnID: m.TurnID,
		}
	}
	mems, err := d.inner.DistillConversation(ctx, conversationID, imsgs, tenantID, userID)
	if err != nil {
		return nil, err
	}
	out := make([]Memory, len(mems))
	for i, m := range mems {
		out[i] = Memory{
		 ID: m.ID, Type: MemoryType(m.Type), Content: m.Content,
		 Importance: m.Importance, Source: m.Source,
		 Vector: m.Vector, TTL: int64(m.TTL), Metadata: m.Metadata,
		}
	}
	return out, nil
}

func (d *distillerAdapter) SubscribeAndDistill(ctx context.Context, eventStore ares_events.EventStore) error {
	d.inner.SubscribeAndDistill(ctx, eventStore)
	return nil
}

var _ Distiller = (*distillerAdapter)(nil)
