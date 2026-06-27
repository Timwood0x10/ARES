package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	memory "github.com/Timwood0x10/ares/internal/ares_memory"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/ares_events"
)

// mockMemoryManager is a mock implementation of memory.MemoryManager for testing.
type mockMemoryManager struct {
	sessions   map[string]bool
	messages   map[string][]memory.Message
	createErr  error
	deleteErr  error
	getErr     error
	distillErr error
	searchErr  error
}

func newMockMemoryManager() *mockMemoryManager {
	return &mockMemoryManager{
		sessions: make(map[string]bool),
		messages: make(map[string][]memory.Message),
	}
}

func (m *mockMemoryManager) CreateSession(ctx context.Context, userID string) (string, error) {
	if m.createErr != nil {
		return "", m.createErr
	}

	sessionID := "session-" + userID
	m.sessions[sessionID] = true
	m.messages[sessionID] = []memory.Message{}

	return sessionID, nil
}

func (m *mockMemoryManager) DeleteSession(ctx context.Context, sessionID string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}

	delete(m.sessions, sessionID)
	delete(m.messages, sessionID)

	return nil
}

func (m *mockMemoryManager) AddMessage(ctx context.Context, sessionID, role, content string) error {
	if _, exists := m.messages[sessionID]; !exists {
		m.messages[sessionID] = []memory.Message{}
	}

	m.messages[sessionID] = append(m.messages[sessionID], memory.Message{
		Role:    role,
		Content: content,
		Time:    time.Now(),
	})

	return nil
}

func (m *mockMemoryManager) GetMessages(ctx context.Context, sessionID string) ([]memory.Message, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}

	msgs, exists := m.messages[sessionID]
	if !exists {
		return []memory.Message{}, nil
	}

	return msgs, nil
}

func (m *mockMemoryManager) BuildContext(ctx context.Context, input string, sessionID string) (string, error) {
	return input, nil
}

func (m *mockMemoryManager) CreateTask(ctx context.Context, sessionID, userID, input string) (string, error) {
	return "task-123", nil
}

func (m *mockMemoryManager) UpdateTaskOutput(ctx context.Context, taskID, output string) error {
	return nil
}

func (m *mockMemoryManager) DistillTask(ctx context.Context, taskID string) (*models.Task, error) {
	if m.distillErr != nil {
		return nil, m.distillErr
	}

	return &models.Task{
		TaskID: taskID,
		Payload: map[string]any{
			"input":   "test input",
			"output":  "test output",
			"context": "test context",
		},
	}, nil
}

func (m *mockMemoryManager) StoreDistilledTask(ctx context.Context, taskID string, distilled *models.Task) error {
	return nil
}

func (m *mockMemoryManager) SearchSimilarTasks(ctx context.Context, query string, limit int) ([]*models.Task, error) {
	if m.searchErr != nil {
		return nil, m.searchErr
	}

	return []*models.Task{
		{
			TaskID: "task-1",
			Payload: map[string]any{
				"input":   "similar input",
				"output":  "similar output",
				"context": "similar context",
			},
		},
	}, nil
}

func (m *mockMemoryManager) GetLatestSessionForLeader(_ context.Context, _ string) (string, error) {
	return "", nil
}

func (m *mockMemoryManager) Start(ctx context.Context) error {
	return nil
}

func (m *mockMemoryManager) Stop(ctx context.Context) error {
	return nil
}

func (m *mockMemoryManager) SetEventStore(store ares_events.EventStore, streamID string) {}

func (m *mockMemoryManager) AddStructuredMessage(ctx context.Context, sessionID string, msg memory.Message) error {
	return nil
}

func (m *mockMemoryManager) BuildPromptMessages(ctx context.Context, sessionID string) ([]memory.Message, error) {
	return nil, nil
}

