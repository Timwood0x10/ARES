package llmsvcapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	llmcore "github.com/Timwood0x10/ares/internal/llmcore"
)

// ── Config.toInternal ───────────────────────────────────────────────────────

func TestToInternal_BasicConfig(t *testing.T) {
	c := &Config{
		LLMConfig: &llmcore.LLMConfig{
			Provider: llmcore.LLMProviderOpenAI,
			Model:    "gpt-4",
			BaseURL:  "https://api.openai.com/v1",
			APIKey:   "sk-test",
		},
	}
	internal := c.toInternal()
	if internal == nil {
		t.Fatal("toInternal returned nil")
	}
	if internal.LLMConfig == nil {
		t.Fatal("LLMConfig is nil")
	}
	if internal.LLMConfig.Model != "gpt-4" {
		t.Errorf("Model = %q", internal.LLMConfig.Model)
	}
}

func TestToInternal_SkipsNilFallbacks(t *testing.T) {
	c := &Config{
		LLMConfig: &llmcore.LLMConfig{
			Provider: llmcore.LLMProviderOpenAI,
			Model:    "gpt-4",
			APIKey:   "sk-test",
		},
		Fallbacks: []*llmcore.LLMConfig{
			nil,
			{Provider: llmcore.LLMProviderOllama, Model: "llama3"},
			nil,
		},
	}
	internal := c.toInternal()
	if internal == nil {
		t.Fatal("toInternal returned nil")
	}
	if len(internal.Fallbacks) != 1 {
		t.Errorf("Fallbacks len = %d, want 1 (nil entries skipped)", len(internal.Fallbacks))
	}
}

// ── Service methods via httptest (using existing newTestService helper) ─────

func TestService_Getters_Extended(t *testing.T) {
	svc, _ := newTestService(t)
	if !svc.IsEnabled() {
		t.Error("IsEnabled should be true")
	}
	if svc.GetConfig() == nil {
		t.Error("GetConfig returned nil")
	}
}

func TestService_GenerateSimple_Response(t *testing.T) {
	svc, _ := newTestService(t)
	got, err := svc.GenerateSimple(context.Background(), "hi")
	if err != nil {
		t.Fatalf("GenerateSimple: %v", err)
	}
	if got == "" {
		t.Error("GenerateSimple returned empty string")
	}
}

func TestService_Generate_Response(t *testing.T) {
	svc, _ := newTestService(t)
	req := &llmcore.GenerateRequest{
		Messages: []*llmcore.LLMMessage{{Role: "user", Content: "hello"}},
	}
	resp, err := svc.Generate(context.Background(), req)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp == nil {
		t.Fatal("Generate returned nil response")
	}
}

func TestService_GenerateEmbedding_NoClient(t *testing.T) {
	svc, _ := newTestService(t)
	req := &llmcore.EmbeddingRequest{Input: "test"}
	_, err := svc.GenerateEmbedding(context.Background(), req)
	if err == nil {
		t.Error("expected error when no embedding client is configured")
	}
}

// ── Config.toInternal with fallbacks (extended) ────────────────────────────

func TestToInternal_FallbackFieldMapping(t *testing.T) {
	c := &Config{
		LLMConfig: &llmcore.LLMConfig{
			Provider: llmcore.LLMProviderOpenAI,
			Model:    "primary",
			APIKey:   "key-1",
		},
		Fallbacks: []*llmcore.LLMConfig{
			{
				Provider:        llmcore.LLMProviderOllama,
				Model:           "llama3",
				BaseURL:         "http://localhost:11434",
				APIKey:          "key-2",
				Timeout:         30,
				MaxTokens:       2048,
				MaxPromptLength: 8192,
			},
		},
	}
	internal := c.toInternal()
	if len(internal.Fallbacks) != 1 {
		t.Fatalf("Fallbacks len = %d", len(internal.Fallbacks))
	}
	fb := internal.Fallbacks[0]
	if fb.Model != "llama3" {
		t.Errorf("fallback Model = %q", fb.Model)
	}
	if fb.BaseURL != "http://localhost:11434" {
		t.Errorf("fallback BaseURL = %q", fb.BaseURL)
	}
	if fb.MaxTokens != 2048 {
		t.Errorf("fallback MaxTokens = %d", fb.MaxTokens)
	}
	if fb.MaxPromptLength != 8192 {
		t.Errorf("fallback MaxPromptLength = %d", fb.MaxPromptLength)
	}
}

// ── httptest server validates request path ──────────────────────────────────

func TestLLMServer_ReceivesChatCompletionsPath(t *testing.T) {
	var capturedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "ok"}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := &Config{
		LLMConfig: &llmcore.LLMConfig{
			Provider: llmcore.LLMProviderOpenAI,
			Model:    "m",
			BaseURL:  server.URL,
			APIKey:   "k",
		},
	}
	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	defer svc.Close()

	_, _ = svc.GenerateSimple(context.Background(), "hi")
	if capturedPath != "/chat/completions" {
		t.Errorf("path = %q, want /chat/completions", capturedPath)
	}
}
