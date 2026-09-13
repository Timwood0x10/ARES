// package config - provides configuration loading and validation for ares.
package ares_config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/Timwood0x10/ares/internal/errors"
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
	Server     ServerConfig     `yaml:"server"`
	LLM        LLMConfig        `yaml:"llm"`
	Agents     AgentsConfig     `yaml:"agents"`
	Tools      ToolsConfig      `yaml:"tools"`
	Prompts    PromptsConfig    `yaml:"prompts"`
	Output     OutputConfig     `yaml:"output"`
	Validation ValidationConfig `yaml:"validation"`
	Workflow   WorkflowConfig   `yaml:"workflow"`
	Storage    StorageConfig    `yaml:"storage"`
	Memory     MemoryConfig     `yaml:"memory"`
	Knowledge  KnowledgeConfig  `yaml:"knowledge"`
	MCP        MCPConfig        `yaml:"mcp"`
	Evolution  EvolutionConfig  `yaml:"evolution"`
	Embedding  EmbeddingConfig  `yaml:"embedding"`
	Discovery  DiscoveryConfig  `yaml:"discovery"`
	Kernel     KernelConfig     `yaml:"kernel"`
	Security   SecurityConfig   `yaml:"security"`
	Introspect IntrospectConfig `yaml:"introspect"`
}

// KernelConfig controls the dual-track dispatch kernel
// (parallel + feature flag gradual cutover). When Policy is "taskfabric" (the
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
	// MaxConcurrent caps how many ready tasks the kernel scheduler runs in
	// parallel per drain (0 = auto: static executor count, then live idle
	// executable fabric agents). A negative is a config error, rejected by
	// Validate.
	MaxConcurrent int `yaml:"max_concurrent"`
	// Resources is the resource budget applied to the Agent Fabric
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
	// A hung policy store must not stall the quota loop.
	QuotaApplyTimeout string `yaml:"quota_apply_timeout"`
	// RecoverySweepInterval is how often the recovery loop sweeps TTL-based
	// lease expiry (default "1s").
	RecoverySweepInterval string `yaml:"recovery_sweep_interval"`
	// RecoverySweepTimeout bounds each recovery sweep (default "30s"). A hung
	// store must neither block the recovery loop nor pile up sweeps.
	RecoverySweepTimeout string `yaml:"recovery_sweep_timeout"`
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
	// the scheduler default; invalid durations are ignored with a warning.
	LeaseTTL string `yaml:"lease_ttl"`
	// LoopMaxIterations caps how many rounds the kernel loop clock advances
	// (0 = unlimited). When the budget is exhausted the round clock stops
	// advancing — the scheduler's task flow is never gated by it.
	LoopMaxIterations int `yaml:"loop_max_iterations"`
	// LoopRoundQuanta is how many scheduler quanta constitute one loop round
	// (default 1: every quantum closes a round). The boundary is decided by
	// the atomic increment's return value, so concurrent drains cannot skip
	// or double-fire a boundary.
	LoopRoundQuanta int `yaml:"loop_round_quanta"`
	// Chaos controls the fault injection subsystem. By default
	// chaos runs in "shadow" mode — a scratch Sandbox verifies recovery
	// without touching production agents. "live" mode (requires
	// allow_live=true) enables real agent kill/suspend; it is dangerous
	// and intended only for dedicated chaos testing environments.
	Chaos ChaosConfig `yaml:"chaos"`
	// DAGExecution configures the L2 session-graph execution path
	// (the only execution path; see DAGExecutionConfig). Every field is
	// zero-value-safe — an absent dag_execution section runs the L2 router
	// with defaults. The former `enabled` gate and the ReAct chat tool-loop
	// it selected are gone; old files carrying `enabled:` still parse and the
	// key is ignored.
	DAGExecution DAGExecutionConfig `yaml:"dag_execution"`
	// AgentBudget is the cognitive-execution budget applied to every agent
	// the production kernel spawns (cumulative tokens/tools and wall-clock
	// deadline). Zero values mean unlimited for that dimension, so a
	// zero-value section preserves the pre-budget behavior. This is the
	// long-task safety gate: without it a runaway agent has no cost or
	// lifetime ceiling. Validated by Validate (negatives and an unparsable
	// deadline are config errors).
	AgentBudget AgentBudgetConfig `yaml:"agent_budget"`
}

