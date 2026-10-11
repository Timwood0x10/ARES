package ares_config

// StorageConfig holds storage configuration.
type StorageConfig struct {
	Enabled  bool   `yaml:"enabled"` // Enable storage
	Type     string `yaml:"type"`    // "postgres", "sqlite"
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password" json:"-"` // json:"-" prevents accidental leak via JSON serialization
	Database string `yaml:"database"`
	SSLMode  string `yaml:"ssl_mode"`

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

// MemoryConfig holds memory and distillation configuration.
type MemoryConfig struct {
	// Enabled enables the memory system. It is *bool so an unset field means
	// "default on": a minimal config that only specifies the LLM endpoint still
	// gets memory (the leader contract requires it), while an explicit
	// `memory.enabled: false` opts out. IsEnabled reports the effective value.
	Enabled       *bool         `yaml:"enabled"` // nil/true = enabled (default); false = disabled.
	SessionMemory SessionConfig `yaml:"session"` // Short-term session memory

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

	// TurnAwareCleaning routes BuildContext's history cleaning through the
	// turn-aware cleaner (tool_call↔tool_result kept paired) instead of the
	// flat default. Default: false (opt-in) — off is byte-for-byte the
	// pre-0.3.3 behaviour; it never widens any character budget.
	TurnAwareCleaning bool `yaml:"turn_aware_cleaning"`

	// ContextTokenBudget is an optional estimated-token ceiling applied to the
	// windowed history before cleaning. 0 (default) disables it. It only
	// trims (plain history before tool-causal, never system); it never widens.
	ContextTokenBudget int `yaml:"context_token_budget"`

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

// SessionConfig holds session memory configuration. The store is gated by the
// top-level MemoryConfig.IsEnabled switch; there is deliberately no per-store
// enable bit, because a `session.enabled: false` that nothing reads would
// silently fail to disable anything.
type SessionConfig struct {
	MaxHistory int `yaml:"max_history"` // Max conversation turns to keep
}

// ToolsConfig holds tool configuration for agents.
type ToolsConfig struct {
	// NativeAllowlist is the set of host commands discovered and registered
	// as tools (primitive 7: native command discovery). Empty (default)
	// disables discovery. This is the single security boundary: only listed
	// commands are ever probed or executed. Formerly the ARES_NATIVE_TOOLS
	// comma-separated env var.
	NativeAllowlist []string `yaml:"native_allowlist"`
	// FileSandboxDir roots the file tool's path-traversal sandbox — the
	// directory agents may read/write via file_tools. Empty (default)
	// falls back to a process-PRIVATE temp dir (not the working
	// directory): an agent served from a repo cannot touch that repo until
	// this value points at the intended workspace. Formerly the
	// ARES_FILE_TOOLS_ALLOWED_DIR / ARES_WORKSPACE_DIR env vars.
	FileSandboxDir string `yaml:"file_sandbox_dir"`
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
