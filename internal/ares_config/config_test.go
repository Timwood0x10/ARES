// nolint: errcheck // Test code may ignore return values
package ares_config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLoad tests the Load function.
func TestLoad(t *testing.T) {
	// Create a temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	configContent := `
server:
  host: "localhost"
  port: 8080

llm:
  provider: "ollama"
  model: "llama3.2"
  timeout: 60
  max_tokens: 4096

agents:
  leader:
    id: "leader-1"
    max_steps: 10
    max_parallel_tasks: 5
    max_validation_retry: 3
    enable_cache: true
  sub: []

prompts:
  profile_extraction: "Extract profile from: {{.input}}"
  recommendation: "Recommend items for: {{.input}}"
  style_analysis: "Analyze style of: {{.input}}"

output:
  format: "simple"
  item_template: "{{.ItemID}}: {{.Name}}"
  summary_template: "Got {{.Count}} items"

validation:
  enabled: true
  schema_type: "default"
  retry_on_fail: true
  max_retries: 3
  strict_mode: false

workflow:
  definition_path: "./workflows"
  auto_reload: true
  reload_interval: 30

storage:
  enabled: true
  type: "postgres"
  host: "localhost"
  port: 5432
  username: "postgres"
  password: "postgres"
  database: "goagent"
  ssl_mode: "disable"
  pgvector:
    enabled: true
    dimension: 1536
    table_name: "embeddings"

memory:
  enabled: true
  session:
    enabled: true
    max_history: 50
  user_profile:
    enabled: true
    storage: "memory"
    vector_db: false
  task_distillation:
    enabled: true
    storage: "memory"
    vector_store: false
    prompt: "Summarize task"
`

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	// Test loading valid config
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// Verify loaded values
	if cfg.Server.Host != "localhost" {
		t.Errorf("Server.Host = %v, want localhost", cfg.Server.Host)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("Server.Port = %v, want 8080", cfg.Server.Port)
	}
	if cfg.LLM.Provider != "ollama" {
		t.Errorf("LLM.Provider = %v, want ollama", cfg.LLM.Provider)
	}
	if cfg.LLM.Model != "llama3.2" {
		t.Errorf("LLM.Model = %v, want llama3.2", cfg.LLM.Model)
	}
	if cfg.Agents.Leader.MaxSteps != 10 {
		t.Errorf("Agents.Leader.MaxSteps = %v, want 10", cfg.Agents.Leader.MaxSteps)
	}
	if cfg.Output.Format != "simple" {
		t.Errorf("Output.Format = %v, want simple", cfg.Output.Format)
	}
	if cfg.Storage.Enabled != true {
		t.Errorf("Storage.Enabled = %v, want true", cfg.Storage.Enabled)
	}
	if cfg.Memory.Enabled != true {
		t.Errorf("Memory.Enabled = %v, want true", cfg.Memory.Enabled)
	}
}

// TestLoadInvalidFile tests loading a non-existent file.
func TestLoadInvalidFile(t *testing.T) {
	_, err := Load("/nonexistent/config.yaml")
	if err == nil {
		t.Error("Load() expected error for non-existent file, got nil")
	}
}

// TestLoadInvalidYAML tests loading invalid YAML.
func TestLoadInvalidYAML(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "invalid.yaml")
	configContent := `
server:
  host: "localhost"
  port: invalid
`

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	_, err := Load(configPath)
	if err == nil {
		t.Error("Load() expected error for invalid YAML, got nil")
	}
}

// TestLoadFromEnv tests loading configuration from environment variables.
func TestLoadFromEnv(t *testing.T) {
	// Create minimal config
	cfg := &Config{
		Server: ServerConfig{
			Host: "localhost",
			Port: 8080,
		},
		LLM: LLMConfig{
			Provider: "ollama",
			Model:    "llama3",
		},
		Storage: StorageConfig{
			Type: "postgres",
		},
	}

	// Set environment variables
	// Test code: os.Setenv is used to set environment variables for testing
	// nolint: errcheck // This is intentional in test code
	if err := os.Setenv("SERVER_HOST", "0.0.0.0"); err != nil {
		t.Fatalf("Failed to set SERVER_HOST: %v", err)
	}

	// Test code: os.Setenv is used to set environment variables for testing
	// nolint: errcheck // This is intentional in test code
	if err := os.Setenv("SERVER_PORT", "9000"); err != nil {
		t.Fatalf("Failed to set SERVER_PORT: %v", err)
	}
	if err := os.Setenv("LLM_API_KEY", "test-api-key"); err != nil {
		t.Fatalf("Failed to set LLM_API_KEY: %v", err)
	}
	if err := os.Setenv("LLM_PROVIDER", "openai"); err != nil {
		t.Fatalf("Failed to set LLM_PROVIDER: %v", err)
	}
	if err := os.Setenv("LLM_BASE_URL", "https://api.openai.com"); err != nil {
		t.Fatalf("Failed to set LLM_BASE_URL: %v", err)
	}
	if err := os.Setenv("LLM_MODEL", "gpt-4"); err != nil {
		t.Fatalf("Failed to set LLM_MODEL: %v", err)
	}
	if err := os.Setenv("DB_HOST", "db.example.com"); err != nil {
		t.Fatalf("Failed to set DB_HOST: %v", err)
	}
	if err := os.Setenv("DB_PORT", "5433"); err != nil {
		t.Fatalf("Failed to set DB_PORT: %v", err)
	}
	if err := os.Setenv("DB_USERNAME", "user"); err != nil {
		t.Fatalf("Failed to set DB_USERNAME: %v", err)
	}
	if err := os.Setenv("DB_PASSWORD", "pass"); err != nil {
		t.Fatalf("Failed to set DB_PASSWORD: %v", err)
	}
	if err := os.Setenv("DB_DATABASE", "testdb"); err != nil {
		t.Fatalf("Failed to set DB_DATABASE: %v", err)
	}
	defer func() {
		// Test code: os.Unsetenv is used to clean up environment variables in test
		// nolint: errcheck // This is intentional in test code
		if err := os.Unsetenv("SERVER_HOST"); err != nil {
			t.Fatalf("Failed to unset SERVER_HOST: %v", err)
		}
		if err := os.Unsetenv("SERVER_PORT"); err != nil {
			t.Fatalf("Failed to unset SERVER_PORT: %v", err)
		}
		if err := os.Unsetenv("LLM_API_KEY"); err != nil {
			t.Fatalf("Failed to unset LLM_API_KEY: %v", err)
		}
		if err := os.Unsetenv("LLM_PROVIDER"); err != nil {
			t.Fatalf("Failed to unset LLM_PROVIDER: %v", err)
		}
		if err := os.Unsetenv("LLM_BASE_URL"); err != nil {
			t.Fatalf("Failed to unset LLM_BASE_URL: %v", err)
		}
		if err := os.Unsetenv("LLM_MODEL"); err != nil {
			t.Fatalf("Failed to unset LLM_MODEL: %v", err)
		}
		if err := os.Unsetenv("DB_HOST"); err != nil {
			t.Fatalf("Failed to unset DB_HOST: %v", err)
		}
		if err := os.Unsetenv("DB_PORT"); err != nil {
			t.Fatalf("Failed to unset DB_PORT: %v", err)
		}
		if err := os.Unsetenv("DB_USERNAME"); err != nil {
			t.Fatalf("Failed to unset DB_USERNAME: %v", err)
		}
		if err := os.Unsetenv("DB_PASSWORD"); err != nil {
			t.Fatalf("Failed to unset DB_PASSWORD: %v", err)
		}
		if err := os.Unsetenv("DB_DATABASE"); err != nil {
			t.Fatalf("Failed to unset DB_DATABASE: %v", err)
		}
	}()

	// Load from environment
	if err := LoadFromEnv(cfg); err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}

	// Verify environment overrides
	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("Server.Host = %v, want 0.0.0.0", cfg.Server.Host)
	}
	if cfg.Server.Port != 9000 {
		t.Errorf("Server.Port = %v, want 9000", cfg.Server.Port)
	}
	if cfg.LLM.APIKey != "test-api-key" {
		t.Errorf("LLM.APIKey = %v, want test-api-key", cfg.LLM.APIKey)
	}
	if cfg.LLM.Provider != "openai" {
		t.Errorf("LLM.Provider = %v, want openai", cfg.LLM.Provider)
	}
	if cfg.LLM.BaseURL != "https://api.openai.com" {
		t.Errorf("LLM.BaseURL = %v, want https://api.openai.com", cfg.LLM.BaseURL)
	}
	if cfg.LLM.Model != "gpt-4" {
		t.Errorf("LLM.Model = %v, want gpt-4", cfg.LLM.Model)
	}
	if cfg.Storage.Host != "db.example.com" {
		t.Errorf("Storage.Host = %v, want db.example.com", cfg.Storage.Host)
	}
	if cfg.Storage.Port != 5433 {
		t.Errorf("Storage.Port = %v, want 5433", cfg.Storage.Port)
	}
	if cfg.Storage.Username != "user" {
		t.Errorf("Storage.Username = %v, want user", cfg.Storage.Username)
	}
	if cfg.Storage.Password != "pass" {
		t.Errorf("Storage.Password = %v, want pass", cfg.Storage.Password)
	}
	if cfg.Storage.Database != "testdb" {
		t.Errorf("Storage.Database = %v, want testdb", cfg.Storage.Database)
	}
}

// TestLoadFromEnvOpenRouterAPIKey tests OPENROUTER_API_KEY environment variable.
func TestLoadFromEnvOpenRouterAPIKey(t *testing.T) {
	cfg := &Config{
		LLM: LLMConfig{
			Provider: "openrouter",
		},
	}

	if err := os.Setenv("OPENROUTER_API_KEY", "openrouter-key"); err != nil {
		t.Fatalf("Failed to set OPENROUTER_API_KEY: %v", err)
	}
	defer func() {
		if err := os.Unsetenv("OPENROUTER_API_KEY"); err != nil {
			t.Fatalf("Failed to unset OPENROUTER_API_KEY: %v", err)
		}
	}()

	if err := LoadFromEnv(cfg); err != nil {
		t.Fatalf("LoadFromEnv() error = %v", err)
	}

	if cfg.LLM.APIKey != "openrouter-key" {
		t.Errorf("LLM.APIKey = %v, want openrouter-key", cfg.LLM.APIKey)
	}
}

// TestLoadFromEnvInvalidPort tests loading invalid port from environment.
func TestLoadFromEnvInvalidPort(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Host: "localhost",
			Port: 8080,
		},
	}

	if err := os.Setenv("SERVER_PORT", "invalid"); err != nil {
		t.Fatalf("Failed to set SERVER_PORT: %v", err)
	}
	defer func() {
		if err := os.Unsetenv("SERVER_PORT"); err != nil {
			t.Logf("Failed to unset SERVER_PORT: %v", err)
		}
	}()

	// Should not fail, just ignore invalid value
	if err := LoadFromEnv(cfg); err != nil {
		t.Errorf("LoadFromEnv() should ignore invalid port, got error: %v", err)
	}

	// Port should remain unchanged
	if cfg.Server.Port != 8080 {
		t.Errorf("Server.Port = %v, want 8080 (unchanged)", cfg.Server.Port)
	}
}

// TestSetDefaults tests the setDefaults method.
func TestSetDefaults(t *testing.T) {
	cfg := &Config{}

	cfg.setDefaults()

	// Verify default values
	if cfg.Server.Host != "localhost" {
		t.Errorf("Server.Host default = %v, want localhost", cfg.Server.Host)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("Server.Port default = %v, want 8080", cfg.Server.Port)
	}
	if cfg.LLM.Provider != "ollama" {
		t.Errorf("LLM.Provider default = %v, want ollama", cfg.LLM.Provider)
	}
	if cfg.LLM.Model != "llama3.2" {
		t.Errorf("LLM.Model default = %v, want llama3.2", cfg.LLM.Model)
	}
	if cfg.LLM.Timeout != 60 {
		t.Errorf("LLM.Timeout default = %v, want 60", cfg.LLM.Timeout)
	}
	if cfg.LLM.MaxTokens != 4096 {
		t.Errorf("LLM.MaxTokens default = %v, want 4096", cfg.LLM.MaxTokens)
	}
	if cfg.Agents.Leader.MaxSteps != 10 {
		t.Errorf("Agents.Leader.MaxSteps default = %v, want 10", cfg.Agents.Leader.MaxSteps)
	}
	if cfg.Agents.Leader.MaxParallelTasks != 5 {
		t.Errorf("Agents.Leader.MaxParallelTasks default = %v, want 5", cfg.Agents.Leader.MaxParallelTasks)
	}
	if cfg.Agents.Leader.MaxValidationRetry != 3 {
		t.Errorf("Agents.Leader.MaxValidationRetry default = %v, want 3", cfg.Agents.Leader.MaxValidationRetry)
	}
	if cfg.Output.Format != "simple" {
		t.Errorf("Output.Format default = %v, want simple", cfg.Output.Format)
	}
	if cfg.Storage.Type != "postgres" {
		t.Errorf("Storage.Type default = %v, want postgres", cfg.Storage.Type)
	}
	if cfg.Storage.Port != 5432 {
		t.Errorf("Storage.Port default = %v, want 5432", cfg.Storage.Port)
	}
	if cfg.Storage.PGVector.Dimension != 1536 {
		t.Errorf("Storage.PGVector.Dimension default = %v, want 1536", cfg.Storage.PGVector.Dimension)
	}
	if cfg.Storage.PGVector.TableName != "embeddings" {
		t.Errorf("Storage.PGVector.TableName default = %v, want embeddings", cfg.Storage.PGVector.TableName)
	}
	if cfg.Memory.SessionMemory.MaxHistory != 50 {
		t.Errorf("Memory.SessionMemory.MaxHistory default = %v, want 50", cfg.Memory.SessionMemory.MaxHistory)
	}
	if cfg.Memory.UserProfile.Storage != "memory" {
		t.Errorf("Memory.UserProfile.Storage default = %v, want memory", cfg.Memory.UserProfile.Storage)
	}
	if cfg.Validation.SchemaType != "default" {
		t.Errorf("Validation.SchemaType default = %v, want default", cfg.Validation.SchemaType)
	}
	if cfg.Validation.MaxRetries != 3 {
		t.Errorf("Validation.MaxRetries default = %v, want 3", cfg.Validation.MaxRetries)
	}
}

// TestValidate tests the Validate method with valid config.
func TestValidate(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Host: "localhost",
			Port: 8080,
		},
		LLM: LLMConfig{
			Provider:  "ollama",
			Model:     "llama3",
			Timeout:   60,
			MaxTokens: 4096,
		},
		Agents: AgentsConfig{
			Leader: LeaderConfig{
				ID:                 "leader-1",
				MaxSteps:           10,
				MaxParallelTasks:   5,
				MaxValidationRetry: 3,
			},
			Sub: []SubAgentConfig{
				{
					ID:         "sub-1",
					Type:       "top",
					Category:   "clothing",
					Timeout:    30,
					MaxRetries: 3,
				},
			},
		},
		Output: OutputConfig{
			Format: "simple",
		},
		Validation: ValidationConfig{
			MaxRetries: 3,
		},
		Storage: StorageConfig{
			Enabled:  true,
			Type:     "postgres",
			Host:     "localhost",
			Port:     5432,
			Database: "goagent",
		},
		Memory: MemoryConfig{
			SessionMemory: SessionConfig{
				MaxHistory: 50,
			},
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() error = %v", err)
	}
}

// TestValidateInvalidServerPort tests validation with invalid server port.
func TestValidateInvalidServerPort(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Host: "localhost",
			Port: 70000, // Invalid port
		},
		LLM: LLMConfig{
			Provider:  "ollama",
			Model:     "llama3",
			Timeout:   60,
			MaxTokens: 4096,
		},
		Agents: AgentsConfig{
			Leader: LeaderConfig{
				ID:                 "leader-1",
				MaxSteps:           10,
				MaxParallelTasks:   5,
				MaxValidationRetry: 3,
			},
			Sub: []SubAgentConfig{},
		},
		Output: OutputConfig{
			Format: "simple",
		},
		Validation: ValidationConfig{
			MaxRetries: 3,
		},
		Memory: MemoryConfig{
			SessionMemory: SessionConfig{
				MaxHistory: 50,
			},
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Error("Validate() expected error for invalid server port, got nil")
	}
}

// TestValidateInvalidLLMTimeout tests validation with invalid LLM timeout.
func TestValidateInvalidLLMTimeout(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Host: "localhost",
			Port: 8080,
		},
		LLM: LLMConfig{
			Provider:  "ollama",
			Model:     "llama3",
			Timeout:   0, // Invalid timeout
			MaxTokens: 4096,
		},
		Agents: AgentsConfig{
			Leader: LeaderConfig{
				ID:                 "leader-1",
				MaxSteps:           10,
				MaxParallelTasks:   5,
				MaxValidationRetry: 3,
			},
			Sub: []SubAgentConfig{},
		},
		Output: OutputConfig{
			Format: "simple",
		},
		Validation: ValidationConfig{
			MaxRetries: 3,
		},
		Memory: MemoryConfig{
			SessionMemory: SessionConfig{
				MaxHistory: 50,
			},
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Error("Validate() expected error for invalid LLM timeout, got nil")
	}
}

// TestValidateInvalidLLMProvider tests validation with invalid LLM provider.
func TestValidateInvalidLLMProvider(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Host: "localhost",
			Port: 8080,
		},
		LLM: LLMConfig{
			Provider:  "invalid", // Invalid provider
			Model:     "llama3",
			Timeout:   60,
			MaxTokens: 4096,
		},
		Agents: AgentsConfig{
			Leader: LeaderConfig{
				ID:                 "leader-1",
				MaxSteps:           10,
				MaxParallelTasks:   5,
				MaxValidationRetry: 3,
			},
			Sub: []SubAgentConfig{},
		},
		Output: OutputConfig{
			Format: "simple",
		},
		Validation: ValidationConfig{
			MaxRetries: 3,
		},
		Memory: MemoryConfig{
			SessionMemory: SessionConfig{
				MaxHistory: 50,
			},
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Error("Validate() expected error for invalid LLM provider, got nil")
	}
}

// TestValidateInvalidLeaderMaxSteps tests validation with invalid leader max steps.
func TestValidateInvalidLeaderMaxSteps(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Host: "localhost",
			Port: 8080,
		},
		LLM: LLMConfig{
			Provider:  "ollama",
			Model:     "llama3",
			Timeout:   60,
			MaxTokens: 4096,
		},
		Agents: AgentsConfig{
			Leader: LeaderConfig{
				ID:                 "leader-1",
				MaxSteps:           0, // Invalid
				MaxParallelTasks:   5,
				MaxValidationRetry: 3,
			},
			Sub: []SubAgentConfig{},
		},
		Output: OutputConfig{
			Format: "simple",
		},
		Validation: ValidationConfig{
			MaxRetries: 3,
		},
		Memory: MemoryConfig{
			SessionMemory: SessionConfig{
				MaxHistory: 50,
			},
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Error("Validate() expected error for invalid leader max steps, got nil")
	}
}

// TestValidateInvalidOutputFormat tests validation with invalid output format.
func TestValidateInvalidOutputFormat(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Host: "localhost",
			Port: 8080,
		},
		LLM: LLMConfig{
			Provider:  "ollama",
			Model:     "llama3",
			Timeout:   60,
			MaxTokens: 4096,
		},
		Agents: AgentsConfig{
			Leader: LeaderConfig{
				ID:                 "leader-1",
				MaxSteps:           10,
				MaxParallelTasks:   5,
				MaxValidationRetry: 3,
			},
			Sub: []SubAgentConfig{},
		},
		Output: OutputConfig{
			Format: "invalid", // Invalid format
		},
		Validation: ValidationConfig{
			MaxRetries: 3,
		},
		Memory: MemoryConfig{
			SessionMemory: SessionConfig{
				MaxHistory: 50,
			},
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Error("Validate() expected error for invalid output format, got nil")
	}
}

// TestValidateInvalidSubAgent tests validation with invalid sub-agent config.
func TestValidateInvalidSubAgent(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Host: "localhost",
			Port: 8080,
		},
		LLM: LLMConfig{
			Provider:  "ollama",
			Model:     "llama3",
			Timeout:   60,
			MaxTokens: 4096,
		},
		Agents: AgentsConfig{
			Leader: LeaderConfig{
				ID:                 "leader-1",
				MaxSteps:           10,
				MaxParallelTasks:   5,
				MaxValidationRetry: 3,
			},
			Sub: []SubAgentConfig{
				{
					ID:         "", // Invalid: empty ID
					Type:       "top",
					Category:   "clothing",
					Timeout:    30,
					MaxRetries: 3,
				},
			},
		},
		Output: OutputConfig{
			Format: "simple",
		},
		Validation: ValidationConfig{
			MaxRetries: 3,
		},
		Memory: MemoryConfig{
			SessionMemory: SessionConfig{
				MaxHistory: 50,
			},
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Error("Validate() expected error for invalid sub-agent, got nil")
	}
}

// TestValidateStorageEnabled tests validation with storage enabled but missing required fields.
func TestValidateStorageEnabled(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Host: "localhost",
			Port: 8080,
		},
		LLM: LLMConfig{
			Provider:  "ollama",
			Model:     "llama3",
			Timeout:   60,
			MaxTokens: 4096,
		},
		Agents: AgentsConfig{
			Leader: LeaderConfig{
				ID:                 "leader-1",
				MaxSteps:           10,
				MaxParallelTasks:   5,
				MaxValidationRetry: 3,
			},
			Sub: []SubAgentConfig{},
		},
		Output: OutputConfig{
			Format: "simple",
		},
		Validation: ValidationConfig{
			MaxRetries: 3,
		},
		Storage: StorageConfig{
			Enabled:  true,
			Type:     "postgres",
			Host:     "", // Missing required field
			Port:     5432,
			Database: "goagent",
		},
		Memory: MemoryConfig{
			SessionMemory: SessionConfig{
				MaxHistory: 50,
			},
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Error("Validate() expected error for storage enabled but missing host, got nil")
	}
}

// TestValidateInvalidSessionMaxHistory tests validation with invalid session max history.
func TestValidateInvalidSessionMaxHistory(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Host: "localhost",
			Port: 8080,
		},
		LLM: LLMConfig{
			Provider:  "ollama",
			Model:     "llama3",
			Timeout:   60,
			MaxTokens: 4096,
		},
		Agents: AgentsConfig{
			Leader: LeaderConfig{
				ID:                 "leader-1",
				MaxSteps:           10,
				MaxParallelTasks:   5,
				MaxValidationRetry: 3,
			},
			Sub: []SubAgentConfig{},
		},
		Output: OutputConfig{
			Format: "simple",
		},
		Validation: ValidationConfig{
			MaxRetries: 3,
		},
		Memory: MemoryConfig{
			SessionMemory: SessionConfig{
				MaxHistory: -1, // Invalid
			},
		},
	}

	if err := cfg.Validate(); err == nil {
		t.Error("Validate() expected error for invalid session max history, got nil")
	}
}

// TestConfigStructs tests config struct initialization.
func TestConfigStructs(t *testing.T) {
	// Test ServerConfig
	serverCfg := ServerConfig{
		Host: "0.0.0.0",
		Port: 9000,
	}
	if serverCfg.Host != "0.0.0.0" || serverCfg.Port != 9000 {
		t.Error("ServerConfig initialization failed")
	}

	// Test LLMConfig
	llmCfg := LLMConfig{
		Provider:  "openai",
		APIKey:    "test-key",
		BaseURL:   "https://api.openai.com",
		Model:     "gpt-4",
		Timeout:   120,
		MaxTokens: 8192,
		Extra:     map[string]string{"custom": "value"},
	}
	if llmCfg.Provider != "openai" || llmCfg.APIKey != "test-key" {
		t.Error("LLMConfig initialization failed")
	}

	// Test LeaderConfig
	leaderCfg := LeaderConfig{
		ID:                 "leader-1",
		MaxSteps:           20,
		MaxParallelTasks:   10,
		MaxValidationRetry: 5,
		EnableCache:        true,
	}
	if leaderCfg.MaxSteps != 20 || leaderCfg.MaxParallelTasks != 10 {
		t.Error("LeaderConfig initialization failed")
	}

	// Test SubAgentConfig
	subCfg := SubAgentConfig{
		ID:         "sub-1",
		Type:       "top",
		Category:   "clothing",
		Triggers:   []string{"style", "budget"},
		MaxRetries: 3,
		Timeout:    30,
		Model:      "gpt-3.5",
		Provider:   "openai",
	}
	if subCfg.Type != "top" || len(subCfg.Triggers) != 2 {
		t.Error("SubAgentConfig initialization failed")
	}

	// Test StorageConfig
	storageCfg := StorageConfig{
		Enabled:  true,
		Type:     "postgres",
		Host:     "localhost",
		Port:     5432,
		Username: "postgres",
		Password: "postgres",
		Database: "goagent",
		SSLMode:  "disable",
		PGVector: PGVectorConfig{
			Enabled:   true,
			Dimension: 1536,
			TableName: "embeddings",
		},
	}
	if storageCfg.Type != "postgres" || storageCfg.Port != 5432 {
		t.Error("StorageConfig initialization failed")
	}

	// Test MemoryConfig
	memoryCfg := MemoryConfig{
		Enabled: true,
		SessionMemory: SessionConfig{
			Enabled:    true,
			MaxHistory: 100,
		},
		UserProfile: ProfileConfig{
			Enabled:  true,
			Storage:  "postgres",
			VectorDB: true,
		},
		TaskDistillation: DistillConfig{
			Enabled:     true,
			Storage:     "postgres",
			VectorStore: true,
			Prompt:      "Test prompt",
		},
	}
	if !memoryCfg.Enabled || memoryCfg.SessionMemory.MaxHistory != 100 {
		t.Error("MemoryConfig initialization failed")
	}
}

// TestValidLLMProviders tests all valid LLM providers.
func TestValidLLMProviders(t *testing.T) {
	validProviders := []string{"openai", "ollama", "openrouter"}

	for _, provider := range validProviders {
		cfg := &Config{
			Server: ServerConfig{
				Host: "localhost",
				Port: 8080,
			},
			LLM: LLMConfig{
				Provider:  provider,
				Model:     "model",
				Timeout:   60,
				MaxTokens: 4096,
			},
			Agents: AgentsConfig{
				Leader: LeaderConfig{
					ID:                 "leader-1",
					MaxSteps:           10,
					MaxParallelTasks:   5,
					MaxValidationRetry: 3,
				},
				Sub: []SubAgentConfig{},
			},
			Output: OutputConfig{
				Format: "simple",
			},
			Validation: ValidationConfig{
				MaxRetries: 3,
			},
			Memory: MemoryConfig{
				SessionMemory: SessionConfig{
					MaxHistory: 50,
				},
			},
		}

		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate() failed for provider %s: %v", provider, err)
		}
	}
}

// TestValidOutputFormats tests all valid output formats.
func TestValidOutputFormats(t *testing.T) {
	validFormats := []string{"table", "json", "simple"}

	for _, format := range validFormats {
		cfg := &Config{
			Server: ServerConfig{
				Host: "localhost",
				Port: 8080,
			},
			LLM: LLMConfig{
				Provider:  "ollama",
				Model:     "llama3",
				Timeout:   60,
				MaxTokens: 4096,
			},
			Agents: AgentsConfig{
				Leader: LeaderConfig{
					ID:                 "leader-1",
					MaxSteps:           10,
					MaxParallelTasks:   5,
					MaxValidationRetry: 3,
				},
				Sub: []SubAgentConfig{},
			},
			Output: OutputConfig{
				Format: format,
			},
			Validation: ValidationConfig{
				MaxRetries: 3,
			},
			Memory: MemoryConfig{
				SessionMemory: SessionConfig{
					MaxHistory: 50,
				},
			},
		}

		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate() failed for format %s: %v", format, err)
		}
	}
}

// nolint: errcheck // Test code may ignore return values