// TestNewService tests the NewService constructor.
func TestNewService(t *testing.T) {
	tests := []struct {
		name      string
		memoryMgr memory.MemoryManager
	}{
		{
			name:      "service with memory manager",
			memoryMgr: newMockMemoryManager(),
		},
		{
			name:      "service with nil memory manager",
			memoryMgr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(tt.memoryMgr)

			require.NotNil(t, svc)
			if svc.memoryMgr != tt.memoryMgr {
				t.Errorf("NewService().memoryMgr = %v, want %v", svc.memoryMgr, tt.memoryMgr)
			}
		})
	}
}

// TestCreateSession tests the CreateSession method.
func TestCreateSession(t *testing.T) {
	ctx := context.Background()
	mockMgr := newMockMemoryManager()
	svc := NewService(mockMgr)

	tests := []struct {
		name      string
		userID    string
		createErr error
		wantErr   error
	}{
		{
			name:      "successful creation",
			userID:    "user-123",
			createErr: nil,
			wantErr:   nil,
		},
		{
			name:      "empty user ID",
			userID:    "",
			createErr: nil,
			wantErr:   ErrInvalidUserID,
		},
		{
			name:      "session creation failure",
			userID:    "user-456",
			createErr: errors.New("session creation failed"),
			wantErr:   errors.New("create session: session creation failed"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockMgr.createErr = tt.createErr

			sessionID, err := svc.CreateSession(ctx, tt.userID)

			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("CreateSession() expected error %v, got nil", tt.wantErr)
				}
				if sessionID != "" {
					t.Error("CreateSession() should return empty session ID on error")
				}
			} else {
				if err != nil {
					t.Errorf("CreateSession() unexpected error: %v", err)
				}
				if sessionID == "" {
					t.Error("CreateSession() should return session ID on success")
				}
			}
		})
	}
}

// TestAddMessage tests the AddMessage method.
func TestAddMessage(t *testing.T) {
	ctx := context.Background()
	mockMgr := newMockMemoryManager()
	svc := NewService(mockMgr)

	// Create a test session
	sessionID, _ := svc.CreateSession(ctx, "user-123")

	tests := []struct {
		name      string
		sessionID string
		role      string
		content   string
		wantErr   error
	}{
		{
			name:      "successful add",
			sessionID: sessionID,
			role:      "user",
			content:   "Hello",
			wantErr:   nil,
		},
		{
			name:      "empty session ID",
			sessionID: "",
			role:      "user",
			content:   "Hello",
			wantErr:   ErrInvalidSessionID,
		},
		{
			name:      "empty role",
			sessionID: sessionID,
			role:      "",
			content:   "Hello",
			wantErr:   ErrInvalidRole,
		},
		{
			name:      "empty content",
			sessionID: sessionID,
			role:      "user",
			content:   "",
			wantErr:   ErrInvalidContent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := svc.AddMessage(ctx, tt.sessionID, tt.role, tt.content)

			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Errorf("AddMessage() error = %v, want %v", err, tt.wantErr)
				}
			} else {
				if err != nil {
					t.Errorf("AddMessage() unexpected error: %v", err)
				}
			}
		})
	}
}

// TestGetMessages tests the GetMessages method.
func TestGetMessages(t *testing.T) {
	ctx := context.Background()
	mockMgr := newMockMemoryManager()
	svc := NewService(mockMgr)

	// Create a test session and add messages
	sessionID, _ := svc.CreateSession(ctx, "user-123")
	_ = svc.AddMessage(ctx, sessionID, "user", "Hello")
	_ = svc.AddMessage(ctx, sessionID, "assistant", "Hi there!")

	tests := []struct {
		name      string
		sessionID string
		getErr    error
		wantErr   error
		wantCount int
	}{
		{
			name:      "successful get",
			sessionID: sessionID,
			getErr:    nil,
			wantErr:   nil,
			wantCount: 2,
		},
		{
			name:      "empty session ID",
			sessionID: "",
			getErr:    nil,
			wantErr:   ErrInvalidSessionID,
			wantCount: 0,
		},
		{
			name:      "non-existent session",
			sessionID: "non-existent",
			getErr:    nil,
			wantErr:   nil,
			wantCount: 0,
		},
		{
			name:      "get messages failure",
			sessionID: sessionID,
			getErr:    errors.New("get messages failed"),
			wantErr:   errors.New("get messages: get messages failed"),
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockMgr.getErr = tt.getErr

			messages, err := svc.GetMessages(ctx, tt.sessionID)

			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("GetMessages() expected error %v, got nil", tt.wantErr)
				}
				if messages != nil {
					t.Error("GetMessages() should return nil messages on error")
				}
			} else {
				if err != nil {
					t.Errorf("GetMessages() unexpected error: %v", err)
				}
				if messages == nil {
					t.Error("GetMessages() should return messages on success")
				} else if len(messages) != tt.wantCount {
					t.Errorf("GetMessages() returned %d messages, want %d", len(messages), tt.wantCount)
				}
			}
		})
	}
}

