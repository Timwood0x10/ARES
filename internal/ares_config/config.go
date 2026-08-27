// package config - provides configuration loading and validation for ares.
package ares_config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/evolution/deployment"

	"gopkg.in/yaml.v3"
)

// allowedConfigDir and its guard mutex restrict where Load may read config
// files from (path-traversal protection). Guarded by a RWMutex so SetAllowed
// can race safely with concurrent Load calls (e.g. hot-reload watchers).
var (
	allowedConfigDirMu sync.RWMutex
	allowedConfigDir   string
)

// SetAllowedConfigDir sets the allowed directory for config files.
// This is a security measure to prevent path traversal attacks.
func SetAllowedConfigDir(dir string) {
	allowedConfigDirMu.Lock()
	defer allowedConfigDirMu.Unlock()
	allowedConfigDir = dir
}

const (
	// DefaultTaskDistillationPrompt is the default prompt for task distillation
	DefaultTaskDistillationPrompt = "Please concisely summarize the key information for the following task, including: user needs, preferences, and budget range. Simply return a JSON object. {\"user_needs\": \"...\", \"preferences\": \"...\", \"budget\": \"...\"}"

	// DefaultRecommendationPrompt is the default recommendation template used
	// when the config omits prompts.recommendation. {{.input}} is the original
	// task input (planner writes it to the task payload as task_desc) and
	// {{.Category}} is the sub-agent type; the executor supplies both.
	DefaultRecommendationPrompt = "You are a {{.Category}} specialist. Analyze the following task and recommend the best items/actions with clear reasoning.\n\nTask: {{.input}}\n\nReturn a structured list of recommendations with name, description and match reason."

	// DefaultProfileExtractionPrompt is the default template used when the
	// config omits prompts.profile_extraction.
	DefaultProfileExtractionPrompt = "Extract the user profile (preferences, style, budget) from: {{.input}}"

	// DefaultStyleAnalysisPrompt is the default template used when the config
	// omits prompts.style_analysis.
	DefaultStyleAnalysisPrompt = "Analyze the style of: {{.input}}"
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
	Knowledge  KnowledgeConfig    `yaml:"knowledge"`
	MCP        MCPConfig          `yaml:"mcp"`
	Dashboard  DashboardAppConfig `yaml:"dashboard"`
	Evolution  EvolutionConfig    `yaml:"evolution"`
	Embedding  EmbeddingConfig    `yaml:"embedding"`
	Discovery  DiscoveryConfig    `yaml:"discovery"`
	Kernel     KernelConfig       `yaml:"kernel"`
	Security   SecurityConfig     `yaml:"security"`
}

