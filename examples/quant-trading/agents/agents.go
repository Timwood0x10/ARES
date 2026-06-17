package agents

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"goagentx/internal/dashboard"
)

// AgentDef defines a single agent from YAML config.
type AgentDef struct {
	ID        string         `yaml:"id"`
	Name      string         `yaml:"name"`
	Type      string         `yaml:"type"`      // "sub" or "leader"
	Category  string         `yaml:"category"`
	MCPTool   string         `yaml:"mcp_tool,omitempty"`
	MCPArgs   map[string]any `yaml:"mcp_args,omitempty"`
	DependsOn []string       `yaml:"depends_on,omitempty"`
	Timeout   int            `yaml:"timeout"`
	MaxRetry  int            `yaml:"max_retries"`
}

// Config wraps the YAML root.
type Config struct {
	Agents []AgentDef `yaml:"agents"`
}

// LoadConfig reads and parses the agents YAML file.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load agent config: %w", err)
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse agent config: %w", err)
	}
	return &cfg, nil
}

// AgentPromptMap returns the prompt for a given agent category.
// Prompts are defined in prompts.go as bilingual Go constants.
func AgentPromptMap() map[string]string {
	return map[string]string{
		"analyst":    FundamentalsPrompt,
		"researcher": ResearcherPrompt,
		"execution":  TraderPrompt,
		"risk":       RiskPrompt,
		"management": PMPrompt,
	}
}

// CreateFromConfig creates agents from YAML config and returns ID map.
func CreateFromConfig(orch *dashboard.Orchestrator, cfg *Config, ticker string) map[string]string {
	prompts := AgentPromptMap()
	ids := make(map[string]string)

	for _, a := range cfg.Agents {
		prompt, ok := prompts[a.Category]
		if !ok {
			prompt = "Analyze " + ticker + " and provide recommendations."
		}

		// Merge ticker into MCP args if any.
		args := a.MCPArgs
		if args == nil {
			args = make(map[string]any)
		}
		if _, exists := args["ticker"]; !exists && a.MCPTool != "" {
			args["ticker"] = ticker
		}

		req := dashboard.AgentRequest{
			Name:      a.Name,
			Target:    "Analyze " + ticker,
			LLMPrompt: prompt,
			MCPTool:   a.MCPTool,
			MCPArgs:   args,
		}
		id, err := orch.CreateAgent(req)
		if err != nil {
			continue
		}
		ids[a.ID] = id
	}
	return ids
}
