package sdk

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tools "github.com/Timwood0x10/ares/internal/apitools"
	llmcore "github.com/Timwood0x10/ares/internal/llmcore"
	memory "github.com/Timwood0x10/ares/internal/runtime/memory"
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
		WithLLMConfig(&llmcore.LLMConfig{Provider: "ollama", Model: "llama3.2"}),
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
		{"openai", WithOpenAI(defaultOpenAIModel)},
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
	NewRuntime(WithOllama(""))
}

func TestToolRegistry(t *testing.T) {
	rt := NewRuntime(WithOllama("llama3.2"), WithTrace(false))
	defer rt.Close()
	reg := rt.ToolRegistry()
	if reg == nil {
		t.Fatal("ToolRegistry returned nil")
	}
}

func TestNewAgent(t *testing.T) {
	rt := NewRuntime(WithOllama("llama3.2"), WithTrace(false))
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
	rt := NewRuntime(WithOllama("nonexistent"), WithTrace(false))
	defer rt.Close()
	agent := rt.NewAgent("test", WithInstruction("hi"))
	_, err := agent.Run(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestToolConversion(t *testing.T) {
	rt := NewRuntime(WithOllama("llama3.2"), WithTrace(false))
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

func TestComposePrompt(t *testing.T) {
	rt := NewRuntime(WithOllama("llama3.2"), WithTrace(false))
	defer rt.Close()
	agent := rt.NewAgent("test", WithInstruction("help"))
	prompt, _, _ := agent.composePrompt(context.Background(), "hello")
	if !strings.Contains(prompt, "help") {
		t.Fatal("expected instruction in prompt")
	}
	if !strings.Contains(prompt, "hello") {
		t.Fatal("expected input in prompt")
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
	prompt, _, _ := agent.composePrompt(context.Background(), "hello")
	// Should contain the instruction and the input. Knowledge context may be
	// empty if no memory data exists, which is fine.
	if !strings.Contains(prompt, "help") || !strings.Contains(prompt, "hello") {
		t.Fatal("expected instruction and input in prompt")
	}
	_ = rt.Close
}

func TestBuildMessagesWithoutKnowledge(t *testing.T) {
	rt := NewRuntime(WithOllama("llama3.2"), WithTrace(false))
	defer rt.Close()
	agent := rt.NewAgent("test", WithInstruction("help"))
	prompt, _, _ := agent.composePrompt(context.Background(), "hello")
	// Without knowledge, no AKF context should be injected.
	if strings.Contains(prompt, "Nodes") {
		t.Fatal("knowledge context should not appear without WithKnowledge()")
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

func TestEvolveNotEnabled(t *testing.T) {
	rt := NewRuntime(WithOllama("llama3.2"), WithTrace(false))
	defer rt.Close()
	agent := rt.NewAgent("test", WithInstruction("be helpful"))
	_, err := rt.Evolve(context.Background(), agent, "task")
	if err == nil {
		t.Fatal("expected error when evolution not enabled")
	}
}

func TestEvolveNilAgent(t *testing.T) {
	rt := NewRuntime(WithOllama("llama3.2"), WithEvolution(), WithTrace(false))
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
	rt := NewRuntime(WithOllama("nonexistent"), WithTrace(false))
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

// ---- integration tests: Agent.Run delegates to the shared L2 session core ----
//
// These tests inject a mock LLM (implementing the unexported llmService
// interface) by overriding Runtime.llmSvc after construction. They verify the
// end-to-end wiring through Agent.Run → submitThroughL2 (the same execution
// core serve/start use) without a real LLM.

// mockLLMSvc scripts Generate responses per call. It implements llmService so
// it can be assigned to Runtime.llmSvc. When responses are exhausted it returns
// a fallback final answer so the loop terminates.
type mockLLMSvc struct {
	responses []*llmcore.GenerateResponse
	calls     int
}

func (m *mockLLMSvc) Generate(_ context.Context, _ *llmcore.GenerateRequest) (*llmcore.GenerateResponse, error) {
	idx := m.calls
	m.calls++
	if idx >= len(m.responses) {
		return &llmcore.GenerateResponse{Content: "mock fallback"}, nil
	}
	return m.responses[idx], nil
}

func (m *mockLLMSvc) GetProvider() llmcore.LLMProvider { return llmcore.LLMProviderOllama }
func (m *mockLLMSvc) GetModel() string                 { return "mock-model" }
func (m *mockLLMSvc) Close()                           {}

// Compile-time check that mockLLMSvc satisfies the unexported llmService
// interface used by Runtime.llmSvc.
var _ llmService = (*mockLLMSvc)(nil)

// recordingMemMgr wraps a real memory.MemoryManager and records AddMessage
// calls so tests can assert which roles/content were persisted. All other
// methods delegate to the embedded manager.
type recordingMemMgr struct {
	memory.MemoryManager
	mu    sync.Mutex
	added []memEntry
}

type memEntry struct {
	sessionID string
	role      string
	content   string
}

// AddMessage records the call then delegates to the wrapped manager.
func (r *recordingMemMgr) AddMessage(ctx context.Context, sessionID, role, content string) error {
	r.mu.Lock()
	r.added = append(r.added, memEntry{sessionID: sessionID, role: role, content: content})
	r.mu.Unlock()
	return r.MemoryManager.AddMessage(ctx, sessionID, role, content)
}

func (r *recordingMemMgr) snapshot() []memEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]memEntry, len(r.added))
	copy(out, r.added)
	return out
}

// TestAgentRun_WithMemory_PersistsMessages verifies that a run with memory
// enabled persists both the user input (from composePrompt) and the assistant
// response (after the L2 session answers) via MemoryManager.AddMessage.
func TestAgentRun_WithMemory_PersistsMessages(t *testing.T) {
	rt := NewRuntime(WithOllama("llama3.2"), WithDefaultMemory(), WithTrace(false))
	defer rt.Close()
	rec := &recordingMemMgr{MemoryManager: rt.memMgr}
	rt.memMgr = rec
	rt.llmSvc = &mockLLMSvc{responses: []*llmcore.GenerateResponse{
		{Content: "hello back"},
	}}

	agent := rt.NewAgent("mem-agent", WithInstruction("help"))
	res, err := agent.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Agent.Run error: %v", err)
	}
	if res.Output != "hello back" {
		t.Fatalf("Output = %q, want %q", res.Output, "hello back")
	}
	if !res.MemoryUsed {
		t.Fatal("MemoryUsed = false, want true")
	}

	added := rec.snapshot()
	// Expect at least a user message (composePrompt) and an assistant message (post-L2).
	var userContent, asstContent string
	roles := map[string]bool{}
	for _, m := range added {
		roles[m.role] = true
		switch m.role {
		case roleUser:
			userContent = m.content
		case roleAssistant:
			asstContent = m.content
		}
	}
	if !roles[roleUser] {
		t.Error("expected AddMessage call for user input")
	}
	if !roles[roleAssistant] {
		t.Error("expected AddMessage call for assistant response")
	}
	if userContent != "hi" {
		t.Errorf("user content = %q, want %q", userContent, "hi")
	}
	if asstContent != "hello back" {
		t.Errorf("assistant content = %q, want %q", asstContent, "hello back")
	}
}

// TestAgentRun_DelegatesToL2 verifies the delegation wiring: a mock LLM
// answer flows back through Agent.Run (the L2 session path) as the
// Result.Output, and the planner quantum's token counts ride on TokenUsage.
func TestAgentRun_DelegatesToL2(t *testing.T) {
	rt := NewRuntime(WithOllama("llama3.2"), WithTrace(false))
	defer rt.Close()
	rt.llmSvc = &mockLLMSvc{responses: []*llmcore.GenerateResponse{
		{Content: "delegated", Usage: llmcore.TokenUsage{PromptTokens: 7, CompletionTokens: 11}},
	}}

	agent := rt.NewAgent("del-agent", WithInstruction("help"))
	res, err := agent.Run(context.Background(), "go")
	if err != nil {
		t.Fatalf("Agent.Run error: %v", err)
	}
	if res.Output != "delegated" {
		t.Fatalf("Output = %q, want %q", res.Output, "delegated")
	}
	if res.TokenUsage.Input != 7 {
		t.Errorf("TokenUsage.Input = %d, want 7", res.TokenUsage.Input)
	}
	if res.TokenUsage.Output != 11 {
		t.Errorf("TokenUsage.Output = %d, want 11", res.TokenUsage.Output)
	}
	if res.TokenUsage.Total != 18 {
		t.Errorf("TokenUsage.Total = %d, want 18", res.TokenUsage.Total)
	}
}

// ---- MustNew quickstart tests ----
//
// MustNew reads ./ares.yaml — the single configuration entry point (no
// environment variable is consulted). Each test writes a temporary
// ares.yaml into a temp working directory so no real config or network
// probe is involved. The LLM client is created lazily, so no running
// server is required.

// writeMustNewConfig writes yaml into dir/ares.yaml and switches the test's
// working directory there, restoring both on cleanup.
func writeMustNewConfig(t *testing.T, yaml string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ares.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatalf("write temp ares.yaml: %v", err)
	}
	t.Chdir(dir)
}

// TestMustNew_PanicMissingConfig verifies that MustNew panics with a message
// containing "load ./ares.yaml" when no config file exists in the working
// directory — the config file is the only way to configure the quickstart.
func TestMustNew_PanicMissingConfig(t *testing.T) {
	t.Chdir(t.TempDir()) // empty dir: no ares.yaml
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected MustNew to panic, got none")
		}
		msg := fmt.Sprint(r)
		if !strings.Contains(msg, "load ./ares.yaml") {
			t.Fatalf("panic message = %q, want substring %q", msg, "load ./ares.yaml")
		}
	}()
	_ = MustNew()
}

