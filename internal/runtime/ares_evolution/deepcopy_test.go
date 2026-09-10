// deepcopy_test.go locks the nested-value isolation of dupStrategy
// (REVIEW 3.4#7) and ShadowExecutor.cloneTask (REVIEW 3.4#10): both
// previously shallow-copied their Params/Payload maps, so nested
// maps/slices stayed shared between the stored copy and every caller (and,
// for the shadow executor, between the A/B arms and the buffered original).
package evolution

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/evidence"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

func TestDupStrategyDeepCopiesNestedParams(t *testing.T) {
	store := NewMemoryStrategyStore(10)
	require.NoError(t, store.SetActive(context.Background(), &Strategy{
		ID: "s-1",
		Params: map[string]any{
			"tools": []any{"search", "calculator"},
			"limits": map[string]any{
				"max_tokens": 4096,
				"nested":     map[string]any{"deep": true},
			},
		},
	}))

	got, err := store.GetActive(context.Background())
	require.NoError(t, err)
	require.NotNil(t, got)

	// Corrupt everything nested through the returned copy.
	got.Params["limits"].(map[string]any)["max_tokens"] = 1
	got.Params["limits"].(map[string]any)["nested"].(map[string]any)["deep"] = false
	got.Params["tools"].([]any)[0] = "hijacked"

	again, err := store.GetActive(context.Background())
	require.NoError(t, err)
	limits := again.Params["limits"].(map[string]any)
	assert.Equal(t, 4096, limits["max_tokens"], "nested map must not alias the returned copy")
	assert.Equal(t, true, limits["nested"].(map[string]any)["deep"], "doubly nested map must not alias")
	assert.Equal(t, []any{"search", "calculator"}, again.Params["tools"], "nested slice must not alias")
}

// mutatingNestedRunner mutates nested payload state in every run, mirroring
// a yield checkpoint envelope riding inside a nested map.
type mutatingNestedRunner struct{}

func (r *mutatingNestedRunner) RunShadow(_ context.Context, task *models.Task, _ *mutation.Strategy) (bool, error) {
	env, ok := task.Payload["envelope"].(map[string]any)
	if !ok {
		return false, nil
	}
	env["payload"].(map[string]any)["progress"] = "hijacked"
	env["tokens"] = 999999
	return true, nil
}

func TestCloneTaskDeepCopiesNestedPayload(t *testing.T) {
	runner := &mutatingNestedRunner{}
	exec, err := NewShadowExecutor(evidence.NewMemoryStore(), runner, 3)
	require.NoError(t, err)

	task := models.NewTask("task-nested", "code", nil)
	task.Payload = map[string]any{
		"envelope": map[string]any{
			"payload": map[string]any{"progress": "half"},
			"tokens":  100,
		},
	}
	exec.OnTaskFinalized(task)

	pairs := exec.Feed(context.Background(),
		&mutation.Strategy{ID: "cand"}, &mutation.Strategy{ID: "active"})
	require.Len(t, pairs, 1, "both arms must have run")

	// The buffered original must be untouched by the arms' nested writes.
	tasks := exec.snapshotTasks(1)
	require.Len(t, tasks, 1)
	env := tasks[0].Payload["envelope"].(map[string]any)
	assert.Equal(t, "half", env["payload"].(map[string]any)["progress"],
		"nested payload values must not leak between an arm's clone and the buffered original")
	assert.Equal(t, 100, env["tokens"],
		"nested payload values must not leak between an arm's clone and the buffered original")
}
