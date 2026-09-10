package output

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestOllamaAdapter_GenerateStream tests streaming from Ollama adapter.
func TestOllamaAdapter_GenerateStream(t *testing.T) { //nolint:errcheck
	// Simulate Ollama streaming NDJSON response.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify stream=true is set.
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("failed to decode request: %v", err)
			return
		}
		if body["stream"] != true {
			t.Error("expected stream=true in request")
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer does not support flushing")
		}

		w.Header().Set("Content-Type", "application/x-ndjson")

		chunks := []OllamaResponse{
			{Model: "llama3.2", Response: "Hello"},
			{Model: "llama3.2", Response: " World"},
			{Model: "llama3.2", Response: "", Done: true},
		}

		for _, chunk := range chunks {
			data, _ := json.Marshal(chunk)
			if _, err := w.Write(data); err != nil {
				t.Errorf("failed to write data: %v", err)
				return
			}
			if _, err := w.Write([]byte("\n")); err != nil {
				t.Errorf("failed to write newline: %v", err)
				return
			}
			flusher.Flush()
		}
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	adapter := NewOllamaAdapter(&Config{
		BaseURL: server.URL,
		Model:   "llama3.2",
		Timeout: 5,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := adapter.GenerateStream(ctx, "test prompt")
	if err != nil {
		t.Fatalf("GenerateStream failed: %v", err)
	}

	var content strings.Builder
	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatalf("unexpected stream error: %v", chunk.Err)
		}
		content.WriteString(chunk.Content)
	}

	if got := content.String(); got != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", got)
	}
}

// TestOllamaAdapter_GenerateStream_Cancel tests context cancellation during streaming.
func TestOllamaAdapter_GenerateStream_Cancel(t *testing.T) { //nolint:errcheck
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer does not support flushing")
		}

		w.Header().Set("Content-Type", "application/x-ndjson")

		// Send many chunks to give time for cancellation.
		for i := 0; i < 1000; i++ {
			chunk := OllamaResponse{Model: "llama3.2", Response: "chunk"}
			data, _ := json.Marshal(chunk)
			if _, err := w.Write(data); err != nil {
				// Broken pipe is expected when client cancels
				return
			}
			if _, err := w.Write([]byte("\n")); err != nil {
				// Broken pipe is expected when client cancels
				return
			}
			flusher.Flush()
			time.Sleep(1 * time.Millisecond)
		}
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	adapter := NewOllamaAdapter(&Config{
		BaseURL: server.URL,
		Model:   "llama3.2",
		Timeout: 5,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := adapter.GenerateStream(ctx, "test prompt")
	if err != nil {
		t.Fatalf("GenerateStream failed: %v", err)
	}

	// Read one chunk then cancel.
	_, ok := <-ch
	if !ok {
		t.Fatal("channel closed immediately")
	}

	cancel()

	// Channel should eventually close (drain remaining with timeout).
	timeout := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // Success: channel closed after cancellation.
			}
		case <-timeout:
			t.Fatal("channel did not close after context cancellation")
		}
	}
}

// TestOllamaAdapter_GenerateStream_EmptyPrompt tests empty prompt rejection.
func TestOllamaAdapter_GenerateStream_EmptyPrompt(t *testing.T) {
	adapter := NewOllamaAdapter(&Config{BaseURL: "http://localhost:11434"})

	_, err := adapter.GenerateStream(context.Background(), "")
	if err == nil {
		t.Error("expected error for empty prompt")
	}
}

// TestOllamaAdapter_GenerateStream_HTTPError tests non-200 response handling.
func TestOllamaAdapter_GenerateStream_HTTPError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		if _, err := w.Write([]byte("internal server error")); err != nil {
			t.Errorf("failed to write error response: %v", err)
		}
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	adapter := NewOllamaAdapter(&Config{
		BaseURL: server.URL,
		Model:   "llama3.2",
		Timeout: 5,
	})

	_, err := adapter.GenerateStream(context.Background(), "test")
	if err == nil {
		t.Error("expected error for HTTP 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should contain status code, got: %v", err)
	}
}

