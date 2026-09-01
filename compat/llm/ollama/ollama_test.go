package ollama

import (
	"strings"
	"testing"

	"github.com/Timwood0x10/ares/compat/llm"
)

// TestNewConfigParsing covers the config-map contract of the official Ollama
// adapter. This is the surface `compat.RegisterLLM("ollama", …)` hands to
// bootstrap (internal/ares_bootstrap/provide_llm.go), and a config map is
// untyped by construction — a wrong key or a wrong value type silently yields
// the zero value, so the parsing rules are the contract that needs pinning.
//
// No network calls: New only builds an internal/llm.Client, it does not dial.
func TestNewConfigParsing(t *testing.T) {
	tests := []struct {
		name        string
		config      map[string]any
		wantErr     string // substring; empty means success expected
		wantEnabled bool
	}{
		{
			name:    "missing_model_is_rejected",
			config:  map[string]any{"base_url": "http://localhost:11434"},
			wantErr: "model is required",
		},
		{
			name:    "nil_config_is_rejected_for_the_same_reason",
			config:  nil,
			wantErr: "model is required",
		},
		{
			name:    "wrong_type_model_reads_as_absent",
			config:  map[string]any{"model": 42},
			wantErr: "model is required",
		},
		{
			name:        "model_only_uses_the_default_endpoint",
			config:      map[string]any{"model": "llama3:8b"},
			wantEnabled: true,
		},
		{
			name: "explicit_base_url_is_honored",
			config: map[string]any{
				"model":    "llama3:8b",
				"base_url": "http://ollama.internal:11434",
			},
			wantEnabled: true,
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
			if got := a.GetProvider(); got != "ollama" {
				t.Fatalf("GetProvider() = %q, want %q", got, "ollama")
			}
			if got := a.IsEnabled(); got != tt.wantEnabled {
				t.Fatalf("IsEnabled() = %v, want %v", got, tt.wantEnabled)
			}
		})
	}
}

// TestAdapterSatisfiesLLMProvider is redundant with the compile-time assertion
// in ollama.go but states the intent for readers: this adapter exists solely to
// satisfy the compat contract.
func TestAdapterSatisfiesLLMProvider(t *testing.T) {
	a, err := New(map[string]any{"model": "llama3:8b"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var _ llm.LLMProvider = a
}
