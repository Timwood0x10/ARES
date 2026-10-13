package taskfabric

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCreate_WithResolvedDependencies pins the strict create path runtime graph
// growth needs: an edge that does not name a live task is refused, because
// depsSatisfiedLocked treats a missing dependency as unsatisfied forever — an
// accepted dangling edge parks its task (and every task behind it) silently
// instead of failing it.
func TestCreate_WithResolvedDependencies(t *testing.T) {
	tests := []struct {
		name    string
		seed    []*Task
		task    *Task
		wantErr bool
	}{
		{
			name: "existing dependency resolves",
			seed: []*Task{{ID: "dep", Capability: "ares/plan"}},
			task: &Task{ID: "root", Capability: "ares/answer", Dependencies: []string{"dep"}},
		},
		{
			name: "no dependencies is the trivially resolved case",
			task: &Task{ID: "root", Capability: "ares/answer"},
		},
		{
			name:    "unknown dependency is refused",
			task:    &Task{ID: "root", Capability: "ares/answer", Dependencies: []string{"ghost"}},
			wantErr: true,
		},
		{
			name:    "empty dependency id is refused",
			task:    &Task{ID: "root", Capability: "ares/answer", Dependencies: []string{""}},
			wantErr: true,
		},
		{
			name:    "self dependency is refused",
			task:    &Task{ID: "root", Capability: "ares/answer", Dependencies: []string{"root"}},
			wantErr: true,
		},
		{
			name:    "one bad edge refuses the whole task",
			seed:    []*Task{{ID: "dep", Capability: "ares/plan"}},
			task:    &Task{ID: "root", Capability: "ares/answer", Dependencies: []string{"dep", "ghost"}},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := NewFabric()
			for _, s := range tt.seed {
				require.NoError(t, f.Create(s))
			}

			err := f.Create(tt.task, WithResolvedDependencies())

			if !tt.wantErr {
				require.NoError(t, err)
				got, gerr := f.Task(tt.task.ID)
				require.NoError(t, gerr)
				assert.Equal(t, tt.task.Dependencies, got.Dependencies)
				return
			}
			require.ErrorIs(t, err, ErrDependencyUnresolved)
			// The refusal must be total: a half-registered task would be a
			// READY task the scheduler can hand out with a dead edge.
			_, gerr := f.Task(tt.task.ID)
			require.ErrorIs(t, gerr, ErrTaskNotFound)
		})
	}
}

// TestCreate_WithResolvedDependencies_ReportsEveryMissingID pins the shape of
// the refusal: the error carries EVERY offending id, not just the first. The
// usual caller turns it into a message for a model that retries with corrected
// ids, and naming one id per attempt costs that model a full quantum each time.
func TestCreate_WithResolvedDependencies_ReportsEveryMissingID(t *testing.T) {
	f := NewFabric()
	require.NoError(t, f.Create(&Task{ID: "known", Capability: "ares/plan"}))

	err := f.Create(&Task{
		ID:           "root",
		Capability:   "ares/answer",
		Dependencies: []string{"ghost-a", "known", "ghost-b"},
	}, WithResolvedDependencies())

	var depErr *DependencyResolutionError
	require.ErrorAs(t, err, &depErr)
	assert.Equal(t, "root", depErr.TaskID)
	assert.Equal(t, []string{"ghost-a", "ghost-b"}, depErr.Missing,
		"the refusal must name every offending id and only those")
	// The sentinel stays usable for callers that care only about the class.
	assert.ErrorIs(t, err, ErrDependencyUnresolved)
	_, gerr := f.Task("root")
	assert.ErrorIs(t, gerr, ErrTaskNotFound)
}

// TestCreate_DefaultAllowsForwardReference is the other half of the contract:
// the permissive default must stay permissive. runCollabGraph creates a
// submitted graph's nodes in submission order, so an edge routinely names a
// node created later in the same loop — validating there would reject a valid
// submission. The forward reference really does wait, which is why the strict
// mode is an opt-in per call site rather than the only behaviour.
func TestCreate_DefaultAllowsForwardReference(t *testing.T) {
	f := NewFabric()
	// "leaf" names a predecessor that does not exist yet.
	require.NoError(t, f.Create(&Task{ID: "leaf", Capability: "ares/answer", Dependencies: []string{"mid"}}))

	ready, err := f.IsReady("leaf")
	require.NoError(t, err)
	assert.False(t, ready, "an unresolved edge must not report ready")

	// The forward reference lands and completes: the dependent unblocks, which
	// is exactly the batch-growth behaviour the default exists for.
	require.NoError(t, f.Create(&Task{ID: "mid", Capability: "ares/plan"}))
	epoch, err := f.Acquire("mid", "agent-1", time.Minute)
	require.NoError(t, err)
	require.NoError(t, f.Start("mid", "agent-1", epoch))
	require.NoError(t, f.Complete("mid", "agent-1", epoch))

	ready, err = f.IsReady("leaf")
	require.NoError(t, err)
	assert.True(t, ready, "the dependent must run once its forward reference completes")
}
