package sdk

import (
	"fmt"

	"github.com/Timwood0x10/ares/api/core"
	"github.com/Timwood0x10/ares/api/tools"
	"github.com/Timwood0x10/ares/internal/knowledge/provider"
)

// ---- Runtime options ----

// Option configures the Runtime during construction.
type Option func(*config) error

// config holds the internal configuration state while options are applied.
type config struct {
	llmCfg      *core.LLMConfig
	baseCfg     *core.BaseConfig
	memCfg      memoryCfg
	evoCfg      evolutionCfg
	knlCfg      knowledgeCfg
	dbCfg       databaseCfg     // optional PostgreSQL connection
	embedCfg    embeddingCfg    // optional external embedding service
	distillCfg  distillationCfg // optional memory distillation
	knowledgeRT knowledgeRTCfg  // optional retrieval tuning
	// extraProviders holds user-registered GraphProviders appended via
	// WithKnowledgeProvider (e.g. code, mysql, postgres providers).
	extraProviders []provider.GraphProvider
	// sqliteStorePath, when non-empty, selects the SQLite knowledge store
	// instead of the default in-memory store.
	sqliteStorePath string
	mcpConns        []MCPConn
	fallbacks       []*core.LLMConfig
	trace           bool
}

// memoryCfg holds memory subsystem configuration.
type memoryCfg struct {
	Enabled     bool
	MaxHistory  int // 0 → component default
	MaxSessions int // 0 → component default
}

type evolutionCfg struct {
	Enabled bool
}

type knowledgeCfg struct {
	Enabled bool
}

// databaseCfg holds PostgreSQL connection parameters. Empty host signals
// in-memory storage fallback.
type databaseCfg struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
	SSLMode  string
}

// embeddingCfg holds an external embedding service endpoint. Empty URL signals
// default embedding fallback.
type embeddingCfg struct {
	ServiceURL string
	Model      string
}

// distillationCfg holds memory distillation knobs. Zero threshold signals
// component default; enabled=false disables the distiller.
type distillationCfg struct {
	Enabled   bool
	Threshold int
}

// knowledgeRTCfg tunes retrieval chunking and similarity bounds. Zero values
// signal component defaults.
type knowledgeRTCfg struct {
	ChunkSize    int
	ChunkOverlap int
	TopK         int
	MinScore     float64
}

func defaultConfig() *config {
	return &config{
		llmCfg: &core.LLMConfig{
			Provider:    core.LLMProviderOllama,
			Model:       defaultModel,
			Temperature: 0.7,
			MaxTokens:   2048,
			Timeout:     60,
		},
		baseCfg: &core.BaseConfig{
			RequestTimeout: 60,
			MaxRetries:     3,
		},
		memCfg: memoryCfg{Enabled: false},
		evoCfg: evolutionCfg{Enabled: false},
		trace:  true,
		// dbCfg, embedCfg, distillCfg, knowledgeRT default to zero values,
		// signalling component defaults downstream.
	}
}

// WithOpenAI configures the OpenAI provider.
// model: model name, e.g. "gpt-4o-mini" or "gpt-4o". Reads OPENAI_API_KEY
// from the environment when apiKey is empty.
// Default base URL is https://api.openai.com/v1.
func WithOpenAI(model string) Option {
	return func(c *config) error {
		c.llmCfg.Provider = core.LLMProviderOpenAI
		c.llmCfg.Model = model
		if c.llmCfg.BaseURL == "" {
			c.llmCfg.BaseURL = "https://api.openai.com/v1"
		}
		return nil
	}
}

// WithOllama configures the Ollama provider.
// model: model name, e.g. "llama3.2" or "qwen2.5". Ollama typically does not
// require an API key.
func WithOllama(model string) Option {
	return func(c *config) error {
		c.llmCfg.Provider = core.LLMProviderOllama
		c.llmCfg.Model = model
		return nil
	}
}

