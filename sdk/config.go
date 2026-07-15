package sdk

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Sentinel errors for config validation. Wrap with %w to preserve chain.
var (
	// ErrNilConfig signals Validate was called on a nil ConfigFile.
	ErrNilConfig = errors.New("nil config")
	// ErrInvalidRange signals a field value outside its valid range.
	ErrInvalidRange = errors.New("value out of valid range")
	// ErrMissingValue signals a required companion field was left unset.
	ErrMissingValue = errors.New("required field missing")
)

// Provider constants used in config file parsing.
const (
	providerOllama     = "ollama"
	providerOpenAI     = "openai"
	providerAnthropic  = "anthropic"
	providerOpenRouter = "openrouter"
	defaultModel       = "llama3.2"
	// defaultMaxIterations is the default cap on the ReAct tool-calling loop.
	defaultMaxIterations = 10
)

// ConfigFile mirrors ares.yaml structure for config-driven Runtime creation.
// Use LoadConfigFile to read from disk, then pass to New.
//
// Each section is optional: a section left at its zero value causes the sdk
// to fall back to the corresponding component default, mirroring the
// "one yaml drives all components; missing means default" philosophy
// established by examples/knowledge-base in v0.2.4.
type ConfigFile struct {
	LLM       LLMFileConfig       `yaml:"llm"`
	Database  DatabaseFileConfig  `yaml:"database"`
	Embedding EmbeddingFileConfig `yaml:"embedding"`
	Memory    MemoryFileConfig    `yaml:"memory"`
	Knowledge KnowledgeFileConfig `yaml:"knowledge"`
	Tools     struct {
		Builtin bool     `yaml:"builtin"`
		MCP     []string `yaml:"mcp"`
	} `yaml:"tools"`
	Reflection struct {
		Enabled bool `yaml:"enabled"`
	} `yaml:"reflection"`
	Evolution struct {
		Enabled bool `yaml:"enabled"`
	} `yaml:"evolution"`
}

// MemoryFileConfig carries all memory subsystem knobs. Fields left at their
// zero value cause the sdk to fall back to the component default.
type MemoryFileConfig struct {
	Enabled               bool `yaml:"enabled"`
	MaxHistory            int  `yaml:"max_history"`
	MaxSessions           int  `yaml:"max_sessions"`
	EnableDistillation    bool `yaml:"enable_distillation"`
	DistillationThreshold int  `yaml:"distillation_threshold"`
}

// DatabaseFileConfig declares PostgreSQL connection parameters. When the
// Database section is omitted entirely, the sdk uses in-memory storage.
type DatabaseFileConfig struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Database string `yaml:"database"`
}

// EmbeddingFileConfig declares an external embedding service endpoint. When
// omitted, the sdk falls back to the default embedding behaviour.
type EmbeddingFileConfig struct {
	ServiceURL string `yaml:"service_url"`
	Model      string `yaml:"model"`
}

// KnowledgeFileConfig controls retrieval chunking and similarity bounds. When
// omitted, the sdk uses default retrieval parameters.
type KnowledgeFileConfig struct {
	ChunkSize    int     `yaml:"chunk_size"`
	ChunkOverlap int     `yaml:"chunk_overlap"`
	TopK         int     `yaml:"top_k"`
	MinScore     float64 `yaml:"min_score"`
}

// LLMFileConfig mirrors the llm section of ares.yaml.
type LLMFileConfig struct {
	Provider    string  `yaml:"provider"`
	Model       string  `yaml:"model"`
	APIKey      string  `yaml:"api_key"`
	BaseURL     string  `yaml:"base_url"`
	Temperature float64 `yaml:"temperature"`
	MaxTokens   int     `yaml:"max_tokens"`
}

// LoadConfigFile reads, parses and validates a YAML config file.
// Returns an error if the file cannot be read, parsed, or fails validation.
//
// Args:
//
//	path - filesystem path to the YAML file, must be non-empty.
//
// Returns:
//
//	cfg - a fully validated configuration, never nil on success.
//	err - a read, parse or validation error with context wrapping.
func LoadConfigFile(path string) (*ConfigFile, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path comes from user flag, safe
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg ConfigFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	return &cfg, nil
}

