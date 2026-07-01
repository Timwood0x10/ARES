package evolution

import (
	"encoding/json"
	"math"
	"testing"
)

func TestGetParamFloat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		params     map[string]any
		key        string
		expected   float64
		expectedOK bool
	}{
		{
			name:       "float64_value",
			params:     map[string]any{"top_k": 40.0},
			key:        "top_k",
			expected:   40.0,
			expectedOK: true,
		},
		{
			name:       "int_value",
			params:     map[string]any{"top_k": 40},
			key:        "top_k",
			expected:   40.0,
			expectedOK: true,
		},
		{
			name:       "int64_value",
			params:     map[string]any{"top_k": int64(40)},
			key:        "top_k",
			expected:   40.0,
			expectedOK: true,
		},
		{
			name:       "int32_value",
			params:     map[string]any{"top_k": int32(40)},
			key:        "top_k",
			expected:   40.0,
			expectedOK: true,
		},
		{
			name:       "float32_value",
			params:     map[string]any{"temperature": float32(0.7)},
			key:        "temperature",
			expected:   0.7,
			expectedOK: true,
		},
		{
			name:       "json_number_value",
			params:     map[string]any{"top_k": json.Number("40")},
			key:        "top_k",
			expected:   40.0,
			expectedOK: true,
		},
		{
			name:       "missing_key",
			params:     map[string]any{"temperature": 0.7},
			key:        "top_k",
			expected:   0.0,
			expectedOK: false,
		},
		{
			name:       "nil_params",
			params:     nil,
			key:        "top_k",
			expected:   0.0,
			expectedOK: false,
		},
		{
			name:       "string_value_not_found",
			params:     map[string]any{"top_k": "40"},
			key:        "top_k",
			expected:   0.0,
			expectedOK: false,
		},
		{
			name:       "invalid_json_number_not_found",
			params:     map[string]any{"top_k": json.Number("invalid")},
			key:        "top_k",
			expected:   0.0,
			expectedOK: false,
		},
		{
			name:       "zero_value_found",
			params:     map[string]any{"temperature": 0.0},
			key:        "temperature",
			expected:   0.0,
			expectedOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := getParamFloat(tt.params, tt.key)
			if ok != tt.expectedOK {
				t.Errorf("getParamFloat(%v, %q) ok = %v, want %v", tt.params, tt.key, ok, tt.expectedOK)
			}
			// Use approximate comparison for float32 values due to precision loss.
			if math.Abs(got-tt.expected) > 1e-6 {
				t.Errorf("getParamFloat(%v, %q) = %v, want %v", tt.params, tt.key, got, tt.expected)
			}
		})
	}
}

func TestDeterministicScore_NumericTypeHandling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		strategy *Strategy
		expected float64
	}{
		{
			name: "top_k_as_float64",
			strategy: &Strategy{
				Params:         map[string]any{"temperature": 0.7, "top_k": 40.0},
				PromptTemplate: "helpful",
			},
			// Base: 50, temp: (1-0.7)*25=7.5, top_k penalty: (40-30)^2/10=10
			// Total: 50 + 7.5 - 10 = 47.5
			expected: 47.5,
		},
		{
			name: "top_k_as_int",
			strategy: &Strategy{
				Params:         map[string]any{"temperature": 0.7, "top_k": 40},
				PromptTemplate: "helpful",
			},
			// Should now correctly apply penalty for int type
			expected: 47.5,
		},
		{
			name: "top_k_as_int64",
			strategy: &Strategy{
				Params:         map[string]any{"temperature": 0.7, "top_k": int64(40)},
				PromptTemplate: "helpful",
			},
			expected: 47.5,
		},
		{
			name: "temperature_as_float32",
			strategy: &Strategy{
				Params:         map[string]any{"temperature": float32(0.7), "top_k": 40.0},
				PromptTemplate: "helpful",
			},
			// Float32(0.7) ~= 0.699999988079071, so (1-temp)*25 ~= 7.500000298023224
			// Use approximate comparison
			expected: 47.5,
		},
		{
			name: "optimal_top_k_30",
			strategy: &Strategy{
				Params:         map[string]any{"temperature": 0.0, "top_k": 30},
				PromptTemplate: "precise",
			},
			// Base: 50, temp: (1-0)*25=25, top_k penalty: 0, prompt: +15
			// Total: 50 + 25 + 0 + 15 = 90
			expected: 90.0,
		},
		{
			name: "high_top_k_penalty",
			strategy: &Strategy{
				Params:         map[string]any{"temperature": 0.0, "top_k": 80},
				PromptTemplate: "helpful",
			},
			// Base: 50, temp: +25, top_k penalty: (80-30)^2/10=2500/10=250
			// Total: 50 + 25 - 250 = -175 (clamped to min 5.0)
			expected: 5.0,
		},
		{
			name: "nil_strategy",
			strategy: nil,
			expected: 50.0,
		},
		{
			name: "missing_temperature",
			strategy: &Strategy{
				Params:         map[string]any{"top_k": 30},
				PromptTemplate: "precise",
			},
			// Base: 50, temp: not found (skip), top_k penalty: 0, prompt: +15
			// Total: 50 + 0 + 0 + 15 = 65
			expected: 65.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := DeterministicScore(tt.strategy)
			// Use approximate comparison for float32 precision issues.
			if math.Abs(got-tt.expected) > 1e-6 {
				t.Errorf("DeterministicScore() = %v, want %v", got, tt.expected)
			}
		})
	}
}