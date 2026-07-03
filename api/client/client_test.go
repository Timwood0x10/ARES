package client

import (
	"context"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/api/core"
)

// stubAgentService implements core.AgentService for testing.
type stubAgentService struct{}

func (s *stubAgentService) CreateAgent(ctx context.Context, config *core.AgentConfig) (*core.Agent, error) {
	return &core.Agent{ID: "stub", Name: "stub"}, nil
}
func (s *stubAgentService) GetAgent(ctx context.Context, agentID string) (*core.Agent, error) {
	return &core.Agent{ID: agentID, Name: "stub"}, nil
}
func (s *stubAgentService) UpdateAgent(ctx context.Context, agentID string, updates map[string]interface{}) (*core.Agent, error) {
	return &core.Agent{ID: agentID, Name: "stub"}, nil
}
func (s *stubAgentService) DeleteAgent(ctx context.Context, agentID string) error {
	return nil
}
func (s *stubAgentService) ListAgents(ctx context.Context, filter *core.AgentFilter) ([]*core.Agent, *core.PaginationResponse, error) {
	return []*core.Agent{{ID: "stub", Name: "stub"}}, &core.PaginationResponse{}, nil
}
func (s *stubAgentService) ExecuteTask(ctx context.Context, task *core.Task) (*core.TaskResult, error) {
	return &core.TaskResult{TaskID: "task-1"}, nil
}
func (s *stubAgentService) GetTaskResult(ctx context.Context, taskID string) (*core.TaskResult, error) {
	return &core.TaskResult{TaskID: taskID}, nil
}

// stubMemoryService implements core.MemoryService for testing.
type stubMemoryService struct{}

func (s *stubMemoryService) CreateSession(ctx context.Context, config *core.SessionConfig) (string, error) {
	return "sess-1", nil
}
func (s *stubMemoryService) GetSession(ctx context.Context, sessionID string) (*core.Session, error) {
	return &core.Session{ID: sessionID}, nil
}
func (s *stubMemoryService) DeleteSession(ctx context.Context, sessionID string) error {
	return nil
}
func (s *stubMemoryService) AddMessage(ctx context.Context, sessionID string, role core.MessageRole, content string) error {
	return nil
}
func (s *stubMemoryService) GetMessages(ctx context.Context, sessionID string, pagination *core.PaginationRequest) ([]*core.Message, error) {
	return []*core.Message{}, nil
}
func (s *stubMemoryService) DistillTask(ctx context.Context, taskID string) (*core.DistilledTask, error) {
	return &core.DistilledTask{TaskID: taskID}, nil
}
func (s *stubMemoryService) SearchSimilarTasks(ctx context.Context, query *core.SearchQuery) ([]*core.SearchResult, error) {
	return []*core.SearchResult{}, nil
}

// stubRetrievalService implements core.RetrievalService for testing.
type stubRetrievalService struct{}

func (s *stubRetrievalService) Search(ctx context.Context, tenantID, query string) ([]*core.RetrievalResult, error) {
	return []*core.RetrievalResult{}, nil
}
func (s *stubRetrievalService) SearchWithConfig(ctx context.Context, request *core.RetrievalRequest) ([]*core.RetrievalResult, error) {
	return []*core.RetrievalResult{}, nil
}
func (s *stubRetrievalService) AddKnowledge(ctx context.Context, item *core.KnowledgeItem) (*core.KnowledgeItem, error) {
	return item, nil
}
func (s *stubRetrievalService) GetKnowledge(ctx context.Context, tenantID, itemID string) (*core.KnowledgeItem, error) {
	return &core.KnowledgeItem{}, nil
}
func (s *stubRetrievalService) UpdateKnowledge(ctx context.Context, tenantID string, item *core.KnowledgeItem) (*core.KnowledgeItem, error) {
	return item, nil
}
func (s *stubRetrievalService) DeleteKnowledge(ctx context.Context, tenantID, itemID string) error {
	return nil
}
func (s *stubRetrievalService) ListKnowledge(ctx context.Context, tenantID string, filter *core.KnowledgeFilter) ([]*core.KnowledgeItem, *core.PaginationResponse, error) {
	return []*core.KnowledgeItem{}, &core.PaginationResponse{}, nil
}

// stubLLMService implements core.LLMService for testing.
type stubLLMService struct {
	disabled bool
}