// TestDeleteSession tests the DeleteSession method.
func TestDeleteSession(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name      string
		setup     func(*Service) (string, error)
		sessionID string
		deleteErr error
		wantErr   error
	}{
		{
			name: "successful deletion",
			setup: func(svc *Service) (string, error) {
				return svc.CreateSession(ctx, "user-123")
			},
			deleteErr: nil,
			wantErr:   nil,
		},
		{
			name: "delete with empty session ID",
			setup: func(svc *Service) (string, error) {
				return "", nil
			},
			sessionID: "",
			deleteErr: nil,
			wantErr:   ErrInvalidSessionID,
		},
		{
			name: "delete with memory manager failure",
			setup: func(svc *Service) (string, error) {
				return svc.CreateSession(ctx, "user-456")
			},
			deleteErr: errors.New("delete failed"),
			wantErr:   errors.New("delete session: delete failed"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockMgr := newMockMemoryManager()
			mockMgr.deleteErr = tt.deleteErr
			svc := NewService(mockMgr)

			sessionID, err := tt.setup(svc)
			if err != nil {
				t.Fatalf("Setup failed: %v", err)
			}

			if tt.sessionID != "" {
				sessionID = tt.sessionID
			}

			err = svc.DeleteSession(ctx, sessionID)

			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("DeleteSession() expected error %v, got nil", tt.wantErr)
				}
			} else {
				if err != nil {
					t.Errorf("DeleteSession() unexpected error: %v", err)
				}
			}
		})
	}
}

// TestDeleteSessionNilMemoryManager tests deletion with nil memory manager.
func TestDeleteSessionNilMemoryManager(t *testing.T) {
	ctx := context.Background()
	svc := NewService(nil)

	err := svc.DeleteSession(ctx, "session-123")
	if err == nil {
		t.Error("DeleteSession() with nil memory manager should return error")
	}
}

// TestDistillTask tests the DistillTask method.
func TestDistillTask(t *testing.T) {
	ctx := context.Background()
	mockMgr := newMockMemoryManager()
	svc := NewService(mockMgr)

	tests := []struct {
		name       string
		taskID     string
		distillErr error
		wantErr    error
	}{
		{
			name:       "successful distillation",
			taskID:     "task-123",
			distillErr: nil,
			wantErr:    nil,
		},
		{
			name:       "empty task ID",
			taskID:     "",
			distillErr: nil,
			wantErr:    ErrInvalidTaskID,
		},
		{
			name:       "distillation failure",
			taskID:     "task-456",
			distillErr: errors.New("distillation failed"),
			wantErr:    errors.New("distill task: distillation failed"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockMgr.distillErr = tt.distillErr

			err := svc.DistillTask(ctx, tt.taskID)

			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("DistillTask() expected error %v, got nil", tt.wantErr)
				}
			} else {
				if err != nil {
					t.Errorf("DistillTask() unexpected error: %v", err)
				}
			}
		})
	}
}

