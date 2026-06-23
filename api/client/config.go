// Package client provides configuration loading utilities for the GoAgent client.
package client

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/api/core"
	agentSvc "github.com/Timwood0x10/ares/api/service/agent"
	llmSvc "github.com/Timwood0x10/ares/api/service/llm"
	memorySvc "github.com/Timwood0x10/ares/api/service/memory"
	retrievalSvc "github.com/Timwood0x10/ares/api/service/retrieval"
	"github.com/Timwood0x10/ares/internal/errors"

	"gopkg.in/yaml.v3"
)

var (
	allowedConfigDir   string
	allowedConfigDirMu sync.RWMutex
)

// SetAllowedConfigDir sets the allowed directory for config files.
// This is a security measure to prevent path traversal attacks.
func SetAllowedConfigDir(dir string) {
	allowedConfigDirMu.Lock()
	allowedConfigDir = dir
	allowedConfigDirMu.Unlock()
}

// getAllowedConfigDir returns the allowed config directory in a thread-safe manner.
func getAllowedConfigDir() string {
	allowedConfigDirMu.RLock()
	dir := allowedConfigDir
	allowedConfigDirMu.RUnlock()
	return dir
}

// ConfigFile represents the structure of the configuration file.

// It follows the configuration structure used across all examples.

type ConfigFile struct {
	Server ServerConfig `yaml:"server"`

	API APIConfig `yaml:"api"`

	LLM core.LLMConfig `yaml:"llm"`

	Database DatabaseConfig `yaml:"database"`

	Storage StorageConfig `yaml:"storage"`

	Memory MemoryConfig `yaml:"memory"`

	Agents AgentsConfig `yaml:"agents"`

	Prompts PromptsConfig `yaml:"prompts"`

	Output OutputConfig `yaml:"output"`
}

// ServerConfig represents server configuration.

type ServerConfig struct {
	Host string `yaml:"host"`

	Port int `yaml:"port"`
}

// APIConfig represents API configuration.

type APIConfig struct {
	RequestTimeout int `yaml:"request_timeout"`

	MaxRetries int `yaml:"max_retries"`

	RetryDelay int `yaml:"retry_delay"`
}

// DatabaseConfig represents database configuration.

type DatabaseConfig struct {
	Enabled bool `yaml:"enabled"`

	Type string `yaml:"type"`

	Host string `yaml:"host"`

	Port int `yaml:"port"`

	User string `yaml:"username"`

	Password string `yaml:"password"`

	DBName string `yaml:"database"`

	SSLMode string `yaml:"ssl_mode"`
}

// StorageConfig represents storage configuration.

type StorageConfig struct {
	Enabled bool `yaml:"enabled"`

	Type string `yaml:"type"`
}

// MemoryConfig represents memory configuration.

type MemoryConfig struct {
	Enabled bool `yaml:"enabled"`

	Session struct {
		MaxHistory int `yaml:"max_history"`
	} `yaml:"session"`
}

// AgentsConfig represents agent configuration.

type AgentsConfig struct {
	Leader LeaderAgentConfig `yaml:"leader"`

	Sub []SubAgentConfig `yaml:"sub"`
}

// LeaderAgentConfig represents leader agent configuration.

type LeaderAgentConfig struct {
	ID string `yaml:"id"`

	MaxSteps int `yaml:"max_steps"`

	MaxParallelTasks int `yaml:"max_parallel_tasks"`
}

// SubAgentConfig represents sub-agent configuration.

type SubAgentConfig struct {
	ID string `yaml:"id"`

	Type string `yaml:"type"`

	Category string `yaml:"category"`

	Name string `yaml:"name"`

	MaxRetries int `yaml:"max_retries"`

	Timeout int `yaml:"timeout"`
}

// PromptsConfig represents prompt templates configuration.

type PromptsConfig struct {
	ProfileExtraction string `yaml:"profile_extraction"`

	Recommendation string `yaml:"recommendation"`
}

// OutputConfig represents output configuration.

type OutputConfig struct {
	Format string `yaml:"format"`
}

