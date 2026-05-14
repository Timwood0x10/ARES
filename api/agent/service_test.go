package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Timwood0x10/goagent/internal/core/models"
	"github.com/Timwood0x10/goagent/internal/memory"
)

// mockMemoryManager is a mock implementation of memory.MemoryManager for testing.
type mockMemoryManager struct {
	sessions   map[string]bool
	sessionsMu sync.RWMutex
	createErr  error
	deleteErr  error
}

func newMockMemoryManager() *mockMemoryManager {
	return &mockMemoryManager{
		sessions: make(map[string]bool),
	}
}

func (m *mockMemoryManager) CreateSession(ctx context.Context, agentID string) (string, error) {
	if m.createErr != nil {
		return "", m.createErr
	}

	sessionID := "session-" + agentID
	m.sessionsMu.Lock()
	m.sessions[sessionID] = true
	m.sessionsMu.Unlock()

	return sessionID, nil
}

func (m *mockMemoryManager) DeleteSession(ctx context.Context, sessionID string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}

	m.sessionsMu.Lock()
	delete(m.sessions, sessionID)
	m.sessionsMu.Unlock()

	return nil
}

func (m *mockMemoryManager) AddMessage(ctx context.Context, sessionID, role, content string) error {
	return nil
}

func (m *mockMemoryManager) GetMessages(ctx context.Context, sessionID string) ([]memory.Message, error) {
	return nil, nil
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
	return nil, nil
}

func (m *mockMemoryManager) StoreDistilledTask(ctx context.Context, taskID string, distilled *models.Task) error {
	return nil
}

func (m *mockMemoryManager) SearchSimilarTasks(ctx context.Context, query string, limit int) ([]*models.Task, error) {
	return nil, nil
}

func (m *mockMemoryManager) Start(ctx context.Context) error {
	return nil
}

func (m *mockMemoryManager) Stop(ctx context.Context) error {
	return nil
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

			if svc == nil {
				t.Fatal("NewService() should not return nil")
			}
			if svc.memoryMgr != tt.memoryMgr {
				t.Errorf("NewService().memoryMgr = %v, want %v", svc.memoryMgr, tt.memoryMgr)
			}
			if svc.agents == nil {
				t.Error("NewService().agents should be initialized, got nil")
			}
		})
	}
}

// TestCreateAgent tests the CreateAgent method.
func TestCreateAgent(t *testing.T) {
	ctx := context.Background()
	mockMgr := newMockMemoryManager()
	svc := NewService(mockMgr)

	tests := []struct {
		name       string
		agentID    string
		createErr  error
		wantErr    error
		wantStatus Status
	}{
		{
			name:       "successful creation",
			agentID:    "agent-123",
			createErr:  nil,
			wantErr:    nil,
			wantStatus: StatusReady,
		},
		{
			name:       "empty agent ID",
			agentID:    "",
			createErr:  nil,
			wantErr:    ErrInvalidAgentID,
			wantStatus: "",
		},

		{
			name:       "session creation failure",
			agentID:    "agent-456",
			createErr:  errors.New("session creation failed"),
			wantErr:    errors.New("session creation failed"),
			wantStatus: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockMgr.createErr = tt.createErr

			agent, err := svc.CreateAgent(ctx, tt.agentID)

			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("CreateAgent() expected error %v, got nil", tt.wantErr)
				}
				if agent != nil {
					t.Error("CreateAgent() should return nil agent on error")
				}
			} else {
				if err != nil {
					t.Errorf("CreateAgent() unexpected error: %v", err)
				}
				if agent == nil {
					t.Error("CreateAgent() should return agent on success")
				} else {
					if agent.ID != tt.agentID {
						t.Errorf("CreateAgent().ID = %q, want %q", agent.ID, tt.agentID)
					}
					if agent.Status != tt.wantStatus {
						t.Errorf("CreateAgent().Status = %q, want %q", agent.Status, tt.wantStatus)
					}
					if agent.SessionID == "" {
						t.Error("CreateAgent().SessionID should not be empty")
					}
					if agent.CreatedAt == 0 {
						t.Error("CreateAgent().CreatedAt should not be zero")
					}
				}
			}
		})
	}
}

