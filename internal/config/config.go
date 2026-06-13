package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"goagentx/internal/errors"

	"gopkg.in/yaml.v3"
)

var allowedConfigDir string

// SetAllowedConfigDir sets the allowed directory for config files.
// This is a security measure to prevent path traversal attacks.
func SetAllowedConfigDir(dir string) {
	allowedConfigDir = dir
}

const (
	// DefaultTaskDistillationPrompt is the default prompt for task distillation
	DefaultTaskDistillationPrompt = "Please concisely summarize the key information for the following task, including: user needs, preferences, and budget range. Simply return a JSON object. {\"user_needs\": \"...\", \"preferences\": \"...\", \"budget\": \"...\"}"
)

// Config holds all configuration for the server.
type Config struct {
	Server     ServerConfig       `yaml:"server"`
	LLM        LLMConfig          `yaml:"llm"`
	Agents     AgentsConfig       `yaml:"agents"`
	Tools      ToolsConfig        `yaml:"tools"`
	Prompts    PromptsConfig      `yaml:"prompts"`
	Output     OutputConfig       `yaml:"output"`
	Validation ValidationConfig   `yaml:"validation"`
	Workflow   WorkflowConfig     `yaml:"workflow"`
	Storage    StorageConfig      `yaml:"storage"`
	Memory     MemoryConfig       `yaml:"memory"`
	MCP        MCPConfig          `yaml:"mcp"`
	Dashboard  DashboardAppConfig `yaml:"dashboard"`
}

// ServerConfig holds server configuration.
type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// LLMConfig holds LLM provider configuration.
type LLMConfig struct {
	Provider  string            `yaml:"provider"` // "openai", "ollama"
	APIKey    string            `yaml:"api_key"`
	BaseURL   string            `yaml:"base_url"`
	Model     string            `yaml:"model"`
	Timeout   int               `yaml:"timeout"`    // seconds
	MaxTokens int               `yaml:"max_tokens"` // max tokens for response
	Extra     map[string]string `yaml:"extra"`
}

// AgentsConfig holds agent configuration.
type AgentsConfig struct {
	Leader LeaderConfig     `yaml:"leader"`
	Sub    []SubAgentConfig `yaml:"sub"`
}

// LeaderConfig holds Leader Agent configuration.
type LeaderConfig struct {
	ID                 string `yaml:"id"`
	MaxSteps           int    `yaml:"max_steps"`
	MaxParallelTasks   int    `yaml:"max_parallel_tasks"`
	MaxValidationRetry int    `yaml:"max_validation_retry"`
	EnableCache        bool   `yaml:"enable_cache"`
}

// SubAgentConfig holds Sub Agent configuration.
type SubAgentConfig struct {
	ID         string   `yaml:"id"`
	Type       string   `yaml:"type"` // Agent type identifier (e.g., "top", "bottom", "custom")
	Category   string   `yaml:"category"`
	Triggers   []string `yaml:"triggers"` // Profile fields that trigger this agent
	MaxRetries int      `yaml:"max_retries"`
	Timeout    int      `yaml:"timeout"`  // seconds
	Model      string   `yaml:"model"`    // Model for this agent (overrides global LLM model)
	Provider   string   `yaml:"provider"` // Provider for this agent (overrides global LLM provider)
}

// PromptsConfig holds prompt templates.
type PromptsConfig struct {
	ProfileExtraction string `yaml:"profile_extraction"`
	Recommendation    string `yaml:"recommendation"`
	StyleAnalysis     string `yaml:"style_analysis"`
}

// OutputConfig holds output formatting configuration.
type OutputConfig struct {
	Format          string `yaml:"format"`           // "table", "json", "simple"
	ItemTemplate    string `yaml:"item_template"`    // Template for each item
	SummaryTemplate string `yaml:"summary_template"` // Template for summary
}