// Validate verifies that all configured values fall within their valid ranges.
// Sections left at zero value are skipped: they defer to the component default.
//
// Returns:
//
//	err - nil when valid, otherwise a wrapped sentinel describing the offending field.
func (c *ConfigFile) Validate() error {
	if c == nil {
		return fmt.Errorf("config: %w", ErrNilConfig)
	}
	// LLM section: provider is required when llm is configured at all.
	if c.LLM.Provider != "" {
		if c.LLM.Temperature < 0 || c.LLM.Temperature > 2 {
			return fmt.Errorf("llm.temperature %v: %w", c.LLM.Temperature, ErrInvalidRange)
		}
		if c.LLM.MaxTokens < 0 {
			return fmt.Errorf("llm.max_tokens %d: %w", c.LLM.MaxTokens, ErrInvalidRange)
		}
	}
	// Memory section.
	if c.Memory.Enabled {
		if c.Memory.MaxHistory < 0 {
			return fmt.Errorf("memory.max_history %d: %w", c.Memory.MaxHistory, ErrInvalidRange)
		}
		if c.Memory.MaxSessions < 0 {
			return fmt.Errorf("memory.max_sessions %d: %w", c.Memory.MaxSessions, ErrInvalidRange)
		}
		// DistillationThreshold 0 means "unset": the sdk falls back to the
		// component default at apply time. Negative is invalid.
		if c.Memory.DistillationThreshold < 0 {
			return fmt.Errorf("memory.distillation_threshold %d: %w",
				c.Memory.DistillationThreshold, ErrInvalidRange)
		}
	}
	// Database section: validate only when host is set (section present).
	if c.Database.Host != "" {
		if c.Database.Port < 1 || c.Database.Port > 65535 {
			return fmt.Errorf("database.port %d: %w", c.Database.Port, ErrInvalidRange)
		}
	}
	// Embedding section: validate only when service URL is set.
	if c.Embedding.ServiceURL != "" && c.Embedding.Model == "" {
		return fmt.Errorf("embedding.model: %w", ErrMissingValue)
	}
	// Knowledge section.
	if c.Knowledge.ChunkSize > 0 {
		if c.Knowledge.ChunkOverlap < 0 || c.Knowledge.ChunkOverlap >= c.Knowledge.ChunkSize {
			return fmt.Errorf("knowledge.chunk_overlap %d vs chunk_size %d: %w",
				c.Knowledge.ChunkOverlap, c.Knowledge.ChunkSize, ErrInvalidRange)
		}
		if c.Knowledge.TopK < 1 {
			return fmt.Errorf("knowledge.top_k %d: %w", c.Knowledge.TopK, ErrInvalidRange)
		}
		if c.Knowledge.MinScore < 0 || c.Knowledge.MinScore > 1 {
			return fmt.Errorf("knowledge.min_score %v: %w", c.Knowledge.MinScore, ErrInvalidRange)
		}
	}
	return nil
}

// resolveAPIKey returns the config-provided key when non-empty, otherwise falls
// back to the named environment variable. This avoids storing secrets in YAML.
func resolveAPIKey(configKey, envVar string) string {
	if configKey != "" {
		return configKey
	}
	return os.Getenv(envVar)
}

// ToOptions converts a ConfigFile into a slice of Option values that can be
// passed to New or MustNew.
func (c *ConfigFile) ToOptions() ([]Option, error) {
	var opts []Option

	// LLM provider.
	switch c.LLM.Provider {
	case "", providerOllama:
		model := c.LLM.Model
		if model == "" {
			model = defaultModel
		}
		opts = append(opts, WithOllama(model))
	case providerOpenAI:
		model := c.LLM.Model
		if model == "" {
			model = "gpt-4o-mini"
		}
		opts = append(opts, WithOpenAI(model))
		if key := resolveAPIKey(c.LLM.APIKey, "OPENAI_API_KEY"); key != "" {
			opts = append(opts, WithAPIKey(key))
		}
	case providerAnthropic:
		model := c.LLM.Model
		if model == "" {
			model = "claude-3-haiku"
		}
		opts = append(opts, WithAnthropic(model))
		if key := resolveAPIKey(c.LLM.APIKey, "ANTHROPIC_API_KEY"); key != "" {
			opts = append(opts, WithAPIKey(key))
		}
	case providerOpenRouter:
		model := c.LLM.Model
		if model == "" {
			model = "openai/gpt-4o-mini"
		}
		opts = append(opts, WithOpenRouter(model))
		if key := resolveAPIKey(c.LLM.APIKey, "OPENROUTER_API_KEY"); key != "" {
			opts = append(opts, WithAPIKey(key))
		}
	default:
		return nil, fmt.Errorf("unknown LLM provider: %s", c.LLM.Provider)
	}

	if c.LLM.BaseURL != "" {
		opts = append(opts, WithBaseURL(c.LLM.BaseURL))
	}

	// Database (optional). Without a host, sdk falls back to in-memory storage.
	if c.Database.Host != "" {
		opts = append(opts, WithPostgres(c.Database))
	}

	// Embedding (optional). Without a service URL, sdk uses default embeddings.
	if c.Embedding.ServiceURL != "" {
		opts = append(opts, WithEmbeddingService(c.Embedding.ServiceURL, c.Embedding.Model))
	}

	// Memory. Each unset field falls back to the component default.
	if c.Memory.Enabled {
		opts = append(opts, WithMemoryConfig(c.Memory.MaxHistory, c.Memory.MaxSessions))
		if c.Memory.EnableDistillation {
			// DistillationThreshold 0 means "ungated": fire on every event,
			// matching every downstream component's contract. We pass it
			// straight through instead of substituting a default, so users
			// can express ungated behaviour explicitly via yaml.
			opts = append(opts, WithDistillation(c.Memory.DistillationThreshold))
		}
	}

	// Knowledge (optional). Without chunk_size, sdk uses default retrieval.
	if c.Knowledge.ChunkSize > 0 {
		opts = append(opts, WithKnowledgeConfig(c.Knowledge))
	}

	// Evolution.
	if c.Evolution.Enabled {
		opts = append(opts, WithEvolution())
	}

	return opts, nil
}
