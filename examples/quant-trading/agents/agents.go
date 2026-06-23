package agents

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Timwood0x10/ares/internal/dashboard"
)

// AgentDef defines a single agent from YAML config.
type AgentDef struct {
	ID        string         `yaml:"id"`
	Name      string         `yaml:"name"`
	Type      string         `yaml:"type"`
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

// PromptFor returns the LLM prompt for a given agent category.
func PromptFor(category string) string {
	switch category {
	case "analyst":
		return FundamentalsPrompt
	case "researcher":
		return ResearcherPrompt
	case "execution":
		return TraderPrompt
	case "risk":
		return RiskPrompt
	case "management":
		return PMPrompt
	default:
		return "Analyze the provided data and give recommendations."
	}
}

// PhaseGroups returns agents grouped by execution phase (topological order).
func (c *Config) PhaseGroups() [][]AgentDef {
	byID := make(map[string]AgentDef)
	for _, a := range c.Agents {
		byID[a.ID] = a
	}
	depth := make(map[string]int)
	for _, a := range c.Agents {
		depth[a.ID] = computeDepth(a.ID, byID)
	}
	maxDepth := 0
	for _, d := range depth {
		if d > maxDepth {
			maxDepth = d
		}
	}
	groups := make([][]AgentDef, maxDepth+1)
	for _, a := range c.Agents {
		d := depth[a.ID]
		groups[d] = append(groups[d], a)
	}
	return groups
}

func computeDepth(id string, byID map[string]AgentDef) int {
	a, ok := byID[id]
	if !ok || len(a.DependsOn) == 0 {
		return 0
	}
	maxDep := 0
	for _, dep := range a.DependsOn {
		d := computeDepth(dep, byID) + 1
		if d > maxDep {
			maxDep = d
		}
	}
	return maxDep
}

// RunPipeline creates agents phase by phase, injecting previous outputs.
// Returns a map of YAML ID → agent orchestrator ID.
func RunPipeline(orch *dashboard.Orchestrator, cfg *Config, ticker string) map[string]string {
	// yamlID → orchID mapping.
	yamlToOrch := make(map[string]string)

	groups := cfg.PhaseGroups()

	for _, group := range groups {
		var createdYAML []string

		for _, a := range group {
			prompt := PromptFor(a.Category)

			// Inject outputs from dependencies into the prompt.
			var depsOutput string
			for _, depID := range a.DependsOn {
				if orchID, ok := yamlToOrch[depID]; ok {
					if ag, ok := orch.GetAgent(orchID); ok && ag.Analysis != "" {
						depName := cfg.AgentNameByID(depID)
						truncated := ag.Analysis
						if len(truncated) > 1500 {
							truncated = truncated[:1500] + "...[truncated]"
						}
						depsOutput += fmt.Sprintf("\n--- %s Output ---\n%s\n", depName, truncated)
					}
				}
			}

			fullPrompt := prompt
			if depsOutput != "" {
				fullPrompt = fmt.Sprintf(
					"%s\n\nInput data from upstream agents:\n%s\nBased on this data, produce your analysis.",
					prompt, depsOutput)
			}

			args := a.MCPArgs
			if args == nil {
				args = make(map[string]any)
			}
			if _, exists := args["ticker"]; !exists && a.MCPTool != "" {
				args["ticker"] = ticker
			}

			req := dashboard.AgentRequest{
				Name:      a.Name,
				Target:    fmt.Sprintf("Analyze %s - %s", ticker, a.Name),
				LLMPrompt: fullPrompt,
				MCPTool:   a.MCPTool,
				MCPArgs:   args,
			}
			orchID, err := orch.CreateAgent(req)
			if err != nil {
				continue
			}
			yamlToOrch[a.ID] = orchID
			createdYAML = append(createdYAML, a.ID)
		}

		// Wait for this phase's agents to complete.
		if len(createdYAML) > 0 {
			waitForPhase(orch, yamlToOrch, createdYAML)
		} else {
			break
		}
	}

	return yamlToOrch
}

// waitForPhase polls until all agents in the phase complete.
func waitForPhase(orch *dashboard.Orchestrator, yamlToOrch map[string]string, yamlIDs []string) {
	for range 90 { // Up to 90 seconds.
		allDone := true
		for _, yID := range yamlIDs {
			orchID, ok := yamlToOrch[yID]
			if !ok {
				continue
			}
			ag, ok := orch.GetAgent(orchID)
			if !ok || (ag.Status != "completed" && ag.Status != "failed") {
				allDone = false
				break
			}
		}
		if allDone {
			return
		}
		// Check again after a short wait so we don't busy-loop.
		for _, yID := range yamlIDs {
			if orchID, ok := yamlToOrch[yID]; ok {
				orch.GetAgent(orchID) // Just to keep the interface warm.
			}
		}
	}
}

// AgentNameByID returns the agent name for a given YAML ID.
func (c *Config) AgentNameByID(id string) string {
	for _, a := range c.Agents {
		if a.ID == id {
			return a.Name
		}
	}
	return id
}

// Order returns agent IDs in display order.
func (c *Config) Order() []string {
	preferred := []string{"fundamentals", "sentiment", "news", "technical", "bull", "bear", "trader", "risk", "pm"}
	var result []string
	seen := make(map[string]bool)
	for _, id := range preferred {
		for _, a := range c.Agents {
			if a.ID == id && !seen[id] {
				result = append(result, id)
				seen[id] = true
			}
		}
	}
	return result
}

// FormatOutput cleans up agent analysis text for display.
func FormatOutput(analysis string) string {
	analysis = strings.TrimSpace(analysis)
	analysis = strings.TrimPrefix(analysis, "```json")
	analysis = strings.TrimPrefix(analysis, "```")
	analysis = strings.TrimSuffix(analysis, "```")
	analysis = strings.TrimSpace(analysis)
	return analysis
}
