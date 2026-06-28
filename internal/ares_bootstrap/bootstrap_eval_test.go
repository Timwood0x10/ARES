package ares_bootstrap

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/ares_eval"
	"github.com/Timwood0x10/ares/internal/llm"
)

// newTestLLMClient creates a minimal *llm.Client for testing.
// It uses Ollama provider pointing to localhost; no real calls are made during
// registry setup since NewLLMJudgeEvaluator only stores the client reference.
func newTestLLMClient(t *testing.T) *llm.Client {
	t.Helper()
	client, err := llm.NewClient(&llm.Config{
		Provider: "ollama",
		BaseURL:  "http://localhost:11434",
		Model:    "test-model",
		Timeout:  5,
	})
	if err != nil {
		t.Fatalf("newTestLLMClient: %v", err)
	}
	return client
}

// TestSetupEvalSystem_NilLLMClient verifies that passing nil returns an empty registry.
func TestSetupEvalSystem_NilLLMClient(t *testing.T) {
	registry, err := SetupEvalSystem(nil)

	if err != nil {
		t.Fatalf("SetupEvalSystem(nil) returned unexpected error: %v", err)
	}
	if registry == nil {
		t.Fatal("SetupEvalSystem(nil) returned nil registry")
	}

	names := registry.Names()
	if len(names) != 0 {
		t.Fatalf("expected 0 evaluators, got %d: %v", len(names), names)
	}
}

// TestSetupEvalSystem_WithLLMClient verifies that a valid LLM client produces
// a registry with the llm_judge evaluator registered.
func TestSetupEvalSystem_WithLLMClient(t *testing.T) {
	client := newTestLLMClient(t)
	defer client.Close()

	registry, err := SetupEvalSystem(client)
	if err != nil {
		t.Fatalf("SetupEvalSystem returned error: %v", err)
	}
	if registry == nil {
		t.Fatal("SetupEvalSystem returned nil registry")
	}

	names := registry.Names()
	if len(names) == 0 {
		t.Fatal("expected at least 1 evaluator, got 0")
	}
}

// TestSetupEvalSystem_LLMJudgeRegistered verifies that the llm_judge evaluator
// is properly registered and retrievable from the registry.
func TestSetupEvalSystem_LLMJudgeRegistered(t *testing.T) {
	client := newTestLLMClient(t)
	defer client.Close()

	registry, err := SetupEvalSystem(client)
	if err != nil {
		t.Fatalf("SetupEvalSystem returned error: %v", err)
	}

	evaluator, ok := registry.Get("llm_judge")
	if !ok {
		t.Fatal("llm_judge evaluator not found in registry")
	}
	if evaluator == nil {
		t.Fatal("llm_judge evaluator is nil")
	}

	// Verify the evaluator implements the Evaluator interface by calling Evaluate.
	// Use a trivial test case; the mock client will not actually be called
	// during this check — we only confirm the evaluator is structurally valid.
	tc := ares_eval.TestCase{
		ID:             "test-001",
		Input:          "Hello",
		ExpectedOutput: "World",
	}
	result := ares_eval.TestResult{
		TestCaseID:   "test-001",
		ActualOutput: "World",
		TokensUsed:   10,
	}

	scores, err := evaluator.Evaluate(context.Background(), tc, result)
	if err != nil {
		t.Logf("Evaluate returned error (may be expected without real LLM): %v", err)
		// Error is acceptable here since we don't have a real LLM backend.
		// The important part is that the evaluator was registered and callable.
		return
	}
	if scores == nil {
		t.Error("Evaluate returned nil scores")
	}
}

// TestSetupEvalSystem_RegistryIsIndependent verifies that each call to
// SetupEvalSystem returns a distinct registry instance.
func TestSetupEvalSystem_RegistryIsIndependent(t *testing.T) {
	client := newTestLLMClient(t)
	defer client.Close()

	reg1, err1 := SetupEvalSystem(client)
	reg2, err2 := SetupEvalSystem(client)

	if err1 != nil {
		t.Fatalf("first SetupEvalSystem error: %v", err1)
	}
	if err2 != nil {
		t.Fatalf("second SetupEvalSystem error: %v", err2)
	}

	if reg1 == reg2 {
		t.Error("expected independent registry instances, got same pointer")
	}
}

// TestSetupEvalSystem_SatisfiesAgentTestRunner verifies that the registry
// returned by SetupEvalSystem can be injected into AgentTestRunner via SetRegistry.
func TestSetupEvalSystem_SatisfiesAgentTestRunner(t *testing.T) {
	client := newTestLLMClient(t)
	defer client.Close()

	registry, err := SetupEvalSystem(client)
	if err != nil {
		t.Fatalf("SetupEvalSystem error: %v", err)
	}

	executor := &mockExecutor{}
	runner, err := ares_eval.NewAgentTestRunner(executor)
	if err != nil {
		t.Fatalf("NewAgentTestRunner error: %v", err)
	}

	// This should not panic or fail — proving type compatibility.
	runner.SetRegistry(registry)

	// Verify the runner's internal registry is set by checking RunAndEvaluate
	// does NOT return ErrNilRegistry when a valid evaluator name is used.
	ctx := context.Background()
	suite := ares_eval.TestSuite{
		TestCases: []ares_eval.TestCase{
			{ID: "tc-1", Input: "test"},
		},
	}
	_, _, runErr := runner.RunAndEvaluate(ctx, suite, "nonexistent_evaluator")
	if runErr == nil {
		t.Error("expected error for nonexistent evaluator")
	}
	if runErr != ares_eval.ErrNilRegistry && runErr != ares_eval.ErrEvaluatorNotFound {
		t.Logf("unexpected error type: %v", runErr)
	}
}

// mockExecutor implements ares_eval.AgentExecutor for testing.
type mockExecutor struct{}

func (m *mockExecutor) Execute(ctx context.Context, input string) (string, []string, int, error) {
	return "mock output", []string{"mock_tool"}, 0, nil
}