// ConfigLoader provides configuration loading functionality with validation.
type ConfigLoader struct {
	defaultPaths []string
	envPrefix    string
}

// NewConfigLoader creates a new configuration loader.
// Args:
// opts - optional configuration options.
// Returns new config loader instance.
func NewConfigLoader(opts ...ConfigLoaderOption) *ConfigLoader {
	loader := &ConfigLoader{
		defaultPaths: []string{
			"./config.yaml",
			"./config/server.yaml",
			"./examples/simple_newapi/config/server.yaml",
		},
		envPrefix: "GOAGENT",
	}

	for _, opt := range opts {
		opt(loader)
	}

	return loader
}

// ConfigLoaderOption represents a configuration loader option.
type ConfigLoaderOption func(*ConfigLoader)

// WithDefaultPaths sets custom default search paths.
func WithDefaultPaths(paths ...string) ConfigLoaderOption {
	return func(l *ConfigLoader) {
		l.defaultPaths = paths
	}
}

// WithEnvPrefix sets the environment variable prefix.
func WithEnvPrefix(prefix string) ConfigLoaderOption {
	return func(l *ConfigLoader) {
		l.envPrefix = prefix
	}
}

// Load loads configuration from the specified path or default locations.
// Args:
// path - optional path to the configuration file.
// Returns loaded and validated configuration or error.
func (l *ConfigLoader) Load(path string) (*ConfigFile, error) {
	// Determine config file path
	configPath, err := l.findConfigPath(path)
	if err != nil {
		return nil, errors.Wrap(err, "find config")
	}

	// Security: validate path is within allowed directory
	if dir := getAllowedConfigDir(); dir != "" {
		absPath, err := filepath.Abs(configPath)
		if err != nil {
			return nil, errors.Wrap(err, "get absolute path")
		}
		absDir, err := filepath.Abs(dir)
		if err != nil {
			return nil, errors.Wrap(err, "get absolute directory")
		}
		if !strings.HasPrefix(absPath, absDir) {
			return nil, fmt.Errorf("config path %s is outside allowed directory %s", configPath, dir)
		}
	}

	// Read file
	data, err := os.ReadFile(configPath) // #nosec G304
	if err != nil {
		return nil, errors.Wrapf(err, "read config file %s", configPath)
	}

	// Parse YAML
	var cfg ConfigFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, errors.Wrapf(err, "parse config file %s", configPath)
	}

	// Load environment variables
	cfg.loadFromEnv(l.envPrefix)

	// Set defaults
	cfg.setDefaults()

	// Validate configuration
	if err := cfg.validate(); err != nil {
		return nil, errors.Wrap(err, "validate config")
	}

	return &cfg, nil
}

// findConfigPath finds the configuration file path.
// Args:
// path - user-provided path (may be empty).
// Returns resolved path or error.
func (l *ConfigLoader) findConfigPath(path string) (string, error) {
	// If path is provided, use it
	if path != "" {
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("config file not found: %s", path)
		}
		return path, nil
	}

	// Search default paths
	for _, p := range l.defaultPaths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("no config file found in default paths: %v", l.defaultPaths)
}

