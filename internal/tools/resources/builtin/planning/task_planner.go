package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/llm"
	"github.com/Timwood0x10/ares/internal/tools/resources/base"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// TaskPlanner provides task planning and decomposition capabilities.
type TaskPlanner struct {
	*base.BaseTool
	llmClient *llm.Client
}

// NewTaskPlanner creates a new TaskPlanner tool.
func NewTaskPlanner(llmClient *llm.Client) *TaskPlanner {
	params := &core.ParameterSchema{
		Type: "object",
		Properties: map[string]*core.Parameter{
			"operation": {
				Type:        "string",
				Description: "Operation to perform (plan_tasks, decompose_task, estimate_time)",
				Enum:        []interface{}{"plan_tasks", "decompose_task", "estimate_time"},
			},
			"goal": {
				Type:        "string",
				Description: "The goal or objective to plan for",
			},
			"context": {
				Type:        "string",
				Description: "Additional context or constraints for the task",
			},
			"available_tools": {
				Type:        "array",
				Description: "List of available tools that can be used",
			},
			"task": {
				Type:        "string",
				Description: "Specific task to decompose (for decompose_task operation)",
			},
			"complexity": {
				Type:        "string",
				Description: "Task complexity level (simple, medium, complex)",
				Enum:        []interface{}{"simple", "medium", "complex"},
				Default:     "medium",
			},
		},
		Required: []string{"operation", "goal"},
	}

	return &TaskPlanner{
		BaseTool:  base.NewBaseToolWithCapabilities("task_planner", "Plan and decompose tasks into executable steps", core.CategoryCore, []core.Capability{core.CapabilityMath}, params),
		llmClient: llmClient,
	}
}

// Execute performs the task planning operation.
func (t *TaskPlanner) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	operation, ok := params["operation"].(string)
	if !ok || operation == "" {
		return core.NewErrorResult("operation is required"), nil
	}

	goal, ok := params["goal"].(string)
	if !ok || goal == "" {
		return core.NewErrorResult("goal is required"), nil
	}

	switch operation {
	case "plan_tasks":
		contextVal := getString(params, "context")
		availableTools := getStringSlice(params, "available_tools")
		return t.planTasks(ctx, goal, contextVal, availableTools)
	case "decompose_task":
		task, ok := params["task"].(string)
		if !ok || task == "" {
			return core.NewErrorResult("task is required for decompose_task operation"), nil
		}
		complexity := getString(params, "complexity")
		if complexity == "" {
			complexity = "medium"
		}
		return t.decomposeTask(ctx, task, complexity)
	case "estimate_time":
		return t.estimateTime(ctx, goal)
	default:
		return core.NewErrorResult(fmt.Sprintf("unsupported operation: %s", operation)), nil
	}
}

// planTasks creates a comprehensive task plan.
func (t *TaskPlanner) planTasks(ctx context.Context, goal, context string, availableTools []string) (core.Result, error) {
	log.Info("Planning tasks", "goal", goal, "available_tools", len(availableTools))

	// Build planning prompt
	prompt := t.buildPlanningPrompt(goal, context, availableTools)

	// Call LLM for planning
	if t.llmClient == nil {
		return core.NewErrorResult("LLM client not available for task planning"), nil
	}

	response, err := t.llmClient.Generate(ctx, prompt)
	if err != nil {
		return core.NewErrorResult(fmt.Sprintf("failed to generate plan: %v", err)), nil
	}

	// Parse the plan
	plan, err := t.parsePlan(response)
	if err != nil {
		log.Warn("Failed to parse plan, returning raw response", "error", err)
		return core.NewResult(true, map[string]interface{}{
			"operation": "plan_tasks",
			"goal":      goal,
			"plan":      response,
			"raw":       true,
		}), nil
	}

	return core.NewResult(true, map[string]interface{}{
		"operation":      "plan_tasks",
		"goal":           goal,
		"plan":           plan,
		"step_count":     len(plan.Steps),
		"estimated_time": plan.EstimatedTime,
		"required_tools": plan.RequiredTools,
	}), nil
}