// TestCreateAgentConcurrent tests concurrent agent creation.
func TestCreateAgentConcurrent(t *testing.T) {
	ctx := context.Background()
	mockMgr := newMockMemoryManager()
	svc := NewService(mockMgr)

	var wg sync.WaitGroup
	numAgents := 10

	for i := 0; i < numAgents; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			agentID := "agent-" + string(rune('0'+index))
			_, err := svc.CreateAgent(ctx, agentID)
			if err != nil {
				t.Errorf("Concurrent CreateAgent() failed: %v", err)
			}
		}(i)
	}

	wg.Wait()

	// Verify all agents were created
	svc.agentsMu.RLock()
	defer svc.agentsMu.RUnlock()

	if len(svc.agents) != numAgents {
		t.Errorf("Expected %d agents, got %d", numAgents, len(svc.agents))
	}
}

// TestGetAgent tests the GetAgent method.
func TestGetAgent(t *testing.T) {
	ctx := context.Background()
	mockMgr := newMockMemoryManager()
	svc := NewService(mockMgr)

	// Create a test agent
	testAgentID := "agent-123"
	createdAgent, err := svc.CreateAgent(ctx, testAgentID)
	if err != nil {
		t.Fatalf("Failed to create test agent: %v", err)
	}

	tests := []struct {
		name    string
		agentID string
		wantErr error
	}{
		{
			name:    "get existing agent",
			agentID: testAgentID,
			wantErr: nil,
		},
		{
			name:    "get non-existent agent",
			agentID: "agent-999",
			wantErr: ErrAgentNotFound,
		},
		{
			name:    "empty agent ID",
			agentID: "",
			wantErr: ErrInvalidAgentID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agent, err := svc.GetAgent(ctx, tt.agentID)

			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Errorf("GetAgent() error = %v, want %v", err, tt.wantErr)
				}
				if agent != nil {
					t.Error("GetAgent() should return nil agent on error")
				}
			} else {
				if err != nil {
					t.Errorf("GetAgent() unexpected error: %v", err)
				}
				if agent == nil {
					t.Error("GetAgent() should return agent on success")
				} else {
					// Verify returned agent is a copy, not the original
					if agent.ID != createdAgent.ID {
						t.Errorf("GetAgent().ID = %q, want %q", agent.ID, createdAgent.ID)
					}
					if agent.SessionID != createdAgent.SessionID {
						t.Errorf("GetAgent().SessionID = %q, want %q", agent.SessionID, createdAgent.SessionID)
					}
					if agent.Status != createdAgent.Status {
						t.Errorf("GetAgent().Status = %q, want %q", agent.Status, createdAgent.Status)
					}
					if agent.CreatedAt != createdAgent.CreatedAt {
						t.Errorf("GetAgent().CreatedAt = %d, want %d", agent.CreatedAt, createdAgent.CreatedAt)
					}

					// Verify modifying the returned agent doesn't affect the stored agent
					agent.ID = "modified-id"
					storedAgent, _ := svc.GetAgent(ctx, tt.agentID)
					if storedAgent.ID == "modified-id" {
						t.Error("Modifying returned agent should not affect stored agent")
					}
				}
			}
		})
	}
}

// TestDeleteAgent tests the DeleteAgent method.
func TestDeleteAgent(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name      string
		setup     func(*Service) (string, error)
		deleteErr error
		wantErr   error
	}{
		{
			name: "successful deletion",
			setup: func(svc *Service) (string, error) {
				agent, err := svc.CreateAgent(ctx, "agent-123")
				return agent.ID, err
			},
			deleteErr: nil,
			wantErr:   nil,
		},
		{
			name: "delete non-existent agent",
			setup: func(svc *Service) (string, error) {
				return "agent-999", nil
			},
			deleteErr: nil,
			wantErr:   ErrAgentNotFound,
		},
		{
			name: "delete with empty agent ID",
			setup: func(svc *Service) (string, error) {
				return "", nil
			},
			deleteErr: nil,
			wantErr:   ErrInvalidAgentID,
		},
		{
			name: "delete with session deletion failure",
			setup: func(svc *Service) (string, error) {
				agent, err := svc.CreateAgent(ctx, "agent-456")
				return agent.ID, err
			},
			deleteErr: errors.New("session deletion failed"),
			wantErr:   nil, // Agent deletion should succeed even if session deletion fails
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockMgr := newMockMemoryManager()
			mockMgr.deleteErr = tt.deleteErr
			svc := NewService(mockMgr)

			agentID, err := tt.setup(svc)
			if err != nil {
				t.Fatalf("Setup failed: %v", err)
			}

			err = svc.DeleteAgent(ctx, agentID)

			if tt.wantErr != nil {
				if err != tt.wantErr {
					t.Errorf("DeleteAgent() error = %v, want %v", err, tt.wantErr)
				}
			} else {
				if err != nil {
					t.Errorf("DeleteAgent() unexpected error: %v", err)
				}

				// Verify agent was removed
				_, err = svc.GetAgent(ctx, agentID)
				if err != ErrAgentNotFound {
					t.Error("DeleteAgent() should remove agent from storage")
				}
			}
		})
	}
}

