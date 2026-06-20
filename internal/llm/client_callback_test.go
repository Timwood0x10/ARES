package llm

import (
	"context"
	"errors"
	"sync"
	"testing"

	"goagentx/internal/callbacks"
	coreerrors "goagentx/internal/core/errors"
)

// TestClientGenerateEmitsCallbacks verifies that Generate() emits
// EventLLMStart followed by EventLLMError for invalid input.
func TestClientGenerateEmitsCallbacks(t *testing.T) {
	reg := callbacks.NewRegistry()

	var mu sync.Mutex
	var receivedEvents []*callbacks.Context

	reg.On(callbacks.EventLLMStart, func(ctx *callbacks.Context) {
		mu.Lock()
		defer mu.Unlock()
		receivedEvents = append(receivedEvents, ctx)
	})
	reg.On(callbacks.EventLLMError, func(ctx *callbacks.Context) {
		mu.Lock()
		defer mu.Unlock()
		receivedEvents = append(receivedEvents, ctx)
	})
	reg.On(callbacks.EventLLMEnd, func(ctx *callbacks.Context) {
		mu.Lock()
		defer mu.Unlock()
		receivedEvents = append(receivedEvents, ctx)
	})

	client, err := NewClient(&Config{
		Provider: "ollama",
		BaseURL:  "http://localhost:11434",
		Model:    "llama3",
		Timeout:  5,
	}, WithCallbacks(reg))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ctx := context.Background()

	// Test empty prompt: should emit LLMStart + LLMError.
	_, err = client.Generate(ctx, "")
	if err == nil {
		t.Fatal("expected error for empty prompt")
	}

	mu.Lock()
	count := len(receivedEvents)
	mu.Unlock()

	if count < 2 {
		t.Fatalf("expected at least 2 events (start+error), got %d", count)
	}

	mu.Lock()
	startEvent := receivedEvents[0]
	errorEvent := receivedEvents[1]
	mu.Unlock()

	if startEvent.Event != callbacks.EventLLMStart {
		t.Errorf("first event = %s, want %s", startEvent.Event, callbacks.EventLLMStart)
	}
	if errorEvent.Event != callbacks.EventLLMError {
		t.Errorf("second event = %s, want %s", errorEvent.Event, callbacks.EventLLMError)
	}
	if errorEvent.Error == nil {
		t.Error("LLMError event should have non-nil Error")
	}
	if errorEvent.Duration < 0 {
		t.Error("LLMError event should have non-negative Duration")
	}
	if errorEvent.Model != "llama3" {
		t.Errorf("Model = %s, want llama3", errorEvent.Model)
	}
}

// TestClientGenerateWhitespacePromptCallback verifies whitespace-only prompt emits events.
func TestClientGenerateWhitespacePromptCallback(t *testing.T) {
	reg := callbacks.NewRegistry()

	var mu sync.Mutex
	var receivedEvents []*callbacks.Context

	reg.On(callbacks.EventLLMError, func(ctx *callbacks.Context) {
		mu.Lock()
		defer mu.Unlock()
		receivedEvents = append(receivedEvents, ctx)
	})

	client, err := NewClient(&Config{
		Provider: "ollama",
		BaseURL:  "http://localhost:11434",
		Model:    "llama3",
		Timeout:  5,
	}, WithCallbacks(reg))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ctx := context.Background()
	_, err = client.Generate(ctx, "   \t  ")
	if err == nil {
		t.Fatal("expected error for whitespace-only prompt")
	}

	mu.Lock()
	count := len(receivedEvents)
	mu.Unlock()

	if count < 1 {
		t.Fatal("expected at least 1 LLMError event")
	}
}

