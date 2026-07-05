package core

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestMessageRole tests MessageRole constants.
func TestMessageRole(t *testing.T) {
	tests := []struct {
		name string
		role MessageRole
		want string
	}{
		{
			name: "system role",
			role: MessageRoleSystem,
			want: "system",
		},
		{
			name: "user role",
			role: MessageRoleUser,
			want: "user",
		},
		{
			name: "assistant role",
			role: MessageRoleAssistant,
			want: "assistant",
		},
		{
			name: "tool role",
			role: MessageRoleTool,
			want: "tool",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.role) != tt.want {
				t.Errorf("MessageRole = %q, want %q", tt.role, tt.want)
			}
		})
	}
}

// TestMessageRoleUniqueness tests that all MessageRole values are unique.
func TestMessageRoleUniqueness(t *testing.T) {
	roles := map[string]bool{
		string(MessageRoleSystem):    true,
		string(MessageRoleUser):      true,
		string(MessageRoleAssistant): true,
		string(MessageRoleTool):      true,
	}

	if len(roles) != 4 {
		t.Errorf("expected 4 unique message roles, got %d", len(roles))
	}
}

// TestMessage tests Message struct.
func TestMessage(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name string
		msg  Message
	}{
		{
			name: "full message",
			msg: Message{
				ID:        "msg-123",
				SessionID: "session-456",
				Role:      MessageRoleUser,
				Content:   "Hello, world!",
				Time:      now,
				Metadata:  Metadata{"key": "value"},
			},
		},
		{
			name: "minimal message",
			msg: Message{
				ID:        "msg-789",
				SessionID: "session-999",
				Role:      MessageRoleAssistant,
				Content:   "Response",
			},
		},
		{
			name: "message with nil metadata",
			msg: Message{
				ID:        "msg-888",
				SessionID: "session-777",
				Role:      MessageRoleSystem,
				Content:   "System prompt",
				Metadata:  nil,
			},
		},
		{
			name: "message with empty metadata",
			msg: Message{
				ID:        "msg-666",
				SessionID: "session-555",
				Role:      MessageRoleTool,
				Content:   "Tool result",
				Metadata:  make(Metadata),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = tt.msg.ID
			_ = tt.msg.SessionID
			_ = tt.msg.Role
			_ = tt.msg.Content
			_ = tt.msg.Time
			_ = tt.msg.Metadata
		})
	}
}

// TestSession tests Session struct.
func TestSession(t *testing.T) {
	now := time.Now()
	expiresAt := now.Add(24 * time.Hour)

	tests := []struct {
		name string
		sess Session
	}{
		{
			name: "full session",
			sess: Session{
				ID:        "session-123",
				UserID:    "user-456",
				TenantID:  "tenant-789",
				Status:    "active",
				CreatedAt: now,
				UpdatedAt: now,
				ExpiresAt: &expiresAt,
				Metadata:  Metadata{"key": "value"},
			},
		},
		{
			name: "minimal session",
			sess: Session{
				ID:        "session-999",
				UserID:    "user-888",
				TenantID:  "tenant-777",
				Status:    "active",
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
		{
			name: "session with nil expires at",
			sess: Session{
				ID:        "session-666",
				UserID:    "user-555",
				TenantID:  "tenant-444",
				Status:    "active",
				CreatedAt: now,
				UpdatedAt: now,
				ExpiresAt: nil,
				Metadata:  make(Metadata),
			},
		},
		{
			name: "session with nil metadata",
			sess: Session{
				ID:        "session-333",
				UserID:    "user-222",
				TenantID:  "tenant-111",
				Status:    "active",
				CreatedAt: now,
				UpdatedAt: now,
				Metadata:  nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = tt.sess.ID
			_ = tt.sess.UserID
			_ = tt.sess.TenantID
			_ = tt.sess.Status
			_ = tt.sess.CreatedAt
			_ = tt.sess.UpdatedAt
			_ = tt.sess.ExpiresAt
			_ = tt.sess.Metadata
		})
	}
}

// TestSessionConfig tests SessionConfig struct.
func TestSessionConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  SessionConfig
	}{
		{
			name: "full config",
			cfg: SessionConfig{
				UserID:    "user-123",
				TenantID:  "tenant-456",
				ExpiresIn: 24 * time.Hour,
				Metadata:  Metadata{"key": "value"},
			},
		},
		{
			name: "minimal config",
			cfg: SessionConfig{
				UserID:   "user-789",
				TenantID: "tenant-999",
			},
		},
		{
			name: "config with zero expiration",
			cfg: SessionConfig{
				UserID:    "user-888",
				TenantID:  "tenant-777",
				ExpiresIn: 0,
			},
		},
		{
			name: "config with nil metadata",
			cfg: SessionConfig{
				UserID:    "user-666",
				TenantID:  "tenant-555",
				ExpiresIn: time.Hour,
				Metadata:  nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = tt.cfg.UserID
			_ = tt.cfg.TenantID
			_ = tt.cfg.ExpiresIn
			_ = tt.cfg.Metadata
		})
	}
}