// AgentBudgetConfig is the per-agent cognitive-execution budget. All zero
// means unlimited (no gate). The token/tool budgets are consumed per quantum
// through the scheduler's governance provider; the deadline is armed at spawn.
type AgentBudgetConfig struct {
	// Tokens caps the cumulative prompt+completion tokens an agent may spend
	// across its lifetime (0 = unlimited).
	Tokens int `yaml:"tokens"`
	// Tools caps the cumulative tool rounds an agent may execute across its
	// lifetime (0 = unlimited).
	Tools int `yaml:"tools"`
	// Deadline caps the agent's wall-clock lifetime ("" or "0" = none).
	// Parsed with time.ParseDuration; an invalid value fails Validate.
	Deadline string `yaml:"deadline"`
}

// DAGExecutionConfig configures the L2 session-graph execution path.
// Every field is zero-value-safe. (The `enabled` gate is gone — the
// L2 path is the only path. Old files carrying `enabled:` still parse;
// the key is ignored.)
type DAGExecutionConfig struct {
	// MaxPlanDepth caps the L2 plan-tool growth depth per session
	// (0 = default 10). A negative is a config error, rejected by Validate.
	MaxPlanDepth int `yaml:"max_plan_depth"`
	// ReaperGrace is the terminal-task reaper's read-window grace: a task of
	// a RELEASED session is harvested only after its state transition is
	// older than this (0 = default 30s). Live sessions are never harvested
	// regardless of age — the registry keep-set gates that. A negative is a
	// config error, rejected by Validate.
	ReaperGrace time.Duration `yaml:"reaper_grace"`
	// SessionIdleTTL is the session registry's idle window: a
	// session untouched for this long is released by the sweeper, which
	// lets the task reaper harvest its terminal tasks on the next sweep
	// (0 = default 30m). Active sessions are immune — every quantum
	// touches its session. A negative is a config error, rejected by
	// Validate.
	SessionIdleTTL time.Duration `yaml:"session_idle_ttl"`
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

// resolveProviderAPIKey returns the credential carried by the environment
// variable that belongs to provider. The provider-specific variable is tried
// first; OPENROUTER_API_KEY is the historical generic override and remains
// the fallback for unlisted providers (including the empty/ollama case).
//
// Returns "" when no credential variable is set; callers treat that as
// "nothing to override", never as an empty credential.
func resolveProviderAPIKey(provider string) string {
	switch provider {
	case providerOpenAI:
		if v := os.Getenv("OPENAI_API_KEY"); v != "" {
			return v
		}
	case providerAnthropic:
		if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" {
			return v
		}
	case providerOpenRouter:
		if v := os.Getenv("OPENROUTER_API_KEY"); v != "" {
			return v
		}
	}
	return os.Getenv("OPENROUTER_API_KEY")
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
	// Provider-specific fallbacks. `ares doctor` and the SDK both document
	// OPENAI_API_KEY / ANTHROPIC_API_KEY, but this path used to read only
	// LLM_API_KEY / OPENROUTER_API_KEY — an operator following the doctor
	// output got a silent 401. An explicit LLM_API_KEY (set above) always
	// wins, and OPENROUTER_API_KEY stays the final generic fallback so the
	// previous behavior is preserved for unlisted providers.
	if cfg.LLM.APIKey == "" {
		cfg.LLM.APIKey = resolveProviderAPIKey(cfg.LLM.Provider)
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
	// middleware (enables unless the value is "0" or "false").
	if v := os.Getenv("ARES_JWT_SECRET"); v != "" {
		cfg.Security.JWTSecret = v
	}
	if v := os.Getenv("ARES_AUTH_ENABLED"); v != "" && v != "0" && !strings.EqualFold(v, "false") {
		cfg.Security.AuthEnabled = true
	}

	return nil
}
