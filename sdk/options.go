package sdk

import (
	"fmt"

	"github.com/Timwood0x10/ares/api/core"
	"github.com/Timwood0x10/ares/api/tools"
)

// ---- Runtime options ----

// Option configures the Runtime during construction.
type Option func(*config) error

// config holds the internal configuration state while options are applied.
type config struct {
	llmCfg   *core.LLMConfig
	baseCfg  *core.BaseConfig
	memCfg   memoryCfg
	evoCfg   evolutionCfg
	knlCfg   knowledgeCfg
	mcpConns []MCPConn
	trace    bool
}

type memoryCfg struct {
	Enabled bool
}

type evolutionCfg struct {
	Enabled bool
}

type knowledgeCfg struct {
	Enabled bool
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

// WithDefaultMemory enables in-memory session storage. Each Run call creates a
// session and conversation history is available to the LLM on subsequent calls.
func WithDefaultMemory() Option {
	return func(c *config) error {
		c.memCfg.Enabled = true
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

// ---- Agent options ----

// AgentOption configures an Agent during construction.
type AgentOption func(*agentConfig)

type agentConfig struct {
	instruction string
	tools       []tools.Tool
	humanInput  HumanInputFunc
}

func defaultAgentConfig() *agentConfig {
	return &agentConfig{
		instruction: "",
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