// loadFromEnv loads sensitive configuration from environment variables.
// Priority: Environment variable > YAML file > Default
func (c *ConfigFile) loadFromEnv(prefix string) {
	// LLM API Key
	if key := os.Getenv(fmt.Sprintf("%s_LLM_API_KEY", prefix)); key != "" {
		c.LLM.APIKey = key
	} else if key := os.Getenv("LLM_API_KEY"); key != "" {
		c.LLM.APIKey = key
	}

	// LLM Base URL
	if url := os.Getenv(fmt.Sprintf("%s_LLM_BASE_URL", prefix)); url != "" {
		c.LLM.BaseURL = url
	} else if url := os.Getenv("LLM_BASE_URL"); url != "" {
		c.LLM.BaseURL = url
	}

	// LLM Model
	if model := os.Getenv(fmt.Sprintf("%s_LLM_MODEL", prefix)); model != "" {
		c.LLM.Model = model
	} else if model := os.Getenv("LLM_MODEL"); model != "" {
		c.LLM.Model = model
	}

	// LLM Provider
	if provider := os.Getenv(fmt.Sprintf("%s_LLM_PROVIDER", prefix)); provider != "" {
		c.LLM.Provider = core.LLMProvider(provider)
	} else if provider := os.Getenv("LLM_PROVIDER"); provider != "" {
		c.LLM.Provider = core.LLMProvider(provider)
	}

	// Database Password
	if password := os.Getenv(fmt.Sprintf("%s_DB_PASSWORD", prefix)); password != "" {
		c.Database.Password = password
	} else if password := os.Getenv("DB_PASSWORD"); password != "" {
		c.Database.Password = password
	}

	// Database Host
	if host := os.Getenv(fmt.Sprintf("%s_DB_HOST", prefix)); host != "" {
		c.Database.Host = host
	} else if host := os.Getenv("DB_HOST"); host != "" {
		c.Database.Host = host
	}
}

// setDefaults sets default values for configuration.
func (c *ConfigFile) setDefaults() {
	// API defaults
	if c.API.RequestTimeout <= 0 {
		c.API.RequestTimeout = 30
	}
	if c.API.MaxRetries <= 0 {
		c.API.MaxRetries = 3
	}
	if c.API.RetryDelay <= 0 {
		c.API.RetryDelay = 1
	}

	// LLM defaults
	if c.LLM.Timeout <= 0 {
		c.LLM.Timeout = 60
	}
	if c.LLM.Provider == "" {
		c.LLM.Provider = core.LLMProviderOllama
	}
	if c.LLM.BaseURL == "" {
		switch c.LLM.Provider {
		case core.LLMProviderOllama:
			c.LLM.BaseURL = "http://localhost:11434"
		case core.LLMProviderOpenRouter:
			c.LLM.BaseURL = "https://openrouter.ai/api/v1"
		case core.LLMProviderOpenAI:
			c.LLM.BaseURL = "https://api.openai.com/v1"
		}
	}
	if c.LLM.Model == "" {
		switch c.LLM.Provider {
		case core.LLMProviderOllama:
			c.LLM.Model = "llama3.2"
		case core.LLMProviderOpenRouter:
			c.LLM.Model = "meta-llama/llama-3.1-8b-instruct"
		case core.LLMProviderOpenAI:
			c.LLM.Model = "gpt-4o"
		}
	}

	// Database defaults
	if c.Database.Port <= 0 {
		c.Database.Port = 5432
	}
	if c.Database.User == "" {
		c.Database.User = "postgres"
	}
	if c.Database.DBName == "" {
		c.Database.DBName = "goagent"
	}

	// Memory defaults
	if c.Memory.Session.MaxHistory <= 0 {
		c.Memory.Session.MaxHistory = 50
	}
}

