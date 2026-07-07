package sdk

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Timwood0x10/ares/api/core"
	"github.com/Timwood0x10/ares/api/tools"
)

func TestNew(t *testing.T) {
	rt, err := New(WithOllama("llama3.2"), WithTrace(false))
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}
	if rt == nil {
		t.Fatal("New() returned nil Runtime")
	}
	rt.Close()
}

func TestNewWithAllFeatures(t *testing.T) {
	rt, err := New(
		WithOllama("llama3.2"),
		WithDefaultMemory(),
		WithEvolution(),
		WithAPIKey("test-key"),
		WithBaseURL("http://localhost:11434"),
		WithLLMConfig(&core.LLMConfig{Provider: "ollama", Model: "llama3.2"}),
		WithTrace(false),
	)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer rt.Close()
}

func TestNewWithProviders(t *testing.T) {
	tests := []struct {
		name string
		opt  Option
	}{
		{"openai", WithOpenAI("gpt-4o-mini")},
		{"anthropic", WithAnthropic("claude-3-haiku")},
		{"openrouter", WithOpenRouter("openai/gpt-4o")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt, err := New(tt.opt, WithTrace(false))
			if err != nil {
				t.Fatalf("New(%s) error: %v", tt.name, err)
			}
			rt.Close()
		})
	}
}

func TestNewError(t *testing.T) {
	_, err := New(func(c *config) error {
		c.llmCfg.Provider = "openai"
		c.llmCfg.Model = ""
		c.llmCfg.APIKey = ""
		return nil
	}, WithTrace(false))
	if err == nil {
		t.Fatal("expected error with empty model")
	}
}

func TestMustNewPanic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic")
		}
	}()
	MustNew(WithOllama(""))
}

func TestToolRegistry(t *testing.T) {
	rt := MustNew(WithOllama("llama3.2"), WithTrace(false))
	defer rt.Close()
	reg := rt.ToolRegistry()
	if reg == nil {
		t.Fatal("ToolRegistry returned nil")
	}
}

func TestNewAgent(t *testing.T) {
	rt := MustNew(WithOllama("llama3.2"), WithTrace(false))
	defer rt.Close()
	agent := rt.NewAgent("test",
		WithInstruction("be helpful"),
		WithTools(calcTool),
	)
	if agent.name != "test" {
		t.Fatalf("name mismatch")
	}
}

