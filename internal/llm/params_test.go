package llm

import "testing"

func TestExtractOverrides_Empty(t *testing.T) {
	o := extractOverrides(nil)
	if o.hasTemp || o.hasMax || o.hasTopK {
		t.Error("nil params should yield no overrides")
	}
	o2 := extractOverrides(map[string]any{})
	if o2.hasTemp || o2.hasMax || o2.hasTopK {
		t.Error("empty params should yield no overrides")
	}
}

func TestExtractOverrides_Parses(t *testing.T) {
	p := map[string]any{
		"temperature": 0.3,
		"max_tokens":  float64(512),
		"top_k":       int(40),
	}
	o := extractOverrides(p)
	if !o.hasTemp || o.temperature != 0.3 {
		t.Errorf("temperature not parsed: %+v", o)
	}
	if !o.hasMax || o.maxTokens != 512 {
		t.Errorf("max_tokens not parsed: %+v", o)
	}
	if !o.hasTopK || o.topK != 40 {
		t.Errorf("top_k not parsed: %+v", o)
	}
}

func TestExtractOverrides_TypeCoercion(t *testing.T) {
	p := map[string]any{
		"temperature": int(1),
		"max_tokens":  int64(1024),
		"top_k":       float32(8),
	}
	o := extractOverrides(p)
	if !o.hasTemp || o.temperature != 1 {
		t.Errorf("int temperature: %+v", o)
	}
	if !o.hasMax || o.maxTokens != 1024 {
		t.Errorf("int64 max_tokens: %+v", o)
	}
	if !o.hasTopK || o.topK != 8 {
		t.Errorf("float32 top_k: %+v", o)
	}
}

func TestExtractOverrides_IgnoresModel(t *testing.T) {
	p := map[string]any{
		"model":       "gpt-4",
		"temperature": 0.9,
	}
	o := extractOverrides(p)
	if !o.hasTemp || o.temperature != 0.9 {
		t.Errorf("temperature: %+v", o)
	}
	// The model field must never produce an override flag.
	if o.hasMax || o.hasTopK {
		t.Error("model should be ignored, but an override flag was set")
	}
}

func TestApplyOverrides_Precedence(t *testing.T) {
	o := extractOverrides(map[string]any{"temperature": 0.2})
	if got := o.applyTemperature(0.7); got != 0.2 {
		t.Errorf("applyTemperature override = %v, want 0.2", got)
	}
	if got := o.applyMaxTokens(2048); got != 2048 {
		t.Errorf("applyMaxTokens default = %v, want 2048 (no override)", got)
	}
	empty := extractOverrides(nil)
	if got := empty.applyTemperature(0.7); got != 0.7 {
		t.Errorf("applyTemperature default = %v, want 0.7", got)
	}
}