// Schema represents a JSON Schema for validation.
type Schema struct {
	Type        string            `yaml:"type,omitempty"`
	Properties  map[string]*Field `yaml:"properties,omitempty"`
	Items       *Field            `yaml:"items,omitempty"`
	Required    []string          `yaml:"required,omitempty"`
	Minimum     *float64          `yaml:"minimum,omitempty"`
	Maximum     *float64          `yaml:"maximum,omitempty"`
	MinLength   *int              `yaml:"min_length,omitempty"`
	MaxLength   *int              `yaml:"max_length,omitempty"`
	Pattern     string            `yaml:"pattern,omitempty"`
	Enum        []interface{}     `yaml:"enum,omitempty"`
	Nullable    bool              `yaml:"nullable,omitempty"`
	MinItems    *int              `yaml:"min_items,omitempty"`
	MaxItems    *int              `yaml:"max_items,omitempty"`
	Description string            `yaml:"description,omitempty"`
	Format      string            `yaml:"format,omitempty"`
}

// Field represents a field definition in schema.
type Field struct {
	Type        string            `yaml:"type,omitempty"`
	Properties  map[string]*Field `yaml:"properties,omitempty"`
	Items       *Field            `yaml:"items,omitempty"`
	Required    []string          `yaml:"required,omitempty"`
	Minimum     *float64          `yaml:"minimum,omitempty"`
	Maximum     *float64          `yaml:"maximum,omitempty"`
	MinLength   *int              `yaml:"min_length,omitempty"`
	MaxLength   *int              `yaml:"max_length,omitempty"`
	Pattern     string            `yaml:"pattern,omitempty"`
	Enum        []interface{}     `yaml:"enum,omitempty"`
	Nullable    bool              `yaml:"nullable,omitempty"`
	MinItems    *int              `yaml:"min_items,omitempty"`
	MaxItems    *int              `yaml:"max_items,omitempty"`
	Format      string            `yaml:"format,omitempty"`
	Description string            `yaml:"description,omitempty"`
}

// ValidationConfig holds validation configuration.
type ValidationConfig struct {
	Enabled      bool          `yaml:"enabled"`       // Enable/disable validation
	SchemaType   string        `yaml:"schema_type"`   // Schema type for validation (e.g., "default", "travel", "custom")
	RetryOnFail  bool          `yaml:"retry_on_fail"` // Retry LLM call on validation failure
	MaxRetries   int           `yaml:"max_retries"`   // Max retry attempts
	StrictMode   bool          `yaml:"strict_mode"`   // If true, fail on validation error
	CustomSchema *CustomSchema `yaml:"custom_schema"` // Custom JSON schema
}

// CustomSchema holds custom validation schema.
type CustomSchema struct {
	ResultSchema *SchemaConfig `yaml:"result_schema"` // Schema for RecommendResult
	ItemSchema   *SchemaConfig `yaml:"item_schema"`   // Schema for RecommendItem
}

// SchemaConfig holds JSON schema configuration.
type SchemaConfig struct {
	Type       string               `yaml:"type"`       // "object", "array"
	Properties map[string]*Property `yaml:"properties"` // Field definitions
	Required   []string             `yaml:"required"`   // Required fields
	MinItems   *int                 `yaml:"min_items"`  // For arrays
	MaxItems   *int                 `yaml:"max_items"`  // For arrays
}

// Property holds property definition for schema.
type Property struct {
	Type       string               `yaml:"type"`       // "string", "number", "integer", "boolean", "array", "object"
	MinLength  *int                 `yaml:"min_length"` // For strings
	MaxLength  *int                 `yaml:"max_length"` // For strings
	Minimum    *float64             `yaml:"minimum"`    // For numbers
	Maximum    *float64             `yaml:"maximum"`    // For numbers
	MinItems   *int                 `yaml:"min_items"`  // For arrays
	MaxItems   *int                 `yaml:"max_items"`  // For arrays
	Enum       []string             `yaml:"enum"`       // Enum values
	Format     string               `yaml:"format"`     // Format (uri, etc)
	Items      *Property            `yaml:"items"`      // For array items
	Properties map[string]*Property `yaml:"properties"` // For nested objects
}

// WorkflowConfig holds workflow configuration.
type WorkflowConfig struct {
	DefinitionPath string `yaml:"definition_path"` // path to workflow YAML
	AutoReload     bool   `yaml:"auto_reload"`
	ReloadInterval int    `yaml:"reload_interval"` // seconds
}

