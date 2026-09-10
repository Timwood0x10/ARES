package llmsvcapi

import (
	"testing"

	llmcore "github.com/Timwood0x10/ares/internal/llmcore"
)

// TestConfigToInternalSkipsNilFallbacks is the #46 regression: a sparse
// Fallbacks slice (nil entries, e.g. from JSON `[null, {...}]`) used to nil-
// panic in toInternal's conversion loop. Nil entries must be skipped.
func TestConfigToInternalSkipsNilFallbacks(t *testing.T) {
	cfg := &Config{
		Fallbacks: []*llmcore.LLMConfig{
			nil,
			{Provider: "openai", Model: "gpt-x"},
			nil,
			{Provider: "anthropic", Model: "claude-x"},
		},
	}

	internal := cfg.toInternal()
	if internal == nil {
		t.Fatal("toInternal returned nil for non-nil Config")
	}
	if len(internal.Fallbacks) != 2 {
		t.Fatalf("len(Fallbacks) = %d, want 2 (nil entries skipped)", len(internal.Fallbacks))
	}
	if internal.Fallbacks[0].Model != "gpt-x" || internal.Fallbacks[1].Model != "claude-x" {
		t.Errorf("fallback order not preserved: %+v", internal.Fallbacks)
	}
}

// TestConfigToInternalNilReceiver guards the nil-receiver path.
func TestConfigToInternalNilReceiver(t *testing.T) {
	var cfg *Config
	if internal := cfg.toInternal(); internal != nil {
		t.Errorf("toInternal(nil) = %+v, want nil", internal)
	}
}

// TestConfigToInternalAllNilFallbacks: an all-nil slice yields no fallbacks,
// not a panic.
func TestConfigToInternalAllNilFallbacks(t *testing.T) {
	cfg := &Config{Fallbacks: []*llmcore.LLMConfig{nil, nil}}
	internal := cfg.toInternal()
	if len(internal.Fallbacks) != 0 {
		t.Errorf("len(Fallbacks) = %d, want 0", len(internal.Fallbacks))
	}
}