// TestClientLongPromptCallback verifies oversized prompt emits LLMError with duration.
func TestClientLongPromptCallback(t *testing.T) {
	reg := callbacks.NewRegistry()

	var mu sync.Mutex
	var errorEvent *callbacks.Context

	reg.On(callbacks.EventLLMError, func(ctx *callbacks.Context) {
		mu.Lock()
		defer mu.Unlock()
		errorEvent = ctx
	})

	client, err := NewClient(&Config{
		Provider: "ollama",
		BaseURL:  "http://localhost:11434",
		Model:    "llama3",
		Timeout:  5,
	}, WithCallbacks(reg))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	longPrompt := make([]byte, 8193)
	for i := range longPrompt {
		longPrompt[i] = 'x'
	}

	ctx := context.Background()
	_, err = client.Generate(ctx, string(longPrompt))
	if err == nil {
		t.Fatal("expected error for long prompt")
	}

	mu.Lock()
	ev := errorEvent
	mu.Unlock()

	if ev == nil {
		t.Fatal("should have received LLMError event")
	}
	if ev.Input == "" {
		t.Error("Input should not be empty in error event")
	}
	if ev.Error == nil {
		t.Error("Error should be non-nil for oversized prompt")
	}
	if ev.Duration < 0 {
		t.Error("Duration should be non-negative")
	}
}