// TestSearchSimilarTasks tests the SearchSimilarTasks method.
func TestSearchSimilarTasks(t *testing.T) {
	ctx := context.Background()
	mockMgr := newMockMemoryManager()
	svc := NewService(mockMgr)

	tests := []struct {
		name      string
		query     string
		limit     int
		searchErr error
		wantErr   error
		wantCount int
	}{
		{
			name:      "successful search",
			query:     "test query",
			limit:     10,
			searchErr: nil,
			wantErr:   nil,
			wantCount: 1,
		},
		{
			name:      "empty query",
			query:     "",
			limit:     10,
			searchErr: nil,
			wantErr:   ErrInvalidQuery,
			wantCount: 0,
		},
		{
			name:      "invalid limit",
			query:     "test query",
			limit:     0,
			searchErr: nil,
			wantErr:   ErrInvalidLimit,
			wantCount: 0,
		},
		{
			name:      "negative limit",
			query:     "test query",
			limit:     -1,
			searchErr: nil,
			wantErr:   ErrInvalidLimit,
			wantCount: 0,
		},
		{
			name:      "search failure",
			query:     "test query",
			limit:     10,
			searchErr: errors.New("search failed"),
			wantErr:   errors.New("search similar tasks: search failed"),
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockMgr.searchErr = tt.searchErr

			tasks, err := svc.SearchSimilarTasks(ctx, tt.query, tt.limit)

			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("SearchSimilarTasks() expected error %v, got nil", tt.wantErr)
				}
				if tasks != nil {
					t.Error("SearchSimilarTasks() should return nil tasks on error")
				}
			} else {
				if err != nil {
					t.Errorf("SearchSimilarTasks() unexpected error: %v", err)
				}
				if tasks == nil {
					t.Error("SearchSimilarTasks() should return tasks on success")
				} else if len(tasks) != tt.wantCount {
					t.Errorf("SearchSimilarTasks() returned %d tasks, want %d", len(tasks), tt.wantCount)
				}
			}
		})
	}
}

// TestGetPayloadString tests the getPayloadString helper function.
func TestGetPayloadString(t *testing.T) {
	tests := []struct {
		name    string
		payload map[string]any
		key     string
		want    string
	}{
		{
			name: "existing string value",
			payload: map[string]any{
				"key": "value",
			},
			key:  "key",
			want: "value",
		},
		{
			name: "non-existent key",
			payload: map[string]any{
				"other": "value",
			},
			key:  "key",
			want: "",
		},
		{
			name: "non-string value",
			payload: map[string]any{
				"key": 123,
			},
			key:  "key",
			want: "",
		},
		{
			name:    "nil payload",
			payload: nil,
			key:     "key",
			want:    "",
		},
		{
			name:    "empty payload",
			payload: map[string]any{},
			key:     "key",
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getPayloadString(tt.payload, tt.key)
			if result != tt.want {
				t.Errorf("getPayloadString() = %q, want %q", result, tt.want)
			}
		})
	}
}

// TestMessage tests Message struct.
func TestMessage(t *testing.T) {
	tests := []struct {
		name string
		msg  Message
	}{
		{
			name: "full message",
			msg: Message{
				Role:    "user",
				Content: "Hello",
				Time:    "2024-01-01 12:00:00",
			},
		},
		{
			name: "minimal message",
			msg: Message{
				Role:    "assistant",
				Content: "Hi",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = tt.msg.Role
			_ = tt.msg.Content
			_ = tt.msg.Time
		})
	}
}

// TestTask tests Task struct.
func TestTask(t *testing.T) {
	tests := []struct {
		name string
		task Task
	}{
		{
			name: "full task",
			task: Task{
				TaskID:  "task-123",
				Input:   "input",
				Output:  "output",
				Context: "context",
			},
		},
		{
			name: "minimal task",
			task: Task{
				TaskID: "task-456",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = tt.task.TaskID
			_ = tt.task.Input
			_ = tt.task.Output
			_ = tt.task.Context
		})
	}
}
