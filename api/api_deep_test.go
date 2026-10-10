package ares

import (
	"testing"
	"time"
)

// ── Option constructors return non-nil ──────────────────────────────────────

func TestOptionConstructors_NonNil(t *testing.T) {
	tests := []struct {
		name string
		fn   func() Option
	}{
		{"WithOpenAI", func() Option { return WithOpenAI("gpt-4") }},
		{"WithAnthropic", func() Option { return WithAnthropic("claude-3") }},
		{"WithOpenRouter", func() Option { return WithOpenRouter("auto") }},
		{"WithBaseURL", func() Option { return WithBaseURL("http://localhost:8080") }},
		{"WithAPIKey", func() Option { return WithAPIKey("sk-test") }},
		{"WithLLMConfig", func() Option { return WithLLMConfig(&LLMConfig{Model: "m"}) }},
		{"WithFallbackLLM", func() Option { return WithFallbackLLM(&LLMConfig{Model: "fb"}) }},
		{"WithMemoryConfig", func() Option { return WithMemoryConfig(100, 10) }},
		{"WithRAG", func() Option { return WithRAG(5, 0.7) }},
		{"WithEvolution", WithEvolution},
		{"WithKnowledge", WithKnowledge},
		{"WithAKGQualityGate", func() Option { return WithAKGQualityGate(DefaultQualityGateConfig()) }},
		{"WithAKGEmbedding", func() Option { return WithAKGEmbedding("model", "http://localhost") }},
		{"WithSQLiteKnowledgeStore", func() Option { return WithSQLiteKnowledgeStore(":memory:") }},
		{"WithPostgres", func() Option { return WithPostgres(DatabaseFileConfig{}) }},
		{"WithKnowledgeConfig", func() Option { return WithKnowledgeConfig(KnowledgeFileConfig{}) }},
		{"WithEmbeddingService", func() Option { return WithEmbeddingService("http://localhost:8000", "e5") }},
		{"WithTrace", func() Option { return WithTrace(true) }},
		{"WithAgentGovernance", func() Option { return WithAgentGovernance(1000, 5, time.Minute) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opt := tt.fn()
			if opt == nil {
				t.Errorf("%s returned nil Option", tt.name)
			}
		})
	}
}

// ── AgentOption constructors return non-nil ─────────────────────────────────

func TestAgentOptionConstructors_NonNil(t *testing.T) {
	tests := []struct {
		name string
		fn   func() AgentOption
	}{
		{"WithInstruction", func() AgentOption { return WithInstruction("be helpful") }},
		{"WithHumanInput", func() AgentOption { return WithHumanInput(nil) }},
		{"WithToolDiscovery", WithToolDiscovery},
		{"WithMaxIterations", func() AgentOption { return WithMaxIterations(5) }},
		{"WithMaxTokens", func() AgentOption { return WithMaxTokens(1000) }},
		{"WithTimeout", func() AgentOption { return WithTimeout(time.Second) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opt := tt.fn()
			if opt == nil {
				t.Errorf("%s returned nil AgentOption", tt.name)
			}
		})
	}
}

// ── ConfigOption ────────────────────────────────────────────────────────────

func TestWithConfig_ReturnsOption(t *testing.T) {
	opt := WithConfig("/nonexistent/ares.yaml")
	if opt == nil {
		t.Error("WithConfig returned nil")
	}
}

// ── FriendlyErr with custom error ───────────────────────────────────────────

func TestFriendlyErr_CustomError(t *testing.T) {
	orig := &testError{"something failed"}
	result := FriendlyErr("test", LLMProviderOpenAI, orig)
	if result == nil {
		t.Error("FriendlyErr returned nil")
	}
}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

// ── CleaningMode constants distinct ─────────────────────────────────────────

func TestCleaningModeConstants_Distinct(t *testing.T) {
	modes := []CleaningMode{CleaningModeDefault, CleaningModeConservative, CleaningModeAggressive}
	seen := make(map[CleaningMode]bool)
	for _, m := range modes {
		if seen[m] {
			t.Errorf("duplicate CleaningMode: %v", m)
		}
		seen[m] = true
	}
}

// ── EvolutionMode constants distinct ────────────────────────────────────────

func TestEvolutionModeConstants_Distinct(t *testing.T) {
	if ModeEvolutionStrategy == ModeGeneticAlgorithm {
		t.Error("EvolutionMode constants should be distinct")
	}
}