// TestDistilledTask tests DistilledTask struct.
func TestDistilledTask(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name string
		task DistilledTask
	}{
		{
			name: "full task",
			task: DistilledTask{
				TaskID:    "task-123",
				Input:     "Input text",
				Output:    "Output text",
				Context:   "Context information",
				Summary:   "Task summary",
				Tags:      []string{"tag1", "tag2"},
				Embedding: []float32{0.1, 0.2, 0.3},
				CreatedAt: now,
			},
		},
		{
			name: "minimal task",
			task: DistilledTask{
				TaskID:    "task-789",
				Input:     "Input",
				Output:    "Output",
				CreatedAt: now,
			},
		},
		{
			name: "task with nil tags",
			task: DistilledTask{
				TaskID:    "task-888",
				Input:     "Input",
				Output:    "Output",
				Tags:      nil,
				Embedding: nil,
				CreatedAt: now,
			},
		},
		{
			name: "task with empty tags",
			task: DistilledTask{
				TaskID:    "task-666",
				Input:     "Input",
				Output:    "Output",
				Tags:      []string{},
				Embedding: []float32{},
				CreatedAt: now,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = tt.task.TaskID
			_ = tt.task.Input
			_ = tt.task.Output
			_ = tt.task.Context
			_ = tt.task.Summary
			_ = tt.task.Tags
			_ = tt.task.Embedding
			_ = tt.task.CreatedAt
		})
	}
}