// WithAnthropic configures the Anthropic provider.
// model: model name, e.g. "claude-3-haiku" or "claude-3-opus". Reads
// ANTHROPIC_API_KEY from the environment when apiKey is empty.
// Default base URL is https://api.anthropic.com/v1.
func WithAnthropic(model string) Option {
	return func(c *config) error {
		c.llmCfg.Provider = core.LLMProviderAnthropic
		c.llmCfg.Model = model
		if c.llmCfg.BaseURL == "" {
			c.llmCfg.BaseURL = "https://api.anthropic.com/v1"
		}
		return nil
	}
}

// WithOpenRouter configures the OpenRouter provider.
// model: model name, e.g. "openai/gpt-4o-mini". Reads OPENROUTER_API_KEY
// from the environment when apiKey is empty.
// Default base URL is https://openrouter.ai/api/v1.
func WithOpenRouter(model string) Option {
	return func(c *config) error {
		c.llmCfg.Provider = core.LLMProviderOpenRouter
		c.llmCfg.Model = model
		if c.llmCfg.BaseURL == "" {
			c.llmCfg.BaseURL = "https://openrouter.ai/api/v1"
		}
		return nil
	}
}

// WithBaseURL overrides the default API base URL for the provider.
func WithBaseURL(url string) Option {
	return func(c *config) error {
		c.llmCfg.BaseURL = url
		return nil
	}
}

// WithAPIKey sets the API key explicitly (instead of reading from the
// environment variable).
func WithAPIKey(key string) Option {
	return func(c *config) error {
		c.llmCfg.APIKey = key
		return nil
	}
}

// WithLLMConfig applies a full core.LLMConfig. Useful when you already have a
// configuration object from a YAML file or shared config store.
func WithLLMConfig(cfg *core.LLMConfig) Option {
	return func(c *config) error {
		c.llmCfg = cfg
		return nil
	}
}

// WithFallbackLLM adds a fallback LLM provider for automatic failover.
// When the primary provider fails (timeout, rate limit, network error),
// the Runtime automatically tries fallbacks in order. Call multiple times
// to add multiple fallbacks.
func WithFallbackLLM(cfg *core.LLMConfig) Option {
	return func(c *config) error {
		c.fallbacks = append(c.fallbacks, cfg)
		return nil
	}
}

// WithDefaultMemory enables in-memory session storage. Each Run call creates a
// session and conversation history is available to the LLM on subsequent calls.
func WithDefaultMemory() Option {
	return func(c *config) error {
		c.memCfg.Enabled = true
		return nil
	}
}

// WithMemoryConfig overrides default memory sizing. Fields left at zero fall
// back to the component default, mirroring the yaml-driven philosophy.
//
// Args:
//
//	maxHistory - max conversation turns retained per session; 0 → default.
//	maxSessions - max concurrent sessions tracked; 0 → default.
func WithMemoryConfig(maxHistory, maxSessions int) Option {
	return func(c *config) error {
		if maxHistory < 0 || maxSessions < 0 {
			return fmt.Errorf("memory config: %w", ErrInvalidRange)
		}
		c.memCfg.Enabled = true
		c.memCfg.MaxHistory = maxHistory
		c.memCfg.MaxSessions = maxSessions
		return nil
	}
}

// WithDistillation enables memory distillation. The threshold controls how
// many conversation rounds accumulate before distillation fires. A threshold
// of 0 falls back to the component default. Mirrors v0.2.4
// examples/knowledge-base config.yaml distillation_threshold semantics.
//
// Args:
//
//	threshold - conversation rounds between distillation triggers; 0 → default.
func WithDistillation(threshold int) Option {
	return func(c *config) error {
		if threshold < 0 {
			return fmt.Errorf("distillation threshold %d: %w", threshold, ErrInvalidRange)
		}
		c.distillCfg.Enabled = true
		c.distillCfg.Threshold = threshold
		return nil
	}
}

// WithEmbeddingService injects an external embedding service endpoint. Empty
// url signals the sdk to fall back to default embedding behaviour.
//
// Args:
//
//	url   - embedding service URL, required when this option is used.
//	model - embedding model name, required when this option is used.
func WithEmbeddingService(url, model string) Option {
	return func(c *config) error {
		if url == "" {
			return fmt.Errorf("embedding service: %w", ErrMissingValue)
		}
		if model == "" {
			return fmt.Errorf("embedding model: %w", ErrMissingValue)
		}
		c.embedCfg.ServiceURL = url
		c.embedCfg.Model = model
		return nil
	}
}

