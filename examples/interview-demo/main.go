package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/llm"
	"github.com/Timwood0x10/ares/internal/tools/resources/agent"
	"github.com/Timwood0x10/ares/internal/tools/resources/builtin"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// InterviewAgent demonstrates autonomous search-analyze-output workflow
// for technical interview questions.
type InterviewAgent struct {
	id           string
	name         string
	desc         string
	tools        *agent.AgentTools
	llmClient    *llm.Client
	systemPrompt string
}

// NewInterviewAgent creates a new interview agent.
func NewInterviewAgent(id, name, desc string, toolCfg *agent.AgentToolConfig, llmClient *llm.Client, systemPrompt string) (*InterviewAgent, error) {
	return &InterviewAgent{
		id:           id,
		name:         name,
		desc:         desc,
		tools:        agent.NewAgentTools(toolCfg),
		llmClient:    llmClient,
		systemPrompt: systemPrompt,
	}, nil
}

// Start logs agent info.
func (a *InterviewAgent) Start(ctx context.Context) error {
	tools := a.tools.ListTools()
	capSummary := a.tools.GetCapabilitySummary()
	slog.Info("Agent started",
		"id", a.id,
		"tools", len(tools),
		"capabilities", capSummary,
	)
	return nil
}

// Process handles a user question using the search-analyze-output workflow.
func (a *InterviewAgent) Process(ctx context.Context, userMsg string) (string, error) {
	// Step 1: Detect capabilities
	capabilities := a.tools.DetectCapabilities(userMsg)
	slog.Info("Query analysis",
		"query", userMsg,
		"capabilities", capabilities,
	)

	// Step 2: Build prompt with matched tools
	toolPrompt := a.buildToolPrompt(userMsg)

	// Phase 1: Tool execution — gather information without accumulating LLM output
	// Only tool results are accumulated in the prompt, keeping it much smaller.
	gathered := make([]string, 0)

	for round := 0; round < 3; round++ {
		var prompt string
		if len(gathered) == 0 {
			prompt = fmt.Sprintf("%s\n\n%s\n\n## User Question\n%s\n\nUse available tools to gather information, then provide your final answer.\n\n## Response\n",
				a.systemPrompt, toolPrompt, userMsg)
		} else {
			prompt = fmt.Sprintf("%s\n\n%s\n\n## User Question\n%s\n\n## Information Gathered\n%s\n\nUse additional tools if needed, or provide your final answer.\n\n## Response\n",
				a.systemPrompt, toolPrompt, userMsg, strings.Join(gathered, "\n---\n"))
		}

		resp, err := a.llmClient.Generate(ctx, prompt)
		if err != nil {
			return "", fmt.Errorf("LLM generation failed: %w", err)
		}

		// No tool call → final answer
		if !strings.Contains(resp, "[TOOL:") {
			return resp, nil
		}

		// Execute tool calls and save results only (not the LLM's own output)
		toolResp, err := a.executeToolCalls(ctx, resp)
		if err != nil {
			return "", fmt.Errorf("tool execution failed: %w", err)
		}
		// Distill: truncate overly large results to keep prompt within budget
		toolResp = distillText(toolResp, 3000)
		gathered = append(gathered, fmt.Sprintf("## Round %d Tool Results\n%s", round+1, toolResp))
	}

	// Phase 2: Force final synthesis with all gathered information
	finalPrompt := fmt.Sprintf("%s\n\nBased on the information gathered below, provide a comprehensive final answer to the user's question.\n\n## User Question\n%s\n\n## Information Gathered\n%s\n\n## Final Answer\n",
		a.systemPrompt, userMsg, strings.Join(gathered, "\n"))
	finalResp, err := a.llmClient.Generate(ctx, finalPrompt)
	if err != nil {
		return "", fmt.Errorf("LLM final answer failed: %w", err)
	}
	return finalResp, nil
}

