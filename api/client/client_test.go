package client

import (
	"context"
	"testing"
	"time"

	"github.com/Timwood0x10/goagent/api/core"
	agentSvc "github.com/Timwood0x10/goagent/api/service/agent"
	llmSvc "github.com/Timwood0x10/goagent/api/service/llm"
	memorySvc "github.com/Timwood0x10/goagent/api/service/memory"
	retrievalSvc "github.com/Timwood0x10/goagent/api/service/retrieval"
)

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
				config.Agent = &agentSvc.Config{
					BaseConfig: config.BaseConfig,
					Repo:       agentSvc.NewMemoryRepository(),
				}
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
				config.Memory = &memorySvc.Config{
					BaseConfig: config.BaseConfig,
					Repo:       memorySvc.NewMemoryRepository(),
				}
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
				config.Retrieval = &retrievalSvc.Config{
					BaseConfig: config.BaseConfig,
					Repo:       retrievalSvc.NewMemoryRepository(),
				}
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
				config.LLM = &llmSvc.Config{
					BaseConfig: config.BaseConfig,
					LLMConfig: &core.LLMConfig{
						Provider: core.LLMProviderOllama,
						BaseURL:  "http://localhost:11434",
						Model:    "llama3.2",
						Timeout:  60,
					},
				}
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

// TestClientGetConfig tests getting the config file.
func TestClientGetConfig(t *testing.T) {
	config := &Config{
		BaseConfig: &core.BaseConfig{
			RequestTimeout: 30 * time.Second,
			MaxRetries:     3,
			RetryDelay:     1 * time.Second,
		},
	}

	configFile := &ConfigFile{
		Server: ServerConfig{
			Host: "localhost",
			Port: 8080,
		},
	}

	client, err := NewClientWithConfigFile(config, configFile)
	if err != nil {
		t.Fatalf("NewClientWithConfigFile() error = %v", err)
	}

	got := client.GetConfig()
	if got == nil {
		t.Errorf("expected config file to be non-nil")
	}

	if got != configFile {
		t.Errorf("expected config file to be the same instance")
	}
}

// TestNewClientWithConfigFile tests creating a client with config file.
func TestNewClientWithConfigFile(t *testing.T) {
	config := &Config{
		BaseConfig: &core.BaseConfig{
			RequestTimeout: 30 * time.Second,
			MaxRetries:     3,
			RetryDelay:     1 * time.Second,
		},
	}

	configFile := &ConfigFile{
		Server: ServerConfig{
			Host: "localhost",
			Port: 8080,
		},
	}

	client, err := NewClientWithConfigFile(config, configFile)
	if err != nil {
		t.Fatalf("NewClientWithConfigFile() error = %v", err)
	}

	if client == nil {
		t.Fatal("expected client to be non-nil")
	}

	if client.configFile == nil {
		t.Errorf("expected configFile to be set")
	}
}

// TestClientPing tests the Ping method.
func TestClientPing(t *testing.T) {
	tests := []struct {
		name       string
		config     *Config
		wantResult bool
	}{
		{
			name: "all services configured",
			config: &Config{
				BaseConfig: &core.BaseConfig{
					RequestTimeout: 30 * time.Second,
					MaxRetries:     3,
					RetryDelay:     1 * time.Second,
				},
				Agent: &agentSvc.Config{
					BaseConfig: &core.BaseConfig{
						RequestTimeout: 30 * time.Second,
						MaxRetries:     3,
						RetryDelay:     1 * time.Second,
					},
					Repo: agentSvc.NewMemoryRepository(),
				},
				Memory: &memorySvc.Config{
					BaseConfig: &core.BaseConfig{
						RequestTimeout: 30 * time.Second,
						MaxRetries:     3,
						RetryDelay:     1 * time.Second,
					},
					Repo: memorySvc.NewMemoryRepository(),
				},
				Retrieval: &retrievalSvc.Config{
					BaseConfig: &core.BaseConfig{
						RequestTimeout: 30 * time.Second,
						MaxRetries:     3,
						RetryDelay:     1 * time.Second,
					},
					Repo: retrievalSvc.NewMemoryRepository(),
				},
			},
			wantResult: true,
		},
		{
			name: "agent service not configured",
			config: &Config{
				BaseConfig: &core.BaseConfig{
					RequestTimeout: 30 * time.Second,
					MaxRetries:     3,
					RetryDelay:     1 * time.Second,
				},
				Memory: &memorySvc.Config{
					BaseConfig: &core.BaseConfig{
						RequestTimeout: 30 * time.Second,
						MaxRetries:     3,
						RetryDelay:     1 * time.Second,
					},
					Repo: memorySvc.NewMemoryRepository(),
				},
				Retrieval: &retrievalSvc.Config{
					BaseConfig: &core.BaseConfig{
						RequestTimeout: 30 * time.Second,
						MaxRetries:     3,
						RetryDelay:     1 * time.Second,
					},
					Repo: retrievalSvc.NewMemoryRepository(),
				},
			},
			wantResult: false,
		},
		{
			name: "memory service not configured",
			config: &Config{
				BaseConfig: &core.BaseConfig{
					RequestTimeout: 30 * time.Second,
					MaxRetries:     3,
					RetryDelay:     1 * time.Second,
				},
				Agent: &agentSvc.Config{
					BaseConfig: &core.BaseConfig{
						RequestTimeout: 30 * time.Second,
						MaxRetries:     3,
						RetryDelay:     1 * time.Second,
					},
					Repo: agentSvc.NewMemoryRepository(),
				},
				Retrieval: &retrievalSvc.Config{
					BaseConfig: &core.BaseConfig{
						RequestTimeout: 30 * time.Second,
						MaxRetries:     3,
						RetryDelay:     1 * time.Second,
					},
					Repo: retrievalSvc.NewMemoryRepository(),
				},
			},
			wantResult: false,
		},
		{
			name: "retrieval service not configured",
			config: &Config{
				BaseConfig: &core.BaseConfig{
					RequestTimeout: 30 * time.Second,
					MaxRetries:     3,
					RetryDelay:     1 * time.Second,
				},
				Agent: &agentSvc.Config{
					BaseConfig: &core.BaseConfig{
						RequestTimeout: 30 * time.Second,
						MaxRetries:     3,
						RetryDelay:     1 * time.Second,
					},
					Repo: agentSvc.NewMemoryRepository(),
				},
				Memory: &memorySvc.Config{
					BaseConfig: &core.BaseConfig{
						RequestTimeout: 30 * time.Second,
						MaxRetries:     3,
						RetryDelay:     1 * time.Second,
					},
					Repo: memorySvc.NewMemoryRepository(),
				},
			},
			wantResult: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClient(tt.config)
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}

			ctx := context.Background()
			result := client.Ping(ctx)
			if result != tt.wantResult {
				t.Errorf("Ping() = %v, want %v", result, tt.wantResult)
			}
		})
	}
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
