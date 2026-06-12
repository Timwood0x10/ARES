package leader

import (
	"context"
	"testing"

	"goagentx/internal/core/models"
)

func TestDefaultEvaluator_Evaluate(t *testing.T) {
	evaluator := NewDefaultEvaluator()
	ctx := context.Background()

	tests := []struct {
		name          string
		result        *models.RecommendResult
		expectedScore float64
		expectError   bool
	}{
		{
			name:          "nil result",
			result:        nil,
			expectedScore: 0.0,
		},
		{
			name: "empty items",
			result: &models.RecommendResult{
				Items: []*models.RecommendItem{},
			},
			expectedScore: 0.3,
		},
		{
			name: "items with no content",
			result: &models.RecommendResult{
				Items: []*models.RecommendItem{
					{Name: "", Description: ""},
				},
			},
			expectedScore: 0.4,
		},
		{
			name: "items with content",
			result: &models.RecommendResult{
				Items: []*models.RecommendItem{
					{Name: "Item 1", Description: "Description 1"},
					{Name: "Item 2", Description: "Description 2"},
				},
			},
			expectedScore: 1.0,
		},
		{
			name: "partial content",
			result: &models.RecommendResult{
				Items: []*models.RecommendItem{
					{Name: "Item 1", Description: "Description 1"},
					{Name: "", Description: ""},
				},
			},
			expectedScore: 0.75, // 0.5 + 0.5 * 0.5
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score, feedback, err := evaluator.Evaluate(ctx, tt.result, "test input")

			if err != nil && !tt.expectError {
				t.Errorf("unexpected error: %v", err)
			}

			if score != tt.expectedScore {
				t.Errorf("expected score %f, got %f", tt.expectedScore, score)
			}

			// Feedback should be empty for good results
			if score >= 0.7 && feedback != "" {
				t.Errorf("expected empty feedback for good result, got: %s", feedback)
			}
		})
	}
}

func TestLoopConfig_Default(t *testing.T) {
	config := DefaultLeaderAgentConfig()

	if config.Loop.MaxIterations != 3 {
		t.Errorf("expected MaxIterations 3, got %d", config.Loop.MaxIterations)
	}

	if config.Loop.QualityThreshold != 0.7 {
		t.Errorf("expected QualityThreshold 0.7, got %f", config.Loop.QualityThreshold)
	}

	if config.Loop.EnableReflection {
		t.Error("expected EnableReflection to be false")
	}

	if config.Loop.MaxTotalLLMCalls != 50 {
		t.Errorf("expected MaxTotalLLMCalls 50, got %d", config.Loop.MaxTotalLLMCalls)
	}
}

func TestTaskPlanner_Replan(t *testing.T) {
	planner := NewTaskPlanner(5).(*taskPlanner)
	ctx := context.Background()

	profile := &models.UserProfile{
		UserID: "test-user",
	}

	// Test basic replan
	tasks, err := planner.Replan(ctx, profile, "original input", nil, "feedback")
	if err != nil {
		t.Fatalf("replan failed: %v", err)
	}

	if len(tasks) == 0 {
		t.Error("expected at least one task")
	}

	// Test replan with empty feedback
	tasks2, err := planner.Replan(ctx, profile, "original input", nil, "")
	if err != nil {
		t.Fatalf("replan with empty feedback failed: %v", err)
	}

	if len(tasks2) == 0 {
		t.Error("expected at least one task")
	}
}
