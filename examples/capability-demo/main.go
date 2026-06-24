package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/config"
	"github.com/Timwood0x10/ares/internal/llm"
	"github.com/Timwood0x10/ares/internal/tools/resources/agent"
	"github.com/Timwood0x10/ares/internal/tools/resources/builtin"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// CapabilityDemoAgent demonstrates the Agent Capability Engine (ACE) workflow.
//
// ACE Workflow:
// 1. User Query → LLM analyzes intent
// 2. Intent → Detect Capability (math, knowledge, network, etc.)
// 3. Capability → Match Tools (2-4 tools instead of all tools)
// 4. Tools → Execute → Return Result
//
// This reduces LLM tool selection overhead and improves accuracy.
type CapabilityDemoAgent struct {
	id           string
	name         string
	desc         string
	tools        *agent.AgentTools
	llmClient    *llm.Client
	systemPrompt string
}

// NewCapabilityDemoAgent creates a new capability demo agent.
func NewCapabilityDemoAgent(id, name, desc string, toolCfg *agent.AgentToolConfig, llmClient *llm.Client, systemPrompt string) (*CapabilityDemoAgent, error) {
	agentTools := agent.NewAgentTools(toolCfg)
	return &CapabilityDemoAgent{
		id:           id,
		name:         name,
		desc:         desc,
		tools:        agentTools,
		llmClient:    llmClient,
		systemPrompt: systemPrompt,
	}, nil
}

// Start initializes the agent.
func (a *CapabilityDemoAgent) Start(ctx context.Context) error {
	slog.Info("Agent started",
		"id", a.id,
		"name", a.name,
		"tool_count", len(a.tools.ListTools()))

	// Log capability summary
	summary := a.tools.GetCapabilitySummary()
	slog.Info("Capability summary", "summary", summary)
	return nil
}

// Process handles user input with ACE workflow.
func (a *CapabilityDemoAgent) Process(ctx context.Context, userMsg string) (string, error) {
	// Step 1: Detect capabilities from user query
	capabilities := a.tools.DetectCapabilities(userMsg)
	slog.Info("ACE: Capabilities detected",
		"query", userMsg,
		"capabilities", capabilities)

	// Step 2: Match tools based on capabilities
	matchedTools := a.tools.MatchToolsByQuery(userMsg)
	slog.Info("ACE: Tools matched",
		"count", len(matchedTools),
		"tools", getToolNames(matchedTools))

	// Step 3: Generate tool prompt (only with matched tools)
	toolPrompt := a.generateToolPrompt(userMsg)
	fullPrompt := fmt.Sprintf("%s\n\n%s\n\nUser: %s\nAssistant:", a.systemPrompt, toolPrompt, userMsg)

	currentPrompt := fullPrompt
	history := make([]string, 0)

	// Step 4: LLM reasoning with filtered tools
	for round := 0; round < 3; round++ {
		resp, err := a.llmClient.Generate(ctx, currentPrompt)
		if err != nil {
			return "", fmt.Errorf("LLM generation failed: %w", err)
		}

		// Check if tool call is needed
		if !strings.Contains(resp, "[TOOL:") {
			return resp, nil
		}

		// Step 5: Execute tools
		toolResp, err := a.handleToolCalls(ctx, resp)
		if err != nil {
			return "", fmt.Errorf("tool execution failed: %w", err)
		}

		history = append(history, fmt.Sprintf("Assistant: %s\nTool Result: %s", resp, toolResp))
		currentPrompt = fmt.Sprintf("%s\n\n%s\n\n%s", a.systemPrompt, userMsg, strings.Join(history, "\n"))
	}

	return a.llmClient.Generate(ctx, currentPrompt)
}

