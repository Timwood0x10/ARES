package leader

import (
	"context"

	"github.com/Timwood0x10/ares/internal/core/models"
)

// Evaluator evaluates the quality of agent results.
type Evaluator interface {
	// Evaluate evaluates the result and returns a quality score and feedback.
	// Args:
	// ctx - operation context.
	// result - the result to evaluate.
	// originalInput - the original user input.
	// Returns:
	// score - quality score between 0.0 and 1.0.
	// feedback - feedback for improvement if score is low.
	// err - evaluation error.
	Evaluate(ctx context.Context, result *models.RecommendResult, originalInput string) (score float64, feedback string, err error)
}

// DefaultEvaluator is a basic evaluator that checks result completeness.
type DefaultEvaluator struct{}

// NewDefaultEvaluator creates a new default evaluator.
func NewDefaultEvaluator() *DefaultEvaluator {
	return &DefaultEvaluator{}
}

// Evaluate checks if the result is complete and returns a score.
func (e *DefaultEvaluator) Evaluate(ctx context.Context, result *models.RecommendResult, originalInput string) (float64, string, error) {
	if result == nil {
		return 0.0, "result is nil", nil
	}

	// Check if result has items
	if len(result.Items) == 0 {
		return 0.3, "result has no items", nil
	}

	// Check if items have meaningful content
	meaningfulCount := 0
	for _, item := range result.Items {
		if item.Name != "" || item.Description != "" {
			meaningfulCount++
		}
	}

	if meaningfulCount == 0 {
		return 0.4, "result items have no meaningful content", nil
	}

	// Calculate score based on completeness
	completeness := float64(meaningfulCount) / float64(len(result.Items))

	// Score between 0.5 and 1.0 based on completeness
	score := 0.5 + 0.5*completeness

	return score, "", nil
}
