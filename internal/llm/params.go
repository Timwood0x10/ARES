package llm

import (
	"encoding/json"
)

// defaultOllamaTopK is Ollama's own default top_k. Emitting it when no
// override is present preserves existing behavior (the field is simply unset).
const defaultOllamaTopK = 40

// requestOverrides holds per-call LLM parameter overrides extracted from
// an evolution strategy's Params map. Only safe, non-routing fields are
// honored (temperature, max_tokens, top_k). The model field is
// intentionally ignored to avoid routing requests to an unexpected model.
type requestOverrides struct {
	temperature float64
	maxTokens   int
	topK        int
	hasTemp     bool
	hasMax      bool
	hasTopK     bool
}

// extractOverrides reads known numeric keys from a params map.
// A nil or empty map returns a zero-value struct (no overrides),
// so callers can pass params unconditionally.
//
// Args:
//
//	p - the strategy parameter map (may be nil).
//
// Returns:
//
//	requestOverrides - extracted overrides (all flags false when empty).
func extractOverrides(p map[string]any) requestOverrides {
	var o requestOverrides
	if p == nil {
		return o
	}
	if v, ok := p["temperature"]; ok {
		if f, ok := toFloat64(v); ok {
			o.temperature, o.hasTemp = f, true
		}
	}
	if v, ok := p["max_tokens"]; ok {
		if f, ok := toFloat64(v); ok {
			o.maxTokens, o.hasMax = int(f), true
		}
	}
	if v, ok := p["top_k"]; ok {
		if f, ok := toFloat64(v); ok {
			o.topK, o.hasTopK = int(f), true
		}
	}
	return o
}

// toFloat64 coerces common numeric types held in a params map.
// Returns (0, false) for unsupported types.
func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		if err == nil {
			return f, true
		}
	}
	return 0, false
}

// applyTemperature returns the override temperature when present, else the default.
func (o requestOverrides) applyTemperature(defaultTemp float64) float64 {
	if o.hasTemp {
		return o.temperature
	}
	return defaultTemp
}

// applyMaxTokens returns the override max_tokens when present, else the default.
func (o requestOverrides) applyMaxTokens(defaultTokens int) int {
	if o.hasMax {
		return o.maxTokens
	}
	return defaultTokens
}

// applyTopK returns the override top_k when present, else the default.
func (o requestOverrides) applyTopK(defaultTopK int) int {
	if o.hasTopK {
		return o.topK
	}
	return defaultTopK
}
