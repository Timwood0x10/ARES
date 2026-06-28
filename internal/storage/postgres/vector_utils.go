// Package postgres provides utility functions for vector operations.
package postgres

import (
	"math"
	"strconv"
	"strings"

	"github.com/Timwood0x10/ares/internal/errors"
)

// FormatVector converts []float64 to pgvector format string.
// This properly formats the embedding array to avoid double brackets.
// Uses %.6f format to limit decimal places to 6 for compact representation.
func FormatVector(embedding []float64) string {
	if len(embedding) == 0 {
		return "[]"
	}

	// Pre-allocate capacity: ~8 chars per number + commas + brackets
	var builder strings.Builder
	builder.Grow(len(embedding)*8 + 2)
	builder.WriteString("[")

	for i, v := range embedding {
		if i > 0 {
			builder.WriteString(",")
		}
		// Optimization: Use strconv directly instead of fmt.Fprintf for better performance
		builder.WriteString(strconv.FormatFloat(v, 'f', 6, 64))
	}

	builder.WriteString("]")
	return builder.String()
}

// NormalizeVector normalizes a vector to unit length.
// This is required for pgvector's cosine distance operator (<=>).
func NormalizeVector(embedding []float64) []float64 {
	if len(embedding) == 0 {
		return embedding
	}

	// Calculate the norm (length) of the vector
	var norm float64
	for _, v := range embedding {
		norm += v * v
	}
	norm = math.Sqrt(norm)

	// Avoid division by zero
	if norm == 0 {
		return embedding
	}

	// Normalize each component
	normalized := make([]float64, len(embedding))
	for i, v := range embedding {
		normalized[i] = v / norm
	}

	return normalized
}

// ParseVectorString converts pgvector string format to []float64.
func ParseVectorString(vecStr string) ([]float64, error) {
	if len(vecStr) == 0 {
		return []float64{}, nil
	}

	vecStr = strings.Trim(vecStr, "[]")
	if vecStr == "" {
		return []float64{}, nil
	}

	parts := strings.Split(vecStr, ",")
	result := make([]float64, len(parts))
	for i, part := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil {
			return nil, errors.Wrap(err, "failed to parse vector component")
		}
		result[i] = v
	}

	return result, nil
}