// KernelConfig controls the dual-track dispatch kernel (ares-runtime.md P4 D4:
// parallel + feature flag gradual cutover). When Policy is "taskfabric" (the
// default), the kernel flips to the Task Fabric path: the shadow scorer is
// replaced by the real Create→Schedule→Acquire→RunQuantum executor, shadow
// mode is disabled (to avoid double execution) and the kernelScheduler starts
// driving ready tasks. When Policy is "legacy", the leader path stays live and
// the Task Fabric path runs in shadow mode (scores every task, Mismatches
// observable). The flip is safe to run at startup; flipKernelToTaskFabric is
// the idempotent live mid-run variant.
type KernelConfig struct {
	// Policy selects the active dispatch policy: "taskfabric" (default) or
	// "legacy". Empty selects the default ("taskfabric").
	Policy string `yaml:"policy"`
	// PollInterval is the kernelScheduler drain interval (default 500ms).
	PollInterval string `yaml:"poll_interval"`
	// Resources is the P5 resource budget applied to the Agent Fabric
	// (name → max total across live agents, e.g. {"cpu": 8, "memory": 8192}).
	// Spawn rejects claims that exceed the remaining budget
	// (agentfabric.ErrResourceQuotaExceeded). Empty disables enforcement.
	Resources map[string]float64 `yaml:"resources"`
	// MaxRestarts bounds agent restart attempts after a crash in the
	// event-driven recovery loop (0 = aresrecovery default of 5).
	MaxRestarts int `yaml:"max_restarts"`
	// QuotaApplyInterval is how often the evolution-aware quota manager
	// pushes the resource budget into the Agent Fabric (default "1m").
	// Parsed with time.ParseDuration; empty/invalid falls back to the default.
	QuotaApplyInterval string `yaml:"quota_apply_interval"`
	// QuotaApplyTimeout bounds each quota policy application (default "30s").
	// A hung policy store must not stall the quota loop (C1).
	QuotaApplyTimeout string `yaml:"quota_apply_timeout"`
	// RecoverySweepInterval is how often the recovery loop sweeps TTL-based
	// lease expiry (default "1s").
	RecoverySweepInterval string `yaml:"recovery_sweep_interval"`
	// RecoverySweepTimeout bounds each recovery sweep (default "30s"). A hung
	// store must neither block the recovery loop nor pile up sweeps (C3).
	RecoverySweepTimeout string `yaml:"recovery_sweep_timeout"`
	// DispatchTimeout bounds how long kernelTaskDispatcher.Dispatch waits for
	// a submitted task's completion event before reporting it failed
	// (default "300s",  mirrors the legacy dispatcher timeout).
	DispatchTimeout string `yaml:"dispatch_timeout"`
	// EvolutionApplyInterval is how often the evolution population adapter
	// applies the agent population policy (spawn/retire) to the Agent Fabric
	// (default "1m"). Parsed with time.ParseDuration; empty/invalid falls back
	// to the default.
	EvolutionApplyInterval string `yaml:"evolution_apply_interval"`
	// EvolutionApplyTimeout bounds each population policy application
	// (default "30s"). A hung policy store must not stall the loop.
	EvolutionApplyTimeout string `yaml:"evolution_apply_timeout"`
	// LeaseTTL is the task-lease duration granted by the kernel scheduler
	// (e.g. "5m" default, "45s" for snappy chaos/recovery demos). Empty keeps
	// the scheduler default.
	LeaseTTL string `yaml:"lease_ttl"`
	// Chaos controls the fault injection subsystem (REVIEW #12). By default
	// chaos runs in "shadow" mode — a scratch Sandbox verifies recovery
	// without touching production agents. "live" mode (requires
	// allow_live=true) enables real agent kill/suspend; it is dangerous
	// and intended only for dedicated chaos testing environments.
	Chaos ChaosConfig `yaml:"chaos"`
}

// ChaosConfig configures the chaos fault injection subsystem.
// Default is zero-impact (shadow sandbox): recovery is verified on a
// scratch fabric, production agents are never touched.
type ChaosConfig struct {
	// Enabled is the master switch. When false (default), no chaos
	// subsystem is constructed at all.
	Enabled bool `yaml:"enabled"`
	// Mode selects shadow (default) or live. In shadow mode, a scratch
	// Sandbox runs Simulate/Replay to verify recovery offline. In live
	// mode, real agents are killed/suspended — requires allow_live=true.
	Mode string `yaml:"mode"`
	// AllowLive is the secondary confirmation for live mode. Live chaos
	// is only active when mode=live AND allow_live=true. This prevents
	// accidental misconfiguration from enabling destructive chaos.
	AllowLive bool `yaml:"allow_live"`
	// Interval is the chaos injection / shadow verification period
	// (default "5m").
	Interval string `yaml:"interval"`
	// RatePerMin limits live injections per minute (default 2).
	RatePerMin int `yaml:"rate_per_min"`
	// Cooldown is the per-agent cooldown after an injection (default "10m").
	Cooldown string `yaml:"cooldown"`
	// PauseDuringGA, when true, pauses live injections while a GA generation
	// is in flight. The generation window is probed via the wired evolution
	// system (DreamCycle / population adapter), so live mode honors this
	// field at injection time.
	PauseDuringGA bool `yaml:"pause_during_ga"`
	// EligibleCapabilities is the target whitelist for live injections
	// (REVIEW #12 Phase 2): only agents declaring at least one of these
	// capabilities may be injected. An empty list disables live injection
	// entirely — it must be populated explicitly before any agent is a valid
	// target, preventing accidental broad targeting.
	EligibleCapabilities []string `yaml:"eligible_capabilities"`
	// StopToken is the bearer token required by the chaos stop endpoint
	// (POST /api/chaos/stop). Live chaos is only armed when StopToken is
	// non-empty; an empty token keeps the stop endpoint disabled along with
	// live mode.
	StopToken string `yaml:"stop_token"`
}

