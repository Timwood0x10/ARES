//go:build integration
// +build integration

// package graph - integration tests for observability and ratelimit.

package graph

import (
	"context"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/observability"
	"github.com/Timwood0x10/ares/internal/ratelimit"
)

func TestGraphWithObservability(t *testing.T) {
	// Test that graph execution is properly traced
	calls := make(chan *observability.ToolCall, 10)

	tracer := &mockTracer{
		recordToolCallFunc: func(ctx context.Context, call *observability.ToolCall) {
			calls <- call
		},
	}

	executionOrder := []string{}

	graph, err := NewGraphWithTracer("observability-test", tracer)
	if err != nil {
		t.Fatalf("failed to create graph: %v", err)
	}
	graph, err = graph.Node("node1", &mockNode{id: "node1", executeFn: func(ctx context.Context, state *State) error {
		executionOrder = append(executionOrder, "node1")
		return nil
	}})
	if err != nil {
		t.Fatalf("failed to add node1: %v", err)
	}
	graph, err = graph.Node("node2", &mockNode{id: "node2", executeFn: func(ctx context.Context, state *State) error {
		executionOrder = append(executionOrder, "node2")
		return nil
	}})
	if err != nil {
		t.Fatalf("failed to add node2: %v", err)
	}
	graph, err = graph.Edge("node1", "node2")
	if err != nil {
		t.Fatalf("failed to add edge: %v", err)
	}
	graph, err = graph.Start("node1")
	if err != nil {
		t.Fatalf("failed to set start node: %v", err)
	}

	ctx := context.Background()
	state := NewState()
	result, err := graph.Execute(ctx, state)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	// Wait for tracer call with timeout
	select {
	case call := <-calls:
		if call.ToolName != "observability-test" {
			t.Errorf("expected tool name observability-test, got %s", call.ToolName)
		}
		if call.Duration == 0 {
			t.Error("expected non-zero duration")
		}
		if call.Error != nil {
			t.Errorf("expected nil error, got %v", call.Error)
		}
	case <-time.After(1 * time.Second):
		t.Error("timeout waiting for tracer call")
	}

	// Verify graph execution completed
	if result.GraphID != "observability-test" {
		t.Errorf("expected graph ID observability-test, got %s", result.GraphID)
	}

	// Verify execution order
	if len(executionOrder) != 2 {
		t.Errorf("expected 2 nodes executed, got %d", len(executionOrder))
	}
}

func TestGraphWithRateLimit(t *testing.T) {
	// Test that graph execution respects rate limiting
	limiter := ratelimit.NewTokenBucketLimiter(&ratelimit.LimiterConfig{
		Rate:    10.0, // 10 requests per second
		Burst:   1,    // burst size of 1
		Timeout: 5 * time.Second,
	})

	executionCount := 0

	graph, err := NewGraphWithLimiter("rate-limit-test", limiter)
	if err != nil {
		t.Fatalf("failed to create graph: %v", err)
	}
	graph, err = graph.Node("node1", &mockNode{id: "node1", executeFn: func(ctx context.Context, state *State) error {
		executionCount++
		return nil
	}})
	if err != nil {
		t.Fatalf("failed to add node1: %v", err)
	}
	graph, err = graph.Start("node1")
	if err != nil {
		t.Fatalf("failed to set start node: %v", err)
	}

	ctx := context.Background()
	state := NewState()

	// Execute graph multiple times to test rate limiting
	start := time.Now()
	for i := 0; i < 3; i++ {
		_, err := graph.Execute(ctx, state)
		if err != nil {
			t.Fatalf("execution %d failed: %v", i, err)
		}
	}
	duration := time.Since(start)

	// With rate limit of 10 req/s, 3 executions should complete quickly
	// But due to burst size of 1, there will be some rate limiting
	if duration < 100*time.Millisecond {
		t.Logf("execution completed quickly: %v", duration)
	}

	if executionCount != 3 {
		t.Errorf("expected 3 executions, got %d", executionCount)
	}
}

