package leader

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/goagent/internal/core/models"
)

// helper to create a minimal UserProfile for tests.
func newTestProfile() *models.UserProfile {
	return &models.UserProfile{
		UserID:      "test_user_001",
		Preferences: map[string]any{"theme": "travel"},
	}
}

// TestPlan_NoSubAgents_BackwardCompat verifies that when no subAgents are
// configured, Plan returns a single default task with AgentTypeTop.
func TestPlan_NoSubAgents_BackwardCompat(t *testing.T) {
	planner := NewTaskPlanner(5)
	profile := newTestProfile()

	tasks, err := planner.Plan(context.Background(), profile, "I want to travel to Japan")

	require.NoError(t, err)
	require.Len(t, tasks, 1, "expected exactly one default task when no subAgents configured")

	task := tasks[0]
	assert.Equal(t, models.AgentTypeTop, task.AgentType, "default task should have AgentTypeTop")
	assert.Equal(t, models.AgentTypeTop, task.TaskType, "default task should have TaskType = AgentTypeTop")
	assert.Equal(t, profile, task.UserProfile, "task should reference the provided profile")
	assert.Equal(t, 1, task.Priority, "default task priority should be 1")
	assert.NotEmpty(t, task.TaskID, "task ID should not be empty")
	assert.Contains(t, task.Payload, "action", "default task payload should contain 'action' key")
	assert.Equal(t, "analyze_profile", task.Payload["action"], "default task action should be 'analyze_profile'")
}

// TestPlan_WithSubAgents_CreatesMultipleTasks verifies that when subAgents are
// configured (none with triggers), Plan creates one task per subAgent.
func TestPlan_WithSubAgents_CreatesMultipleTasks(t *testing.T) {
	subAgents := []SubAgentConfig{
		{ID: "dest_01", Type: "destination", Triggers: nil},
		{ID: "food_01", Type: "food", Triggers: nil},
		{ID: "hotel_01", Type: "hotel", Triggers: nil},
	}
	planner := NewTaskPlannerWithConfig(10, subAgents)
	profile := newTestProfile()

	tasks, err := planner.Plan(context.Background(), profile, "plan my trip")

	require.NoError(t, err)
	require.Len(t, tasks, 3, "expected one task per subAgent when none have triggers")

	expectedTypes := []models.AgentType{"destination", "food", "hotel"}
	expectedIDs := []string{"dest_01", "food_01", "hotel_01"}
	for i, task := range tasks {
		assert.Equal(t, expectedTypes[i], task.AgentType, "task %d: AgentType should match subAgent Type", i)
		assert.Equal(t, expectedTypes[i], task.TaskType, "task %d: TaskType should match subAgent Type", i)
		assert.Equal(t, expectedIDs[i], task.Payload["subAgentID"], "task %d: payload should contain subAgentID", i)
		assert.Equal(t, profile, task.UserProfile, "task %d: should reference the provided profile", i)
		assert.NotEmpty(t, task.TaskID, "task %d: task ID should not be empty", i)
	}
}

// TestPlan_WithTriggers_MatchesKeywords verifies that when subAgents have
// triggers, only matching subAgents (plus those without triggers) get tasks.
func TestPlan_WithTriggers_MatchesKeywords(t *testing.T) {
	subAgents := []SubAgentConfig{
		{ID: "dest_01", Type: "destination", Triggers: []string{"travel", "flight", "destination"}},
		{ID: "food_01", Type: "food", Triggers: []string{"food", "restaurant", "cuisine"}},
		{ID: "hotel_01", Type: "hotel", Triggers: nil}, // no triggers -> always selected
	}
	planner := NewTaskPlannerWithConfig(10, subAgents)
	profile := newTestProfile()

	// Input contains "food" which should match food_01, but not dest_01.
	// hotel_01 has no triggers so it should always be selected.
	tasks, err := planner.Plan(context.Background(), profile, "I want to eat local food and find a nice hotel")

	require.NoError(t, err)
	require.Len(t, tasks, 2, "expected 2 tasks: matched 'food' subAgent + no-trigger 'hotel' subAgent")

	// Collect agent types from returned tasks.
	agentTypes := make(map[models.AgentType]bool)
	for _, task := range tasks {
		agentTypes[task.AgentType] = true
	}

	assert.True(t, agentTypes["food"], "food subAgent should be selected (trigger matched)")
	assert.True(t, agentTypes["hotel"], "hotel subAgent should be selected (no triggers)")
	assert.False(t, agentTypes["destination"], "destination subAgent should NOT be selected (no trigger match)")
}

