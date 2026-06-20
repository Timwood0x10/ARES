package bootstrap

import (
	"context"
	"sync"
	"testing"

	"goagentx/internal/agents/sub"
	"goagentx/internal/callbacks"
	"goagentx/internal/core/models"
	"goagentx/internal/llm"
	"goagentx/internal/llm/output"
)

// TestNewCallbackRegistry verifies that NewCallbackRegistry returns a non-nil
// Registry that implements both Emitter and CallbackRegistrar interfaces.
func TestNewCallbackRegistry(t *testing.T) {
	reg := NewCallbackRegistry()
	if reg == nil {
		t.Fatal("expected non-nil registry")
	}

	var _ callbacks.Emitter = reg
	var _ callbacks.CallbackRegistrar = reg
}

// TestNewLLMClientWithCallbacks verifies that the factory method creates an LLM
// client with the callback registry properly wired.
func TestNewLLMClientWithCallbacks(t *testing.T) {
	reg := NewCallbackRegistry()

	var emitted bool
	reg.On(callbacks.EventLLMStart, func(ctx *callbacks.Context) {
		emitted = true
	})

	cfg := &llm.Config{
		Provider: "ollama",
		Model:    "test-model",
		BaseURL:  "http://localhost:11434",
	}

	client, err := NewLLMClientWithCallbacks(cfg, reg)
	if err != nil {
		t.Fatalf("NewLLMClientWithCallbacks failed: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}

	_, _ = client.Generate(context.TODO(), "test prompt")
	if !emitted {
		t.Error("expected LLM start event to be emitted via callback registry")
	}
}

// TestWireTaskExecutorCallbacks verifies that WireTaskExecutorCallbacks returns
// a valid TaskExecutorOption that correctly wires callbacks at construction time.
func TestWireTaskExecutorCallbacks(t *testing.T) {
	reg := NewCallbackRegistry()

	var mu sync.Mutex
	var emittedEvents []string

	reg.On(callbacks.EventToolStart, func(ctx *callbacks.Context) {
		mu.Lock()
		emittedEvents = append(emittedEvents, string(ctx.Event))
		mu.Unlock()
	})

	opt := WireTaskExecutorCallbacks(reg)
	if opt == nil {
		t.Fatal("expected non-nil option for non-nil registry")
	}

	executor := sub.NewTaskExecutor(
		nil,
		nil,
		output.NewTemplateEngine(),
		"test prompt",
		nil,
		3,
		opt,
	)

	task := &models.Task{
		TaskID:    "test-task-001",
		AgentType: models.AgentTypeDestination,
	}

	_, _ = executor.Execute(context.TODO(), task)

	mu.Lock()
	defer mu.Unlock()

	found := false
	for _, ev := range emittedEvents {
		if ev == string(callbacks.EventToolStart) {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected ToolStart event to be emitted after wiring callbacks via option")
	}
}

// TestWireLeaderAgentCallbacks verifies that WireLeaderAgentCallbacks returns
// a valid LeaderOption that can be passed to leader.New.
func TestWireLeaderAgentCallbacks(t *testing.T) {
	t.Run("non-nil registry returns option", func(t *testing.T) {
		reg := NewCallbackRegistry()
		opt := WireLeaderAgentCallbacks(reg)
		if opt == nil {
			t.Fatal("expected non-nil option for non-nil registry")
		}
	})

	t.Run("nil registry returns nil option", func(t *testing.T) {
		opt := WireLeaderAgentCallbacks(nil)
		if opt != nil {
			t.Fatal("expected nil option for nil registry")
		}
	})
}

// TestCallbackInjectionChain verifies that all three components (LLM Client,
// TaskExecutor, Leader Agent) can share the same Registry instance and emit
// events to it.
func TestCallbackInjectionChain(t *testing.T) {
	reg := NewCallbackRegistry()

	var mu sync.Mutex
	eventSources := make(map[string]string)

	reg.On(callbacks.EventLLMStart, func(ctx *callbacks.Context) {
		mu.Lock()
		eventSources["llm"] = "llm.start"
		mu.Unlock()
	})
	reg.On(callbacks.EventToolStart, func(ctx *callbacks.Context) {
		mu.Lock()
		eventSources["tool"] = "tool.start"
		mu.Unlock()
	})
	reg.On(callbacks.EventAgentStart, func(ctx *callbacks.Context) {
		mu.Lock()
		eventSources["agent"] = "agent.start"
		mu.Unlock()
	})

	llmCfg := &llm.Config{
		Provider: "ollama",
		Model:    "test-model",
		BaseURL:  "http://localhost:11434",
	}

	llmClient, err := NewLLMClientWithCallbacks(llmCfg, reg)
	if err != nil {
		t.Fatalf("failed to create LLM client with callbacks: %v", err)
	}

	callbackOpt := WireTaskExecutorCallbacks(reg)
	if callbackOpt == nil {
		t.Fatal("expected non-nil callback option")
	}

	taskExec := sub.NewTaskExecutor(
		nil,
		nil,
		output.NewTemplateEngine(),
		"test",
		nil,
		1,
		callbackOpt,
	)

	_, _ = taskExec.Execute(context.TODO(), &models.Task{
		TaskID:    "chain-test",
		AgentType: models.AgentTypeDestination,
	})

	_, _ = llmClient.Generate(context.TODO(), "chain test")

	mu.Lock()
	defer mu.Unlock()

	if _, ok := eventSources["llm"]; !ok {
		t.Error("LLM client did not emit events to shared registry")
	}
	if _, ok := eventSources["tool"]; !ok {
		t.Error("TaskExecutor did not emit events to shared registry")
	}
}

// TestNilSafety verifies that all bootstrap functions handle nil inputs gracefully.
func TestNilSafety(t *testing.T) {
	t.Run("WireTaskExecutorCallbacks with nil registry returns nil option", func(t *testing.T) {
		opt := WireTaskExecutorCallbacks(nil)
		if opt != nil {
			t.Error("expected nil option for nil registry")
		}
	})

	t.Run("WireTaskExecutorCallbacks with valid registry returns option", func(t *testing.T) {
		reg := NewCallbackRegistry()
		opt := WireTaskExecutorCallbacks(reg)
		if opt == nil {
			t.Error("expected non-nil option for valid registry")
		}
	})

	t.Run("NewLLMClientWithCallbacks with nil registry", func(t *testing.T) {
		cfg := &llm.Config{
			Provider: "ollama",
			Model:    "test",
			BaseURL:  "http://localhost:11434",
		}
		client, err := NewLLMClientWithCallbacks(cfg, nil)
		if err != nil {
			t.Fatalf("unexpected error with nil registry: %v", err)
		}
		if client == nil {
			t.Fatal("expected non-nil client even with nil registry")
		}
	})

	t.Run("WireLeaderAgentCallbacks with nil registry", func(t *testing.T) {
		opt := WireLeaderAgentCallbacks(nil)
		if opt != nil {
			t.Error("expected nil option for nil registry")
		}
	})
}