// TestOpenAIAdapter_GenerateStream tests streaming from OpenAI adapter.
func TestOpenAIAdapter_GenerateStream(t *testing.T) { //nolint:errcheck
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("failed to decode request: %v", err)
			return
		}
		if body["stream"] != true {
			t.Error("expected stream=true in request")
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer does not support flushing")
		}

		w.Header().Set("Content-Type", "text/event-stream")

		events := []string{
			`data: {"choices":[{"delta":{"content":"Hello"}}]}`,
			`data: {"choices":[{"delta":{"content":" World"}}]}`,
			`data: {"choices":[{"delta":{"content":""}}]}`,
			`data: [DONE]`,
		}

		for _, event := range events {
			if _, err := w.Write([]byte(event + "\n\n")); err != nil {
				t.Errorf("failed to write event: %v", err)
				return
			}
			flusher.Flush()
		}
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	adapter := NewOpenAIAdapter(&Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   "gpt-3.5-turbo",
		Timeout: 5,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := adapter.GenerateStream(ctx, "test prompt")
	if err != nil {
		t.Fatalf("GenerateStream failed: %v", err)
	}

	var content strings.Builder
	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatalf("unexpected stream error: %v", chunk.Err)
		}
		content.WriteString(chunk.Content)
	}

	if got := content.String(); got != "Hello World" {
		t.Errorf("expected 'Hello World', got %q", got)
	}
}

// TestOpenAIAdapter_GenerateStream_Cancel tests context cancellation.
func TestOpenAIAdapter_GenerateStream_Cancel(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer does not support flushing")
		}

		w.Header().Set("Content-Type", "text/event-stream")

		for i := 0; i < 1000; i++ {
			if _, err := w.Write([]byte(`data: {"choices":[{"delta":{"content":"x"}}]}` + "\n\n")); err != nil {
				// Broken pipe is expected when client cancels
				return
			}
			flusher.Flush()
			time.Sleep(1 * time.Millisecond)
		}
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	adapter := NewOpenAIAdapter(&Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   "gpt-3.5-turbo",
		Timeout: 5,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := adapter.GenerateStream(ctx, "test prompt")
	if err != nil {
		t.Fatalf("GenerateStream failed: %v", err)
	}

	// Read one chunk then cancel.
	_, ok := <-ch
	if !ok {
		t.Fatal("channel closed immediately")
	}

	cancel()

	timeout := time.After(2 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-timeout:
			t.Fatal("channel did not close after context cancellation")
		}
	}
}

// TestOpenAIAdapter_GenerateStream_EmptyPrompt tests empty prompt rejection.
func TestOpenAIAdapter_GenerateStream_EmptyPrompt(t *testing.T) {
	adapter := NewOpenAIAdapter(&Config{BaseURL: "http://localhost:11434"})

	_, err := adapter.GenerateStream(context.Background(), "")
	if err == nil {
		t.Error("expected error for empty prompt")
	}
}

// TestOpenAIAdapter_GenerateStream_HTTPError tests non-200 response handling.
func TestOpenAIAdapter_GenerateStream_HTTPError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		if _, err := w.Write([]byte("unauthorized")); err != nil {
			t.Errorf("failed to write error response: %v", err)
		}
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	adapter := NewOpenAIAdapter(&Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   "gpt-3.5-turbo",
		Timeout: 5,
	})

	_, err := adapter.GenerateStream(context.Background(), "test")
	if err == nil {
		t.Error("expected error for HTTP 401")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should contain status code, got: %v", err)
	}
}

// TestOpenAIAdapter_GenerateStream_MalformedChunk tests handling of malformed SSE data.
func TestOpenAIAdapter_GenerateStream_MalformedChunk(t *testing.T) { //nolint:errcheck
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer does not support flushing")
		}

		w.Header().Set("Content-Type", "text/event-stream")

		// Send valid chunk, then malformed, then valid, then DONE.
		events := []string{
			`data: {"choices":[{"delta":{"content":"OK"}}]}`,
			`data: {invalid json}`,
			`data: {"choices":[{"delta":{"content":"!"}}]}`,
			`data: [DONE]`,
		}

		for _, event := range events {
			if _, err := w.Write([]byte(event + "\n\n")); err != nil {
				t.Errorf("failed to write event: %v", err)
				return
			}
			flusher.Flush()
		}
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	adapter := NewOpenAIAdapter(&Config{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Model:   "gpt-3.5-turbo",
		Timeout: 5,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := adapter.GenerateStream(ctx, "test prompt")
	if err != nil {
		t.Fatalf("GenerateStream failed: %v", err)
	}

	var content strings.Builder
	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatalf("unexpected stream error: %v", chunk.Err)
		}
		content.WriteString(chunk.Content)
	}

	if got := content.String(); got != "OK!" {
		t.Errorf("expected 'OK!', got %q", got)
	}
}