// TestPlan_WithTriggers_NoMatch_Fallback verifies that when all subAgents have
// triggers but none match the input, all subAgent tasks are created as fallback.
func TestPlan_WithTriggers_NoMatch_Fallback(t *testing.T) {
	subAgents := []SubAgentConfig{
		{ID: "dest_01", Type: "destination", Triggers: []string{"travel", "flight"}},
		{ID: "food_01", Type: "food", Triggers: []string{"food", "restaurant"}},
		{ID: "hotel_01", Type: "hotel", Triggers: []string{"hotel", "accommodation"}},
	}
	planner := NewTaskPlannerWithConfig(10, subAgents)
	profile := newTestProfile()

	// Input does not match any trigger keywords.
	tasks, err := planner.Plan(context.Background(), profile, "I need help with my homework")

	require.NoError(t, err)
	require.Len(t, tasks, 3, "fallback: expected all 3 subAgent tasks when no triggers match")

	agentTypes := make(map[models.AgentType]bool)
	for _, task := range tasks {
		agentTypes[task.AgentType] = true
	}
	assert.True(t, agentTypes["destination"], "fallback should include destination")
	assert.True(t, agentTypes["food"], "fallback should include food")
	assert.True(t, agentTypes["hotel"], "fallback should include hotel")
}

// TestPlan_MaxTasksLimit verifies that the planner respects the maxTasks limit
// by truncating the result when more tasks would be created.
func TestPlan_MaxTasksLimit(t *testing.T) {
	subAgents := []SubAgentConfig{
		{ID: "sa_01", Type: "type_a", Triggers: nil},
		{ID: "sa_02", Type: "type_b", Triggers: nil},
		{ID: "sa_03", Type: "type_c", Triggers: nil},
		{ID: "sa_04", Type: "type_d", Triggers: nil},
		{ID: "sa_05", Type: "type_e", Triggers: nil},
	}
	planner := NewTaskPlannerWithConfig(3, subAgents)
	profile := newTestProfile()

	tasks, err := planner.Plan(context.Background(), profile, "any input")

	require.NoError(t, err)
	assert.LessOrEqual(t, len(tasks), 3, "should return at most maxTasks (3) tasks")
	assert.Len(t, tasks, 3, "should return exactly 3 tasks when 5 subAgents match but maxTasks is 3")

	// Verify the first 3 subAgents are returned (order preserved).
	for i, task := range tasks {
		assert.Equal(t, models.AgentType(subAgents[i].Type), task.AgentType,
			"task %d should correspond to subAgent %s", i, subAgents[i].ID)
	}
}

