package postgres

import (
	"strings"
	"testing"
)

// TestVectorArgEmptyBindsNULL locks the empty-embedding rule for `::vector`
// columns.
//
// Regression (docs/reviews/0.3.1-final-deep-review.md finding S-3): carriers
// that build `SET embedding = $n::vector` passed FormatVector directly, which
// returns the zero-dimension literal "[]" for an empty slice. pgvector rejects
// '[]' for a VECTOR(n) column, so updating a row whose embedding had not been
// backfilled yet failed with a dimension error — while Create wrote NULL for
// exactly the same input.
func TestVectorArgEmptyBindsNULL(t *testing.T) {
	tests := []struct {
		name      string
		embedding []float64
	}{
		{name: "nil", embedding: nil},
		{name: "empty_slice", embedding: []float64{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := VectorArg(tc.embedding); got != nil {
				t.Fatalf("VectorArg(%v) = %v, want nil (SQL NULL)", tc.embedding, got)
			}
		})
	}
}

// TestVectorArgNonEmptyFormats verifies a real embedding is still rendered in
// pgvector literal form, so the NULL rule does not leak into the normal path.
func TestVectorArgNonEmptyFormats(t *testing.T) {
	got := VectorArg([]float64{1, 2.5})
	s, ok := got.(string)
	if !ok {
		t.Fatalf("VectorArg(non-empty) = %T, want string", got)
	}
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		t.Fatalf("VectorArg(non-empty) = %q, want a bracketed vector literal", s)
	}
	if strings.Contains(s, "[]") {
		t.Fatalf("VectorArg(non-empty) = %q, must not be the zero-dimension literal", s)
	}
}
