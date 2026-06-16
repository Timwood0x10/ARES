package callbacks

import (
	"sync"
	"testing"
	"time"
)

func TestOnRegistersHandler(t *testing.T) {
	r := NewRegistry()

	r.On(EventLLMStart, func(ctx *Context) {})

	if c := r.Count(EventLLMStart); c != 1 {
		t.Fatalf("expected 1 handler, got %d", c)
	}
}

func TestEmitCallsHandler(t *testing.T) {
	r := NewRegistry()

	var called bool
	var received *Context
	r.On(EventLLMStart, func(ctx *Context) {
		called = true
		received = ctx
	})

	input := &Context{
		Event: EventLLMStart,
		Model: "gpt-4",
		Input: "hello",
	}
	r.Emit(input)

	if !called {
		t.Fatal("handler was not called")
	}
	if received.Model != "gpt-4" {
		t.Fatalf("expected model gpt-4, got %s", received.Model)
	}
	if received.Input != "hello" {
		t.Fatalf("expected input hello, got %s", received.Input)
	}
}

func TestEmitNoHandlersNoPanic(t *testing.T) {
	r := NewRegistry()

	// Should not panic with no handlers registered.
	r.Emit(&Context{
		Event:   EventAgentStart,
		AgentID: "agent-1",
	})
}

func TestMultipleHandlersForSameEvent(t *testing.T) {
	r := NewRegistry()

	var order []int
	r.On(EventToolEnd, func(ctx *Context) {
		order = append(order, 1)
	})
	r.On(EventToolEnd, func(ctx *Context) {
		order = append(order, 2)
	})
	r.On(EventToolEnd, func(ctx *Context) {
		order = append(order, 3)
	})

	if c := r.Count(EventToolEnd); c != 3 {
		t.Fatalf("expected 3 handlers, got %d", c)
	}

	r.Emit(&Context{Event: EventToolEnd})

	if len(order) != 3 {
		t.Fatalf("expected 3 calls, got %d", len(order))
	}
	// Handlers should execute in registration order.
	for i, v := range order {
		if v != i+1 {
			t.Fatalf("handler order mismatch at %d: expected %d, got %d", i, i+1, v)
		}
	}
}

func TestClearRemovesAllHandlers(t *testing.T) {
	r := NewRegistry()

	r.On(EventLLMStart, func(ctx *Context) {})
	r.On(EventLLMEnd, func(ctx *Context) {})
	r.On(EventAgentStart, func(ctx *Context) {})

	r.Clear()

	if c := r.Count(EventLLMStart); c != 0 {
		t.Fatalf("expected 0 handlers for llm.start, got %d", c)
	}
	if c := r.Count(EventLLMEnd); c != 0 {
		t.Fatalf("expected 0 handlers for llm.end, got %d", c)
	}
	if c := r.Count(EventAgentStart); c != 0 {
		t.Fatalf("expected 0 handlers for agent.start, got %d", c)
	}
}

func TestConcurrentEmitSafety(t *testing.T) {
	r := NewRegistry()

	var count int
	var mu sync.Mutex
	r.On(EventLLMToken, func(ctx *Context) {
		mu.Lock()
		count++
		mu.Unlock()
	})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Emit(&Context{
				Event:      EventLLMToken,
				TokenCount: 1,
			})
		}()
	}

	wg.Wait()

	mu.Lock()
	if count != 100 {
		t.Fatalf("expected 100 handler calls, got %d", count)
	}
	mu.Unlock()
}

func TestConcurrentOnAndEmit(t *testing.T) {
	r := NewRegistry()

	// Register some handlers concurrently while emitting.
	var wg sync.WaitGroup

	// Writers.
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.On(EventAgentEnd, func(ctx *Context) {})
		}()
	}

	// Readers (emitters).
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Emit(&Context{Event: EventAgentEnd})
		}()
	}

	wg.Wait()
	// If we get here without a race condition, the test passes.
}

func TestHandlerReceivesAllFields(t *testing.T) {
	r := NewRegistry()

	var received Context
	r.On(EventToolError, func(ctx *Context) {
		received = *ctx
	})

	err := &testError{"something failed"}
	input := &Context{
		Event:    EventToolError,
		AgentID:  "agent-42",
		ToolName: "web_search",
		Model:    "claude-3",
		Input:    "query",
		Output:   "result",
		Error:    err,
		Duration: 500 * time.Millisecond,
		Extra:    map[string]any{"key": "value"},
	}

	r.Emit(input)

	if received.AgentID != "agent-42" {
		t.Fatalf("expected agent-42, got %s", received.AgentID)
	}
	if received.ToolName != "web_search" {
		t.Fatalf("expected web_search, got %s", received.ToolName)
	}
	if received.Model != "claude-3" {
		t.Fatalf("expected claude-3, got %s", received.Model)
	}
	if received.Error != err {
		t.Fatalf("expected error to match")
	}
	if received.Duration != 500*time.Millisecond {
		t.Fatalf("expected 500ms, got %s", received.Duration)
	}
	if received.Extra["key"] != "value" {
		t.Fatalf("expected extra key=value")
	}
}

type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}
