package api

import (
	"fmt"

	"github.com/Timwood0x10/ares/internal/dashboard"
	"github.com/Timwood0x10/ares/internal/ares_mcp"
)

// ReviewTask defines a data-driven review agent configuration.
type ReviewTask struct {
	Name   string
	Tools  [][2]string // pairs of (shortName, argsValue)
	Prompt string
}

// DefaultReviewTasks is the default set of code review tasks.
var DefaultReviewTasks = []ReviewTask{
	{
		Name:   "Architecture Review",
		Tools:  [][2]string{{"files", ""}, {"search", "func main|func New|func Start"}, {"context", "analyze package dependencies"}},
		Prompt: "You are a senior code architect. Analyze:\n1. Architecture pattern\n2. Dependency flow\n3. Entry points\n4. Separation of concerns\n5. Improvement suggestions\n\nData:\n{{.raw_data}}",
	},
	{
		Name:   "Error Handling Review",
		Tools:  [][2]string{{"search", "ErrXxx|errors.New|fmt.Errorf"}, {"context", "find all error handling patterns"}, {"callers", "fmt.Errorf"}},
		Prompt: "Review error handling:\n1. Errors wrapped with context?\n2. Swallowed errors?\n3. Sentinel errors?\n4. Panic in non-init?\n\nData:\n{{.raw_data}}",
	},
	{
		Name:   "Concurrency Review",
		Tools:  [][2]string{{"search", "go func|errgroup|sync.Mutex"}, {"context", "find goroutine and mutex patterns"}, {"callers", "errgroup.Group"}},
		Prompt: "Review concurrency:\n1. Bare go without errgroup?\n2. Goroutine leaks?\n3. Unprotected shared state?\n4. Race conditions?\n\nData:\n{{.raw_data}}",
	},
	{
		Name:   "API Surface Review",
		Tools:  [][2]string{{"search", "type.*interface"}, {"search", "func New"}, {"context", "analyze public API consistency"}},
		Prompt: "Review API surface:\n1. Interfaces small and focused?\n2. Naming consistent?\n3. Constructor patterns?\n4. Breaking change risks?\n\nData:\n{{.raw_data}}",
	},
	{
		Name:   "Change Impact Analysis",
		Tools:  [][2]string{{"impact", "Tool"}, {"callers", "Tool"}, {"context", "find all Tool interface implementations"}},
		Prompt: "Analyze impact of changing Tool interface:\n1. Implementations that break\n2. Test coverage gaps\n3. Migration strategy\n4. Risk assessment\n\nData:\n{{.raw_data}}",
	},
}

// reviewArgKeys maps tool short names to their argument key names.
var reviewArgKeys = map[string]string{
	"search":  "search",
	"context": "task",
	"callers": "symbol",
	"impact":  "symbol",
}

// BuildAgentRequest converts a ReviewTask to a dashboard.AgentRequest.
func BuildAgentRequest(task ReviewTask) dashboard.AgentRequest {
	steps := make([]dashboard.AgentStep, 0, len(task.Tools))
	for _, pair := range task.Tools {
		shortName, argValue := pair[0], pair[1]
		step := dashboard.AgentStep{Tool: shortName}
		if argValue != "" {
			if argKey, ok := reviewArgKeys[shortName]; ok {
				step.Args = map[string]any{argKey: argValue}
			}
		}
		steps = append(steps, step)
	}
	return dashboard.AgentRequest{
		Name:      task.Name,
		Steps:     steps,
		LLMPrompt: task.Prompt,
	}
}

// BuildTemplates creates agent templates for the orchestrator UI.
func BuildTemplates() []dashboard.AgentTemplate {
	templates := make([]dashboard.AgentTemplate, 0, len(DefaultReviewTasks))
	for i, task := range DefaultReviewTasks {
		toolName := ""
		if len(task.Tools) > 0 {
			toolName = task.Tools[0][0]
		}
		templates = append(templates, dashboard.AgentTemplate{
			ID:        fmt.Sprintf("tpl-%d", i),
			Name:      task.Name,
			MCPTool:   toolName,
			LLMPrompt: task.Prompt,
		})
	}
	return templates
}

// BuildToolAliases creates short-name mappings from MCP tool list.
func BuildToolAliases(tools []ares_mcp.MCPToolDef) map[string]string {
	infos := make([]dashboard.MCPToolInfo, len(tools))
	for i, t := range tools {
		infos[i] = dashboard.MCPToolInfo{Name: t.Name, Description: t.Description}
	}
	return dashboard.BuildToolAliases(infos)
}