// TestSearchQuery tests SearchQuery struct.
func TestSearchQuery(t *testing.T) {
	tests := []struct {
		name  string
		query SearchQuery
	}{
		{
			name: "full query",
			query: SearchQuery{
				Query:    "search text",
				Limit:    10,
				MinScore: 0.5,
				Tags:     []string{"tag1", "tag2"},
			},
		},
		{
			name: "minimal query",
			query: SearchQuery{
				Query: "test",
			},
		},
		{
			name: "query with zero limit",
			query: SearchQuery{
				Query:    "test",
				Limit:    0,
				MinScore: 0.0,
			},
		},
		{
			name: "query with nil tags",
			query: SearchQuery{
				Query: "test",
				Tags:  nil,
			},
		},
		{
			name: "query with empty tags",
			query: SearchQuery{
				Query: "test",
				Tags:  []string{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = tt.query.Query
			_ = tt.query.Limit
			_ = tt.query.MinScore
			_ = tt.query.Tags
		})
	}
}

// TestSearchResult tests SearchResult struct.
func TestSearchResult(t *testing.T) {
	tests := []struct {
		name   string
		result SearchResult
	}{
		{
			name: "full result",
			result: SearchResult{
				TaskID:  "task-123",
				Input:   "Input text",
				Output:  "Output text",
				Context: "Context",
				Summary: "Summary",
				Score:   0.95,
				Tags:    []string{"tag1", "tag2"},
			},
		},
		{
			name: "minimal result",
			result: SearchResult{
				TaskID: "task-789",
				Input:  "Input",
				Output: "Output",
				Score:  0.8,
			},
		},
		{
			name: "result with nil tags",
			result: SearchResult{
				TaskID: "task-888",
				Input:  "Input",
				Output: "Output",
				Score:  0.7,
				Tags:   nil,
			},
		},
		{
			name: "result with empty tags",
			result: SearchResult{
				TaskID: "task-666",
				Input:  "Input",
				Output: "Output",
				Score:  0.6,
				Tags:   []string{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = tt.result.TaskID
			_ = tt.result.Input
			_ = tt.result.Output
			_ = tt.result.Context
			_ = tt.result.Summary
			_ = tt.result.Score
			_ = tt.result.Tags
		})
	}
}

// TestMemoryRepository tests that MemoryRepository interface is properly defined.
func TestMemoryRepository(t *testing.T) {
	var _ MemoryRepository = (*mockMemoryRepository)(nil)
}

// mockMemoryRepository is a mock implementation of MemoryRepository for testing.
type mockMemoryRepository struct{}

func (m *mockMemoryRepository) CreateSession(ctx context.Context, session *Session) error {
	return nil
}

func (m *mockMemoryRepository) GetSession(ctx context.Context, sessionID string) (*Session, error) {
	return nil, errors.New("not found")
}

func (m *mockMemoryRepository) UpdateSession(ctx context.Context, session *Session) error {
	return nil
}

func (m *mockMemoryRepository) DeleteSession(ctx context.Context, sessionID string) error {
	return nil
}

func (m *mockMemoryRepository) AddMessage(ctx context.Context, message *Message) error {
	return nil
}

func (m *mockMemoryRepository) GetMessages(ctx context.Context, sessionID string, pagination *PaginationRequest) ([]*Message, error) {
	return nil, errors.New("not found")
}

func (m *mockMemoryRepository) StoreDistilledTask(ctx context.Context, task *DistilledTask) error {
	return nil
}

func (m *mockMemoryRepository) GetDistilledTask(ctx context.Context, taskID string) (*DistilledTask, error) {
	return nil, errors.New("not found")
}

func (m *mockMemoryRepository) SearchSimilarTasks(ctx context.Context, query *SearchQuery) ([]*SearchResult, error) {
	return nil, errors.New("not found")
}

// TestMemoryService tests that MemoryService interface is properly defined.
func TestMemoryService(t *testing.T) {
	var _ MemoryService = (*mockMemoryService)(nil)
}

// mockMemoryService is a mock implementation of MemoryService for testing.
type mockMemoryService struct{}

func (m *mockMemoryService) CreateSession(ctx context.Context, config *SessionConfig) (string, error) {
	return "", nil
}

func (m *mockMemoryService) GetSession(ctx context.Context, sessionID string) (*Session, error) {
	return nil, errors.New("not found")
}

func (m *mockMemoryService) DeleteSession(ctx context.Context, sessionID string) error {
	return nil
}

func (m *mockMemoryService) AddMessage(ctx context.Context, sessionID string, role MessageRole, content string) error {
	return nil
}

func (m *mockMemoryService) GetMessages(ctx context.Context, sessionID string, pagination *PaginationRequest) ([]*Message, error) {
	return nil, errors.New("not found")
}

func (m *mockMemoryService) UpdateSession(ctx context.Context, session *Session) error {
	return nil
}

func (m *mockMemoryService) DistillTask(ctx context.Context, taskID string) (*DistilledTask, error) {
	return nil, errors.New("not found")
}

func (m *mockMemoryService) SearchSimilarTasks(ctx context.Context, query *SearchQuery) ([]*SearchResult, error) {
	return nil, errors.New("not found")
}
