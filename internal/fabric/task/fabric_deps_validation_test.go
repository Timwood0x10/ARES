package taskfabric

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apperrors "github.com/Timwood0x10/ares/internal/errors"
)

// readyTask seeds one READY task, which is the only state SetDependencies accepts.
func readyTask(t *testing.T, f *Fabric, id string) {
	t.Helper()
	require.NoError(t, f.Create(&Task{ID: id, Capability: "ares/plan"}))
	got, err := f.Task(id)
	require.NoError(t, err)
	require.Equal(t, StateReady, got.State, "the fixture expects a READY task")
}

// depsOf reads a task's current dependency list.
func depsOf(t *testing.T, f *Fabric, id string) []string {
	t.Helper()
	got, err := f.Task(id)
	require.NoError(t, err)
	return got.Dependencies
}

// TestSetDependencies_RejectsWhatCouldStrandTheTask is the validation contract for the
// runtime rewire path, which had none: Create's strict variant already refused these
// three shapes, but the path that grows a LIVE graph accepted them all. Each one parks
// its task (and everything behind it) in READY forever with no owner and no error — the
// silence P0-2 closed on the create side.
//
// Every rejection also asserts the list is UNCHANGED: a validator that reports the
// problem after applying the bad list has created the stranding it detected.
func TestSetDependencies_RejectsWhatCouldStrandTheTask(t *testing.T) {
	t.Run("unknown dependency", func(t *testing.T) {
		f := NewFabric()
		require.NoError(t, f.Create(&Task{ID: "known", Capability: "ares/plan"}))
		readyTask(t, f, "root")
		require.NoError(t, f.SetDependencies("root", []string{"known"}))

		err := f.SetDependencies("root", []string{"ghost"})
		require.ErrorIs(t, err, ErrDependencyUnresolved)
		assert.Equal(t, []string{"known"}, depsOf(t, f, "root"), "a refused rewire must leave the live list untouched")
	})

	t.Run("self edge", func(t *testing.T) {
		f := NewFabric()
		readyTask(t, f, "root")

		err := f.SetDependencies("root", []string{"root"})
		require.ErrorIs(t, err, apperrors.ErrDependencyCycle)
		assert.Empty(t, depsOf(t, f, "root"))
	})

	t.Run("direct cycle", func(t *testing.T) {
		f := NewFabric()
		readyTask(t, f, "a")
		readyTask(t, f, "b")
		require.NoError(t, f.SetDependencies("a", []string{"b"}))

		err := f.SetDependencies("b", []string{"a"})
		require.ErrorIs(t, err, apperrors.ErrDependencyCycle)
		assert.Empty(t, depsOf(t, f, "b"))
	})

	t.Run("transitive cycle", func(t *testing.T) {
		f := NewFabric()
		readyTask(t, f, "a")
		readyTask(t, f, "b")
		readyTask(t, f, "c")
		require.NoError(t, f.SetDependencies("a", []string{"b"}))
		require.NoError(t, f.SetDependencies("b", []string{"c"}))

		// c -> a would close a -> b -> c -> a.
		err := f.SetDependencies("c", []string{"a"})
		require.ErrorIs(t, err, apperrors.ErrDependencyCycle)
		assert.Empty(t, depsOf(t, f, "c"))
	})
}

// TestSetDependencies_AcceptsAValidRewire is the other half: a legitimate edit must not
// be refused by the new validation. A validator that also blocks correct graphs would
// trade a silent strand for a loud inability to grow the graph.
func TestSetDependencies_AcceptsAValidRewire(t *testing.T) {
	f := NewFabric()
	readyTask(t, f, "dep-1")
	readyTask(t, f, "dep-2")
	readyTask(t, f, "root")

	require.NoError(t, f.SetDependencies("root", []string{"dep-1"}))
	require.NoError(t, f.SetDependencies("root", []string{"dep-1", "dep-2"}))
	assert.Equal(t, []string{"dep-1", "dep-2"}, depsOf(t, f, "root"))

	// Rewiring back to none is also legitimate (RemoveEdge's primitive).
	require.NoError(t, f.SetDependencies("root", nil))
	assert.Empty(t, depsOf(t, f, "root"))
}

// TestSetDependencies_TerminatesOnAPreExistingCycle matters because the walk now runs on
// every rewire: a cycle written BEFORE this validation existed (or restored from an older
// store) must make the traversal stop, not spin forever holding the fabric mutex — a
// hang there would freeze every task operation, which is far worse than the strand the
// validation prevents.
func TestSetDependencies_TerminatesOnAPreExistingCycle(t *testing.T) {
	f := NewFabric()
	readyTask(t, f, "x")
	readyTask(t, f, "y")
	readyTask(t, f, "z")

	// Write the corrupt graph directly, the way an old store would restore it.
	f.mu.Lock()
	f.tasks["x"].Dependencies = []string{"y"}
	f.tasks["y"].Dependencies = []string{"z"}
	f.tasks["z"].Dependencies = []string{"x"}
	f.mu.Unlock()

	// A rewire that touches the corrupted component must return (detecting the cycle),
	// and an unrelated one must still succeed.
	err := f.SetDependencies("x", []string{"y"})
	require.ErrorIs(t, err, apperrors.ErrDependencyCycle)

	readyTask(t, f, "free")
	require.NoError(t, f.SetDependencies("free", []string{"z"}), "an unrelated edge is still legal")
}