// TestMustNew_Ollama verifies that MustNew returns a usable Runtime with
// memory enabled when ares.yaml configures the Ollama provider and declares
// memory.enabled (ToOptions maps an absent memory section to WithoutMemory —
// the YAML is the single source of truth for the toggle).
func TestMustNew_Ollama(t *testing.T) {
	writeMustNewConfig(t, "llm:\n  provider: ollama\n  model: llama3.2\n  base_url: http://localhost:11434\nmemory:\n  enabled: true\n")
	rt := MustNew()
	defer rt.Close()
	if rt == nil {
		t.Fatal("MustNew returned nil Runtime")
	}
	if !rt.memEnabled {
		t.Fatal("rt.memEnabled = false, want true (memory.enabled declared in yaml)")
	}
}

// TestMustNew_MemoryFollowsConfig asserts the memory toggle follows the YAML
// across every provider ares.yaml can declare: memory.enabled: true yields
// memEnabled == true; an absent memory section disables memory explicitly
// (WithoutMemory), never silently-on.
func TestMustNew_MemoryFollowsConfig(t *testing.T) {
	tests := []struct {
		name        string
		yaml        string
		wantEnabled bool
	}{
		{
			name:        "ollama_memory_on",
			yaml:        "llm:\n  provider: ollama\n  model: llama3.2\n  base_url: http://localhost:11434\nmemory:\n  enabled: true\n",
			wantEnabled: true,
		},
		{
			name:        "openai_memory_on",
			yaml:        "llm:\n  provider: openai\n  model: gpt-4o-mini\n  api_key: test-key\nmemory:\n  enabled: true\n",
			wantEnabled: true,
		},
		{
			name:        "anthropic_memory_on",
			yaml:        "llm:\n  provider: anthropic\n  model: claude-3-haiku\n  api_key: test-key\nmemory:\n  enabled: true\n",
			wantEnabled: true,
		},
		{
			name:        "absent_memory_section_disables",
			yaml:        "llm:\n  provider: ollama\n  model: llama3.2\n  base_url: http://localhost:11434\n",
			wantEnabled: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writeMustNewConfig(t, tt.yaml)
			rt := MustNew()
			defer rt.Close()
			if rt.memEnabled != tt.wantEnabled {
				t.Fatalf("rt.memEnabled = %v, want %v for %s", rt.memEnabled, tt.wantEnabled, tt.name)
			}
		})
	}
}
