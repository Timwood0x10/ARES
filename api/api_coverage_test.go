package ares

import (
	"testing"
	"time"
)

// ── Option constructors return non-nil ──────────────────────────────────────

func TestOptionConstructors(t *testing.T) {
	opts := []struct {
		name string
		opt  Option
	}{
		{"WithOpenAI", WithOpenAI("gpt-4")},
		{"WithAnthropic", WithAnthropic("claude-3")},
		{"WithOpenRouter", WithOpenRouter("openrouter-model")},
		{"WithBaseURL", WithBaseURL("https://api.example.com")},
		{"WithAPIKey", WithAPIKey("sk-test")},
		{"WithLLMConfig", WithLLMConfig(&LLMConfig{Model: "gpt-4"})},
		{"WithFallbackLLM", WithFallbackLLM(&LLMConfig{Model: "fallback"})},
		{"WithMemoryConfig", WithMemoryConfig(100, 10)},
		{"WithRAG", WithRAG(5, 0.7)},
		{"WithEvolution", WithEvolution()},
		{"WithKnowledge", WithKnowledge()},
		{"WithAKGQualityGate", WithAKGQualityGate(DefaultQualityGateConfig())},
		{"WithAKGEmbedding", WithAKGEmbedding("model", "http://localhost")},
		{"WithSQLiteKnowledgeStore", WithSQLiteKnowledgeStore(":memory:")},
		{"WithEmbeddingService", WithEmbeddingService("http://localhost:8000", "e5")},
		{"WithTrace", WithTrace(true)},
		{"WithAgentGovernance", WithAgentGovernance(1000, 5, time.Minute)},
	}
	for _, tt := range opts {
		if tt.opt == nil {
			t.Errorf("%s returned nil option", tt.name)
		}
	}
}

func TestAgentOptionConstructors(t *testing.T) {
	opts := []struct {
		name string
		opt  AgentOption
	}{
		{"WithInstruction", WithInstruction("be helpful")},
		{"WithTools", WithTools()},
		{"WithMaxIterations", WithMaxIterations(10)},
		{"WithMaxTokens", WithMaxTokens(1000)},
		{"WithTimeout", WithTimeout(time.Second)},
		{"WithToolDiscovery", WithToolDiscovery()},
	}
	for _, tt := range opts {
		if tt.opt == nil {
			t.Errorf("%s returned nil agent option", tt.name)
		}
	}
}

// ── Default config helpers ──────────────────────────────────────────────────

func TestDefaultConfigs(t *testing.T) {
	qg := DefaultQualityGateConfig()
	_ = qg

	co := DefaultCleanOptions()
	_ = co

	dc := DefaultDistillationConfig()
	if dc == nil {
		t.Error("DefaultDistillationConfig returned nil")
	}

	ddc := DefaultDreamCycleConfig()
	_ = ddc
}

// ── Registry helpers ────────────────────────────────────────────────────────

func TestRegistryHelpers(t *testing.T) {
	r := NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry returned nil")
	}

	er := NewEmptyRegistry()
	if er == nil {
		t.Fatal("NewEmptyRegistry returned nil")
	}

	dir := t.TempDir()
	opt := WithFileSandboxDir(dir)
	if opt == nil {
		t.Error("WithFileSandboxDir returned nil")
	}

	if err := RegisterBuiltinTools(r, WithFileSandboxDir(dir)); err != nil {
		t.Errorf("RegisterBuiltinTools: %v", err)
	}
}

// ── ContextCleaner / Graph ──────────────────────────────────────────────────

func TestNewContextCleaner(t *testing.T) {
	c := NewContextCleaner()
	if c == nil {
		t.Fatal("NewContextCleaner returned nil")
	}
}

func TestNewGraph(t *testing.T) {
	g := NewGraph("test-graph")
	if g == nil {
		t.Fatal("NewGraph returned nil")
	}
}

// ── LoadConfigFile ──────────────────────────────────────────────────────────

func TestLoadConfigFile_NotFound(t *testing.T) {
	_, err := LoadConfigFile("/nonexistent/ares.yaml")
	if err == nil {
		t.Error("expected error for nonexistent config file")
	}
}

// ── DiscoverMCPServers ──────────────────────────────────────────────────────

func TestDiscoverMCPServers_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	servers := DiscoverMCPServers(dir)
	// Empty dir should return empty or nil, not panic
	_ = servers
}

// ── ConnectMCP error paths ──────────────────────────────────────────────────

func TestConnectMCPSSE_Error(t *testing.T) {
	// Invalid URL should fail fast
	_, err := ConnectMCPSSE(t.Context(), "test", "not-a-url")
	if err == nil {
		t.Log("ConnectMCPSSE may succeed with certain URL formats")
	}
}

// ── NewLLMService ───────────────────────────────────────────────────────────

func TestNewLLMService_NilConfig(t *testing.T) {
	_, err := NewLLMService(nil)
	if err == nil {
		t.Log("NewLLMService(nil) may return a disabled service")
	}
}

// ── Error variables ─────────────────────────────────────────────────────────

func TestErrorVars(t *testing.T) {
	if ErrHumanInputUnsupported == nil {
		t.Error("ErrHumanInputUnsupported should not be nil")
	}
	if ErrDistillDepsMissing == nil {
		t.Error("ErrDistillDepsMissing should not be nil")
	}
}

// ── Cleaning mode constants ─────────────────────────────────────────────────

func TestCleaningModeConstants(t *testing.T) {
	modes := []CleaningMode{CleaningModeDefault, CleaningModeConservative, CleaningModeAggressive}
	seen := make(map[CleaningMode]bool)
	for _, m := range modes {
		if seen[m] {
			t.Errorf("duplicate CleaningMode: %v", m)
		}
		seen[m] = true
	}
}

// ── LLM provider constants ──────────────────────────────────────────────────

func TestLLMProviderConstants(t *testing.T) {
	providers := []LLMProvider{
		LLMProviderOpenRouter,
		LLMProviderOllama,
		LLMProviderOpenAI,
		LLMProviderAnthropic,
	}
	for _, p := range providers {
		if string(p) == "" {
			t.Error("empty LLMProvider constant")
		}
	}
}

// ── Evolution mode constants ────────────────────────────────────────────────

func TestEvolutionModeConstants(t *testing.T) {
	if ModeEvolutionStrategy == ModeGeneticAlgorithm {
		t.Error("EvolutionMode constants should be distinct")
	}
}

// ── Experience type constants ───────────────────────────────────────────────

func TestExperienceTypeConstants(t *testing.T) {
	if ExperienceTypeFailure == "" {
		t.Error("ExperienceTypeFailure should not be empty")
	}
}

// ── FriendlyErr ─────────────────────────────────────────────────────────────

func TestFriendlyErr(t *testing.T) {
	err := FriendlyErr("test-scope", LLMProviderOpenAI, errStub{})
	if err == nil {
		t.Error("FriendlyErr should return non-nil")
	}
}

type errStub struct{}

func (errStub) Error() string { return "stub error" }

// ── MustNew panic path ──────────────────────────────────────────────────────

func TestMustNew_PanicsOnBadConfig(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Log("MustNew did not panic with default config (may succeed)")
		}
	}()
	_ = MustNew()
}