func TestAgentRunNoLLM(t *testing.T) {
	rt := MustNew(WithOllama("nonexistent"), WithTrace(false))
	defer rt.Close()
	agent := rt.NewAgent("test", WithInstruction("hi"))
	_, err := agent.Run(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestToolConversion(t *testing.T) {
	rt := MustNew(WithOllama("llama3.2"), WithTrace(false))
	defer rt.Close()
	agent := rt.NewAgent("t", WithTools(calcTool))
	coreTools := agent.toCoreTools(agent.tools)
	if len(coreTools) != 1 || coreTools[0].Function.Name != "calculator" {
		t.Fatal("tool conversion failed")
	}
}

func TestParseArgs(t *testing.T) {
	m := parseArgs(`{"x":"1"}`)
	if m == nil || m["x"] != "1" {
		t.Fatal("parseArgs failed")
	}
	if got := parseArgs(""); got != nil {
		t.Fatal("expected nil for empty")
	}
	if got := parseArgs("bad"); got != nil {
		t.Fatal("expected nil for invalid")
	}
}

func TestBuildMessages(t *testing.T) {
	rt := MustNew(WithOllama("llama3.2"), WithTrace(false))
	defer rt.Close()
	agent := rt.NewAgent("test", WithInstruction("help"))
	msgs := agent.buildMessages(context.Background(), "hello", "sess")
	if len(msgs) < 2 {
		t.Fatal("expected system+user messages")
	}
}

func TestWithKnowledgeEnabled(t *testing.T) {
	rt, err := New(
		WithOllama("llama3.2"),
		WithDefaultMemory(),
		WithKnowledge(),
		WithTrace(false),
	)
	if err != nil {
		t.Fatalf("New() with knowledge error: %v", err)
	}
	defer rt.Close()

	if !rt.knowledgeEnabled {
		t.Fatal("expected knowledgeEnabled to be true")
	}
	if rt.knowledgeRT == nil {
		t.Fatal("expected knowledgeRT to be non-nil")
	}
}

func TestBuildMessagesWithKnowledge(t *testing.T) {
	rt, err := New(
		WithOllama("llama3.2"),
		WithDefaultMemory(),
		WithKnowledge(),
		WithTrace(false),
	)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer rt.Close()

	agent := rt.NewAgent("test", WithInstruction("help"))
	msgs := agent.buildMessages(context.Background(), "hello", "sess")
	// Should have at least system (instruction) + user messages.
	// Knowledge context may be empty if no memory data exists, which is fine.
	if len(msgs) < 2 {
		t.Fatal("expected at least system+user messages")
	}
	_ = rt.Close
}

func TestBuildMessagesWithoutKnowledge(t *testing.T) {
	rt := MustNew(WithOllama("llama3.2"), WithTrace(false))
	defer rt.Close()
	agent := rt.NewAgent("test", WithInstruction("help"))
	msgs := agent.buildMessages(context.Background(), "hello", "sess")
	// Without knowledge, no AKF context should be injected.
	for _, m := range msgs {
		if m.Role == roleSystem && strings.Contains(m.Content, "Nodes") {
			t.Fatal("knowledge context should not appear without WithKnowledge()")
		}
	}
}

func TestLoadConfigFile(t *testing.T) {
	tmp := tmpFile(t, "llm:\n  provider: openai\n  model: gpt-4o\nmemory:\n  enabled: true\n")
	defer func() { _ = os.Remove(tmp) }()

	cfg, err := LoadConfigFile(tmp)
	if err != nil {
		t.Fatalf("LoadConfigFile error: %v", err)
	}
	if cfg.LLM.Provider != "openai" || cfg.LLM.Model != "gpt-4o" {
		t.Fatal("config values mismatch")
	}
}

func TestLoadConfigFileNotFound(t *testing.T) {
	_, err := LoadConfigFile("/nonexistent/path.yaml")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestToOptions(t *testing.T) {
	cfg := &ConfigFile{LLM: LLMFileConfig{Provider: "ollama"}}
	opts, err := cfg.ToOptions()
	if err != nil || len(opts) == 0 {
		t.Fatal("ToOptions failed")
	}
}

func TestToOptionsUnknownProvider(t *testing.T) {
	cfg := &ConfigFile{LLM: LLMFileConfig{Provider: "unknown"}}
	_, err := cfg.ToOptions()
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestNewTeam(t *testing.T) {
	rt := MustNew(WithOllama("llama3.2"), WithTrace(false))
	defer rt.Close()
	leader := rt.NewAgent("lead", WithInstruction("lead"))
	member := rt.NewAgent("mem", WithInstruction("work"))
	team := rt.NewTeam("project", leader, []*Agent{member})
	if team == nil || team.name != "project" || len(team.members) != 1 {
		t.Fatal("team creation failed")
	}
}

func TestTeamRunNoLLM(t *testing.T) {
	rt := MustNew(WithOllama("nonexistent"), WithTrace(false))
	defer rt.Close()
	leader := rt.NewAgent("lead", WithInstruction("lead"))
	member := rt.NewAgent("mem", WithInstruction("work"))
	team := rt.NewTeam("project", leader, []*Agent{member})
	_, err := team.Run(context.Background(), "test")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestEvolveNotEnabled(t *testing.T) {
	rt := MustNew(WithOllama("llama3.2"), WithTrace(false))
	defer rt.Close()
	agent := rt.NewAgent("test", WithInstruction("be helpful"))
	_, err := rt.Evolve(context.Background(), agent, "task")
	if err == nil {
		t.Fatal("expected error when evolution not enabled")
	}
}

func TestEvolveNilAgent(t *testing.T) {
	rt := MustNew(WithOllama("llama3.2"), WithEvolution(), WithTrace(false))
	defer rt.Close()
	_, err := rt.Evolve(context.Background(), nil, "task")
	if err == nil {
		t.Fatal("expected error for nil agent")
	}
}

func TestWithMCPMissingCommand(t *testing.T) {
	_, err := New(WithMCP(MCPConn{Name: "test"}), WithTrace(false))
	if err == nil {
		t.Fatal("expected error for MCP without command")
	}
}

func TestStream(t *testing.T) {
	rt := MustNew(WithOllama("nonexistent"), WithTrace(false))
	defer rt.Close()
	agent := rt.NewAgent("test", WithInstruction("hi"))
	ch, err := agent.Stream(context.Background(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	for chunk := range ch {
		if chunk.Err != nil {
			return // expected, no LLM available
		}
	}
}

// tmpFile creates a temp file with given content and returns its path.
func tmpFile(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp("", "ares-test-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(content)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	return f.Name()
}

var calcTool = tools.ToolFunc{
	ToolName: "calculator",
	ToolDesc: "test tool",
	Fn:       func(ctx context.Context, p map[string]any) (any, error) { return "42", nil },
}
