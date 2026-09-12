package ares_config

import (
	"time"
)

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
	// EligibleCapabilities is the target whitelist for live injections:
	// only agents declaring at least one of these
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
	// Host is the actual HTTP bind address (default "127.0.0.1" from
	// setDefaults): the introspect read side (/api/v1/introspect/*) carries
	// task payloads, so serve must never default to a wildcard bind. The
	// default is the explicit loopback IP rather than the "localhost" name
	// so the bind cannot be widened by a hosts-file remap. "0.0.0.0" opts
	// into all interfaces and requires security.auth_enabled (or
	// introspect.token) to keep the read side closed.
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

// IntrospectConfig configures the runtime introspection read side: the
// /introspect panel UI, the /api/v1/introspect/* JSON feed, the LLM cost
// dashboard, and the read-only control-server surfaces under /api/*. It is
// the M-S1 closure of the "panel carries agent/task/event data with no auth
// of its own" exposure.
type IntrospectConfig struct {
	// Token is a static bearer token for the introspect read side. Empty
	// (default): the read side is open, protected only by the loopback
	// default bind (server.host 127.0.0.1) — startup logs a Warn stating
	// that posture. Non-empty: non-loopback clients must present
	// "Authorization: Bearer <token>" (constant-time compare); loopback
	// clients stay open. It composes with security.auth_enabled: a valid
	// read-permission JWT or the legacy API key also passes the read gate.
	Token string `yaml:"token"`
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
	// Peers is the flat capability-agent population (flat peer
	// structure as the default). Each entry is an equal peer spawned into the Agent
	// Fabric with its execution body; the kernel scheduler selects among them
	// by capability. When Peers is non-empty it is the authoritative agent
	// source (createPeerAgents reads it); Sub remains as the legacy fallback
	// (each sub's single Type becomes its only capability) so legacy configs
	// keep working.
	Peers []PeerAgentConfig `yaml:"peers"`
	// Sub is the LEGACY leader/sub-era sub-agent list, normalized into peers
	// when no peers are configured (see normalizedPeers). The leader side of
	// the legacy structure was removed in v0.4.0; an ignored `leader:` key in
	// an old config file is harmless.
	Sub []SubAgentConfig `yaml:"sub"`
}

// PeerAgentConfig is one flat peer agent (flat peer structure).
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
	// when choosing among capable candidates.
	Priority float64 `yaml:"priority"`
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
	// this sub-agent's task runs (Task Fabric DAG gate, ares-runtime).
	Dependencies []string `yaml:"dependencies"`
	// Priority is the scheduling priority of this sub-agent (>= 0; 0 =
	// normal). It mirrors OS-thread priority: the kernel scheduler boosts
	// higher-priority agents when choosing among capable candidates. Read by
	// the kernel wiring (thread priority) into the shared load tracker.
	Priority float64 `yaml:"priority"`
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
