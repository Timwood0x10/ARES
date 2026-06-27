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

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/llm"
	"github.com/Timwood0x10/ares/internal/tools/resources/agent"
	"github.com/Timwood0x10/ares/internal/tools/resources/builtin"
)

type DialogAgent struct {
	id           string
	name         string
	desc         string
	tools        *agent.AgentTools
	llmClient    *llm.Client
	systemPrompt string
}

func NewDialogAgent(id, name, desc string, toolCfg *agent.AgentToolConfig, llmClient *llm.Client, systemPrompt string) (*DialogAgent, error) {
	agentTools := agent.NewAgentTools(toolCfg)
	return &DialogAgent{
		id:           id,
		name:         name,
		desc:         desc,
		tools:        agentTools,
		llmClient:    llmClient,
		systemPrompt: systemPrompt,
	}, nil
}

func (a *DialogAgent) Start(ctx context.Context) error {
	slog.Info("Agent started", "id", a.id, "name", a.name, "tool_count", len(a.tools.ListTools()))
	return nil
}

func (a *DialogAgent) Process(ctx context.Context, userMsg string) (string, error) {
	if a.isCapabilityQuestion(userMsg) {
		return a.listCapabilities(), nil
	}

	// Generate tool prompt with usage instructions (filtered by ACE)
	toolPrompt := a.generateToolPrompt(userMsg)
	fullPrompt := fmt.Sprintf("%s\n\n%s\n\nUser: %s\nAssistant:", a.systemPrompt, toolPrompt, userMsg)

	currentPrompt := fullPrompt
	history := make([]string, 0)

	for round := 0; round < 3; round++ {
		resp, err := a.llmClient.Generate(ctx, currentPrompt)
		if err != nil {
			return "", fmt.Errorf("LLM failed: %w", err)
		}

		if !strings.Contains(resp, "[TOOL:") {
			return resp, nil
		}

		toolResp, err := a.handleToolCalls(ctx, resp)
		if err != nil {
			return "", fmt.Errorf("tool failed: %w", err)
		}

		history = append(history, fmt.Sprintf("Assistant: %s\nTool Result: %s", resp, toolResp))
		currentPrompt = fmt.Sprintf("%s\n\n%s\n\n%s", a.systemPrompt, userMsg, strings.Join(history, "\n"))
	}

	return a.llmClient.Generate(ctx, currentPrompt)
}

func (a *DialogAgent) isCapabilityQuestion(msg string) bool {
	// Only explicitly ask about capabilities
	keywords := []string{"你会什么", "你能做什么", "what can you do", "capabilities", "有哪些工具", "工具列表"}
	lower := strings.ToLower(msg)
	for _, k := range keywords {
		if strings.Contains(lower, k) {
			return true
		}
	}
	return false
}

func (a *DialogAgent) generateToolPrompt(userMsg string) string {
	// Use ACE MatchToolsByQuery to filter tools based on user query
	// This limits LLM to see only relevant tools (2-4) instead of all tools
	schemas := a.tools.MatchToolSchemasByQuery(userMsg)
	if len(schemas) == 0 {
		schemas = a.tools.GetSchemas()
	}

	if len(schemas) == 0 {
		return "No tools available."
	}

	var sb strings.Builder
	sb.WriteString("Tools:\n")
	for _, s := range schemas {
		fmt.Fprintf(&sb, "- %s: %s\n", s.Name, s.Description)
		if len(s.Parameters.GetProperties()) > 0 {
			sb.WriteString("  Params:\n")
			for name, p := range s.Parameters.GetProperties() {
				info := fmt.Sprintf("    - %s (%s): %s", name, p.Type, p.Description)
				if len(p.Enum) > 0 {
					info += fmt.Sprintf(" [values: %v]", p.Enum)
				}
				sb.WriteString(info + "\n")
			}
		}
	}

	sb.WriteString("\nUse: [TOOL:tool_name {\"param\": \"value\"}]\n")
	sb.WriteString("Examples:\n")
	sb.WriteString("- [TOOL:datetime {\"operation\": \"now\"}]\n")
	sb.WriteString("- [TOOL:calculator {\"operation\": \"add\", \"operands\": [1, 2]}]\n")
	sb.WriteString("- [TOOL:file_tools {\"operation\": \"read\", \"file_path\": \"/path/to/file\"}]\n")
	return sb.String()
}

func (a *DialogAgent) listCapabilities() string {
	schemas := a.tools.GetSchemas()
	var sb strings.Builder
	fmt.Fprintf(&sb, "I'm %s: %s\n\nTools:\n", a.name, a.desc)
	for _, s := range schemas {
		fmt.Fprintf(&sb, "• %s: %s\n", s.Name, s.Description)
	}
	return sb.String()
}