// buildToolPrompt creates the tool instructions with matched tools.
func (a *InterviewAgent) buildToolPrompt(userMsg string) string {
	schemas := a.tools.MatchToolSchemasByQuery(userMsg)

	if len(schemas) == 0 {
		// If ACE misses, fall back to web_search
		schemas = a.tools.GetSchemas()
		filtered := make([]core.ToolSchema, 0)
		for _, s := range schemas {
			if s.Name == "web_search" {
				filtered = append(filtered, s)
			}
		}
		schemas = filtered
		slog.Warn("No tools matched by ACE, falling back to web_search", "query", userMsg)
	}

	if len(schemas) == 0 {
		return "No tools available."
	}

	var sb strings.Builder
	sb.WriteString("## Available Tools\n\n")
	for _, s := range schemas {
		fmt.Fprintf(&sb, "### %s\n%s\n", s.Name, s.Description)
		if len(s.Parameters.GetProperties()) > 0 {
			sb.WriteString("**Parameters:**\n")
			for name, p := range s.Parameters.GetProperties() {
				req := ""
				for _, r := range s.Parameters.GetRequired() {
					if r == name {
						req = " (required)"
						break
					}
				}
				fmt.Fprintf(&sb, "- `%s` (%s)%s: %s\n", name, p.Type, req, p.Description)
				if len(p.Enum) > 0 {
					fmt.Fprintf(&sb, "  Values: %v\n", p.Enum)
				}
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Tool Usage Format\n")
	sb.WriteString("`[TOOL:tool_name {\"param\": \"value\"}]`\n\n")
	sb.WriteString("Examples:\n")
	sb.WriteString("- `[TOOL:web_search {\"query\": \"Golang context deadline exceeded\"}]`\n")
	sb.WriteString("- `[TOOL:web_search {\"query\": \"system design interview\", \"max_results\": 5}]`\n")

	return sb.String()
}

// executeToolCalls parses and executes tool calls from LLM response.
func (a *InterviewAgent) executeToolCalls(ctx context.Context, resp string) (string, error) {
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

		slog.Info("Executing tool", "tool", toolName, "params", paramsJSON)
		result, err := a.tools.Execute(ctx, toolName, jsonToMap(paramsJSON))
		if err != nil {
			slog.Error("Tool execution failed", "tool", toolName, "error", err)
			results = append(results, fmt.Sprintf("**%s** error: %v", toolName, err))
			continue
		}

		if result.Error != "" {
			results = append(results, fmt.Sprintf("**%s** failed: %s", toolName, result.Error))
			continue
		}

		// Format result for LLM consumption
		if data, ok := result.Data.(map[string]interface{}); ok {
			var sb strings.Builder
			fmt.Fprintf(&sb, "**%s** results:\n", toolName)

			if resultsList, ok := data["results"].([]map[string]interface{}); ok {
				fmt.Fprintf(&sb, "- Query: %s\n", data["query"])
				fmt.Fprintf(&sb, "- Total results: %v\n", data["total_results"])
				for i, r := range resultsList {
					title, _ := r["title"].(string)
					url, _ := r["url"].(string)
					snippet, _ := r["snippet"].(string)
					engine, _ := r["engine"].(string)
					fmt.Fprintf(&sb, "\n**Result %d**\n", i+1)
					fmt.Fprintf(&sb, "- Title: %s\n", title)
					fmt.Fprintf(&sb, "- URL: %s\n", url)
					fmt.Fprintf(&sb, "- Snippet: %s\n", snippet)
					fmt.Fprintf(&sb, "- Engine: %s\n", engine)
				}
			} else {
				// Generic fallback for other tools (http_request, etc.)
				formatted, _ := json.MarshalIndent(data, "", "  ")
				sb.WriteString(string(formatted))
			}
			results = append(results, sb.String())
		} else if dataStr, ok := result.Data.(string); ok {
			results = append(results, fmt.Sprintf("**%s** result: %s", toolName, dataStr))
		} else if formatted, ok := result.Metadata["formatted"]; ok {
			results = append(results, formatted.(string))
		} else {
			results = append(results, fmt.Sprintf("**%s** completed successfully", toolName))
		}
	}

	if len(results) == 0 {
		return "", nil
	}
	return strings.Join(results, "\n"), nil
}

// showCapabilities prints available capabilities and tools.
func (a *InterviewAgent) showCapabilities() string {
	summary := a.tools.GetCapabilitySummary()
	var sb strings.Builder
	fmt.Fprintf(&sb, "=== %s ===\n", a.name)
	fmt.Fprintf(&sb, "%s\n\nCapabilities:\n", a.desc)
	for cap, count := range summary {
		tools := a.tools.GetToolsByCapability(cap)
		names := make([]string, len(tools))
		for i, t := range tools {
			names[i] = t.Name()
		}
		fmt.Fprintf(&sb, "  %s (%d): %s\n", cap, count, strings.Join(names, ", "))
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

// distillText truncates text that exceeds maxChars, keeping the head and adding a truncation note.
// This prevents single large tool results from blowing the prompt budget.
func distillText(s string, maxChars int) string {
	const truncNote = "\n\n[... truncated; original length: %d chars, kept head %d chars]"
	const maxNoteLen = 80

	effectiveMax := maxChars - maxNoteLen
	if effectiveMax <= 0 {
		effectiveMax = maxChars / 2
	}
	if len(s) <= maxChars {
		return s
	}
	return s[:effectiveMax] + fmt.Sprintf(truncNote, len(s), effectiveMax)
}

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	fmt.Println("========================================")
	fmt.Println("  Interview Search Agent Demo")
	fmt.Println("  Autonomous Search → Analyze → Output")
	fmt.Println("========================================")
	fmt.Println()

	// Load ares_config
	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = "./ares_config/server.yaml"
	}

	cfg, err := ares_config.Load(cfgPath)
	if err != nil {
		slog.Error("Load ares_config failed", "error", err)
		fmt.Fprintf(os.Stderr, "Config error: %v\n", err)
		os.Exit(1)
	}

	if err := ares_config.LoadFromEnv(cfg); err != nil {
		slog.Error("Load env ares_config failed", "error", err)
		os.Exit(1)
	}

	// Create LLM client
	llmClient, err := llm.NewClient(&llm.Config{
		Provider:        cfg.LLM.Provider,
		APIKey:          cfg.LLM.APIKey,
		BaseURL:         cfg.LLM.BaseURL,
		Model:           cfg.LLM.Model,
		Timeout:         cfg.LLM.Timeout,
		MaxTokens:       cfg.LLM.MaxTokens,
		MaxPromptLength: cfg.LLM.MaxPromptLength,
	})
	if err != nil {
		slog.Error("Create LLM client failed", "error", err)
		os.Exit(1)
	}

	// Register builtin tools (including web_search)
	if err := builtin.RegisterGeneralTools(); err != nil {
		slog.Error("Register tools failed", "error", err)
		os.Exit(1)
	}

	// Create agent with all tools
	toolCfg := &agent.AgentToolConfig{
		Enabled: nil, // All tools enabled
	}

	systemPrompt := `You are an Interview Search Agent designed to answer technical interview questions by searching the web and synthesizing results.

## Workflow
1. Analyze the question to determine what information is needed
2. Use web_search to find relevant, up-to-date information
3. Analyze and synthesize the search results
4. Provide a clear, structured answer

## Tool Usage Format
[TOOL:tool_name {"param": "value"}]

Include ALL required parameters for the tool. After executing a tool, provide a natural language analysis.

## Rules
- Always search before answering for factual or technical questions
- Cite sources by URL from the search results
- If results are insufficient, refine your search query and try again
- Provide structured answers with code examples when relevant
- Be honest — if you can't find good results, say so`

	agent, err := NewInterviewAgent(
		"interview-demo-1",
		"Interview Search Agent",
		"Autonomous search-analyze-output agent for technical interview questions",
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

	fmt.Println("Agent initialized. Ready for interview questions.")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  caps              - Show available capabilities and tools")
	fmt.Println("  exit / quit       - Exit")
	fmt.Println()

	// Check SearXNG connectivity
	checkSearXNG()

	fmt.Println("Ask a technical interview question (e.g., 'Explain Go context deadline exceeded'):")
	fmt.Println("---")

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
			fmt.Println("Bye!")
			return
		case "caps", "capabilities":
			fmt.Println(agent.showCapabilities())
			continue
		}

		// Process with search-analyze-output workflow
		start := time.Now()
		resp, err := agent.Process(ctx, input)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}
		fmt.Println(resp)
		fmt.Printf("\n--- (completed in %s) ---\n", time.Since(start).Round(time.Millisecond))
	}
}

// checkSearXNG verifies SearXNG is reachable.
func checkSearXNG() {
	client := &http.Client{Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()

	resp, err := client.Get("http://localhost:5605/search?q=test&format=json")
	if err != nil {
		fmt.Println("WARNING: SearXNG not reachable on http://localhost:5605")
		fmt.Println("  Run: docker compose up -d searxng")
		fmt.Println("  Or set SEARXNG_BASE_URL env var")
		fmt.Println()
		return
	}

	if err := resp.Body.Close(); err != nil {
		fmt.Printf("Error closing response body: %v\n", err)
	}

	if resp.StatusCode == 200 {
		fmt.Println("SearXNG is ready on http://localhost:5605")
	} else {
		fmt.Printf("SearXNG returned status %d\n", resp.StatusCode)
	}
	fmt.Println()
}