// TestDeleteAgentNilMemoryManager tests deletion with nil memory manager.
func TestDeleteAgentNilMemoryManager(t *testing.T) {
	ctx := context.Background()
	svc := NewService(nil)

	// Create an agent with nil memory manager will fail, so we'll manually add one
	agentID := "agent-123"
	agent := &Agent{
		ID:        agentID,
		SessionID: "session-123",
		Status:    StatusReady,
		CreatedAt: time.Now().Unix(),
	}

	svc.agentsMu.Lock()
	svc.agents[agentID] = agent
	svc.agentsMu.Unlock()

	// Delete should succeed even with nil memory manager
	err := svc.DeleteAgent(ctx, agentID)
	if err != nil {
		t.Errorf("DeleteAgent() with nil memory manager failed: %v", err)
	}

	// Verify agent was removed
	_, err = svc.GetAgent(ctx, agentID)
	if err != ErrAgentNotFound {
		t.Error("DeleteAgent() should remove agent even with nil memory manager")
	}
}

// TestStatus tests Status constants.
func TestStatus(t *testing.T) {
	tests := []struct {
		name   string
		status Status
		want   string
	}{
		{
			name:   "ready status",
			status: StatusReady,
			want:   "ready",
		},
		{
			name:   "running status",
			status: StatusRunning,
			want:   "running",
		},
		{
			name:   "stopped status",
			status: StatusStopped,
			want:   "stopped",
		},
		{
			name:   "error status",
			status: StatusError,
			want:   "error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.status) != tt.want {
				t.Errorf("Status = %q, want %q", tt.status, tt.want)
			}
		})
	}
}

// TestStatusUniqueness tests that all Status values are unique.
func TestStatusUniqueness(t *testing.T) {
	statuses := map[string]bool{
		string(StatusReady):   true,
		string(StatusRunning): true,
		string(StatusStopped): true,
		string(StatusError):   true,
	}

	if len(statuses) != 4 {
		t.Errorf("expected 4 unique status values, got %d", len(statuses))
	}
}

// TestAgent tests Agent struct.
func TestAgent(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name  string
		agent Agent
	}{
		{
			name: "fully populated agent",
			agent: Agent{
				ID:        "agent-123",
				SessionID: "session-456",
				Status:    StatusReady,
				CreatedAt: now.Unix(),
			},
		},
		{
			name: "minimal agent",
			agent: Agent{
				ID:        "agent-789",
				SessionID: "session-999",
				Status:    StatusRunning,
				CreatedAt: 0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = tt.agent.ID
			_ = tt.agent.SessionID
			_ = tt.agent.Status
			_ = tt.agent.CreatedAt
		})
	}
}

// TestGetCurrentTimestamp tests the getCurrentTimestamp function.
func TestGetCurrentTimestamp(t *testing.T) {
	timestamp := getCurrentTimestamp()

	if timestamp == 0 {
		t.Error("getCurrentTimestamp() should return non-zero timestamp")
	}

	// Verify it's close to current time (within 1 second)
	currentTime := time.Now().Unix()
	diff := currentTime - timestamp
	if diff < 0 {
		diff = -diff
	}

	if diff > 1 {
		t.Errorf("getCurrentTimestamp() returned timestamp too far from current time: diff = %d seconds", diff)
	}
}