// StorageConfig holds storage configuration.
type StorageConfig struct {
	Enabled  bool           `yaml:"enabled"` // Enable storage
	Type     string         `yaml:"type"`    // "postgres", "sqlite"
	Host     string         `yaml:"host"`
	Port     int            `yaml:"port"`
	Username string         `yaml:"username"`
	Password string         `yaml:"password"`
	Database string         `yaml:"database"`
	SSLMode  string         `yaml:"ssl_mode"`
	PGVector PGVectorConfig `yaml:"pgvector"`
}

// PGVectorConfig holds pgvector specific configuration.
type PGVectorConfig struct {
	Enabled   bool   `yaml:"enabled"`    // Enable vector similarity search
	Dimension int    `yaml:"dimension"`  // Embedding dimension (default 1536 for OpenAI)
	TableName string `yaml:"table_name"` // Table name for vector storage
}

// MemoryConfig holds memory and distillation configuration.
type MemoryConfig struct {
	Enabled          bool          `yaml:"enabled"`           // Enable memory system
	SessionMemory    SessionConfig `yaml:"session"`           // Short-term session memory
	UserProfile      ProfileConfig `yaml:"user_profile"`      // Long-term user profile
	TaskDistillation DistillConfig `yaml:"task_distillation"` // Task distillation
}

// SessionConfig holds session memory configuration.
type SessionConfig struct {
	Enabled    bool `yaml:"enabled"`     // Enable session memory
	MaxHistory int  `yaml:"max_history"` // Max conversation turns to keep
}

// ProfileConfig holds user profile memory configuration.
type ProfileConfig struct {
	Enabled  bool   `yaml:"enabled"`   // Enable persistent user profile
	Storage  string `yaml:"storage"`   // "memory" or "postgres"
	VectorDB bool   `yaml:"vector_db"` // Store profile as vectors for similarity search
}

// DistillConfig holds task distillation configuration.
type DistillConfig struct {
	Enabled     bool   `yaml:"enabled"`      // Enable task distillation
	Storage     string `yaml:"storage"`      // Where to store distilled info: "memory" or "postgres"
	VectorStore bool   `yaml:"vector_store"` // Store distilled results as vectors in pgvector
	Prompt      string `yaml:"prompt"`       // Custom prompt for distillation
}

// Load reads configuration from a YAML file.
func Load(path string) (*Config, error) {
	// Security: validate path is within allowed directory
	if allowedConfigDir != "" {
		absPath, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("failed to get absolute path: %w", err)
		}
		absDir, err := filepath.Abs(allowedConfigDir)
		if err != nil {
			return nil, fmt.Errorf("failed to get absolute directory: %w", err)
		}
		if !strings.HasPrefix(absPath, absDir) {
			return nil, fmt.Errorf("config path %s is outside allowed directory %s", path, allowedConfigDir)
		}
	}

	data, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// Set defaults
	cfg.setDefaults()

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, errors.Wrap(err, "configuration validation failed")
	}

	return &cfg, nil
}

// LoadFromEnv loads configuration from environment variables.
// Environment variables override YAML config.
func LoadFromEnv(cfg *Config) error {
	if v := os.Getenv("SERVER_HOST"); v != "" {
		cfg.Server.Host = v
	}
	if v := os.Getenv("SERVER_PORT"); v != "" {
		var port int
		if _, err := fmt.Sscanf(v, "%d", &port); err == nil {
			cfg.Server.Port = port
		}
	}
	if v := os.Getenv("LLM_API_KEY"); v != "" {
		cfg.LLM.APIKey = v
	}
	// Also support OPENROUTER_API_KEY as alternative
	if v := os.Getenv("OPENROUTER_API_KEY"); v != "" && cfg.LLM.APIKey == "" {
		cfg.LLM.APIKey = v
	}
	if v := os.Getenv("LLM_PROVIDER"); v != "" {
		cfg.LLM.Provider = v
	}
	if v := os.Getenv("LLM_BASE_URL"); v != "" {
		cfg.LLM.BaseURL = v
	}
	if v := os.Getenv("LLM_MODEL"); v != "" {
		cfg.LLM.Model = v
	}
	// Storage environment variables
	if v := os.Getenv("DB_HOST"); v != "" {
		cfg.Storage.Host = v
	}
	if v := os.Getenv("DB_PORT"); v != "" {
		var port int
		if _, err := fmt.Sscanf(v, "%d", &port); err == nil {
			cfg.Storage.Port = port
		}
	}
	if v := os.Getenv("DB_USERNAME"); v != "" {
		cfg.Storage.Username = v
	}
	if v := os.Getenv("DB_PASSWORD"); v != "" {
		cfg.Storage.Password = v
	}
	if v := os.Getenv("DB_DATABASE"); v != "" {
		cfg.Storage.Database = v
	}

	return nil
}

