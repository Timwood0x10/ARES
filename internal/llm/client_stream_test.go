package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coreerrors "github.com/Timwood0x10/ares/internal/core/errors"
)

// TestClient_GenerateStream_Ollama tests Ollama streaming through llm.Client.
func TestClient_GenerateStream_Ollama(t *testing.T) {
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

		w.Header().Set("Content-Type", "application/x-ndjson")

		chunks := []struct {
			Response string `json:"response"`
			Done     bool   `json:"done"`
		}{
			{Response: "Hi"},
			{Response: " there"},
			{Response: "", Done: true},
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

	client, err := NewClient(&Config{
		Provider: "ollama",
		BaseURL:  server.URL,
		Model:    "llama3.2",
		Timeout:  5,
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := client.GenerateStream(ctx, "hello")
	if err != nil {
		t.Fatalf("GenerateStream failed: %v", err)
	}

	var content strings.Builder
	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatalf("unexpected error: %v", chunk.Err)
		}
		content.WriteString(chunk.Content)
	}

	if got := content.String(); got != "Hi there" {
		t.Errorf("expected 'Hi there', got %q", got)
	}
}

// TestClient_GenerateStream_OpenRouter tests OpenRouter streaming through llm.Client.
func TestClient_GenerateStream_OpenRouter(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("expected Authorization header")
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer does not support flushing")
		}

		w.Header().Set("Content-Type", "text/event-stream")

		events := []string{
			`data: {"choices":[{"delta":{"content":"A"}}]}`,
			`data: {"choices":[{"delta":{"content":"B"}}]}`,
			`data: {"choices":[{"delta":{"content":"C"}}]}`,
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

	client, err := NewClient(&Config{
		Provider: "openrouter",
		APIKey:   "test-key",
		BaseURL:  server.URL,
		Model:    "openai/gpt-3.5-turbo",
		Timeout:  5,
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := client.GenerateStream(ctx, "test")
	if err != nil {
		t.Fatalf("GenerateStream failed: %v", err)
	}

	var content strings.Builder
	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatalf("unexpected error: %v", chunk.Err)
		}
		content.WriteString(chunk.Content)
	}

	if got := content.String(); got != "ABC" {
		t.Errorf("expected 'ABC', got %q", got)
	}
}

// TestClient_GenerateStream_EmptyPrompt tests empty prompt rejection.
func TestClient_GenerateStream_EmptyPrompt(t *testing.T) {
	client, err := NewClient(&Config{Provider: "ollama", Model: "llama3.2", Timeout: 5})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	_, err = client.GenerateStream(context.Background(), "")
	if err != coreerrors.ErrInvalidArgument {
		t.Errorf("expected ErrInvalidArgument, got %v", err)
	}
}

// TestClient_GenerateStream_UnsupportedProvider tests unsupported provider.
func TestClient_GenerateStream_UnsupportedProvider(t *testing.T) {
	client, err := NewClient(&Config{Provider: "unknown", Model: "llama3.2", Timeout: 5})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	_, err = client.GenerateStream(context.Background(), "test")
	if err == nil {
		t.Error("expected error for unsupported provider")
	}
}

// TestClient_GenerateStream_Cancel tests context cancellation.
func TestClient_GenerateStream_Cancel(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("response writer does not support flushing")
		}

		w.Header().Set("Content-Type", "application/x-ndjson")

		for i := 0; i < 1000; i++ {
			chunk := struct {
				Response string `json:"response"`
			}{Response: "x"}
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

	client, err := NewClient(&Config{
		Provider: "ollama",
		BaseURL:  server.URL,
		Model:    "llama3.2",
		Timeout:  5,
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := client.GenerateStream(ctx, "test")
	if err != nil {
		t.Fatalf("GenerateStream failed: %v", err)
	}

	// Read one chunk.
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
			t.Fatal("channel did not close after cancellation")
		}
	}
}

// TestClient_GenerateStream_HTTPError tests non-200 response handling.
func TestClient_GenerateStream_HTTPError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		if _, err := w.Write([]byte("bad gateway")); err != nil {
			t.Errorf("failed to write error response: %v", err)
		}
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	client, err := NewClient(&Config{
		Provider: "ollama",
		BaseURL:  server.URL,
		Model:    "llama3.2",
		Timeout:  5,
	})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	_, err = client.GenerateStream(context.Background(), "test")
	if err == nil {
		t.Error("expected error for HTTP 502")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("error should contain status code, got: %v", err)
	}
}

// TestClient_GenerateStream_WhitespaceOnlyPrompt tests whitespace-only prompt rejection.
func TestClient_GenerateStream_WhitespaceOnlyPrompt(t *testing.T) {
	client, err := NewClient(&Config{Provider: "ollama", Model: "llama3.2", Timeout: 5})
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	_, err = client.GenerateStream(context.Background(), "   \t\n  ")
	if err != coreerrors.ErrInvalidArgument {
		t.Errorf("expected ErrInvalidArgument for whitespace prompt, got %v", err)
	}
}
