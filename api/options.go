package ares

import (
	"time"

	"github.com/Timwood0x10/ares/sdk"
)

// ---------------------------------------------------------------------------
// LLM options
// ---------------------------------------------------------------------------

// WithOpenAI configures the OpenAI provider with the given model.
func WithOpenAI(model string) Option { return sdk.WithOpenAI(model) }

// WithOllama configures the Ollama provider with the given model.
func WithOllama(model string) Option { return sdk.WithOllama(model) }

// WithAnthropic configures the Anthropic provider with the given model.
func WithAnthropic(model string) Option { return sdk.WithAnthropic(model) }

// WithOpenRouter configures the OpenRouter provider with the given model.
func WithOpenRouter(model string) Option { return sdk.WithOpenRouter(model) }

// WithBaseURL sets a custom base URL for the LLM API.
func WithBaseURL(url string) Option { return sdk.WithBaseURL(url) }

// WithAPIKey sets the API key for the LLM provider.
func WithAPIKey(key string) Option { return sdk.WithAPIKey(key) }

// WithLLMConfig applies a full LLM configuration.
func WithLLMConfig(cfg *LLMConfig) Option { return sdk.WithLLMConfig(cfg) }

// WithFallbackLLM adds a fallback LLM configuration for failover.
func WithFallbackLLM(cfg *LLMConfig) Option { return sdk.WithFallbackLLM(cfg) }

// ---------------------------------------------------------------------------
// Memory options
// ---------------------------------------------------------------------------

// WithDefaultMemory enables memory with default settings.
func WithDefaultMemory() Option { return sdk.WithDefaultMemory() }

// WithoutMemory disables memory entirely.
func WithoutMemory() Option { return sdk.WithoutMemory() }

// WithMemoryConfig sets memory history and session limits.
func WithMemoryConfig(maxHistory, maxSessions int) Option {
	return sdk.WithMemoryConfig(maxHistory, maxSessions)
}

// WithSessionMaxHistory sets the per-session stored-message cap (0 = default;
// clamped up to the read-side context window at runtime).
func WithSessionMaxHistory(n int) Option { return sdk.WithSessionMaxHistory(n) }

// WithDistillation enables memory distillation at the given threshold.
func WithDistillation(threshold int) Option { return sdk.WithDistillation(threshold) }

// WithRAG enables RAG retrieval with the given top-K and min-score.
func WithRAG(topK int, minScore float64) Option { return sdk.WithRAG(topK, minScore) }

// ---------------------------------------------------------------------------
// Knowledge options
// ---------------------------------------------------------------------------

// WithEvolution enables the evolution subsystem.
func WithEvolution() Option { return sdk.WithEvolution() }

// WithKnowledge enables the knowledge subsystem.
func WithKnowledge() Option { return sdk.WithKnowledge() }

// WithAKGQualityGate sets the knowledge quality gate thresholds.
func WithAKGQualityGate(q QualityGateConfig) Option { return sdk.WithAKGQualityGate(q) }

// WithAKGEmbedding configures the knowledge embedding model.
func WithAKGEmbedding(model, baseURL string) Option { return sdk.WithAKGEmbedding(model, baseURL) }

// WithKnowledgeProvider registers a custom knowledge provider.
func WithKnowledgeProvider(p GraphProvider) Option { return sdk.WithKnowledgeProvider(p) }

// WithSQLiteKnowledgeStore uses a SQLite file for knowledge persistence.
func WithSQLiteKnowledgeStore(dbPath string) Option {
	return sdk.WithSQLiteKnowledgeStore(dbPath)
}

// ---------------------------------------------------------------------------
// Storage options
// ---------------------------------------------------------------------------

// WithPostgres configures PostgreSQL storage.
func WithPostgres(cfg DatabaseFileConfig) Option { return sdk.WithPostgres(cfg) }

// WithKnowledgeConfig applies knowledge configuration from a config struct.
func WithKnowledgeConfig(cfg KnowledgeFileConfig) Option {
	return sdk.WithKnowledgeConfig(cfg)
}

// WithEmbeddingService configures the embedding service endpoint.
func WithEmbeddingService(url, model string) Option {
	return sdk.WithEmbeddingService(url, model)
}

// ---------------------------------------------------------------------------
// MCP options
// ---------------------------------------------------------------------------

// WithMCP connects to an MCP server and registers its tools.
func WithMCP(conn MCPConn) Option { return sdk.WithMCP(conn) }

// ---------------------------------------------------------------------------
// Observability options
// ---------------------------------------------------------------------------

// WithTrace enables or disables tracing.
func WithTrace(isEnabled bool) Option { return sdk.WithTrace(isEnabled) }

// ---------------------------------------------------------------------------
// Governance options
// ---------------------------------------------------------------------------

// WithAgentGovernance sets token/tool/deadline budgets for all agents.
func WithAgentGovernance(tokens, tools int, deadline time.Duration) Option {
	return sdk.WithAgentGovernance(tokens, tools, deadline)
}

// ---------------------------------------------------------------------------
// Config file options
// ---------------------------------------------------------------------------

// WithConfig loads configuration from an ares.yaml file.
func WithConfig(path string) ConfigOption { return sdk.WithConfig(path) }

// ---------------------------------------------------------------------------
// Agent options
// ---------------------------------------------------------------------------

// AgentOption configures an Agent at creation time.
type AgentOption = sdk.AgentOption

// WithInstruction sets the agent's system instruction.
func WithInstruction(instruction string) AgentOption { return sdk.WithInstruction(instruction) }

// WithTools registers custom tools with the agent.
func WithTools(tt ...Tool) AgentOption { return sdk.WithTools(tt...) }

// WithHumanInput sets a human-in-the-loop approval callback.
//
// Deprecated: not supported on the L2 execution path; passing it makes Run
// fail with ErrHumanInputUnsupported.
func WithHumanInput(fn HumanInputFunc) AgentOption { return sdk.WithHumanInput(fn) }

// WithMaxIterations caps the agent's ReAct loop iterations.
func WithMaxIterations(n int) AgentOption { return sdk.WithMaxIterations(n) }

// WithMaxTokens caps LLM token consumption for the agent.
func WithMaxTokens(n int) AgentOption { return sdk.WithMaxTokens(n) }

// WithTimeout caps the total wall-clock duration of one agent run.
func WithTimeout(d time.Duration) AgentOption { return sdk.WithTimeout(d) }

// WithToolDiscovery enables automatic tool discovery.
func WithToolDiscovery() AgentOption { return sdk.WithToolDiscovery() }

// WithToolSource sets a dynamic tool source for the agent.
func WithToolSource(s ToolSource) AgentOption { return sdk.WithToolSource(s) }

// WithToolSelector sets a tool selector for the agent.
func WithToolSelector(s ToolSelector) AgentOption { return sdk.WithToolSelector(s) }
