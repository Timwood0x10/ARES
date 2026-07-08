package sdk

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
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
type ConfigFile struct {
	LLM    LLMFileConfig `yaml:"llm"`
	Memory struct {
		Enabled bool `yaml:"enabled"`
	} `yaml:"memory"`
	Tools struct {
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

// LLMFileConfig mirrors the llm section of ares.yaml.
type LLMFileConfig struct {
	Provider    string  `yaml:"provider"`
	Model       string  `yaml:"model"`
	APIKey      string  `yaml:"api_key"`
	BaseURL     string  `yaml:"base_url"`
	Temperature float64 `yaml:"temperature"`
	MaxTokens   int     `yaml:"max_tokens"`
}

// LoadConfigFile reads and parses a YAML config file.
// Returns an error if the file cannot be read or parsed.
func LoadConfigFile(path string) (*ConfigFile, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path comes from user flag, safe
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg ConfigFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	return &cfg, nil
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

	// Memory.
	if c.Memory.Enabled {
		opts = append(opts, WithDefaultMemory())
	}

	// Evolution.
	if c.Evolution.Enabled {
		opts = append(opts, WithEvolution())
	}

	return opts, nil
}