// decomposeTask breaks down a complex task into smaller subtasks.
func (t *TaskPlanner) decomposeTask(ctx context.Context, task, complexity string) (core.Result, error) {
	log.Info("Decomposing task", "task", task, "complexity", complexity)

	// Build decomposition prompt
	prompt := t.buildDecompositionPrompt(task, complexity)

	// Call LLM for decomposition
	if t.llmClient == nil {
		return core.NewErrorResult("LLM client not available for task decomposition"), nil
	}

	response, err := t.llmClient.Generate(ctx, prompt)
	if err != nil {
		return core.NewErrorResult(fmt.Sprintf("failed to decompose task: %v", err)), nil
	}

	// Parse the decomposition
	subtasks, err := t.parseSubtasks(response)
	if err != nil {
		log.Warn("Failed to parse subtasks, returning raw response", "error", err)
		return core.NewResult(true, map[string]interface{}{
			"operation": "decompose_task",
			"task":      task,
			"subtasks":  response,
			"raw":       true,
		}), nil
	}

	return core.NewResult(true, map[string]interface{}{
		"operation":     "decompose_task",
		"task":          task,
		"complexity":    complexity,
		"subtasks":      subtasks,
		"subtask_count": len(subtasks),
	}), nil
}

// estimateTime estimates the time required to complete a task.
func (t *TaskPlanner) estimateTime(ctx context.Context, goal string) (core.Result, error) {
	log.Info("Estimating time", "goal", goal)

	// Build estimation prompt
	prompt := t.buildEstimationPrompt(goal)

	// Call LLM for estimation
	if t.llmClient == nil {
		// Return a default estimation without LLM
		defaultEstimation := map[string]interface{}{
			"estimated_minutes": 30,
			"confidence":        "low",
			"factors":           []string{"No LLM available for detailed estimation"},
		}
		return core.NewResult(true, map[string]interface{}{
			"operation": "estimate_time",
			"goal":      goal,
			"estimate":  defaultEstimation,
		}), nil
	}

	response, err := t.llmClient.Generate(ctx, prompt)
	if err != nil {
		return core.NewErrorResult(fmt.Sprintf("failed to estimate time: %v", err)), nil
	}

	// Parse the estimation
	estimate, err := t.parseEstimation(response)
	if err != nil {
		log.Warn("Failed to parse estimation, returning raw response", "error", err)
		return core.NewResult(true, map[string]interface{}{
			"operation": "estimate_time",
			"goal":      goal,
			"estimate":  response,
			"raw":       true,
		}), nil
	}

	return core.NewResult(true, map[string]interface{}{
		"operation": "estimate_time",
		"goal":      goal,
		"estimate":  estimate,
	}), nil
}

// buildPlanningPrompt builds a prompt for task planning.
func (t *TaskPlanner) buildPlanningPrompt(goal, context string, availableTools []string) string {
	prompt := `You are a task planning assistant. Your goal is to break down complex objectives into clear, actionable steps.

Goal: ` + goal + `
`

	if context != "" {
		prompt += `
Context/Constraints:
` + context + `
`
	}

	if len(availableTools) > 0 {
		prompt += `
Available Tools:
` + formatToolsList(availableTools) + `
`
	}

	prompt += `
Please provide a detailed task plan in the following JSON format:
{
  "summary": "Brief summary of the overall approach",
  "steps": [
    {
      "step_number": 1,
      "description": "What to do",
      "tool": "tool to use (if any)",
      "expected_output": "What this step produces"
    }
  ],
  "estimated_time": "X hours",
  "required_tools": ["list of tools needed"],
  "risks": ["potential risks or blockers"]
}

Think step by step and provide a practical, executable plan.`

	return prompt
}

// buildDecompositionPrompt builds a prompt for task decomposition.
func (t *TaskPlanner) buildDecompositionPrompt(task, complexity string) string {
	prompt := `You are a task decomposition assistant. Your goal is to break down complex tasks into smaller, manageable subtasks.

Task: ` + task + `
Complexity: ` + complexity + `

Please decompose this task into subtasks in the following JSON format:
{
  "subtasks": [
    {
      "subtask_id": "1",
      "description": "What this subtask accomplishes",
      "dependencies": [],
      "estimated_minutes": X,
      "priority": "high/medium/low"
    }
  ]
}

Ensure subtasks:
1. Are logically ordered (dependencies should be reflected)
2. Are specific and actionable
3. Have reasonable time estimates
4. Include clear priorities`

	return prompt
}

// buildEstimationPrompt builds a prompt for time estimation.
func (t *TaskPlanner) buildEstimationPrompt(goal string) string {
	prompt := `You are a time estimation assistant. Estimate the time required to complete the given goal.

Goal: ` + goal + `

Please provide your estimation in the following JSON format:
{
  "estimated_minutes": X,
  "confidence": "high/medium/low",
  "factors": ["list of factors affecting the estimate"],
  "assumptions": ["list of assumptions made"]
}

Be realistic and consider potential complexity.`

	return prompt
}