func (s *stubLLMService) Generate(ctx context.Context, request *core.GenerateRequest) (*core.GenerateResponse, error) {
	return &core.GenerateResponse{Content: "hello"}, nil
}
func (s *stubLLMService) GenerateSimple(ctx context.Context, prompt string) (string, error) {
	return "hello", nil
}
func (s *stubLLMService) GenerateEmbedding(ctx context.Context, request *core.EmbeddingRequest) (*core.EmbeddingResponse, error) {
	return &core.EmbeddingResponse{}, nil
}
func (s *stubLLMService) GetConfig() *core.LLMConfig {
	return &core.LLMConfig{Provider: core.LLMProviderOllama, Model: "llama3.2"}
}
func (s *stubLLMService) IsEnabled() bool { return !s.disabled }
func (s *stubLLMService) GetProvider() core.LLMProvider { return core.LLMProviderOllama }
func (s *stubLLMService) GetModel() string { return "llama3.2" }

// TestNewClient tests the creation of a new client instance.
func TestNewClient(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
	}{
		{
			name:    "nil config returns error",
			config:  nil,
			wantErr: true,
		},
		{
			name: "empty config with base config",
			config: &Config{
				BaseConfig: &core.BaseConfig{
					RequestTimeout: 30 * time.Second,
					MaxRetries:     3,
					RetryDelay:     1 * time.Second,
				},
			},
			wantErr: false,
		},
		{
			name:    "config without base config gets defaults",
			config:  &Config{},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClient(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewClient() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && client == nil {
				t.Errorf("expected client to be non-nil when wantErr is false")
			}
		})
	}
}

// TestClientBaseConfigDefaults tests that base config gets proper defaults.
func TestClientBaseConfigDefaults(t *testing.T) {
	config := &Config{}
	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	if client.config.BaseConfig.RequestTimeout != 30*time.Second {
		t.Errorf("expected RequestTimeout to be 30s, got %v", client.config.BaseConfig.RequestTimeout)
	}

	if client.config.BaseConfig.MaxRetries != 3 {
		t.Errorf("expected MaxRetries to be 3, got %d", client.config.BaseConfig.MaxRetries)
	}

	if client.config.BaseConfig.RetryDelay != 1*time.Second {
		t.Errorf("expected RetryDelay to be 1s, got %v", client.config.BaseConfig.RetryDelay)
	}
}

// TestClientAgent tests accessing the agent service.
func TestClientAgent(t *testing.T) {
	tests := []struct {
		name          string
		agentConfig   bool
		wantErr       bool
		expectedError error
	}{
		{
			name:          "agent service not configured",
			agentConfig:   false,
			wantErr:       true,
			expectedError: ErrAgentNotConfigured,
		},
		{
			name:        "agent service configured",
			agentConfig: true,
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &Config{
				BaseConfig: &core.BaseConfig{
					RequestTimeout: 30 * time.Second,
					MaxRetries:     3,
					RetryDelay:     1 * time.Second,
				},
			}

			if tt.agentConfig {
				config.Agent = &stubAgentService{}
			}

			client, err := NewClient(config)
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}

			agent, err := client.Agent()
			if (err != nil) != tt.wantErr {
				t.Errorf("Agent() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && err != tt.expectedError {
				t.Errorf("expected error %v, got %v", tt.expectedError, err)
			}

			if !tt.wantErr && agent == nil {
				t.Errorf("expected agent service to be non-nil")
			}
		})
	}
}

// TestClientMemory tests accessing the memory service.
func TestClientMemory(t *testing.T) {
	tests := []struct {
		name          string
		memoryConfig  bool
		wantErr       bool
		expectedError error
	}{
		{
			name:          "memory service not configured",
			memoryConfig:  false,
			wantErr:       true,
			expectedError: ErrMemoryNotConfigured,
		},
		{
			name:         "memory service configured",
			memoryConfig: true,
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &Config{
				BaseConfig: &core.BaseConfig{
					RequestTimeout: 30 * time.Second,
					MaxRetries:     3,
					RetryDelay:     1 * time.Second,
				},
			}

			if tt.memoryConfig {
				config.Memory = &stubMemoryService{}
			}

			client, err := NewClient(config)
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}

			memory, err := client.Memory()
			if (err != nil) != tt.wantErr {
				t.Errorf("Memory() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && err != tt.expectedError {
				t.Errorf("expected error %v, got %v", tt.expectedError, err)
			}

			if !tt.wantErr && memory == nil {
				t.Errorf("expected memory service to be non-nil")
			}
		})
	}
}

