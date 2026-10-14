package ares_events

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAnswerQualityPenalty_Contract pins the numbers two scorers share: the
// runtime observer's fitness factor and the eval judge's deterministic floor.
// They are asserted HERE, once, because the failure mode of a second copy is
// silence — the two sides would simply stop agreeing, with nothing failing.
func TestAnswerQualityPenalty_Contract(t *testing.T) {
	tests := []struct {
		name       string
		bodySource string
		truncated  bool
		wantFactor float64
		wantDegrad bool
	}{
		{
			name:       "gap produced nothing, so it scores nothing",
			bodySource: AnswerBodySourceGap,
			wantFactor: 0,
			wantDegrad: true,
		},
		{
			name:       "gap dominates the truncated tier",
			bodySource: AnswerBodySourceGap, truncated: true,
			wantFactor: 0,
			wantDegrad: true,
		},
		{
			name:       "depth-capped: something real came out under duress",
			truncated:  true,
			wantFactor: 0.5,
			wantDegrad: true,
		},
		{
			name:       "a supplied answer is not penalised",
			bodySource: AnswerBodySourceSupplied,
			wantFactor: 1,
		},
		{
			name:       "synthesised bodies are real answers too",
			bodySource: AnswerBodySourceSynthesized,
			wantFactor: 1,
		},
		{
			name:       "absent signals keep 1 — a non-answer task or a pre-fix envelope",
			wantFactor: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			factor, degraded := AnswerQualityPenalty(tt.bodySource, tt.truncated)
			assert.InDelta(t, tt.wantFactor, factor, 1e-9)
			assert.Equal(t, tt.wantDegrad, degraded)
		})
	}
}
