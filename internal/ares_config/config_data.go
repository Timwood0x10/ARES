package ares_config

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
	Password string         `yaml:"password" json:"-"` // json:"-" prevents accidental leak via JSON serialization
	Database string         `yaml:"database"`
	SSLMode  string         `yaml:"ssl_mode"`
	PGVector PGVectorConfig `yaml:"pgvector"`

	// EventsRetentionDays bounds the PG events table's growth: when > 0,
	// the maintenance worker periodically deletes event rows older than
	// this many days. Default 0 = keep forever. PG mode has no
	// round_N.json archive and no compaction trim — the table IS the
	// durable history — so this cleaner is the only bound on growth.
	// WARNING: deleting events narrows the task fabric's cross-restart
	// restore window to the retention horizon; choose it well past any
	// plausible recovery horizon.
	EventsRetentionDays int `yaml:"events_retention_days"`
}

// PGVectorConfig holds pgvector specific configuration.
type PGVectorConfig struct {
	Enabled   bool   `yaml:"enabled"`    // Enable vector similarity search
	Dimension int    `yaml:"dimension"`  // Embedding dimension (default 1536 for OpenAI)
	TableName string `yaml:"table_name"` // Table name for vector storage
}

// MemoryConfig holds memory and distillation configuration.
type MemoryConfig struct {
	// Enabled enables the memory system. It is *bool so an unset field means
	// "default on": a minimal config that only specifies the LLM endpoint still
	// gets memory (the leader contract requires it), while an explicit
	// `memory.enabled: false` opts out. IsEnabled reports the effective value.
	Enabled          *bool         `yaml:"enabled"`           // nil/true = enabled (default); false = disabled.
	SessionMemory    SessionConfig `yaml:"session"`           // Short-term session memory
	UserProfile      ProfileConfig `yaml:"user_profile"`      // Long-term user profile
	TaskDistillation DistillConfig `yaml:"task_distillation"` // Task distillation

	// MaxHistory is the maximum number of turns to keep in the closed-loop
	// memory context. Defaults to 10 when zero. This is independent of
	// SessionMemory.MaxHistory (which controls the session store window).
	MaxHistory int `yaml:"max_history"`

	// EnableDistillation mirrors v0.2.4 memory.enable_distillation: when true,
	// the closed loop distills task experiences into long-term memory.
	// EnableDistillation gates the experience distillation wiring.
	// Pointer tri-state: nil (unset) defaults to true — an explicit
	// `enable_distillation: false` in YAML is the only way to disable.
	EnableDistillation *bool `yaml:"enable_distillation"`

	// DistillationThreshold is the number of conversation rounds that must
	// accumulate before distillation fires. Defaults to 3 when zero (only
	// applied when EnableDistillation is true). Mirrors v0.2.4
	// memory.distillation_threshold semantics.
	DistillationThreshold int `yaml:"distillation_threshold"`

	// EnableRAG enables retrieval-augmented generation: past experiences and
	// distilled memories are retrieved and injected into the LLM prompt.
	// Default: false (opt-in).
	EnableRAG bool `yaml:"enable_rag"`

	// RAGTopK is the maximum number of retrieved snippets to inject.
	// Defaults to 5 when zero (only applied when EnableRAG is true).
	RAGTopK int `yaml:"rag_top_k"`

	// RAGMinScore is the minimum similarity score for a retrieved snippet to
	// be included. Snippets below this threshold are filtered out.
	// Defaults to 0.4 when zero (only applied when EnableRAG is true).
	RAGMinScore float64 `yaml:"rag_min_score"`

	// Archive holds round-archive settings. Enabled by default: a nil or true
	// Enabled field turns archiving on; explicit false opts out.
	Archive ArchiveConfig `yaml:"archive"`
}

// DistillationEnabled reports whether distillation should be wired:
// unset (nil) defaults to true; only an explicit YAML false disables it.
func (m *MemoryConfig) DistillationEnabled() bool {
	return m.EnableDistillation == nil || *m.EnableDistillation
}

// ArchiveConfig holds round-archive settings. Enabled by default: a nil or
// true Enabled field turns archiving on; explicit false opts out. A plain
// bool cannot distinguish "unset" from false, so Enabled is *bool to allow
// operators to disable with `enabled: false`.
type ArchiveConfig struct {
	Enabled   *bool  `yaml:"enabled"`    // nil/true = enabled (default); false = disabled.
	Dir       string `yaml:"dir"`        // Default ".context/rounds".
	MaxRounds int    `yaml:"max_rounds"` // Default 200.
}

// IsEnabled reports whether memory is active. nil is treated as enabled
// (default-on) so a minimal config that omits the memory section still gets
// the memory component, while an explicit `memory.enabled: false` opts out.
func (m MemoryConfig) IsEnabled() bool { return m.Enabled == nil || *m.Enabled }

// IsEnabled reports whether archiving is active. nil is treated as enabled
// (default-on) so callers need not dereference the pointer.
func (a ArchiveConfig) IsEnabled() bool { return a.Enabled == nil || *a.Enabled }

// KnowledgeConfig holds configuration for the optional AKG (Agent Knowledge
// Graph) retrieval integration. When RetrievalEnabled is false (the default),
// knowledge retrieval is not wired and the closed loop runs without AKG
// injection, preserving prior behavior.
type KnowledgeConfig struct {
	// RetrievalEnabled activates AKG knowledge retrieval. Default: false.
	RetrievalEnabled bool `yaml:"retrieval_enabled"`

	// TopK is the maximum number of knowledge snippets to retrieve.
	// Defaults to 5 when zero (only applied when RetrievalEnabled is true).
	TopK int `yaml:"top_k"`

	// MinScore is the minimum similarity score for a retrieved snippet to
	// be included. Snippets below this threshold are filtered out.
	// Defaults to 0.4 when zero (only applied when RetrievalEnabled is true).
	MinScore float64 `yaml:"min_score"`
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
	// Threshold is the number of conversation rounds that accumulate before
	// distillation fires in the event subscription path. 0 preserves legacy
	// ungated behaviour. Mirrors v0.2.4 examples/knowledge-base config.yaml
	// distillation_threshold semantics.
	Threshold int `yaml:"threshold"`
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