func (c *Config) setDefaults() {
	if c.Server.Host == "" {
		c.Server.Host = "localhost"
	}
	if c.Server.Port == 0 {
		c.Server.Port = 8080
	}
	if c.LLM.Provider == "" {
		c.LLM.Provider = "ollama"
	}
	if c.LLM.Model == "" {
		c.LLM.Model = "llama3.2"
	}
	if c.LLM.Timeout == 0 {
		c.LLM.Timeout = 60
	}
	if c.LLM.MaxTokens == 0 {
		c.LLM.MaxTokens = 4096
	}
	if c.Agents.Leader.MaxSteps == 0 {
		c.Agents.Leader.MaxSteps = 10
	}
	if c.Agents.Leader.MaxParallelTasks == 0 {
		c.Agents.Leader.MaxParallelTasks = 5
	}
	if c.Agents.Leader.MaxValidationRetry == 0 {
		c.Agents.Leader.MaxValidationRetry = 3
	}
	if c.Output.Format == "" {
		c.Output.Format = "simple"
	}
	if c.Output.ItemTemplate == "" {
		c.Output.ItemTemplate = "{{.ItemID}}: {{.Name}} ({{.Price}})"
	}
	if c.Output.SummaryTemplate == "" {
		c.Output.SummaryTemplate = "Got {{.Count}} recommendations"
	}
	// Storage defaults
	if c.Storage.Type == "" {
		c.Storage.Type = "postgres"
	}
	if c.Storage.Port == 0 {
		c.Storage.Port = 5432
	}
	if c.Storage.PGVector.Dimension == 0 {
		c.Storage.PGVector.Dimension = 1536
	}
	if c.Storage.PGVector.TableName == "" {
		c.Storage.PGVector.TableName = "embeddings"
	}
	// Memory defaults
	if c.Memory.SessionMemory.MaxHistory == 0 {
		c.Memory.SessionMemory.MaxHistory = 50
	}
	if c.Memory.UserProfile.Storage == "" {
		c.Memory.UserProfile.Storage = "memory"
	}
	if c.Memory.TaskDistillation.Prompt == "" {
		c.Memory.TaskDistillation.Prompt = DefaultTaskDistillationPrompt
	}
	// Validation defaults
	if c.Validation.SchemaType == "" {
		c.Validation.SchemaType = "default" // "default", "travel", "custom"
	}
	if c.Validation.MaxRetries == 0 {
		c.Validation.MaxRetries = 3
	}
	// Workflow defaults
	if c.Workflow.ReloadInterval == 0 && c.Workflow.AutoReload {
		c.Workflow.ReloadInterval = 30 // seconds
	}
}

