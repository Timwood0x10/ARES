package client

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/api/core"
)

// TestNewConfigLoader tests the creation of a new config loader.
func TestNewConfigLoader(t *testing.T) {
	tests := []struct {
		name string
		opts []ConfigLoaderOption
		want *ConfigLoader
	}{
		{
			name: "default loader",
			opts: nil,
			want: &ConfigLoader{
				defaultPaths: []string{
					"./config.yaml",
					"./config/server.yaml",
					"./examples/simple_newapi/config/server.yaml",
				},
				envPrefix: "GOAGENT",
			},
		},
		{
			name: "loader with custom paths",
			opts: []ConfigLoaderOption{WithDefaultPaths("/custom/path.yaml")},
			want: &ConfigLoader{
				defaultPaths: []string{"/custom/path.yaml"},
				envPrefix:    "GOAGENT",
			},
		},
		{
			name: "loader with custom env prefix",
			opts: []ConfigLoaderOption{WithEnvPrefix("CUSTOM")},
			want: &ConfigLoader{
				defaultPaths: []string{
					"./config.yaml",
					"./config/server.yaml",
					"./examples/simple_newapi/config/server.yaml",
				},
				envPrefix: "CUSTOM",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewConfigLoader(tt.opts...)
			if got.envPrefix != tt.want.envPrefix {
				t.Errorf("envPrefix mismatch: got %q, want %q", got.envPrefix, tt.want.envPrefix)
			}
			if len(tt.opts) > 0 {
				if len(got.defaultPaths) != len(tt.want.defaultPaths) {
					t.Errorf("defaultPaths length mismatch: got %d, want %d", len(got.defaultPaths), len(tt.want.defaultPaths))
				}
			}
		})
	}
}

