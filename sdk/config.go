package sdk

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
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

// ToOptions converts a ConfigFile into a slice of Option values that can be
// passed to New or MustNew.
func (c *ConfigFile) ToOptions() ([]Option, error) {
	var opts []Option

	// LLM provider.
	switch c.LLM.Provider {
	case "", "ollama":
		model := c.LLM.Model
		if model == "" {
			model = "llama3.2"
		}
		opts = append(opts, WithOllama(model))
	case "openai":
		model := c.LLM.Model
		if model == "" {
			model = "gpt-4o-mini"
		}
		opts = append(opts, WithOpenAI(model))
		if c.LLM.APIKey != "" {
			opts = append(opts, WithAPIKey(c.LLM.APIKey))
		}
	case "anthropic":
		model := c.LLM.Model
		if model == "" {
			model = "claude-3-haiku"
		}
		opts = append(opts, WithAnthropic(model))
		if c.LLM.APIKey != "" {
			opts = append(opts, WithAPIKey(c.LLM.APIKey))
		}
	case "openrouter":
		model := c.LLM.Model
		if model == "" {
			model = "openai/gpt-4o-mini"
		}
		opts = append(opts, WithOpenRouter(model))
		if c.LLM.APIKey != "" {
			opts = append(opts, WithAPIKey(c.LLM.APIKey))
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
