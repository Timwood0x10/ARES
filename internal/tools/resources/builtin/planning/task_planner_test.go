package builtin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTaskPlanner_New(t *testing.T) {
	tp := NewTaskPlanner(nil)
	assert.NotNil(t, tp)
	assert.Equal(t, "task_planner", tp.Name())
}

func TestTaskPlanner_Execute_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		params    map[string]any
		wantError string // substring expected in result.Error
	}{
		{
			name:      "missing_operation",
			params:    map[string]any{"goal": "do something"},
			wantError: "operation is required",
		},
		{
			name:      "missing_goal",
			params:    map[string]any{"operation": "plan_tasks"},
			wantError: "goal is required",
		},
		{
			name:      "unsupported_operation",
			params:    map[string]any{"operation": "invalid_op", "goal": "test"},
			wantError: "unsupported operation: invalid_op",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tp := NewTaskPlanner(nil)
			result, err := tp.Execute(context.Background(), tt.params)
			assert.NoError(t, err)
			assert.False(t, result.Success)
			assert.Contains(t, result.Error, tt.wantError,
				"Execute() Error should mention %q", tt.wantError)
		})
	}
}

func TestTaskPlanner_Execute_LLMClientRequired(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		params    map[string]any
		wantError string // substring expected in result.Error
	}{
		{
			name: "plan_tasks_nil_client",
			params: map[string]any{
				"operation": "plan_tasks",
				"goal":      "build a website",
			},
			wantError: "LLM client",
		},
		{
			name: "decompose_task_missing_task",
			params: map[string]any{
				"operation": "decompose_task",
				"goal":      "test",
			},
			wantError: "task is required",
		},
		{
			name: "decompose_task_nil_client",
			params: map[string]any{
				"operation": "decompose_task",
				"goal":      "test",
				"task":      "complex task",
			},
			wantError: "LLM client",
		},
		{
			name: "decompose_task_with_complexity_nil_client",
			params: map[string]any{
				"operation":  "decompose_task",
				"goal":       "test",
				"task":       "complex task",
				"complexity": "complex",
			},
			wantError: "LLM client",
		},
		{
			name: "plan_tasks_with_context_tools_nil_client",
			params: map[string]any{
				"operation":       "plan_tasks",
				"goal":            "build app",
				"context":         "limited budget",
				"available_tools": []any{"code", "test"},
			},
			wantError: "LLM client",
		},
		{
			name: "estimate_time_nil_client",
			params: map[string]any{
				"operation": "estimate_time",
				"goal":      "build a website",
			},
			// estimateTime returns default estimation even without LLM client.
			wantError: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tp := NewTaskPlanner(nil)
			result, err := tp.Execute(context.Background(), tt.params)
			assert.NoError(t, err)
			if tt.wantError != "" {
				assert.False(t, result.Success, "Execute(%q) should fail", tt.name)
				assert.Contains(t, result.Error, tt.wantError,
					"Execute() Error should mention %q", tt.wantError)
			} else {
				assert.True(t, result.Success, "Execute(%q) should succeed", tt.name)
			}
		})
	}
}

func TestTaskPlanner_parsePlan(t *testing.T) {
	tp := NewTaskPlanner(nil)
	plan, err := tp.parsePlan(`{
		"summary": "test plan",
		"steps": [{"step_number": 1, "description": "do something", "tool": "", "expected_output": "done"}],
		"estimated_time": "1 hour",
		"required_tools": [],
		"risks": []
	}`)
	assert.NoError(t, err)
	assert.NotNil(t, plan)
	assert.Equal(t, "test plan", plan.Summary)
	assert.Equal(t, 1, len(plan.Steps))
}

func TestTaskPlanner_parsePlan_InvalidJSON(t *testing.T) {
	tp := NewTaskPlanner(nil)
	_, err := tp.parsePlan("not json")
	assert.Error(t, err)
}

func TestTaskPlanner_parsePlan_NoJSON(t *testing.T) {
	tp := NewTaskPlanner(nil)
	_, err := tp.parsePlan("plain text without braces")
	assert.Error(t, err)
}

func TestTaskPlanner_parseSubtasks(t *testing.T) {
	tp := NewTaskPlanner(nil)
	subtasks, err := tp.parseSubtasks(`{
		"subtasks": [
			{"subtask_id": "1", "description": "subtask 1", "dependencies": [], "estimated_minutes": 30, "priority": "high"}
		]
	}`)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(subtasks))
	assert.Equal(t, "1", subtasks[0].SubtaskID)
}

func TestTaskPlanner_parseSubtasks_InvalidJSON(t *testing.T) {
	tp := NewTaskPlanner(nil)
	_, err := tp.parseSubtasks("not json")
	assert.Error(t, err)
}

func TestTaskPlanner_parseEstimation(t *testing.T) {
	tp := NewTaskPlanner(nil)
	est, err := tp.parseEstimation(`{
		"estimated_minutes": 60,
		"confidence": "medium",
		"factors": ["complexity"],
		"assumptions": ["nothing"]
	}`)
	assert.NoError(t, err)
	assert.NotNil(t, est)
	assert.Equal(t, 60, est.EstimatedMinutes)
}

func TestTaskPlanner_parseEstimation_InvalidJSON(t *testing.T) {
	tp := NewTaskPlanner(nil)
	_, err := tp.parseEstimation("not json")
	assert.Error(t, err)
}

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"simple object", `{"a": 1}`, `{"a": 1}`},
		{"with prefix text", `text {"key": "val"} more`, `{"key": "val"}`},
		{"nested braces", `{"outer": {"inner": true}}`, `{"outer": {"inner": true}}`},
		{"string with braces", `{"data": "test {x}"}`, `{"data": "test {x}"}`},
		{"no json found", "just text", ""},
		{"escaped quotes", `{"key": "value \"escaped\""}`, `{"key": "value \"escaped\""}`},
		{"multiple objects", `{"first": 1} and {"second": 2}`, `{"first": 1}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractJSON(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFormatToolsList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		tools []string
		want  string
	}{
		{name: "multiple_tools", tools: []string{"tool1", "tool2"}, want: "- tool1\n- tool2\n"},
		{name: "empty_list", tools: []string{}, want: ""},
		{name: "single_tool", tools: []string{"only"}, want: "- only\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatToolsList(tt.tools)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSetLLMClient(t *testing.T) {
	tp := NewTaskPlanner(nil)
	tp.SetLLMClient(nil)
	// SetLLMClient with nil should not panic.
	assert.Equal(t, "task_planner", tp.Name())
}

func TestTaskPlanner_DecomposeTask_DefaultComplexity(t *testing.T) {
	tp := NewTaskPlanner(nil)
	result, err := tp.Execute(context.Background(), map[string]interface{}{
		"operation": "decompose_task",
		"goal":      "test",
		"task":      "a task",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success) // nil client path
}

func TestTaskPlanner_PlanTasks_WithContextAndTools(t *testing.T) {
	tp := NewTaskPlanner(nil)
	result, err := tp.Execute(context.Background(), map[string]interface{}{
		"operation":       "plan_tasks",
		"goal":            "build app",
		"context":         "limited budget",
		"available_tools": []interface{}{"code", "test"},
	})
	assert.NoError(t, err)
	assert.False(t, result.Success) // nil client
}
