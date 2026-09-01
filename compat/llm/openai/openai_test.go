package openai

import (
	"strings"
	"testing"

	"github.com/Timwood0x10/ares/compat/llm"
)

// TestNewConfigParsing covers the config-map contract of the official
// OpenAI-compatible adapter, which bootstrap registers as the fallback for every
// non-Ollama provider (internal/ares_bootstrap/provide_llm.go). A config map is
// untyped, so a wrong key or value type silently degrades to the zero value —
// these rules are the contract worth pinning.
//
// No network calls: New only builds an internal/llm.Client.
func TestNewConfigParsing(t *testing.T) {
	tests := []struct {
		name         string
		config       map[string]any
		wantErr      string // substring; empty means success expected
		wantProvider string
	}{
		{
			name:    "missing_api_key_is_rejected",
			config:  map[string]any{"model": "gpt-4o-mini"},
			wantErr: "api_key is required",
		},
		{
			name:    "nil_config_is_rejected_for_the_same_reason",
			config:  nil,
			wantErr: "api_key is required",
		},
		{
			name:    "wrong_type_api_key_reads_as_absent",
			config:  map[string]any{"api_key": 12345},
			wantErr: "api_key is required",
		},
		{
			name:         "api_key_only_defaults_model_and_provider",
			config:       map[string]any{"api_key": "sk-test"},
			wantProvider: "openai",
		},
		{
			name: "provider_override_is_honored",
			config: map[string]any{
				"api_key":  "sk-test",
				"provider": "openrouter",
				"base_url": "https://openrouter.ai/api/v1",
				"model":    "anthropic/claude-3.5-sonnet",
			},
			wantProvider: "openrouter",
		},
		{
			// A non-openai provider has no canonical endpoint, so guessing one
			// would ship the caller's key to the wrong host. NewClient rejects
			// it; this pins that we do NOT paper over it with a default.
			name: "non_openai_provider_without_base_url_is_rejected",
			config: map[string]any{
				"api_key":  "sk-test",
				"provider": "openrouter",
			},
			wantErr: "base_url is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := New(tt.config)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("New(%v) succeeded, want error containing %q", tt.config, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("New error = %q, want it to mention %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("New(%v): %v", tt.config, err)
			}
			if a == nil {
				t.Fatal("New returned a nil adapter without an error")
			}
			if got := a.GetProvider(); got != tt.wantProvider {
				t.Fatalf("GetProvider() = %q, want %q", got, tt.wantProvider)
			}
			if !a.IsEnabled() {
				t.Fatal("an adapter built with an api_key must report enabled")
			}
		})
	}
}

// TestAdapterSatisfiesLLMProvider states the adapter's only reason to exist.
func TestAdapterSatisfiesLLMProvider(t *testing.T) {
	a, err := New(map[string]any{"api_key": "sk-test"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var _ llm.LLMProvider = a
}