// TestClientRetrieval tests accessing the retrieval service.
func TestClientRetrieval(t *testing.T) {
	tests := []struct {
		name            string
		retrievalConfig bool
		wantErr         bool
		expectedError   error
	}{
		{
			name:            "retrieval service not configured",
			retrievalConfig: false,
			wantErr:         true,
			expectedError:   ErrRetrievalNotConfigured,
		},
		{
			name:            "retrieval service configured",
			retrievalConfig: true,
			wantErr:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &Config{
				BaseConfig: &core.BaseConfig{
					RequestTimeout: 30 * time.Second,
					MaxRetries:     3,
					RetryDelay:     1 * time.Second,
				},
			}

			if tt.retrievalConfig {
				config.Retrieval = &stubRetrievalService{}
			}

			client, err := NewClient(config)
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}

			retrieval, err := client.Retrieval()
			if (err != nil) != tt.wantErr {
				t.Errorf("Retrieval() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && err != tt.expectedError {
				t.Errorf("expected error %v, got %v", tt.expectedError, err)
			}

			if !tt.wantErr && retrieval == nil {
				t.Errorf("expected retrieval service to be non-nil")
			}
		})
	}
}

// TestClientLLM tests accessing the LLM service.
func TestClientLLM(t *testing.T) {
	tests := []struct {
		name          string
		llmConfig     bool
		wantErr       bool
		expectedError error
	}{
		{
			name:          "LLM service not configured",
			llmConfig:     false,
			wantErr:       true,
			expectedError: ErrLLMNotConfigured,
		},
		{
			name:      "LLM service configured",
			llmConfig: true,
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &Config{
				BaseConfig: &core.BaseConfig{
					RequestTimeout: 30 * time.Second,
					MaxRetries:     3,
					RetryDelay:     1 * time.Second,
				},
			}

			if tt.llmConfig {
				config.LLM = &stubLLMService{}
			}

			client, err := NewClient(config)
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}

			llm, err := client.LLM()
			if (err != nil) != tt.wantErr {
				t.Errorf("LLM() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && err != tt.expectedError {
				t.Errorf("expected error %v, got %v", tt.expectedError, err)
			}

			if !tt.wantErr && llm == nil {
				t.Errorf("expected LLM service to be non-nil")
			}
		})
	}
}

// TestClientClose tests closing the client.
func TestClientClose(t *testing.T) {
	config := &Config{
		BaseConfig: &core.BaseConfig{
			RequestTimeout: 30 * time.Second,
			MaxRetries:     3,
			RetryDelay:     1 * time.Second,
		},
	}

	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ctx := context.Background()
	err = client.Close(ctx)
	if err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

// TestClientPing tests the Ping method returns true for open clients and false for closed clients.
func TestClientPing(t *testing.T) {
	t.Run("open client returns true", func(t *testing.T) {
		client, err := NewClient(&Config{
			BaseConfig: &core.BaseConfig{
				RequestTimeout: 30 * time.Second,
				MaxRetries:     3,
				RetryDelay:     1 * time.Second,
			},
		})
		if err != nil {
			t.Fatalf("NewClient() error = %v", err)
		}

		ctx := context.Background()
		if !client.Ping(ctx) {
			t.Errorf("Ping() = false, want true for open client")
		}
	})

	t.Run("closed client returns false", func(t *testing.T) {
		client, err := NewClient(&Config{
			BaseConfig: &core.BaseConfig{
				RequestTimeout: 30 * time.Second,
				MaxRetries:     3,
				RetryDelay:     1 * time.Second,
			},
		})
		if err != nil {
			t.Fatalf("NewClient() error = %v", err)
		}

		ctx := context.Background()
		_ = client.Close(ctx)
		if client.Ping(ctx) {
			t.Errorf("Ping() = true, want false for closed client")
		}
	})
}

// TestConfigStructure tests the Config structure.
func TestConfigStructure(t *testing.T) {
	config := &Config{
		BaseConfig: &core.BaseConfig{
			RequestTimeout: 30 * time.Second,
			MaxRetries:     3,
			RetryDelay:     1 * time.Second,
		},
	}

	if config.BaseConfig == nil {
		t.Error("expected BaseConfig to be non-nil")
	}
}