// WithPostgres enables PostgreSQL-backed memory. Empty host signals in-memory
// storage fallback; when host is set, the sdk wires a pool to the Runtime.
//
// Args:
//
//	cfg - database connection parameters; host is the trigger field.
func WithPostgres(cfg DatabaseFileConfig) Option {
	return func(c *config) error {
		if cfg.Host == "" {
			return fmt.Errorf("postgres host: %w", ErrMissingValue)
		}
		if cfg.Port < 1 || cfg.Port > 65535 {
			return fmt.Errorf("postgres port %d: %w", cfg.Port, ErrInvalidRange)
		}
		c.dbCfg = databaseCfg(cfg)
		return nil
	}
}

// WithKnowledgeConfig tunes retrieval chunking and similarity bounds. Zero
// fields fall back to component defaults.
//
// Args:
//
//	cfg - knowledge retrieval parameters; chunk_size > 0 signals the section is active.
func WithKnowledgeConfig(cfg KnowledgeFileConfig) Option {
	return func(c *config) error {
		if cfg.ChunkSize > 0 {
			if cfg.ChunkOverlap < 0 || cfg.ChunkOverlap >= cfg.ChunkSize {
				return fmt.Errorf("knowledge chunk_overlap %d vs chunk_size %d: %w",
					cfg.ChunkOverlap, cfg.ChunkSize, ErrInvalidRange)
			}
			if cfg.TopK < 1 {
				return fmt.Errorf("knowledge top_k %d: %w", cfg.TopK, ErrInvalidRange)
			}
			if cfg.MinScore < 0 || cfg.MinScore > 1 {
				return fmt.Errorf("knowledge min_score %v: %w", cfg.MinScore, ErrInvalidRange)
			}
		}
		c.knowledgeRT = knowledgeRTCfg(cfg)
		return nil
	}
}

// WithEvolution enables strategy evolution. When enabled, the Runtime tracks
// agent performance and can evolve instructions to improve results over time.
func WithEvolution() Option {
	return func(c *config) error {
		c.evoCfg.Enabled = true
		return nil
	}
}

// WithKnowledge enables the AKF Knowledge Fabric pipeline.
// When enabled, each Agent.Run call automatically builds a knowledge graph
// from registered providers (e.g. Memory) and injects relevant context
// into the LLM's system prompt.
//
// If WithDefaultMemory is also enabled, historical tasks are automatically
// registered as a knowledge source.
func WithKnowledge() Option {
	return func(c *config) error {
		c.knlCfg.Enabled = true
		return nil
	}
}

// WithKnowledgeProvider registers an additional GraphProvider with the AKF
// Knowledge Fabric. Call multiple times to register multiple providers (e.g.
// code, mysql, postgres). Providers are only wired into the runtime when
// WithKnowledge is also enabled.
//
// Args:
//
//	p - a GraphProvider implementation; must not be nil.
//
// Returns:
//
//	An Option that appends p to the extra provider list. Returns an error
//	wrapping ErrNilProvider when p is nil.
func WithKnowledgeProvider(p provider.GraphProvider) Option {
	return func(c *config) error {
		if p == nil {
			return fmt.Errorf("knowledge provider: %w", ErrNilProvider)
		}
		c.extraProviders = append(c.extraProviders, p)
		return nil
	}
}

// WithSQLiteKnowledgeStore selects a file-backed SQLite knowledge store instead
// of the default in-memory store. Only takes effect when WithKnowledge is also
// enabled. When the SQLite path is set it takes priority over the PostgreSQL
// store configured via WithPostgres.
//
// Args:
//
//	dbPath - filesystem path to the SQLite database file; must be non-empty.
//
// Returns:
//
//	An Option that records the SQLite path. Returns an error wrapping
//	ErrMissingValue when dbPath is empty.
func WithSQLiteKnowledgeStore(dbPath string) Option {
	return func(c *config) error {
		if dbPath == "" {
			return fmt.Errorf("sqlite knowledge store path: %w", ErrMissingValue)
		}
		c.sqliteStorePath = dbPath
		return nil
	}
}