// TestConfigFileSetDefaults tests setting default values.
func TestConfigFileSetDefaults(t *testing.T) {
	tests := []struct {
		name string
		cfg  *ConfigFile
	}{
		{
			name: "empty config gets defaults",
			cfg:  &ConfigFile{},
		},
		{
			name: "partial config gets defaults",
			cfg: &ConfigFile{
				LLM: core.LLMConfig{
					Provider: core.LLMProviderOpenAI,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.cfg.setDefaults()

			// Check API defaults
			if tt.cfg.API.RequestTimeout != 30 {
				t.Errorf("expected API.RequestTimeout to be 30, got %d", tt.cfg.API.RequestTimeout)
			}
			if tt.cfg.API.MaxRetries != 3 {
				t.Errorf("expected API.MaxRetries to be 3, got %d", tt.cfg.API.MaxRetries)
			}
			if tt.cfg.API.RetryDelay != 1 {
				t.Errorf("expected API.RetryDelay to be 1, got %d", tt.cfg.API.RetryDelay)
			}

			// Check LLM defaults
			if tt.cfg.LLM.Timeout != 60 {
				t.Errorf("expected LLM.Timeout to be 60, got %d", tt.cfg.LLM.Timeout)
			}
			if tt.cfg.LLM.BaseURL == "" {
				t.Errorf("expected LLM.BaseURL to be set, got empty string")
			}
			if tt.cfg.LLM.Model == "" {
				t.Errorf("expected LLM.Model to be set, got empty string")
			}

			// Check database defaults
			if tt.cfg.Database.Port != 5432 {
				t.Errorf("expected Database.Port to be 5432, got %d", tt.cfg.Database.Port)
			}
			if tt.cfg.Database.User != "postgres" {
				t.Errorf("expected Database.User to be postgres, got %q", tt.cfg.Database.User)
			}
			if tt.cfg.Database.DBName != "goagent" {
				t.Errorf("expected Database.DBName to be goagent, got %q", tt.cfg.Database.DBName)
			}

			// Check memory defaults
			if tt.cfg.Memory.Session.MaxHistory != 50 {
				t.Errorf("expected Memory.Session.MaxHistory to be 50, got %d", tt.cfg.Memory.Session.MaxHistory)
			}
		})
	}
}

// TestConfigFileValidate tests configuration validation.
func TestConfigFileValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *ConfigFile
		wantErr bool
	}{
		{
			name: "valid minimal config",
			cfg: &ConfigFile{
				API: APIConfig{
					RequestTimeout: 30,
					MaxRetries:     3,
					RetryDelay:     1,
				},
				LLM: core.LLMConfig{
					Provider: core.LLMProviderOllama,
					BaseURL:  "http://localhost:11434",
					Model:    "llama3.2",
					Timeout:  60,
				},
			},
			wantErr: false,
		},
		{
			name: "invalid api request timeout (too low)",
			cfg: &ConfigFile{
				API: APIConfig{
					RequestTimeout: 0,
				},
			},
			wantErr: true,
		},
		{
			name: "invalid api request timeout (too high)",
			cfg: &ConfigFile{
				API: APIConfig{
					RequestTimeout: 301,
				},
			},
			wantErr: true,
		},
		{
			name: "invalid api max retries (negative)",
			cfg: &ConfigFile{
				API: APIConfig{
					MaxRetries: -1,
				},
			},
			wantErr: true,
		},
		{
			name: "invalid api max retries (too high)",
			cfg: &ConfigFile{
				API: APIConfig{
					MaxRetries: 11,
				},
			},
			wantErr: true,
		},
		{
			name: "missing llm provider",
			cfg: &ConfigFile{
				API: APIConfig{
					RequestTimeout: 30,
					MaxRetries:     3,
					RetryDelay:     1,
				},
			},
			wantErr: true,
		},
		{
			name: "invalid llm provider",
			cfg: &ConfigFile{
				API: APIConfig{
					RequestTimeout: 30,
					MaxRetries:     3,
					RetryDelay:     1,
				},
				LLM: core.LLMConfig{
					Provider: core.LLMProvider("invalid"),
				},
			},
			wantErr: true,
		},
		{
			name: "database enabled but missing host",
			cfg: &ConfigFile{
				API: APIConfig{
					RequestTimeout: 30,
					MaxRetries:     3,
					RetryDelay:     1,
				},
				LLM: core.LLMConfig{
					Provider: core.LLMProviderOllama,
					BaseURL:  "http://localhost:11434",
					Model:    "llama3.2",
					Timeout:  60,
				},
				Database: DatabaseConfig{
					Enabled: true,
				},
			},
			wantErr: true,
		},
		{
			name: "valid config with database",
			cfg: &ConfigFile{
				API: APIConfig{
					RequestTimeout: 30,
					MaxRetries:     3,
					RetryDelay:     1,
				},
				LLM: core.LLMConfig{
					Provider: core.LLMProviderOllama,
					BaseURL:  "http://localhost:11434",
					Model:    "llama3.2",
					Timeout:  60,
				},
				Database: DatabaseConfig{
					Enabled: true,
					Host:    "localhost",
					Port:    5432,
					DBName:  "testdb",
				},
			},
			wantErr: false,
		},
		{
			name: "memory enabled with invalid max history",
			cfg: &ConfigFile{
				API: APIConfig{
					RequestTimeout: 30,
					MaxRetries:     3,
					RetryDelay:     1,
				},
				LLM: core.LLMConfig{
					Provider: core.LLMProviderOllama,
					BaseURL:  "http://localhost:11434",
					Model:    "llama3.2",
					Timeout:  60,
				},
				Memory: MemoryConfig{
					Enabled: true,
					Session: struct {
						MaxHistory int `yaml:"max_history"`
					}{
						MaxHistory: 1001,
					},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestConfigFileLoadFromEnv tests loading configuration from environment variables.
func TestConfigFileLoadFromEnv(t *testing.T) {
	tests := []struct {
		name     string
		envVars  map[string]string
		setupEnv bool
		check    func(t *testing.T, cfg *ConfigFile)
	}{
		{
			name: "load llm api key from env",
			envVars: map[string]string{
				"GOAGENT_LLM_API_KEY": "test-api-key",
			},
			setupEnv: true,
			check: func(t *testing.T, cfg *ConfigFile) {
				if cfg.LLM.APIKey != "test-api-key" {
					t.Errorf("expected LLM.APIKey to be test-api-key, got %q", cfg.LLM.APIKey)
				}
			},
		},
		{
			name: "load llm base url from env",
			envVars: map[string]string{
				"GOAGENT_LLM_BASE_URL": "http://custom-llm:8080",
			},
			setupEnv: true,
			check: func(t *testing.T, cfg *ConfigFile) {
				if cfg.LLM.BaseURL != "http://custom-llm:8080" {
					t.Errorf("expected LLM.BaseURL to be http://custom-llm:8080, got %q", cfg.LLM.BaseURL)
				}
			},
		},
		{
			name: "load llm model from env",
			envVars: map[string]string{
				"GOAGENT_LLM_MODEL": "custom-model",
			},
			setupEnv: true,
			check: func(t *testing.T, cfg *ConfigFile) {
				if cfg.LLM.Model != "custom-model" {
					t.Errorf("expected LLM.Model to be custom-model, got %q", cfg.LLM.Model)
				}
			},
		},
		{
			name: "load database password from env",
			envVars: map[string]string{
				"GOAGENT_DB_PASSWORD": "db-password",
			},
			setupEnv: true,
			check: func(t *testing.T, cfg *ConfigFile) {
				if cfg.Database.Password != "db-password" {
					t.Errorf("expected Database.Password to be db-password, got %q", cfg.Database.Password)
				}
			},
		},
		{
			name:     "no env vars set",
			envVars:  map[string]string{},
			setupEnv: false,
			check: func(t *testing.T, cfg *ConfigFile) {
				// Should use defaults from setDefaults
				if cfg.LLM.Provider == "" {
					t.Errorf("expected LLM.Provider to be set from defaults")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup environment variables
			if tt.setupEnv {
				for k, v := range tt.envVars {
					_ = os.Setenv(k, v)
					defer func() {
						_ = os.Unsetenv(k)
					}()
				}
			}

			cfg := &ConfigFile{}
			cfg.loadFromEnv("GOAGENT")
			cfg.setDefaults()

			if tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}

// TestConfigFileToClientConfig tests conversion to client.Config.
func TestConfigFileToClientConfig(t *testing.T) {
	cfg := &ConfigFile{
		API: APIConfig{
			RequestTimeout: 30,
			MaxRetries:     3,
			RetryDelay:     1,
		},
		LLM: core.LLMConfig{
			Provider: core.LLMProviderOllama,
			BaseURL:  "http://localhost:11434",
			Model:    "llama3.2",
			Timeout:  60,
		},
	}

	clientConfig := cfg.ToClientConfig()

	require.NotNil(t, clientConfig)

	require.NotNil(t, clientConfig.BaseConfig)

	if clientConfig.BaseConfig.RequestTimeout != 30*time.Second {
		t.Errorf("expected RequestTimeout to be 30s, got %v", clientConfig.BaseConfig.RequestTimeout)
	}

	if clientConfig.BaseConfig.MaxRetries != 3 {
		t.Errorf("expected MaxRetries to be 3, got %d", clientConfig.BaseConfig.MaxRetries)
	}

	if clientConfig.Agent == nil {
		t.Error("expected Agent config to be non-nil")
	}

	if clientConfig.Memory == nil {
		t.Error("expected Memory config to be non-nil")
	}

	if clientConfig.Retrieval == nil {
		t.Error("expected Retrieval config to be non-nil")
	}

	if clientConfig.LLM == nil {
		t.Error("expected LLM config to be non-nil")
	}
}

// TestConfigStructures tests that all config structures are properly defined.
func TestConfigStructures(t *testing.T) {
	tests := []struct {
		name string
		cfg  interface{}
	}{
		{
			name: "ServerConfig",
			cfg:  &ServerConfig{},
		},
		{
			name: "APIConfig",
			cfg:  &APIConfig{},
		},
		{
			name: "DatabaseConfig",
			cfg:  &DatabaseConfig{},
		},
		{
			name: "StorageConfig",
			cfg:  &StorageConfig{},
		},
		{
			name: "MemoryConfig",
			cfg:  &MemoryConfig{},
		},
		{
			name: "AgentsConfig",
			cfg:  &AgentsConfig{},
		},
		{
			name: "LeaderAgentConfig",
			cfg:  &LeaderAgentConfig{},
		},
		{
			name: "SubAgentConfig",
			cfg:  &SubAgentConfig{},
		},
		{
			name: "PromptsConfig",
			cfg:  &PromptsConfig{},
		},
		{
			name: "OutputConfig",
			cfg:  &OutputConfig{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.cfg == nil {
				t.Errorf("expected %s to be non-nil", tt.name)
			}
		})
	}
}

// TestLLMProviderDefaults tests default LLM provider settings.
func TestLLMProviderDefaults(t *testing.T) {
	tests := []struct {
		name        string
		provider    core.LLMProvider
		wantBaseURL string
		wantModel   string
	}{
		{
			name:        "Ollama defaults",
			provider:    core.LLMProviderOllama,
			wantBaseURL: "http://localhost:11434",
			wantModel:   "llama3.2",
		},
		{
			name:        "OpenRouter defaults",
			provider:    core.LLMProviderOpenRouter,
			wantBaseURL: "https://openrouter.ai/api/v1",
			wantModel:   "meta-llama/llama-3.1-8b-instruct",
		},
		{
			name:        "OpenAI defaults",
			provider:    core.LLMProviderOpenAI,
			wantBaseURL: "https://api.openai.com/v1",
			wantModel:   "gpt-4o",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &ConfigFile{
				LLM: core.LLMConfig{
					Provider: tt.provider,
				},
			}
			cfg.setDefaults()

			if cfg.LLM.BaseURL != tt.wantBaseURL {
				t.Errorf("expected BaseURL to be %q, got %q", tt.wantBaseURL, cfg.LLM.BaseURL)
			}

			if cfg.LLM.Model != tt.wantModel {
				t.Errorf("expected Model to be %q, got %q", tt.wantModel, cfg.LLM.Model)
			}
		})
	}
}

// TestDatabaseDefaults tests default database settings.
func TestDatabaseDefaults(t *testing.T) {
	cfg := &ConfigFile{
		Database: DatabaseConfig{},
	}
	cfg.setDefaults()

	if cfg.Database.Port != 5432 {
		t.Errorf("expected default port to be 5432, got %d", cfg.Database.Port)
	}

	if cfg.Database.User != "postgres" {
		t.Errorf("expected default user to be postgres, got %q", cfg.Database.User)
	}

	if cfg.Database.DBName != "goagent" {
		t.Errorf("expected default database name to be goagent, got %q", cfg.Database.DBName)
	}
}