func (a *DialogAgent) handleToolCalls(ctx context.Context, resp string) (string, error) {
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

func jsonToMap(s string) map[string]interface{} {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return map[string]interface{}{}
	}
	return m
}

type ConversationSession struct {
	agents map[string]*DialogAgent
	memory []string
}

func NewConversationSession(agents []*DialogAgent) *ConversationSession {
	m := make(map[string]*DialogAgent)
	for _, a := range agents {
		m[a.id] = a
	}
	return &ConversationSession{agents: m, memory: make([]string, 0)}
}

func (cs *ConversationSession) Process(ctx context.Context, input string) (string, error) {
	cs.memory = append(cs.memory, fmt.Sprintf("User: %s", input))
	if len(cs.memory) > 20 {
		cs.memory = cs.memory[1:]
	}

	agentID := cs.selectAgent(input)
	agent, ok := cs.agents[agentID]
	if !ok {
		return "", fmt.Errorf("agent %s not found", agentID)
	}

	resp, err := agent.Process(ctx, input)
	if err != nil {
		return "", fmt.Errorf("agent failed: %w", err)
	}

	cs.memory = append(cs.memory, fmt.Sprintf("%s: %s", agent.name, resp))
	if len(cs.memory) > 20 {
		cs.memory = cs.memory[1:]
	}

	return fmt.Sprintf("[%s]: %s", agent.name, resp), nil
}

func (cs *ConversationSession) selectAgent(input string) string {
	lower := strings.ToLower(input)
	keywords := []string{"搜索", "分析", "天气", "search", "analyze", "weather"}
	for _, k := range keywords {
		if strings.Contains(lower, k) {
			return "research-agent-1"
		}
	}
	return "tools-agent-1"
}

func (cs *ConversationSession) ShowMemory() string {
	if len(cs.memory) == 0 {
		return "No history."
	}
	return strings.Join(cs.memory, "\n")
}

func createAgents(cfg *ares_config.Config, llmClient *llm.Client) ([]*DialogAgent, error) {
	if len(cfg.Tools.Agents) == 0 {
		return nil, fmt.Errorf("no agent ares_config")
	}

	var agents []*DialogAgent
	for id, ac := range cfg.Tools.Agents {
		prompt := ac.SystemPrompt
		if prompt == "" {
			prompt = fmt.Sprintf("You are %s. Use [TOOL:tool_name {\"param\": \"value\"}] when needed.", ac.Name)
		}

		toolCfg := &agent.AgentToolConfig{Enabled: ac.Tools}
		a, err := NewDialogAgent(id, ac.Name, ac.Description, toolCfg, llmClient, prompt)
		if err != nil {
			return nil, fmt.Errorf("create agent %s: %w", id, err)
		}
		agents = append(agents, a)
	}
	return agents, nil
}

func main() {
	slog.Info("Starting Multi-Agent Dialog")

	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = "./ares_config/server.yaml"
	}

	cfg, err := ares_config.Load(cfgPath)
	if err != nil {
		slog.Error("Load ares_config failed", "error", err)
		os.Exit(1)
	}

	if err := ares_config.LoadFromEnv(cfg); err != nil {
		slog.Error("Load env ares_config failed", "error", err)
		os.Exit(1)
	}

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

	if err := builtin.RegisterGeneralTools(); err != nil {
		slog.Error("Register tools failed", "error", err)
		os.Exit(1)
	}

	agents, err := createAgents(cfg, llmClient)
	if err != nil {
		slog.Error("Create agents failed", "error", err)
		os.Exit(1)
	}

	session := NewConversationSession(agents)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, a := range agents {
		if err := a.Start(ctx); err != nil {
			slog.Warn("Start agent failed", "id", a.id, "error", err)
		}
	}

	fmt.Println("=== Multi-Agent Dialog ===")
	fmt.Println("Agents:")
	for i, a := range agents {
		fmt.Printf("  %d. %s (%s)\n", i+1, a.name, a.id)
	}
	fmt.Println("Commands: exit, memory, agents")
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
			return
		case "memory":
			fmt.Println(session.ShowMemory())
			continue
		case "agents":
			for _, a := range agents {
				fmt.Printf("\n--- %s ---\n%s\n", a.name, a.listCapabilities())
			}
			continue
		}

		start := time.Now()
		resp, err := session.Process(ctx, input)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}
		fmt.Printf("%s (%v)\n\n", resp, time.Since(start).Round(time.Millisecond))
	}
}