// TestOllamaAdapter_GenerateStream_TemperatureInOptionsAndTerminalDone
// locks two REVIEW 3.8 adapter fixes:
//  1. Sampling parameters must sit inside the "options" object — Ollama
//     silently ignores a top-level "temperature".
//  2. A clean stream end must emit a terminal {Done: true} chunk so
//     consumers watching for Done (instead of channel close) don't hang.
func TestOllamaAdapter_GenerateStream_TemperatureInOptionsAndTerminalDone(t *testing.T) {
	var sawOptionsTemperature bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("failed to decode request: %v", err)
			return
		}
		if _, ok := body["temperature"]; ok {
			t.Error("temperature must NOT be a top-level field; Ollama ignores it there")
		}
		opts, ok := body["options"].(map[string]interface{})
		if !ok {
			t.Error("request must carry an options object")
			return
		}
		if temp, ok := opts["temperature"].(float64); ok && temp == 0.7 {
			sawOptionsTemperature = true
		}

		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "application/x-ndjson")
		chunks := []OllamaResponse{
			{Model: "llama3.2", Response: "Hi"},
			{Model: "llama3.2", Response: "", Done: true},
		}
		for _, chunk := range chunks {
			data, _ := json.Marshal(chunk)
			if _, err := w.Write(data); err != nil {
				t.Errorf("failed to write data: %v", err)
				return
			}
			if _, err := w.Write([]byte("\n")); err != nil {
				t.Errorf("failed to write newline: %v", err)
				return
			}
			flusher.Flush()
		}
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	adapter := NewOllamaAdapter(&Config{
		BaseURL:     server.URL,
		Model:       "llama3.2",
		Timeout:     5,
		Temperature: 0.7,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := adapter.GenerateStream(ctx, "test prompt")
	if err != nil {
		t.Fatalf("GenerateStream failed: %v", err)
	}

	sawTerminalDone := false
	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatalf("unexpected stream error: %v", chunk.Err)
		}
		if chunk.Done {
			sawTerminalDone = true
		}
	}
	if !sawTerminalDone {
		t.Error("clean stream end must emit a terminal {Done: true} chunk")
	}
	if !sawOptionsTemperature {
		t.Error("temperature must be sent inside the options object")
	}
}

// TestOpenAIAdapter_GenerateStream_TerminalDoneChunk locks REVIEW 3.8: the
// OpenAI-compatible adapter must emit a terminal {Done: true} chunk on the
// "data: [DONE]" sentinel (and on a server-side close without it) so
// consumers watching for Done instead of channel close don't hang.
func TestOpenAIAdapter_GenerateStream_TerminalDoneChunk(t *testing.T) {
	for _, tc := range []struct {
		name    string
		withEnd bool
	}{
		{name: "with DONE sentinel", withEnd: true},
		{name: "server closes without sentinel", withEnd: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				flusher := w.(http.Flusher)
				w.Header().Set("Content-Type", "text/event-stream")
				events := []string{`data: {"choices":[{"delta":{"content":"Hi"}}]}`}
				if tc.withEnd {
					events = append(events, `data: [DONE]`)
				}
				for _, event := range events {
					if _, err := w.Write([]byte(event + "\n\n")); err != nil {
						t.Errorf("failed to write event: %v", err)
						return
					}
					flusher.Flush()
				}
			})

			server := httptest.NewServer(handler)
			defer server.Close()

			adapter := NewOpenAIAdapter(&Config{
				BaseURL: server.URL,
				APIKey:  "test-key",
				Model:   "gpt-3.5-turbo",
				Timeout: 5,
			})

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			ch, err := adapter.GenerateStream(ctx, "test prompt")
			if err != nil {
				t.Fatalf("GenerateStream failed: %v", err)
			}

			sawDone, gotContent := false, ""
			for chunk := range ch {
				if chunk.Err != nil {
					t.Fatalf("unexpected stream error: %v", chunk.Err)
				}
				if chunk.Done {
					sawDone = true
				}
				gotContent += chunk.Content
			}
			if gotContent != "Hi" {
				t.Errorf("expected 'Hi', got %q", gotContent)
			}
			if !sawDone {
				t.Error("clean stream end must emit a terminal {Done: true} chunk")
			}
		})
	}
}
