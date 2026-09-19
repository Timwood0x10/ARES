package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_ratelimit"
	llmcore "github.com/Timwood0x10/ares/internal/llmcore"
)

// ── Client.GenerateWithParams ───────────────────────────────────────────────

func TestClientGenerateWithParams_OpenRouter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if temp, ok := body["temperature"].(float64); ok && temp != 0.9 {
			t.Errorf("temperature = %v, want 0.9", temp)
		}
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "param-ok"}},
			},
			"usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 3},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer server.Close()

	c, err := NewClient(&Config{
		Provider: "openai",
		Model:    "gpt-4",
		BaseURL:  server.URL,
		APIKey:   "test-key",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	got, err := c.GenerateWithParams(context.Background(), "hello", map[string]any{
		"temperature": 0.9,
		"max_tokens":  100.0,
	})
	if err != nil {
		t.Fatalf("GenerateWithParams: %v", err)
	}
	if got != "param-ok" {
		t.Errorf("got %q, want param-ok", got)
	}
}

func TestClientGenerateWithParams_Ollama(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
			return
		}
		opts, _ := body["options"].(map[string]any)
		if opts == nil {
			t.Error("options must not be nil")
		}
		resp := map[string]any{
			"response":          "ollama-param-ok",
			"prompt_eval_count": 10,
			"eval_count":        5,
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer server.Close()

	c, err := NewClient(&Config{
		Provider: "ollama",
		Model:    "llama3",
		BaseURL:  server.URL,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	got, err := c.GenerateWithParams(context.Background(), "hello", map[string]any{
		"temperature": 0.3,
		"top_k":       20.0,
	})
	if err != nil {
		t.Fatalf("GenerateWithParams: %v", err)
	}
	if got != "ollama-param-ok" {
		t.Errorf("got %q", got)
	}
}

// ── streamAnthropic ─────────────────────────────────────────────────────────

func TestStreamAnthropic_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			t.Errorf("path = %q", r.URL.Path)
		}
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		events := []string{
			`event: content_block_delta`,
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"Hello"}}`,
			``,
			`data: {"type":"content_block_delta","delta":{"type":"text_delta","text":" World"}}`,
			``,
			`data: {"type":"message_stop"}`,
			``,
		}
		for _, e := range events {
			if _, err := w.Write([]byte(e + "\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}))
	defer server.Close()

	c, err := NewClient(&Config{
		Provider: "anthropic",
		Model:    "claude-3",
		BaseURL:  server.URL,
		APIKey:   "test-key",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := c.GenerateStream(ctx, "prompt")
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}

	var sb strings.Builder
	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatalf("stream error: %v", chunk.Err)
		}
		sb.WriteString(chunk.Content)
	}
	if sb.String() != "Hello World" {
		t.Errorf("content = %q, want Hello World", sb.String())
	}
}

func TestStreamAnthropic_NoAPIKey(t *testing.T) {
	c, err := NewClient(&Config{Provider: "anthropic", Model: "claude-3", BaseURL: "http://localhost"})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	_, err = c.GenerateStream(context.Background(), "prompt")
	if err == nil {
		t.Error("expected error for missing API key")
	}
}

func TestStreamAnthropic_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	c, err := NewClient(&Config{
		Provider: "anthropic",
		Model:    "claude-3",
		BaseURL:  server.URL,
		APIKey:   "bad-key",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, err = c.GenerateStream(context.Background(), "prompt")
	if err == nil {
		t.Error("expected error for HTTP 401")
	}
}

// ── FailoverClient getters ─────────────────────────────────────────────────

func TestFailoverClientGetters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "ok"}}},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer server.Close()

	cfg := &Config{Provider: "openai", Model: "test-model", BaseURL: server.URL, APIKey: "k"}
	fc, err := NewFailoverClient([]*Config{cfg}, 5*time.Second, 0, 0)
	if err != nil {
		t.Fatalf("NewFailoverClient: %v", err)
	}
	defer fc.Close()

	if !fc.IsEnabled() {
		t.Error("IsEnabled should be true")
	}
	if fc.GetProvider() != "openai" {
		t.Errorf("GetProvider = %q", fc.GetProvider())
	}
	if fc.GetModel() != "test-model" {
		t.Errorf("GetModel = %q", fc.GetModel())
	}
	if fc.Timeout() != 5*time.Second {
		t.Errorf("Timeout = %v", fc.Timeout())
	}
	clients := fc.Clients()
	if len(clients) != 1 {
		t.Errorf("Clients len = %d", len(clients))
	}
}

func TestFailoverClientEmptyClients(t *testing.T) {
	fc := &FailoverClient{}
	if fc.IsEnabled() {
		t.Error("IsEnabled on empty should be false")
	}
	if fc.GetProvider() != "" {
		t.Errorf("GetProvider on empty = %q", fc.GetProvider())
	}
	if fc.GetModel() != "" {
		t.Errorf("GetModel on empty = %q", fc.GetModel())
	}
}

func TestFailoverClientSetTracer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "ok"}}},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer server.Close()

	cfg := &Config{Provider: "openai", Model: "m", BaseURL: server.URL, APIKey: "k"}
	fc, err := NewFailoverClient([]*Config{cfg}, 5*time.Second, 0, 0)
	if err != nil {
		t.Fatalf("NewFailoverClient: %v", err)
	}
	defer fc.Close()
	fc.SetTracer(nil)
}

// ── FailoverClient.GenerateWithParams ──────────────────────────────────────

func TestFailoverClientGenerateWithParams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "failover-param-ok"}}},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer server.Close()

	cfg := &Config{Provider: "openai", Model: "m", BaseURL: server.URL, APIKey: "k"}
	fc, err := NewFailoverClient([]*Config{cfg}, 5*time.Second, 0, 0)
	if err != nil {
		t.Fatalf("NewFailoverClient: %v", err)
	}
	defer fc.Close()

	got, err := fc.GenerateWithParams(context.Background(), "hi", map[string]any{"temperature": 0.5})
	if err != nil {
		t.Fatalf("GenerateWithParams: %v", err)
	}
	if got != "failover-param-ok" {
		t.Errorf("got %q", got)
	}
}

// ── WithRateLimiter option ─────────────────────────────────────────────────

func TestWithRateLimiterOption(t *testing.T) {
	limiter := ares_ratelimit.NewTokenBucketLimiter(&ares_ratelimit.LimiterConfig{
		Rate:  10,
		Burst: 20,
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "ok"}}},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer server.Close()

	c, err := NewClient(&Config{Provider: "openai", Model: "m", BaseURL: server.URL, APIKey: "k"},
		WithRateLimiter(limiter))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	if c.limiter == nil {
		t.Error("limiter should be set")
	}
}

// ── extractOverrides / toFloat64 ───────────────────────────────────────────

func TestExtractOverridesAllFields(t *testing.T) {
	o := extractOverrides(map[string]any{"temperature": 0.8, "max_tokens": 200.0, "top_k": 30.0})
	if !o.hasTemp || o.temperature != 0.8 {
		t.Errorf("temperature = %v, hasTemp = %v", o.temperature, o.hasTemp)
	}
	if !o.hasMax || o.maxTokens != 200 {
		t.Errorf("maxTokens = %v, hasMax = %v", o.maxTokens, o.hasMax)
	}
	if !o.hasTopK || o.topK != 30 {
		t.Errorf("topK = %v, hasTopK = %v", o.topK, o.hasTopK)
	}
}

func TestExtractOverrides_NilAndEmpty(t *testing.T) {
	for _, params := range []map[string]any{nil, {}} {
		o := extractOverrides(params)
		if o.hasTemp || o.hasMax || o.hasTopK {
			t.Errorf("extractOverrides(%v) has flags set, want all false", params)
		}
	}
}

func TestExtractOverrides_WrongTypes(t *testing.T) {
	o := extractOverrides(map[string]any{"temperature": "hot", "max_tokens": "many"})
	if o.hasTemp || o.hasMax {
		t.Error("wrong types should not set hasTemp/hasMax")
	}
}

func TestExtractOverrides_IntTypes(t *testing.T) {
	o := extractOverrides(map[string]any{"temperature": 1, "max_tokens": 200, "top_k": 30})
	if !o.hasTemp || o.temperature != 1 {
		t.Errorf("temperature = %v, hasTemp = %v", o.temperature, o.hasTemp)
	}
	if !o.hasMax || o.maxTokens != 200 {
		t.Errorf("maxTokens = %v, hasMax = %v", o.maxTokens, o.hasMax)
	}
	if !o.hasTopK || o.topK != 30 {
		t.Errorf("topK = %v, hasTopK = %v", o.topK, o.hasTopK)
	}
}

func TestExtractOverrides_Partial(t *testing.T) {
	o := extractOverrides(map[string]any{"temperature": 0.5})
	if !o.hasTemp || o.temperature != 0.5 {
		t.Errorf("temperature = %v, hasTemp = %v", o.temperature, o.hasTemp)
	}
	if o.hasMax || o.hasTopK {
		t.Error("hasMax/hasTopK should be false for partial params")
	}
}

func TestToFloat64(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want float64
		ok   bool
	}{
		{"float64", 3.14, 3.14, true},
		{"float32", float32(2.5), 2.5, true},
		{"int", 42, 42, true},
		{"int64", int64(7), 7, true},
		{"string", "nope", 0, false},
		{"nil", nil, 0, false},
		{"bool", true, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := toFloat64(tt.v)
			if ok != tt.ok {
				t.Errorf("ok = %v, want %v", ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Errorf("got = %v, want %v", got, tt.want)
			}
		})
	}
}

// ── requestOverrides apply* helpers ────────────────────────────────────────

func TestRequestOverridesApply(t *testing.T) {
	o := requestOverrides{temperature: 0.9, maxTokens: 200, topK: 50, hasTemp: true, hasMax: true, hasTopK: true}
	if o.applyTemperature(0.7) != 0.9 {
		t.Errorf("applyTemperature = %v", o.applyTemperature(0.7))
	}
	if o.applyMaxTokens(100) != 200 {
		t.Errorf("applyMaxTokens = %v", o.applyMaxTokens(100))
	}
	if o.applyTopK(40) != 50 {
		t.Errorf("applyTopK = %v", o.applyTopK(40))
	}

	empty := requestOverrides{}
	if empty.applyTemperature(0.7) != 0.7 {
		t.Errorf("empty applyTemperature = %v", empty.applyTemperature(0.7))
	}
	if empty.applyMaxTokens(100) != 100 {
		t.Errorf("empty applyMaxTokens = %v", empty.applyMaxTokens(100))
	}
	if empty.applyTopK(40) != 40 {
		t.Errorf("empty applyTopK = %v", empty.applyTopK(40))
	}
}

// ── NewFailoverClient error paths ──────────────────────────────────────────

func TestNewFailoverClientNoConfigs(t *testing.T) {
	_, err := NewFailoverClient(nil, 5*time.Second, 0, 0)
	if err == nil {
		t.Error("expected error for empty configs")
	}
}

func TestNewFailoverClientRateLimiting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "ok"}}},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer server.Close()

	cfg := &Config{Provider: "openai", Model: "m", BaseURL: server.URL, APIKey: "k"}
	fc, err := NewFailoverClient([]*Config{cfg}, 5*time.Second, 10.0, 20, WithCooldownDuration(30*time.Second))
	if err != nil {
		t.Fatalf("NewFailoverClient: %v", err)
	}
	defer fc.Close()

	if fc.cooldownDuration != 30*time.Second {
		t.Errorf("cooldownDuration = %v", fc.cooldownDuration)
	}
}

// ── Anthropic Chat with usage ──────────────────────────────────────────────

func TestChatAnthropicUsagePassthrough(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "response text"},
			},
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 15, "output_tokens": 25},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer server.Close()

	c, err := NewClient(&Config{
		Provider: "anthropic",
		Model:    "claude-3",
		BaseURL:  server.URL,
		APIKey:   "test-key",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	msgs := []*llmcore.LLMMessage{{Role: "user", Content: "hi"}}
	resp, err := c.Chat(context.Background(), msgs, nil, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "response text" {
		t.Errorf("Content = %q", resp.Content)
	}
	if resp.Usage.PromptTokens != 15 || resp.Usage.CompletionTokens != 25 {
		t.Errorf("Usage = %+v", resp.Usage)
	}
	if resp.Usage.TotalTokens != 40 {
		t.Errorf("TotalTokens = %d, want 40", resp.Usage.TotalTokens)
	}
}
