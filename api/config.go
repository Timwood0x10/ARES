package api

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Config holds all configuration for the service.
type ServiceConfig struct {
	LLM struct {
		Provider        string `yaml:"provider"`
		Model           string `yaml:"model"`
		BaseURL         string `yaml:"base_url"`
		APIKey          string `yaml:"api_key"`
		Timeout         int    `yaml:"timeout"`
		MaxPromptLength int    `yaml:"max_prompt_length"`
	} `yaml:"llm"`
	MCP struct {
		Servers []struct {
			Name      string `yaml:"name"`
			Transport struct {
				Stdio struct {
					Command string   `yaml:"command"`
					Args    []string `yaml:"args"`
				} `yaml:"stdio"`
			} `yaml:"transport"`
		} `yaml:"servers"`
	} `yaml:"mcp"`
	Dashboard struct {
		Addr string `yaml:"addr"`
	} `yaml:"dashboard"`
}

// LoadConfig reads configuration from a YAML file.
func LoadServiceConfig(path string) (*ServiceConfig, error) {
	data, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		return nil, err
	}
	var cfg ServiceConfig
	return &cfg, yaml.Unmarshal(data, &cfg)
}
