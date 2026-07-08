package postgres

import (
	"math"
	"testing"
)

func TestFormatVector(t *testing.T) {
	tests := []struct {
		name     string
		vector   []float64
		expected string
	}{
		{
			name:     "empty vector",
			vector:   []float64{},
			expected: "[]",
		},
		{
			name:     "single element",
			vector:   []float64{1.234567},
			expected: "[1.234567]",
		},
		{
			name:     "multiple elements",
			vector:   []float64{1.234567, -2.345678, 3.456789},
			expected: "[1.234567,-2.345678,3.456789]",
		},
		{
			name:     "zero values",
			vector:   []float64{0.0, 0.0},
			expected: "[0,0]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatVector(tt.vector)
			if result != tt.expected {
				t.Errorf("FormatVector() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestNormalizeVector(t *testing.T) {
	tests := []struct {
		name      string
		vector    []float64
		expected  []float64
		tolerance float64
	}{
		{
			name:      "empty vector",
			vector:    []float64{},
			expected:  []float64{},
			tolerance: 1e-10,
		},
		{
			name:      "zero vector",
			vector:    []float64{0, 0, 0},
			expected:  []float64{0, 0, 0},
			tolerance: 1e-10,
		},
		{
			name:      "unit vector",
			vector:    []float64{1, 0, 0},
			expected:  []float64{1, 0, 0},
			tolerance: 1e-10,
		},
		{
			name:      "normalized vector",
			vector:    []float64{3, 4},
			expected:  []float64{0.6, 0.8},
			tolerance: 1e-10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeVector(tt.vector)
			if len(result) != len(tt.expected) {
				t.Errorf("NormalizeVector() length = %v, want %v", len(result), len(tt.expected))
				return
			}
			for i := range result {
				if math.Abs(result[i]-tt.expected[i]) > tt.tolerance {
					t.Errorf("NormalizeVector()[%d] = %v, want %v", i, result[i], tt.expected[i])
				}
			}
		})
	}
}