// generateToolPrompt creates a prompt with matched tools.
func (a *CapabilityDemoAgent) generateToolPrompt(userMsg string) string {
	// Use ACE MatchToolSchemasByQuery to filter tools
	schemas := a.tools.MatchToolSchemasByQuery(userMsg)

	if len(schemas) == 0 {
		// Fallback to essential tools if no match (avoid prompt overflow)
		essentialTools := []string{"calculator", "datetime", "file_tools", "http_request"}
		schemas = a.tools.GetSchemas()

		// Filter to only essential tools
		essentialSet := make(map[string]bool)
		for _, tool := range essentialTools {
			essentialSet[tool] = true
		}

		filtered := make([]core.ToolSchema, 0)
		for _, schema := range schemas {
			if essentialSet[schema.Name] {
				filtered = append(filtered, schema)
			}
		}
		schemas = filtered

		slog.Warn("ACE: No tools matched, using essential tools", "tools", essentialTools)
	}

	if len(schemas) == 0 {
		return "No tools available."
	}

	var sb strings.Builder
	sb.WriteString("Available Tools:\n")
	for _, s := range schemas {
		fmt.Fprintf(&sb, "- %s: %s\n", s.Name, s.Description)
		if len(s.Parameters.GetProperties()) > 0 {
			sb.WriteString("  Parameters:\n")
			for name, p := range s.Parameters.GetProperties() {
				info := fmt.Sprintf("    - %s (%s): %s", name, p.Type, p.Description)
				if len(p.Enum) > 0 {
					info += fmt.Sprintf(" [values: %v]", p.Enum)
				}
				sb.WriteString(info + "\n")
			}
		}
	}

	sb.WriteString("\nTool Usage: [TOOL:tool_name {\"param\": \"value\"}]\n")
	sb.WriteString("CRITICAL: Use exact format [TOOL:tool_name {...}] with colon after TOOL\n")
	sb.WriteString("Examples:\n")
	sb.WriteString("- [TOOL:calculator {\"expression\": \"1+2\"}]\n")
	sb.WriteString("- [TOOL:datetime {\"operation\": \"now\"}]\n")
	sb.WriteString("- [TOOL:http_request {\"url\": \"https://api.example.com/data\"}]\n")
	sb.WriteString("- [TOOL:file_tools {\"operation\": \"list\", \"directory_path\": \".\"}]\n")
	sb.WriteString("- [TOOL:file_tools {\"operation\": \"read\", \"file_path\": \"./config/server.yaml\"}]\n")
	return sb.String()
}

// handleToolCalls executes tool calls from LLM response.
func (a *CapabilityDemoAgent) handleToolCalls(ctx context.Context, resp string) (string, error) {
	lines := strings.Split(resp, "\n")
	var results []string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "[TOOL:") {
			continue
		}

		end := strings.Index(line, "]")
		if end == -1 {
			continue
		}

		content := strings.TrimPrefix(line[:end], "[TOOL:")
		parts := strings.SplitN(content, " ", 2)
		if len(parts) < 2 {
			continue
		}

		toolName := parts[0]
		paramsJSON := parts[1]

		result, err := a.tools.Execute(ctx, toolName, jsonToMap(paramsJSON))
		if err != nil {
			slog.Error("Tool execution failed", "tool", toolName, "error", err)
			results = append(results, fmt.Sprintf("%s failed: %v", toolName, err))
			continue
		}

		if len(result.Metadata) > 0 {
			if formatted, ok := result.Metadata["formatted"]; ok {
				results = append(results, formatted.(string))
			}
		}
	}

	return strings.Join(results, "\n"), nil
}

// listCapabilities shows all available capabilities.
func (a *CapabilityDemoAgent) listCapabilities() string {
	summary := a.tools.GetCapabilitySummary()
	var sb strings.Builder
	fmt.Fprintf(&sb, "I'm %s: %s\n\nCapabilities:\n", a.name, a.desc)

	for cap, count := range summary {
		tools := a.tools.GetToolsByCapability(cap)
		toolNames := getToolNames(tools)
		fmt.Fprintf(&sb, "• %s (%d tools): %v\n", cap, count, toolNames)
	}
	return sb.String()
}

// showACEWorkflow demonstrates the ACE workflow for a query.
func (a *CapabilityDemoAgent) showACEWorkflow(query string) string {
	var sb strings.Builder

	sb.WriteString("=== ACE Workflow Analysis ===\n")
	fmt.Fprintf(&sb, "Query: %s\n\n", query)

	// Step 1: Detect capabilities
	capabilities := a.tools.DetectCapabilities(query)
	sb.WriteString("Step 1 - Capability Detection:\n")
	if len(capabilities) == 0 {
		sb.WriteString("  No specific capabilities detected\n")
	} else {
		for _, cap := range capabilities {
			fmt.Fprintf(&sb, "  ✓ %s\n", cap)
		}
	}
	sb.WriteString("\n")

	// Step 2: Match tools
	matchedTools := a.tools.MatchToolsByQuery(query)
	sb.WriteString("Step 2 - Tool Matching:\n")
	if len(matchedTools) == 0 {
		sb.WriteString("  No tools matched\n")
	} else {
		for _, tool := range matchedTools {
			fmt.Fprintf(&sb, "  ✓ %s: %s\n", tool.Name(), tool.Description())
		}
	}
	sb.WriteString("\n")

	// Step 3: Capability summary
	summary := a.tools.GetCapabilitySummary()
	sb.WriteString("Step 3 - System Capability Summary:\n")
	for cap, count := range summary {
		fmt.Fprintf(&sb, "  • %s: %d tools\n", cap, count)
	}

	return sb.String()
}