// TestPlan_EmptyInputText verifies behavior when inputText is an empty string.
// Since no triggers can match an empty input, the planner should fall back to
// creating tasks for all subAgents (or the default task if no subAgents).
func TestPlan_EmptyInputText(t *testing.T) {
	t.Run("with_subagents_all_have_triggers", func(t *testing.T) {
		subAgents := []SubAgentConfig{
			{ID: "dest_01", Type: "destination", Triggers: []string{"travel"}},
			{ID: "food_01", Type: "food", Triggers: []string{"food"}},
		}
		planner := NewTaskPlannerWithConfig(10, subAgents)
		profile := newTestProfile()

		tasks, err := planner.Plan(context.Background(), profile, "")

		require.NoError(t, err)
		// All subAgents have triggers but empty input matches none -> fallback to all.
		require.Len(t, tasks, 2, "empty input with all-trigger subAgents should fallback to all")
	})

	t.Run("with_subagents_mixed_triggers", func(t *testing.T) {
		subAgents := []SubAgentConfig{
			{ID: "dest_01", Type: "destination", Triggers: []string{"travel"}},
			{ID: "hotel_01", Type: "hotel", Triggers: nil}, // no triggers -> always selected
		}
		planner := NewTaskPlannerWithConfig(10, subAgents)
		profile := newTestProfile()

		tasks, err := planner.Plan(context.Background(), profile, "")

		require.NoError(t, err)
		// hotel_01 has no triggers so it is always selected.
		// dest_01 has triggers but empty input does not match.
		// Since not all subAgents have triggers (allHaveTriggers = false), no fallback.
		require.Len(t, tasks, 1, "empty input with mixed triggers should only return no-trigger subAgent")
		assert.Equal(t, models.AgentType("hotel"), tasks[0].AgentType)
	})

	t.Run("without_subagents", func(t *testing.T) {
		planner := NewTaskPlanner(5)
		profile := newTestProfile()

		tasks, err := planner.Plan(context.Background(), profile, "")

		require.NoError(t, err)
		require.Len(t, tasks, 1, "empty input without subAgents should return default task")
		assert.Equal(t, models.AgentTypeTop, tasks[0].AgentType)
	})
}

// TestPlan_NilProfile_ReturnsError verifies that Plan returns an error when
// the profile is nil.
func TestPlan_NilProfile_ReturnsError(t *testing.T) {
	planner := NewTaskPlanner(5)

	tasks, err := planner.Plan(context.Background(), nil, "some input")

	require.Error(t, err)
	assert.Nil(t, tasks, "tasks should be nil on error")
	assert.Contains(t, err.Error(), "profile cannot be nil")
}

// TestNewTaskPlanner_DefaultMaxTasks verifies that NewTaskPlanner defaults
// maxTasks to 5 when a non-positive value is provided.
func TestNewTaskPlanner_DefaultMaxTasks(t *testing.T) {
	planner := NewTaskPlanner(0)
	profile := newTestProfile()

	tasks, err := planner.Plan(context.Background(), profile, "test")

	require.NoError(t, err)
	// With default maxTasks=5 and no subAgents, should return 1 task.
	assert.Len(t, tasks, 1)
}

// TestNewTaskPlannerWithConfig_DefaultMaxTasks verifies that
// NewTaskPlannerWithConfig defaults maxTasks to 5 when a non-positive value
// is provided.
func TestNewTaskPlannerWithConfig_DefaultMaxTasks(t *testing.T) {
	subAgents := []SubAgentConfig{
		{ID: "a", Type: "alpha", Triggers: nil},
	}
	planner := NewTaskPlannerWithConfig(-1, subAgents)
	profile := newTestProfile()

	tasks, err := planner.Plan(context.Background(), profile, "test")

	require.NoError(t, err)
	require.Len(t, tasks, 1)
}

// TestPlan_TriggerCaseInsensitive verifies that trigger matching is
// case-insensitive.
func TestPlan_TriggerCaseInsensitive(t *testing.T) {
	subAgents := []SubAgentConfig{
		{ID: "food_01", Type: "food", Triggers: []string{"FOOD", "Restaurant"}},
	}
	planner := NewTaskPlannerWithConfig(10, subAgents)
	profile := newTestProfile()

	// Lowercase input should still match uppercase triggers.
	tasks, err := planner.Plan(context.Background(), profile, "i want some food")

	require.NoError(t, err)
	require.Len(t, tasks, 1, "trigger matching should be case-insensitive")
	assert.Equal(t, models.AgentType("food"), tasks[0].AgentType)
}