// DiscoveryConfig configures the optional service discovery engine that
// auto-detects MCP servers and agent runtimes from local config files
// (Claude, Cursor, VSCode, ARES) and the system PATH. When Enabled is
// false (the default), discovery is not wired and the discovery packages
// remain unused, preserving prior behavior.
type DiscoveryConfig struct {
	Enabled    bool          `yaml:"enabled"`
	Interval   time.Duration `yaml:"interval"`
	ProjectDir string        `yaml:"project_dir"`
}

// EmbeddingConfig holds configuration for the embedding client used by
// experience distillation. Distillation requires an embedding client to
// vectorize distilled experiences; the rest of the system can run without it.
// When Enabled is false, experience distillation is not wired (graceful skip).
type EmbeddingConfig struct {
	Enabled   bool   `yaml:"enabled"`    // Enable embedding client + experience distillation
	BaseURL   string `yaml:"base_url"`   // Embedding service base URL
	Model     string `yaml:"model"`      // Embedding model name
	RedisAddr string `yaml:"redis_addr"` // Optional Redis for embedding cache (empty = no cache)
	Dimension int    `yaml:"dimension"`  // Vector dimension (0 = use model default)
	Timeout   int    `yaml:"timeout"`    // Request timeout in seconds (0 = 30s default)
}

// ServerConfig holds server configuration.
type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// SecurityConfig holds JWT authentication and RBAC settings for the HTTP
// surfaces (monitoring console, dashboard, arena). When JWTSecret is empty
// every protected endpoint stays deny-by-default (401) — the same posture as
// the legacy ARES_API_KEY, so enabling JWT cannot accidentally open a
// destructive endpoint. JWTSecret must be kept out of YAML config committed
// to VCS; prefer the ARES_JWT_SECRET environment variable.
type SecurityConfig struct {
	// JWTSecret is the HS256 signing key for issued tokens. Empty disables
	// JWT (deny all protected endpoints).
	JWTSecret string `yaml:"jwt_secret"`
	// JWTExpiry is the token lifetime, parsed with time.ParseDuration
	// (default "24h"). Empty/invalid falls back to the default.
	JWTExpiry string `yaml:"jwt_expiry"`
	// AuthEnabled gates whether the JWT middleware is mounted at all. When
	// true and JWTSecret is empty, protected endpoints deny (misconfig is
	// safer than open). Default false preserves the pre-JWT behavior for
	// read-only surfaces; destructive endpoints always require auth.
	AuthEnabled bool `yaml:"auth_enabled"`
}

// LLMConfig holds LLM provider configuration.
type LLMConfig struct {
	Provider        string            `yaml:"provider"` // "openai", "ollama"
	APIKey          string            `yaml:"api_key"`
	BaseURL         string            `yaml:"base_url"`
	Model           string            `yaml:"model"`
	Timeout         int               `yaml:"timeout"`           // seconds
	MaxTokens       int               `yaml:"max_tokens"`        // max tokens for response
	MaxPromptLength int               `yaml:"max_prompt_length"` // max prompt characters (0 = default 8192)
	Extra           map[string]string `yaml:"extra"`
	ScorerAPIRate   float64           `yaml:"scorer_api_rate,omitempty"`  // requests per second for LLM scorer
	ScorerAPIBurst  int               `yaml:"scorer_api_burst,omitempty"` // burst size for LLM scorer
	Fallbacks       []LLMConfig       `yaml:"fallbacks,omitempty"`        // fallback LLM providers for scoring failover
}

// AgentsConfig holds agent configuration.
type AgentsConfig struct {
	// Peers is the flat capability-agent population (aresos-agentos-plan C1:
	// 平铺结构作为默认). Each entry is an equal peer spawned into the Agent
	// Fabric with its execution body; the kernel scheduler selects among them
	// by capability. When Peers is non-empty it is the authoritative agent
	// source (createPeerAgents reads it); Sub remains as the legacy fallback
	// (each sub's single Type becomes its only capability) so pre-C1 configs
	// keep working.
	Peers []PeerAgentConfig `yaml:"peers"`
	// Sub is the LEGACY leader/sub-era sub-agent list, normalized into peers
	// when no peers are configured (see normalizedPeers). The leader side of
	// the legacy structure was removed in v0.4.0; an ignored `leader:` key in
	// an old config file is harmless.
	Sub []SubAgentConfig `yaml:"sub"`
}