// Validate validates the configuration values.
func (c *Config) Validate() error {
	// Validate server configuration
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d, must be between 1 and 65535", c.Server.Port)
	}

	// Validate LLM configuration
	if c.LLM.Timeout < 1 {
		return fmt.Errorf("invalid LLM timeout: %d, must be positive", c.LLM.Timeout)
	}
	if c.LLM.MaxTokens < 1 {
		return fmt.Errorf("invalid LLM max tokens: %d, must be positive", c.LLM.MaxTokens)
	}
	if c.LLM.Provider != "openai" && c.LLM.Provider != "ollama" && c.LLM.Provider != "openrouter" {
		return fmt.Errorf("invalid LLM provider: %s, must be 'openai', 'ollama', or 'openrouter'", c.LLM.Provider)
	}

	// Validate agents configuration
	if c.Agents.Leader.MaxSteps < 1 {
		return fmt.Errorf("invalid leader max steps: %d, must be positive", c.Agents.Leader.MaxSteps)
	}
	if c.Agents.Leader.MaxParallelTasks < 1 {
		return fmt.Errorf("invalid leader max parallel tasks: %d, must be positive", c.Agents.Leader.MaxParallelTasks)
	}
	if c.Agents.Leader.MaxValidationRetry < 0 {
		return fmt.Errorf("invalid leader max validation retry: %d, must be non-negative", c.Agents.Leader.MaxValidationRetry)
	}

	// Validate sub-agent configurations
	for i, subAgent := range c.Agents.Sub {
		if subAgent.ID == "" {
			return fmt.Errorf("sub-agent %d: ID cannot be empty", i)
		}
		if subAgent.Type == "" {
			return fmt.Errorf("sub-agent %d: Type cannot be empty", i)
		}
		if subAgent.Timeout < 1 {
			return fmt.Errorf("sub-agent %d: timeout must be positive", i)
		}
		if subAgent.MaxRetries < 0 {
			return fmt.Errorf("sub-agent %d: max retries must be non-negative", i)
		}
	}

	// Validate output configuration
	validFormats := map[string]bool{"table": true, "json": true, "simple": true}
	if !validFormats[c.Output.Format] {
		return fmt.Errorf("invalid output format: %s, must be 'table', 'json', or 'simple'", c.Output.Format)
	}

	// Validate validation configuration
	if c.Validation.MaxRetries < 0 {
		return fmt.Errorf("invalid validation max retries: %d, must be non-negative", c.Validation.MaxRetries)
	}

	// Validate storage configuration if enabled
	if c.Storage.Enabled {
		if c.Storage.Host == "" {
			return fmt.Errorf("storage enabled but host is empty")
		}
		if c.Storage.Port < 1 || c.Storage.Port > 65535 {
			return fmt.Errorf("invalid storage port: %d, must be between 1 and 65535", c.Storage.Port)
		}
		if c.Storage.Database == "" {
			return fmt.Errorf("storage enabled but database name is empty")
		}
	}

	// Validate memory configuration
	if c.Memory.SessionMemory.MaxHistory < 0 {
		return fmt.Errorf("invalid session memory max history: %d, must be non-negative", c.Memory.SessionMemory.MaxHistory)
	}

	return nil
}

// ToolsConfig holds tool configuration for agents.
type ToolsConfig struct {
	Defaults []string                   `yaml:"defaults"` // Default tools for all agents
	Agents   map[string]AgentToolConfig `yaml:"agents"`   // Agent-specific tool assignments
}

// AgentToolConfig holds tool configuration for a specific agent.
type AgentToolConfig struct {
	Name         string   `yaml:"name"`          // Agent display name
	Description  string   `yaml:"description"`   // Agent description
	SystemPrompt string   `yaml:"system_prompt"` // Custom system prompt for this agent
	Tools        []string `yaml:"tools"`         // List of tool names this agent can use
}

// MCPConfig holds MCP client configuration.
type MCPConfig struct {
	Servers []MCPServerEntry `yaml:"servers"`
}

// MCPServerEntry holds configuration for a single MCP server.
type MCPServerEntry struct {
	Name      string         `yaml:"name"`
	Enabled   bool           `yaml:"enabled"`
	AutoStart bool           `yaml:"auto_start"`
	Timeout   int            `yaml:"timeout"` // seconds
	Transport TransportEntry `yaml:"transport"`
}

// TransportEntry holds transport configuration.
type TransportEntry struct {
	Type  string      `yaml:"type"` // "stdio" or "sse"
	Stdio *StdioEntry `yaml:"stdio,omitempty"`
	SSE   *SSEEntry   `yaml:"sse,omitempty"`
}

// StdioEntry holds stdio transport configuration.
type StdioEntry struct {
	Command string            `yaml:"command"`
	Args    []string          `yaml:"args"`
	Env     map[string]string `yaml:"env"`
	WorkDir string            `yaml:"work_dir"`
}

// SSEEntry holds SSE transport configuration.
type SSEEntry struct {
	URL     string            `yaml:"url"`
	Headers map[string]string `yaml:"headers"`
	Timeout int               `yaml:"timeout"` // seconds
}

// DashboardAppConfig holds dashboard configuration.
type DashboardAppConfig struct {
	Addr           string `yaml:"addr"`
	EnableAuth     bool   `yaml:"enable_auth"`
	WSPingInterval int    `yaml:"ws_ping_interval"` // seconds
}