// MCPConn configures an MCP server connection.
type MCPConn struct {
	// Name is a human-readable label for this MCP server.
	Name string
	// Command is the absolute path to the MCP server binary.
	Command string
	// Args are command-line arguments passed to the server.
	Args []string
}

// WithMCP connects to an MCP server and registers its tools with the Runtime.
// Call multiple times to connect to multiple servers.
func WithMCP(conn MCPConn) Option {
	return func(c *config) error {
		if conn.Name == "" {
			conn.Name = "mcp"
		}
		if conn.Command == "" {
			return fmt.Errorf("mcp: command is required")
		}
		c.mcpConns = append(c.mcpConns, conn)
		return nil
	}
}

// WithTrace toggles per-step trace logging. Enabled by default.
func WithTrace(isEnabled bool) Option {
	return func(c *config) error {
		c.trace = isEnabled
		return nil
	}
}

// ── Team options ───────────────────────────────────────────────────────────

// TeamOption configures a Team during construction.
type TeamOption func(*Team)

// WithTeamConfig applies a complete TeamConfig to the team.
func WithTeamConfig(cfg TeamConfig) TeamOption {
	return func(t *Team) {
		t.cfg = cfg
	}
}

// WithAutoSplit configures auto-split mode (default). The leader
// automatically breaks the task into sub-tasks and delegates them.
func WithAutoSplit() TeamOption {
	return func(t *Team) {
		t.cfg.Mode = ModeAutoSplit
	}
}

// WithExplicitGroups configures explicit assignment mode with the given groups.
// Each GroupConfig specifies which members (by index) handle which task.
func WithExplicitGroups(groups ...GroupConfig) TeamOption {
	return func(t *Team) {
		t.cfg.Mode = ModeExplicit
		t.cfg.Groups = groups
	}
}

// WithVerifier sets the verifier agent by member index. The verifier
// reviews all sub-results and reports PASS/FAIL before synthesis.
// Use -1 to disable verification (default).
func WithVerifier(index int) TeamOption {
	return func(t *Team) {
		t.cfg.VerifierIndex = index
	}
}

// WithMaxConcurrency caps the number of members that execute simultaneously.
// 0 means unlimited (default).
func WithMaxConcurrency(n int) TeamOption {
	return func(t *Team) {
		if n > 0 {
			t.cfg.MaxConcurrency = n
		}
	}
}

// ---- Agent options ----

// AgentOption configures an Agent during construction.
type AgentOption func(*agentConfig)

type agentConfig struct {
	instruction string
	tools       []tools.Tool
	humanInput  HumanInputFunc
	maxIter     int
}

func defaultAgentConfig() *agentConfig {
	return &agentConfig{
		instruction: "",
		maxIter:     defaultMaxIterations,
	}
}

// WithInstruction sets the system-level instruction (system prompt) for the
// agent. This is always prepended to the conversation.
func WithInstruction(instruction string) AgentOption {
	return func(c *agentConfig) {
		c.instruction = instruction
	}
}

// WithTools attaches tools to the agent. The agent will expose these tools to
// the LLM as function-calling primitives.
func WithTools(tt ...tools.Tool) AgentOption {
	return func(c *agentConfig) {
		c.tools = append(c.tools, tt...)
	}
}

// WithHumanInput attaches a human-in-the-loop approval function. Before each
// tool call, the function is invoked so a human can approve or reject it.
// Return true to approve, false to skip the tool call.
func WithHumanInput(fn HumanInputFunc) AgentOption {
	return func(c *agentConfig) {
		c.humanInput = fn
	}
}

// WithMaxIterations caps the number of ReAct (tool-calling) iterations the
// agent will run before returning a "max iterations reached" result. Values
// <= 0 fall back to the default (defaultMaxIterations).
func WithMaxIterations(n int) AgentOption {
	return func(c *agentConfig) {
		if n > 0 {
			c.maxIter = n
		}
	}
}
