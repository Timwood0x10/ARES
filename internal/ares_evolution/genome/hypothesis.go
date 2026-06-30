package genome

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
)

// MutationHypothesis represents a testable hypothesis about what mutation
// would improve strategy performance. It bridges LLM reflection into
// concrete mutation guidance for the genetic algorithm.
type MutationHypothesis struct {
	// TargetType is the type of mutation target: "param", "prompt", "tool".
	TargetType string `json:"target_type"`

	// TargetKey is the specific parameter name or "prompt_template" or tool name.
	TargetKey string `json:"target_key"`

	// SuggestedValue is the recommended new value (nil = remove/randomize).
	SuggestedValue any `json:"suggested_value,omitempty"`

	// Direction indicates the change direction: "increase", "decrease", "swap", "restructure".
	Direction string `json:"direction"`

	// Rationale explains why this change is expected to help.
	Rationale string `json:"rationale"`

	// Confidence in this hypothesis [0, 1].
	Confidence float64 `json:"confidence"`

	// SourceReflectionID tracks which reflection generated this hypothesis.
	SourceReflectionID string `json:"source_reflection_id,omitempty"`
}

// HypothesisGenerator converts reflections into testable mutation hypotheses.
type HypothesisGenerator struct {
	minConfidence float64
}

// NewHypothesisGenerator creates a hypothesis generator with the given minimum
// confidence threshold. Hypotheses below this threshold are filtered out.
func NewHypothesisGenerator(minConfidence float64) *HypothesisGenerator {
	if minConfidence <= 0 {
		minConfidence = 0.3
	}
	return &HypothesisGenerator{minConfidence: minConfidence}
}

// Generate converts reflections into mutation hypotheses.
// Returns all hypotheses that meet the confidence threshold.
func (hg *HypothesisGenerator) Generate(ctx context.Context, ref *Reflection) []MutationHypothesis {
	if ref == nil || len(ref.Recommendations) == 0 {
		return nil
	}

	hypotheses := make([]MutationHypothesis, 0, len(ref.Recommendations))
	for _, rec := range ref.Recommendations {
		if rec.Confidence < hg.minConfidence {
			continue
		}
		hyp := hg.recommendationToHypothesis(rec)
		if hyp != nil {
			hypotheses = append(hypotheses, *hyp)
		}
	}

	slog.Debug("generated mutation hypotheses from reflection",
		"total_recommendations", len(ref.Recommendations),
		"accepted", len(hypotheses),
		"min_confidence", hg.minConfidence,
	)
	return hypotheses
}

// recommendationToHypothesis converts a single Recommendation to a MutationHypothesis.
func (hg *HypothesisGenerator) recommendationToHypothesis(rec Recommendation) *MutationHypothesis {
	hyp := &MutationHypothesis{
		Direction:  rec.Action,
		Rationale:  rec.Rationale,
		Confidence: rec.Confidence,
	}

	// Parse target to determine type and key.
	target := rec.Target
	if strings.HasPrefix(target, "param:") {
		hyp.TargetType = "param"
		hyp.TargetKey = strings.TrimPrefix(target, "param:")
	} else if target == "prompt" || target == "prompt_template" {
		hyp.TargetType = "prompt"
		hyp.TargetKey = "prompt_template"
		hyp.Direction = "restructure"
	} else if strings.HasPrefix(target, "tool:") {
		hyp.TargetType = "tool"
		hyp.TargetKey = strings.TrimPrefix(target, "tool:")
	} else if strings.HasPrefix(target, "param:") {
		hyp.TargetType = "param"
		hyp.TargetKey = target
	} else {
		// Generic target: try to infer from action.
		switch rec.Action {
		case "increase", "decrease":
			hyp.TargetType = "param"
			hyp.TargetKey = target
		case "swap":
			hyp.TargetType = "tool"
			hyp.TargetKey = target
		case "restructure":
			hyp.TargetType = "prompt"
			hyp.TargetKey = "prompt_template"
		default:
			hyp.TargetType = "param"
			hyp.TargetKey = target
		}
	}

	// Map direction to standard forms.
	switch strings.ToLower(hyp.Direction) {
	case "increase", "decrease", "swap", "restructure", "replace", "remove":
		// Valid direction.
	default:
		hyp.Direction = "replace"
	}

	return hyp
}

// ApplyHypothesis applies a hypothesis to a strategy, producing a mutated clone.
// Returns nil if the hypothesis cannot be applied to the given strategy.
func ApplyHypothesis(base *mutation.Strategy, hyp MutationHypothesis) *mutation.Strategy {
	if base == nil {
		return nil
	}
	clone := base.Clone()

	switch hyp.TargetType {
	case "param":
		if hyp.SuggestedValue != nil {
			clone.Params[hyp.TargetKey] = hyp.SuggestedValue
		} else {
			// Apply direction-based change to numeric params.
			if v, ok := clone.Params[hyp.TargetKey].(float64); ok {
				switch hyp.Direction {
				case "increase":
					clone.Params[hyp.TargetKey] = v * 1.3
				case "decrease":
					clone.Params[hyp.TargetKey] = v * 0.7
				}
			}
		}
	case "prompt":
		if hyp.SuggestedValue != nil {
			if s, ok := hyp.SuggestedValue.(string); ok {
				clone.PromptTemplate = s
			}
		}
	case "tool":
		if hyp.SuggestedValue != nil {
			if s, ok := hyp.SuggestedValue.(string); ok {
				clone.Params["tools"] = s
			}
		}
	}

	clone.Score = -1
	clone.MutationDesc = fmt.Sprintf("hypothesis: %s (%.0f%%)", hyp.Rationale, hyp.Confidence*100)
	return clone
}

// FormatHypotheses formats a set of hypotheses as a diagnostic string.
func FormatHypotheses(hypotheses []MutationHypothesis) string {
	if len(hypotheses) == 0 {
		return "no hypotheses"
	}
	parts := make([]string, 0, len(hypotheses))
	for _, h := range hypotheses {
		parts = append(parts, fmt.Sprintf("%s:%s → %s (%.0f%%)",
			h.TargetType, h.TargetKey, h.Direction, h.Confidence*100))
	}
	return strings.Join(parts, "; ")
}