// PeerAgentConfig is one flat peer agent (C1 平铺结构).
type PeerAgentConfig struct {
	// ID is the agent's unique identity (also its scheduler executor id and
	// fabric agent id).
	ID string `yaml:"id"`
	// Capabilities is the agent's declared capability set. The first entry is
	// the primary capability (used as the sub-executor Type); the full set is
	// offered to the scheduler's candidate scorer so a task matching ANY
	// capability can be scheduled to it.
	Capabilities []string `yaml:"capabilities"`
	// Priority is the scheduling priority (>= 0; 0 = normal). It mirrors
	// OS-thread priority: the kernel scheduler boosts higher-priority agents
	// when choosing among capable candidates (B2).
	Priority float64 `yaml:"priority"`
	// MaxToolRounds caps the tool-calling iterations per task execution
	// (default 5 when 0/unset).
	MaxToolRounds int `yaml:"max_tool_rounds"`
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
	// Dependencies lists other sub-agent IDs whose tasks must COMPLETE before
	// this sub-agent's task runs (Task Fabric DAG gate, ares-runtime.md §9).
	Dependencies []string `yaml:"dependencies"`
	// Priority is the scheduling priority of this sub-agent (>= 0; 0 =
	// normal). It mirrors OS-thread priority: the kernel scheduler boosts
	// higher-priority agents when choosing among capable candidates. Read by
	// the kernel wiring (B2: thread priority) into the shared load tracker.
	Priority float64 `yaml:"priority"`
	// MaxToolRounds caps the tool-calling iterations per task execution for
	// this agent (default 5 when 0/unset — see sub.defaultMaxToolRounds).
	// Exposed so operators can tune tool-loop depth without code changes
	// (code_rules_v2: config over magic constants).
	MaxToolRounds int `yaml:"max_tool_rounds"`
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
	Password string         `yaml:"password" json:"-"` // json:"-" prevents accidental leak via JSON serialization
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
	EnableDistillation bool `yaml:"enable_distillation"`

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

// Load reads configuration from a YAML file.
func Load(path string) (*Config, error) {
	// Security: validate path is within allowed directory using filepath.Rel
	// to correctly reject path-traversal attempts (e.g. "/allowed/../secret").
	// Snapshot under the read lock so SetAllowedConfigDir can race with Load.
	allowedConfigDirMu.RLock()
	dir := allowedConfigDir
	allowedConfigDirMu.RUnlock()
	if dir != "" {
		absPath, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("failed to get absolute path: %w", err)
		}
		absDir, err := filepath.Abs(dir)
		if err != nil {
			return nil, fmt.Errorf("failed to get absolute directory: %w", err)
		}
		rel, err := filepath.Rel(absDir, absPath)
		if err != nil {
			return nil, fmt.Errorf("failed to compute relative path: %w", err)
		}
		// Reject paths that escape the allowed directory via ".." prefix.
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("config path %s is outside allowed directory %s", path, dir)
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
	// Security environment variables. JWTSecret prefers ARES_JWT_SECRET and
	// must not be stored in committed YAML; ARES_AUTH_ENABLED toggles the
	// middleware (any non-empty value enables).
	if v := os.Getenv("ARES_JWT_SECRET"); v != "" {
		cfg.Security.JWTSecret = v
	}
	if v := os.Getenv("ARES_AUTH_ENABLED"); v != "" {
		cfg.Security.AuthEnabled = true
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

// EvolutionConfig holds genetic algorithm evolution system configuration.
// When Enabled is false (default), the entire evolution pipeline is skipped
// during bootstrap — no scheduler, no dream cycle, no GA overhead.
// This makes the genome/mutation libraries available as pure utilities while
// keeping the expensive evolution orchestration opt-in.
// EvolutionConfig controls the GA evolution pipeline.
// All fields have sensible defaults — only set what you need to override.
type EvolutionConfig struct {
	// Enabled activates the full evolution pipeline (scheduler + dream cycle + GA).
	// Default: false — must be explicitly enabled in YAML.
	Enabled bool `yaml:"enabled"`

	// PopulationSize is the number of agents in each GA generation.
	// Larger = more diverse search, slower per generation. Default: 20.
	PopulationSize int `yaml:"population_size"`

	// EliteCount is the number of top agents preserved unchanged per generation.
	// Prevents loss of the best solutions. Default: 2.
	EliteCount int `yaml:"elite_count"`

	// SurvivalRate is the fraction of population that survives selection [0.0, 1.0].
	// Higher = more diversity, slower convergence. Default: 0.6.
	SurvivalRate float64 `yaml:"survival_rate"`

	// MutationRate is the base probability of gene mutation per agent.
	// Higher = more exploration, less stability. Default: 0.2.
	MutationRate float64 `yaml:"mutation_rate"`

	// MinMutationRate is the floor for adaptive mutation rate decay.
	// Prevents mutation from dropping too low. Default: 0.05.
	MinMutationRate float64 `yaml:"min_mutation_rate"`

	// MaxMutationRate is the ceiling for adaptive mutation rate bursts.
	// Prevents excessive random search. Default: 0.5.
	MaxMutationRate float64 `yaml:"max_mutation_rate"`

	// Generations is the maximum number of GA generations to run.
	// 0 means unlimited (run until manually stopped). Default: 15.
	Generations int `yaml:"generations"`

	// BreedingPoolRatio is the fraction of population used as crossover parents.
	// Higher = more offspring from top individuals. Default: 0.5.
	BreedingPoolRatio float64 `yaml:"breeding_pool_ratio"`

	// MinInterval is the minimum time between evolution scheduler runs.
	// Format: duration string (e.g., "5m", "10m"). Default: "5m".
	MinInterval string `yaml:"min_interval"`

	// SelectionStrategy selects the parent selection algorithm.
	// Supported: "tournament", "rank", "roulette", "sus", "truncation", "random".
	// Default: "tournament".
	SelectionStrategy string `yaml:"selection_strategy"`

	// TournamentSize is the number of competitors per tournament selection round.
	// Larger = stronger selection pressure (faster convergence, less diversity).
	// Only used when selection_strategy is "tournament". Default: 3.
	TournamentSize int `yaml:"tournament_size"`

	// CrossoverType selects the parameter recombination strategy.
	// Supported: "uniform", "two_point", "segment".
	// Default: "uniform".
	CrossoverType string `yaml:"crossover_type"`

	// TargetFitness stops evolution when the best fitness reaches this threshold.
	// 0 means no target (run until Generations). Scale: 0-100.
	TargetFitness float64 `yaml:"target_fitness"`

	// SteadyState enables steady-state GA: each generation replaces only a fraction
	// of the population instead of full generational replacement.
	// Default: false.
	SteadyState bool `yaml:"steady_state"`

	// SteadyStateReplaceRate is the fraction of population replaced each generation
	// in steady-state mode [0.0, 1.0]. Only used when steady_state is true.
	// Default: 0.3.
	SteadyStateReplaceRate float64 `yaml:"steady_state_replace_rate"`

	// Deployment configures safe promotion of evolution patches to the live
	// runtime via the DeploymentPipeline. Disabled by default — when enabled,
	// accepted patches are promoted through staging → live instead of applied
	// directly by the Coordinator.
	Deployment deployment.DeploymentConfig `yaml:"deployment"`

	// LLMScoring configures the opt-in LLM-backed strategy scorer for the
	// GA evolution system. When Enabled is false (the default), evolution
	// uses the constant baseline scorer, preserving prior behavior.
	LLMScoring LLMScoringConfig `yaml:"llm_scoring"`
}

// LLMScoringConfig configures the opt-in LLM-backed strategy scorer for the
// GA evolution system. When Enabled is false (the default), evolution uses the
// constant baseline scorer (ConstantScorer(50.0)), preserving prior behavior
// and avoiding uncontrolled LLM API costs during tests.
type LLMScoringConfig struct {
	// Enabled activates the LLM-backed scorer. When false, the GA evolution
	// system falls back to the constant baseline scorer. Default: false.
	Enabled bool `yaml:"enabled"`

	// Seed enables deterministic LLM scoring when > 0. Forces the LLM
	// temperature to 0 and embeds the seed in the evaluation prompt so
	// identical strategies always receive the same score. Default: 0
	// (non-deterministic).
	Seed int64 `yaml:"seed"`

	// MaxCallsPerGeneration caps the number of LLM scoring calls per
	// generation to control cost. When the budget is exhausted, remaining
	// strategies are scored by the deterministic heuristic fallback.
	// Default: 100 when zero.
	MaxCallsPerGeneration int `yaml:"max_calls_per_generation"`
}