// validate validates the configuration.
// Returns error if validation fails.
func (c *ConfigFile) validate() error {
	// Validate API configuration
	if c.API.RequestTimeout < 1 || c.API.RequestTimeout > 300 {
		return fmt.Errorf("invalid api.request_timeout: must be between 1 and 300, got %d", c.API.RequestTimeout)
	}
	if c.API.MaxRetries < 0 || c.API.MaxRetries > 10 {
		return fmt.Errorf("invalid api.max_retries: must be between 0 and 10, got %d", c.API.MaxRetries)
	}
	if c.API.RetryDelay < 0 || c.API.RetryDelay > 60 {
		return fmt.Errorf("invalid api.retry_delay: must be between 0 and 60, got %d", c.API.RetryDelay)
	}

	// Validate LLM configuration
	if c.LLM.Provider == "" {
		return fmt.Errorf("llm.provider is required")
	}
	if c.LLM.Timeout < 1 || c.LLM.Timeout > 600 {
		return fmt.Errorf("invalid llm.timeout: must be between 1 and 600, got %d", c.LLM.Timeout)
	}
	if c.LLM.BaseURL == "" {
		return fmt.Errorf("llm.base_url is required")
	}
	if c.LLM.Model == "" {
		return fmt.Errorf("llm.model is required")
	}

	// Validate LLM provider
	validProviders := map[core.LLMProvider]bool{
		core.LLMProviderOllama:     true,
		core.LLMProviderOpenRouter: true,
		core.LLMProviderOpenAI:     true,
		core.LLMProviderAnthropic:  true,
	}
	if !validProviders[c.LLM.Provider] {
		return fmt.Errorf("invalid llm.provider: %s, must be one of ollama, openrouter, openai, anthropic", c.LLM.Provider)
	}

	// Validate database configuration if enabled
	if c.Database.Enabled {
		if c.Database.Host == "" {
			return fmt.Errorf("database.host is required when database.enabled is true")
		}
		if c.Database.DBName == "" {
			return fmt.Errorf("database.database is required when database.enabled is true")
		}
		if c.Database.Port < 1 || c.Database.Port > 65535 {
			return fmt.Errorf("invalid database.port: must be between 1 and 65535, got %d", c.Database.Port)
		}
	}

	// Validate memory configuration
	if c.Memory.Enabled {
		if c.Memory.Session.MaxHistory < 0 || c.Memory.Session.MaxHistory > 1000 {
			return fmt.Errorf("invalid memory.session.max_history: must be between 0 and 1000, got %d", c.Memory.Session.MaxHistory)
		}
	}

	return nil
}

// ToClientConfig converts ConfigFile to client.Config.
// Returns client configuration instance.
func (c *ConfigFile) ToClientConfig() *Config {
	baseConfig := &core.BaseConfig{
		RequestTimeout: time.Duration(c.API.RequestTimeout) * time.Second,
		MaxRetries:     c.API.MaxRetries,
		RetryDelay:     time.Duration(c.API.RetryDelay) * time.Second,
	}

	return &Config{
		BaseConfig: baseConfig,
		Agent: &agentSvc.Config{
			BaseConfig: baseConfig,
			Repo:       agentSvc.NewMemoryRepository(),
		},
		Memory: &memorySvc.Config{
			BaseConfig: baseConfig,
			Repo:       memorySvc.NewMemoryRepository(),
		},
		Retrieval: &retrievalSvc.Config{
			BaseConfig: baseConfig,
			Repo:       retrievalSvc.NewMemoryRepository(),
		},
		LLM: &llmSvc.Config{
			BaseConfig: baseConfig,
			LLMConfig:  &c.LLM,
		},
	}
}

// LoadConfigFile loads configuration from a YAML file using default loader.
// This is a convenience function that creates a default ConfigLoader.
// Args:
// path - path to the configuration file.
// Returns loaded and validated configuration or error.
//
// Deprecated: Use NewConfigLoader().Load(path) instead for more control.
func LoadConfigFile(path string) (*ConfigFile, error) {
	loader := NewConfigLoader()
	return loader.Load(path)
}

// NewClientFromConfigPath creates a new GoAgent client from a configuration file path.
// This is the simplest way to initialize the client - just provide the config file path.
// Args:
// configPath - path to the configuration file.
// Returns new client instance or error.
//
// Example:
//
//	client, err := client.NewClientFromConfigPath("config.yaml")
//	if err != nil {
//	    slog.Error(err)
//	}
//	defer client.Close(ctx)
func NewClientFromConfigPath(configPath string) (*Client, error) {
	loader := NewConfigLoader()

	// Load configuration
	cfg, err := loader.Load(configPath)
	if err != nil {
		return nil, errors.Wrap(err, "load config")
	}

	// Convert to client config
	clientConfig := cfg.ToClientConfig()

	// Create client
	return NewClientWithConfigFile(clientConfig, cfg)
}

// NewClientFromDefaultPath creates a new GoAgent client from default configuration paths.
// It searches for configuration files in standard locations.
// Returns new client instance or error.
func NewClientFromDefaultPath() (*Client, error) {
	return NewClientFromConfigPath("")
}