// TaskPlan represents a structured task plan.
type TaskPlan struct {
	Summary       string     `json:"summary"`
	Steps         []TaskStep `json:"steps"`
	EstimatedTime string     `json:"estimated_time"`
	RequiredTools []string   `json:"required_tools"`
	Risks         []string   `json:"risks"`
}

// TaskStep represents a single step in the plan.
type TaskStep struct {
	StepNumber     int    `json:"step_number"`
	Description    string `json:"description"`
	Tool           string `json:"tool"`
	ExpectedOutput string `json:"expected_output"`
}

// Subtask represents a decomposed subtask.
type Subtask struct {
	SubtaskID        string   `json:"subtask_id"`
	Description      string   `json:"description"`
	Dependencies     []string `json:"dependencies"`
	EstimatedMinutes int      `json:"estimated_minutes"`
	Priority         string   `json:"priority"`
}

// TimeEstimate represents a time estimation.
type TimeEstimate struct {
	EstimatedMinutes int      `json:"estimated_minutes"`
	Confidence       string   `json:"confidence"`
	Factors          []string `json:"factors"`
	Assumptions      []string `json:"assumptions"`
}

// parsePlan parses LLM response into a TaskPlan.
func (t *TaskPlanner) parsePlan(response string) (*TaskPlan, error) {
	// Extract JSON from response
	jsonStr := extractJSON(response)
	if jsonStr == "" {
		return nil, fmt.Errorf("no JSON found in response")
	}

	var plan TaskPlan
	if err := json.Unmarshal([]byte(jsonStr), &plan); err != nil {
		return nil, errors.Wrap(err, "failed to parse plan JSON")
	}

	return &plan, nil
}

// parseSubtasks parses LLM response into subtasks.
func (t *TaskPlanner) parseSubtasks(response string) ([]Subtask, error) {
	jsonStr := extractJSON(response)
	if jsonStr == "" {
		return nil, fmt.Errorf("no JSON found in response")
	}

	var result struct {
		Subtasks []Subtask `json:"subtasks"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, errors.Wrap(err, "failed to parse subtasks JSON")
	}

	return result.Subtasks, nil
}

// parseEstimation parses LLM response into a TimeEstimate.
func (t *TaskPlanner) parseEstimation(response string) (*TimeEstimate, error) {
	jsonStr := extractJSON(response)
	if jsonStr == "" {
		return nil, fmt.Errorf("no JSON found in response")
	}

	var estimate TimeEstimate
	if err := json.Unmarshal([]byte(jsonStr), &estimate); err != nil {
		return nil, errors.Wrap(err, "failed to parse estimation JSON")
	}

	return &estimate, nil
}

// extractJSON extracts JSON from a text response.
func extractJSON(text string) string {
	start := strings.Index(text, "{")
	if start == -1 {
		return ""
	}

	// Find matching closing brace
	braceCount := 0
	inString := false
	escapeNext := false

	for i := start; i < len(text); i++ {
		char := text[i]

		if escapeNext {
			escapeNext = false
			continue
		}

		if char == '\\' {
			escapeNext = true
			continue
		}

		if char == '"' {
			inString = !inString
			continue
		}

		if !inString {
			switch char {
			case '{':
				braceCount++
			case '}':
				braceCount--
				if braceCount == 0 {
					return text[start : i+1]
				}
			}
		}
	}

	return ""
}

// formatToolsList formats a list of tools for display.
func formatToolsList(tools []string) string {
	result := ""
	for _, tool := range tools {
		result += "- " + tool + "\n"
	}
	return result
}

// SetLLMClient sets the LLM client for the planner.
func (t *TaskPlanner) SetLLMClient(client *llm.Client) {
	t.llmClient = client
}

// Helper functions.
func getString(params map[string]interface{}, key string) string {
	if v, ok := params[key].(string); ok {
		return v
	}
	return ""
}

func getStringSlice(params map[string]interface{}, key string) []string {
	if v, ok := params[key].([]interface{}); ok {
		result := make([]string, len(v))
		for i, val := range v {
			if s, ok := val.(string); ok {
				result[i] = s
			}
		}
		return result
	}
	return nil
}
