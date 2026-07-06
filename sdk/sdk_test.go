package sdk

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/api/tools"
)

// TestNew verifies that New() succeeds with valid options.
func TestNew(t *testing.T) {
	rt, err := New(
		WithOllama("llama3.2"),
		WithTrace(false),
	)
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}
	if rt == nil {
		t.Fatal("New() returned nil Runtime")
	}
	rt.Close()
}

// TestNewAgent verifies that NewAgent creates an agent with the correct name.
func TestNewAgent(t *testing.T) {
	rt, err := New(WithOllama("llama3.2"), WithTrace(false))
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	agent := rt.NewAgent("test-bot",
		WithInstruction("You are a test bot."),
		WithTools(calcTool),
	)
	if agent == nil {
		t.Fatal("NewAgent returned nil")
	}
	if agent.name != "test-bot" {
		t.Fatalf("expected name 'test-bot', got %q", agent.name)
	}
	if agent.instruction != "You are a test bot." {
		t.Fatalf("expected instruction mismatch")
	}
	if len(agent.tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(agent.tools))
	}
}

// TestToolConversion verifies that custom tools are correctly converted to
// core.Tool format for the LLM API.
func TestToolConversion(t *testing.T) {
	rt, err := New(WithOllama("llama3.2"), WithTrace(false))
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	agent := rt.NewAgent("t",
		WithTools(calcTool),
	)

	coreTools := agent.toCoreTools(agent.tools)
	if len(coreTools) != 1 {
		t.Fatalf("expected 1 core tool, got %d", len(coreTools))
	}
	if coreTools[0].Function.Name != "calculator" {
		t.Fatalf("expected tool name 'calculator', got %q", coreTools[0].Function.Name)
	}
}

// TestParseArgs verifies the JSON argument parser.
func TestParseArgs(t *testing.T) {
	m := parseArgs(`{"expression": "1+1"}`)
	if m == nil {
		t.Fatal("parseArgs returned nil")
	}
	if m["expression"] != "1+1" {
		t.Fatalf("expected '1+1', got %v", m["expression"])
	}

	// Empty string should return nil
	if got := parseArgs(""); got != nil {
		t.Fatal("expected nil for empty string")
	}

	// Invalid JSON should return nil
	if got := parseArgs("not-json"); got != nil {
		t.Fatal("expected nil for invalid JSON")
	}
}

// TestMustNewPanic verifies MustNew panics on bad config.
func TestMustNewPanic(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic with invalid config")
		}
	}()
	// This should panic because provider is empty and model is empty
	MustNew(WithOllama(""))
}

// TestRunNoLLM verifies that Run returns an error (not a panic) when the LLM
// is unreachable — key for the quickstart UX.
func TestRunNoLLM(t *testing.T) {
	rt := MustNew(WithOllama("nonexistent-model"), WithTrace(false))
	defer rt.Close()

	agent := rt.NewAgent("test", WithInstruction("say hello"))
	_, err := agent.Run(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error when LLM is unreachable, got nil")
	}
	t.Logf("expected error: %v", err)
}

// bench tool for tests
var calcTool = tools.ToolFunc{
	ToolName: "calculator",
	ToolDesc: "Evaluate a mathematical expression",
	Fn: func(ctx context.Context, params map[string]any) (any, error) {
		return "42", nil
	},
}