// jsonToMap converts JSON string to map.
func jsonToMap(s string) map[string]interface{} {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return map[string]interface{}{}
	}
	return m
}

// getToolNames extracts tool names.
func getToolNames(tools []core.Tool) []string {
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name()
	}
	return names
}

func main() {
	slog.Info("Starting Capability Demo Agent")

	// Load config
	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = "./config/server.yaml"
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		slog.Error("Load config failed", "error", err)
		os.Exit(1)
	}

	if err := config.LoadFromEnv(cfg); err != nil {
		slog.Error("Load env config failed", "error", err)
		os.Exit(1)
	}

	// Create LLM client
	llmClient, err := llm.NewClient(&llm.Config{
		Provider: cfg.LLM.Provider,
		APIKey:   cfg.LLM.APIKey,
		BaseURL:  cfg.LLM.BaseURL,
		Model:    cfg.LLM.Model,
		Timeout:  cfg.LLM.Timeout,
	})
	if err != nil {
		slog.Error("Create LLM client failed", "error", err)
		os.Exit(1)
	}

	// Register builtin tools
	if err := builtin.RegisterGeneralTools(); err != nil {
		slog.Error("Register tools failed", "error", err)
		os.Exit(1)
	}

	// Create agent with all tools enabled
	toolCfg := &agent.AgentToolConfig{
		Enabled: nil, // All tools enabled
	}

	systemPrompt := `You are a helpful assistant with access to various tools.
Use tools when appropriate to answer user questions accurately.

CRITICAL: When you need to use a tool, you MUST format your response EXACTLY as:
[TOOL:tool_name {"param": "value"}]

The format must start with [TOOL: followed by the tool name, then a space, then JSON parameters.

For example:
- [TOOL:calculator {"expression": "100*(100+1)/2"}]
- [TOOL:datetime {"operation": "now"}]
- [TOOL:file_tools {"operation": "list", "directory_path": "."}]

IMPORTANT: 
- Always use the exact format: [TOOL:tool_name {...}]
- Include ALL required parameters for the tool
- After executing a tool, provide a natural language response based on the result

If you don't need a tool, just answer directly.`

	agent, err := NewCapabilityDemoAgent(
		"capability-demo-1",
		"Capability Demo Agent",
		"Demonstrates ACE workflow: Query → Capability → Tools → Result",
		toolCfg,
		llmClient,
		systemPrompt,
	)
	if err != nil {
		slog.Error("Create agent failed", "error", err)
		os.Exit(1)
	}

	ctx := context.Background()
	if err := agent.Start(ctx); err != nil {
		slog.Warn("Start agent failed", "error", err)
	}

	fmt.Println("=== Capability Demo Agent ===")
	fmt.Println("This demo shows the ACE workflow:")
	fmt.Println("  1. Query → LLM analyzes intent")
	fmt.Println("  2. Intent → Detect Capability")
	fmt.Println("  3. Capability → Match Tools (2-4 tools)")
	fmt.Println("  4. Tools → Execute → Return Result")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  capabilities - Show all capabilities and tools")
	fmt.Println("  analyze <query> - Show ACE workflow analysis for a query")
	fmt.Println("  exit - Quit")
	fmt.Println()
	fmt.Println("Try queries like:")
	fmt.Println("  - 'Calculate 1 to 100 sum' (math capability)")
	fmt.Println("  - 'What time is it?' (time capability)")
	fmt.Println("  - 'Search for information' (knowledge capability)")
	fmt.Println("  - 'Send HTTP request' (network capability)")
	fmt.Println()
	fmt.Println("Start... (type 'exit' to quit)")

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			break
		}

		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		switch strings.ToLower(input) {
		case "exit", "quit":
			slog.Info("Shutting down...")
			return
		case "capabilities":
			fmt.Println(agent.listCapabilities())
			continue
		}

		// Check for analyze command
		if strings.HasPrefix(strings.ToLower(input), "analyze ") {
			query := strings.TrimSpace(input[8:])
			if query != "" {
				fmt.Println(agent.showACEWorkflow(query))
			}
			continue
		}

		// Process with ACE workflow
		start := time.Now()
		resp, err := agent.Process(ctx, input)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}
		fmt.Printf("%s (%v)\n\n", resp, time.Since(start).Round(time.Millisecond))
	}
}