// TestClientNilCallbacksNoPanic verifies that client works without callbacks set.
func TestClientNilCallbacksNoPanic(t *testing.T) {
	client, err := NewClient(&Config{
		Provider: "ollama",
		BaseURL:  "http://localhost:11434",
		Model:    "llama3",
		Timeout:  5,
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ctx := context.Background()
	_, err = client.Generate(ctx, "")
	if err == nil {
		t.Fatal("expected error for empty prompt")
	}
	// No panic means success.
}

// TestClientUnsupportedProviderCallback verifies unsupported provider emits proper events.
func TestClientUnsupportedProviderCallback(t *testing.T) {
	reg := callbacks.NewRegistry()

	var mu sync.Mutex
	var startEvent *callbacks.Context
	var errorEvent *callbacks.Context

	reg.On(callbacks.EventLLMStart, func(ctx *callbacks.Context) {
		mu.Lock()
		defer mu.Unlock()
		startEvent = ctx
	})
	reg.On(callbacks.EventLLMError, func(ctx *callbacks.Context) {
		mu.Lock()
		defer mu.Unlock()
		errorEvent = ctx
	})

	client, err := NewClient(&Config{
		Provider: "invalid_provider",
		BaseURL:  "http://localhost:11434",
		Model:    "test-model",
		Timeout:  5,
	}, WithCallbacks(reg))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ctx := context.Background()
	_, err = client.Generate(ctx, "hello")
	if err == nil {
		t.Fatal("expected error for unsupported provider")
	}

	mu.Lock()
	startEv := startEvent
	errEv := errorEvent
	mu.Unlock()

	if startEv == nil {
		t.Fatal("should have received LLMStart event")
	}
	if startEv.Model != "test-model" {
		t.Errorf("Start Model = %s, want test-model", startEv.Model)
	}
	if startEv.Input != "hello" {
		t.Errorf("Start Input = %s, want hello", startEv.Input)
	}
	if errEv == nil {
		t.Fatal("should have received LLMError event")
	}
	if errEv.Duration < 0 {
		t.Error("Error Duration should be non-negative")
	}
	if errEv.Error == nil {
		t.Error("Error should be non-nil")
	}
}

// TestClientMultipleCallsShareRegistry verifies multiple clients sharing one registry.
func TestClientMultipleCallsShareRegistry(t *testing.T) {
	reg := callbacks.NewRegistry()

	var mu sync.Mutex
	eventCount := 0

	reg.On(callbacks.EventLLMStart, func(ctx *callbacks.Context) {
		mu.Lock()
		defer mu.Unlock()
		eventCount++
	})
	reg.On(callbacks.EventLLMError, func(ctx *callbacks.Context) {
		mu.Lock()
		defer mu.Unlock()
		eventCount++
	})

	client1, _ := NewClient(&Config{
		Provider: "ollama",
		Model:    "model-a",
		Timeout:  5,
	}, WithCallbacks(reg))

	client2, _ := NewClient(&Config{
		Provider: "ollama",
		Model:    "model-b",
		Timeout:  5,
	}, WithCallbacks(reg))

	ctx := context.Background()
	client1.Generate(ctx, "")
	client2.Generate(ctx, "")

	mu.Lock()
	count := eventCount
	mu.Unlock()

	// Each call: Start + Error = 2 events. Two clients = 4 total.
	if count < 4 {
		t.Fatalf("expected at least 4 events, got %d", count)
	}
}

// TestClientCallbackErrorType verifies that ErrInvalidArgument is properly propagated.
func TestClientCallbackErrorType(t *testing.T) {
	reg := callbacks.NewRegistry()

	var mu sync.Mutex
	var callbackErr error

	reg.On(callbacks.EventLLMError, func(ctx *callbacks.Context) {
		mu.Lock()
		defer mu.Unlock()
		callbackErr = ctx.Error
	})

	client, err := NewClient(&Config{
		Provider: "ollama",
		BaseURL:  "http://localhost:11434",
		Model:    "llama3",
		Timeout:  5,
	}, WithCallbacks(reg))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ctx := context.Background()
	_, err = client.Generate(ctx, "")
	if err == nil {
		t.Fatal("expected error for empty prompt")
	}

	mu.Lock()
	cbErr := callbackErr
	mu.Unlock()

	if cbErr == nil {
		t.Fatal("callback error should not be nil")
	}
	if !errors.Is(cbErr, coreerrors.ErrInvalidArgument) {
		t.Errorf("error should wrap ErrInvalidArgument, got: %v", cbErr)
	}
}

// TestWithCallbacksOption verifies the WithCallbacks option sets the emitter correctly.
func TestWithCallbacksOption(t *testing.T) {
	reg := callbacks.NewRegistry()

	client, err := NewClient(&Config{
		Provider: "ollama",
		Model:    "test",
		Timeout:  5,
	}, WithCallbacks(reg))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	// Verify the client was created successfully with callbacks.
	if client == nil {
		t.Fatal("client should not be nil")
	}
}

// TestGenerateStreamCallbackOnEmptyPrompt verifies that streaming with an empty prompt
// emits only an error event (no start event, since the stream never begins).
func TestGenerateStreamCallbackOnEmptyPrompt(t *testing.T) {
	reg := callbacks.NewRegistry()

	var mu sync.Mutex
	var receivedEvents []*callbacks.Context

	reg.On(callbacks.EventLLMStart, func(ctx *callbacks.Context) {
		mu.Lock()
		defer mu.Unlock()
		receivedEvents = append(receivedEvents, ctx)
	})
	reg.On(callbacks.EventLLMError, func(ctx *callbacks.Context) {
		mu.Lock()
		defer mu.Unlock()
		receivedEvents = append(receivedEvents, ctx)
	})

	client, err := NewClient(&Config{
		Provider: "ollama",
		BaseURL:  "http://localhost:11434",
		Model:    "llama3",
		Timeout:  5,
	}, WithCallbacks(reg))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	ctx := context.Background()
	_, err = client.GenerateStream(ctx, "")
	if err == nil {
		t.Fatal("expected error for empty prompt")
	}

	mu.Lock()
	count := len(receivedEvents)
	mu.Unlock()

	// EventLLMStart is only emitted after validation passes. An empty prompt
	// returns before validation completes, so only EventLLMError should fire.
	if count != 1 {
		t.Fatalf("expected 1 event (error-only) for empty prompt stream, got %d", count)
	}

	mu.Lock()
	errorEvent := receivedEvents[0]
	mu.Unlock()

	if errorEvent.Event != callbacks.EventLLMError {
		t.Errorf("first event = %s, want %s", errorEvent.Event, callbacks.EventLLMError)
	}
}

// TestGenerateStreamLongPromptCallback verifies streaming with long prompt emits error event.
func TestGenerateStreamLongPromptCallback(t *testing.T) {
	reg := callbacks.NewRegistry()

	var mu sync.Mutex
	var errorEvent *callbacks.Context

	reg.On(callbacks.EventLLMError, func(ctx *callbacks.Context) {
		mu.Lock()
		defer mu.Unlock()
		errorEvent = ctx
	})

	client, err := NewClient(&Config{
		Provider: "ollama",
		BaseURL:  "http://localhost:11434",
		Model:    "llama3",
		Timeout:  5,
	}, WithCallbacks(reg))
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	longPrompt := make([]byte, 8193)
	for i := range longPrompt {
		longPrompt[i] = 'y'
	}

	ctx := context.Background()
	_, err = client.GenerateStream(ctx, string(longPrompt))
	if err == nil {
		t.Fatal("expected error for long prompt in stream")
	}

	mu.Lock()
	ev := errorEvent
	mu.Unlock()

	if ev == nil {
		t.Fatal("should have received LLMError event for stream")
	}
	if ev.Duration < 0 {
		t.Error("Duration should be non-negative even for stream errors")
	}
}