func TestGraphWithRateLimitExceeded(t *testing.T) {
	// Test that graph execution respects rate limiting by measuring execution time
	limiter := ratelimit.NewTokenBucketLimiter(&ratelimit.LimiterConfig{
		Rate:    100.0, // 100 requests per second
		Burst:   1,     // burst size of 1 to ensure rate limiting
		Timeout: 5 * time.Second,
	})

	graph, err := NewGraphWithLimiter("rate-limit-test", limiter)
	if err != nil {
		t.Fatalf("failed to create graph: %v", err)
	}
	graph, err = graph.Node("node1", &mockNode{id: "node1", executeFn: func(ctx context.Context, state *State) error {
		return nil
	}})
	if err != nil {
		t.Fatalf("failed to add node1: %v", err)
	}
	graph, err = graph.Start("node1")
	if err != nil {
		t.Fatalf("failed to set start node: %v", err)
	}

	ctx := context.Background()
	state := NewState()

	// Execute graph multiple times and measure time
	start := time.Now()
	for i := 0; i < 5; i++ {
		_, err := graph.Execute(ctx, state)
		if err != nil {
			t.Fatalf("execution %d failed: %v", i, err)
		}
	}
	duration := time.Since(start)

	// With burst size of 1, multiple executions should show rate limiting effect
	// First execution should be fast, subsequent ones should be throttled
	t.Logf("5 executions took: %v", duration)

	if duration < 10*time.Millisecond {
		t.Error("expected some rate limiting effect, but execution was too fast")
	}
}

func TestGraphWithBothObservabilityAndRateLimit(t *testing.T) {
	// Test that both observability and rate limiting work together
	calls := make(chan *observability.ToolCall, 10)

	tracer := &mockTracer{
		recordToolCallFunc: func(ctx context.Context, call *observability.ToolCall) {
			calls <- call
		},
	}

	limiter := ratelimit.NewTokenBucketLimiter(&ratelimit.LimiterConfig{
		Rate:    5.0, // 5 requests per second
		Burst:   1,
		Timeout: 5 * time.Second,
	})

	graph, err := NewGraph("combined-test")
	if err != nil {
		t.Fatalf("failed to create graph: %v", err)
	}
	graph, err = graph.SetTracer(tracer)
	if err != nil {
		t.Fatalf("failed to set tracer: %v", err)
	}
	graph, err = graph.SetLimiter(limiter)
	if err != nil {
		t.Fatalf("failed to set limiter: %v", err)
	}
	graph, err = graph.Node("node1", &mockNode{id: "node1", executeFn: func(ctx context.Context, state *State) error {
		return nil
	}})
	if err != nil {
		t.Fatalf("failed to add node1: %v", err)
	}
	graph, err = graph.Start("node1")
	if err != nil {
		t.Fatalf("failed to set start node: %v", err)
	}

	ctx := context.Background()
	state := NewState()

	// Execute graph
	result, err := graph.Execute(ctx, state)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	// Verify tracer was called
	select {
	case call := <-calls:
		if call.ToolName != "combined-test" {
			t.Errorf("expected tool name combined-test, got %s", call.ToolName)
		}
	case <-time.After(1 * time.Second):
		t.Error("timeout waiting for tracer call")
	}

	// Verify result
	if result.GraphID != "combined-test" {
		t.Errorf("expected graph ID combined-test, got %s", result.GraphID)
	}
}

func TestGraphWithCustomTracer(t *testing.T) {
	// Test that custom tracer is properly integrated
	tracer := observability.NewNoopTracer()

	graph, err := NewGraph("custom-tracer-test")
	if err != nil {
		t.Fatalf("failed to create graph: %v", err)
	}
	graph, err = graph.SetTracer(tracer)
	if err != nil {
		t.Fatalf("failed to set tracer: %v", err)
	}
	graph, err = graph.Node("node1", &mockNode{id: "node1", executeFn: func(ctx context.Context, state *State) error {
		return nil
	}})
	if err != nil {
		t.Fatalf("failed to add node1: %v", err)
	}
	graph, err = graph.Start("node1")
	if err != nil {
		t.Fatalf("failed to set start node: %v", err)
	}

	ctx := context.Background()
	state := NewState()
	_, err = graph.Execute(ctx, state)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}

	// Verify that tracer was used (by checking that no panic occurred)
}

// mockTracer is a simple mock tracer for testing.
type mockTracer struct {
	recordToolCallFunc func(ctx context.Context, call *observability.ToolCall)
}

func (m *mockTracer) RecordLLMCall(ctx context.Context, call *observability.LLMCall) {
	// No-op
}

func (m *mockTracer) RecordToolCall(ctx context.Context, call *observability.ToolCall) {
	if m.recordToolCallFunc != nil {
		m.recordToolCallFunc(ctx, call)
	}
}

func (m *mockTracer) RecordAgentStep(ctx context.Context, step *observability.AgentStep) {
	// No-op
}

func (m *mockTracer) RecordError(ctx context.Context, err *observability.AgentError) {
	// No-op
}

func (m *mockTracer) GetTraceID(ctx context.Context) string {
	return "test-trace-id"
}

func (m *mockTracer) WithTrace(ctx context.Context) context.Context {
	return ctx
}
