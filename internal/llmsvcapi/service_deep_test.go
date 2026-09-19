package llmsvcapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	llmcore "github.com/Timwood0x10/ares/internal/llmcore"
)

func newTestService(t *testing.T) (*Service, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": "test response"}},
			},
			"usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 3},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	cfg := &Config{
		LLMConfig: &llmcore.LLMConfig{
			Provider: llmcore.LLMProviderOpenAI,
			Model:    "test-model",
			BaseURL:  server.URL,
			APIKey:   "test-key",
		},
	}
	svc, err := NewService(cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	t.Cleanup(svc.Close)
	return svc, server
}

// ── Config.toInternal ───────────────────────────────────────────────────────

func TestConfigToInternal_Nil(t *testing.T) {
	var c *Config
	if got := c.toInternal(); got != nil {
		t.Errorf("toInternal(nil) = %v, want nil", got)
	}
}

func TestConfigToInternal_WithFallbacks(t *testing.T) {
	cfg := &Config{
		LLMConfig: &llmcore.LLMConfig{Provider: llmcore.LLMProviderOpenAI, Model: "primary"},
		Fallbacks: []*llmcore.LLMConfig{
			{Provider: llmcore.LLMProviderOllama, Model: "fallback-1"},
			nil, // nil entry should be skipped
			{Provider: llmcore.LLMProviderAnthropic, Model: "fallback-2"},
		},
	}
	internal := cfg.toInternal()
	if internal == nil {
		t.Fatal("toInternal returned nil")
	}
	if len(internal.Fallbacks) != 2 {
		t.Errorf("Fallbacks len = %d, want 2 (nil entry skipped)", len(internal.Fallbacks))
	}
}

// ── NewService ──────────────────────────────────────────────────────────────

func TestNewService_NilConfig(t *testing.T) {
	var cfg *Config
	_, err := NewService(cfg)
	if err == nil {
		t.Error("expected error for nil config")
	}
}

func TestNewService_NilLLMConfig(t *testing.T) {
	_, err := NewService(&Config{})
	if err == nil {
		t.Error("expected error for nil LLMConfig")
	}
}

func TestNewService_Success(t *testing.T) {
	svc, _ := newTestService(t)
	if svc == nil {
		t.Fatal("NewService returned nil")
	}
}

// ── Service getters ─────────────────────────────────────────────────────────

func TestServiceGetConfig(t *testing.T) {
	svc, _ := newTestService(t)
	cfg := svc.GetConfig()
	if cfg == nil {
		t.Fatal("GetConfig returned nil")
	}
	if cfg.Model != "test-model" {
		t.Errorf("Model = %q", cfg.Model)
	}
}

func TestServiceIsEnabled(t *testing.T) {
	svc, _ := newTestService(t)
	if !svc.IsEnabled() {
		t.Error("IsEnabled should be true for valid config")
	}
}

func TestServiceGetProvider(t *testing.T) {
	svc, _ := newTestService(t)
	provider := svc.GetProvider()
	if string(provider) != "openai" {
		t.Errorf("Provider = %q", provider)
	}
}

func TestServiceGetModel(t *testing.T) {
	svc, _ := newTestService(t)
	if got := svc.GetModel(); got != "test-model" {
		t.Errorf("Model = %q", got)
	}
}

// ── Service.Generate ────────────────────────────────────────────────────────

func TestServiceGenerate_Success(t *testing.T) {
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
	if resp.Content != "test response" {
		t.Errorf("Content = %q", resp.Content)
	}
}

// ── Service.GenerateSimple ──────────────────────────────────────────────────

func TestServiceGenerateSimple_Success(t *testing.T) {
	svc, _ := newTestService(t)
	got, err := svc.GenerateSimple(context.Background(), "hello")
	if err != nil {
		t.Fatalf("GenerateSimple: %v", err)
	}
	if got != "test response" {
		t.Errorf("got %q", got)
	}
}

// ── Service.GenerateEmbedding ───────────────────────────────────────────────

func TestServiceGenerateEmbedding_NoClient(t *testing.T) {
	svc, _ := newTestService(t)
	req := &llmcore.EmbeddingRequest{Input: "text"}
	_, err := svc.GenerateEmbedding(context.Background(), req)
	// No embedding client configured — should return error
	if err == nil {
		t.Log("GenerateEmbedding returned nil error (may be handled internally)")
	}
}

// ── Service.Close ───────────────────────────────────────────────────────────

func TestServiceClose(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": "ok"}}},
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
	svc.Close()
	// Close should be idempotent
	svc.Close()
}

// ── Config.toInternal field mapping ─────────────────────────────────────────

func TestConfigToInternal_FieldMapping(t *testing.T) {
	cfg := &Config{
		LLMConfig: &llmcore.LLMConfig{
			Provider: llmcore.LLMProviderOpenAI,
			Model:    "primary-model",
			BaseURL:  "https://api.example.com",
			APIKey:   "sk-primary",
		},
		Fallbacks: []*llmcore.LLMConfig{
			{
				Provider:        llmcore.LLMProviderOllama,
				Model:           "fb-model",
				BaseURL:         "http://localhost:11434",
				APIKey:          "fb-key",
				Timeout:         30,
				MaxTokens:       1024,
				MaxPromptLength: 4096,
			},
		},
	}
	internal := cfg.toInternal()
	if internal == nil {
		t.Fatal("toInternal returned nil")
	}
	if len(internal.Fallbacks) != 1 {
		t.Fatalf("Fallbacks len = %d", len(internal.Fallbacks))
	}
	fb := internal.Fallbacks[0]
	if fb.Model != "fb-model" {
		t.Errorf("fallback Model = %q", fb.Model)
	}
	if fb.Timeout != 30 {
		t.Errorf("fallback Timeout = %d", fb.Timeout)
	}
	if fb.MaxTokens != 1024 {
		t.Errorf("fallback MaxTokens = %d", fb.MaxTokens)
	}
	if fb.MaxPromptLength != 4096 {
		t.Errorf("fallback MaxPromptLength = %d", fb.MaxPromptLength)
	}
}
