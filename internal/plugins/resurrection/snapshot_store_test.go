package resurrection

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMemorySnapshotStore_Initialized(t *testing.T) {
	store := NewMemorySnapshotStore()
	require.NotNil(t, store)
	assert.NotNil(t, store.snapshots)
}

func TestMemorySnapshotStore_SaveAndLoad(t *testing.T) {
	store := NewMemorySnapshotStore()
	ctx := context.Background()

	err := store.Save(ctx, "agent-1", map[string]any{"key": "value", "count": 42})
	require.NoError(t, err)

	snap, err := store.Load(ctx, "agent-1")
	require.NoError(t, err)
	assert.Equal(t, "value", snap["key"])
	assert.Equal(t, 42, snap["count"])
}

func TestMemorySnapshotStore_LoadNonExistent_ReturnsErrSnapshotNotFound(t *testing.T) {
	store := NewMemorySnapshotStore()
	ctx := context.Background()

	snap, err := store.Load(ctx, "nonexistent")
	assert.ErrorIs(t, err, ErrSnapshotNotFound)
	assert.Nil(t, snap)
}

func TestMemorySnapshotStore_LoadEmptyAgentID_ReturnsErrSnapshotNotFound(t *testing.T) {
	store := NewMemorySnapshotStore()
	ctx := context.Background()

	snap, err := store.Load(ctx, "")
	assert.ErrorIs(t, err, ErrSnapshotNotFound)
	assert.Nil(t, snap)
}

func TestMemorySnapshotStore_SaveNilSnapshot_IsNoOp(t *testing.T) {
	store := NewMemorySnapshotStore()
	ctx := context.Background()

	err := store.Save(ctx, "agent-1", nil)
	assert.NoError(t, err)

	snap, err := store.Load(ctx, "agent-1")
	assert.ErrorIs(t, err, ErrSnapshotNotFound)
	assert.Nil(t, snap)
}

func TestMemorySnapshotStore_SaveEmptyAgentID_IsNoOp(t *testing.T) {
	store := NewMemorySnapshotStore()
	ctx := context.Background()

	err := store.Save(ctx, "", map[string]any{"key": "value"})
	assert.NoError(t, err)

	snap, err := store.Load(ctx, "")
	assert.ErrorIs(t, err, ErrSnapshotNotFound)
	assert.Nil(t, snap)
}

func TestMemorySnapshotStore_SaveOverwritesExisting(t *testing.T) {
	store := NewMemorySnapshotStore()
	ctx := context.Background()

	err := store.Save(ctx, "agent-1", map[string]any{"version": "1"})
	require.NoError(t, err)

	err = store.Save(ctx, "agent-1", map[string]any{"version": "2", "extra": "data"})
	require.NoError(t, err)

	snap, err := store.Load(ctx, "agent-1")
	require.NoError(t, err)
	assert.Equal(t, "2", snap["version"])
	assert.Equal(t, "data", snap["extra"])
	assert.Len(t, snap, 2)
}

func TestMemorySnapshotStore_Delete_RemovesData(t *testing.T) {
	store := NewMemorySnapshotStore()
	ctx := context.Background()

	require.NoError(t, store.Save(ctx, "agent-1", map[string]any{"key": "value"}))

	err := store.Delete(ctx, "agent-1")
	assert.NoError(t, err)

	snap, err := store.Load(ctx, "agent-1")
	assert.ErrorIs(t, err, ErrSnapshotNotFound)
	assert.Nil(t, snap)
}

func TestMemorySnapshotStore_DeleteEmptyAgentID_IsNoOp(t *testing.T) {
	store := NewMemorySnapshotStore()
	ctx := context.Background()

	err := store.Delete(ctx, "")
	assert.NoError(t, err)
}

func TestMemorySnapshotStore_DeleteNonExistent_IsNoOp(t *testing.T) {
	store := NewMemorySnapshotStore()
	ctx := context.Background()

	err := store.Delete(ctx, "nonexistent")
	assert.NoError(t, err)
}

func TestMemorySnapshotStore_SaveDeepCopy_IsolationFromCaller(t *testing.T) {
	store := NewMemorySnapshotStore()
	ctx := context.Background()

	original := map[string]any{"key": "original"}
	err := store.Save(ctx, "agent-1", original)
	require.NoError(t, err)

	original["key"] = "modified"

	snap, err := store.Load(ctx, "agent-1")
	require.NoError(t, err)
	assert.Equal(t, "original", snap["key"])
}

func TestMemorySnapshotStore_LoadDeepCopy_IsolationFromCaller(t *testing.T) {
	store := NewMemorySnapshotStore()
	ctx := context.Background()

	err := store.Save(ctx, "agent-1", map[string]any{"key": "value"})
	require.NoError(t, err)

	snap, err := store.Load(ctx, "agent-1")
	require.NoError(t, err)

	snap["key"] = "mutated"

	snap2, err := store.Load(ctx, "agent-1")
	require.NoError(t, err)
	assert.Equal(t, "value", snap2["key"])
}

func TestMemorySnapshotStore_MultipleAgents_Isolated(t *testing.T) {
	store := NewMemorySnapshotStore()
	ctx := context.Background()

	require.NoError(t, store.Save(ctx, "a1", map[string]any{"name": "agent1"}))
	require.NoError(t, store.Save(ctx, "a2", map[string]any{"name": "agent2"}))

	snap1, err := store.Load(ctx, "a1")
	require.NoError(t, err)
	assert.Equal(t, "agent1", snap1["name"])

	snap2, err := store.Load(ctx, "a2")
	require.NoError(t, err)
	assert.Equal(t, "agent2", snap2["name"])

	require.NoError(t, store.Delete(ctx, "a1"))

	snap1, err = store.Load(ctx, "a1")
	assert.ErrorIs(t, err, ErrSnapshotNotFound)
	assert.Nil(t, snap1)

	snap2, err = store.Load(ctx, "a2")
	require.NoError(t, err)
	assert.Equal(t, "agent2", snap2["name"])
}

func TestMemorySnapshotStore_LoadEmptyStore_ReturnsErrSnapshotNotFound(t *testing.T) {
	store := NewMemorySnapshotStore()
	ctx := context.Background()

	snap, err := store.Load(ctx, "agent-1")
	assert.ErrorIs(t, err, ErrSnapshotNotFound)
	assert.Nil(t, snap)
}

func TestMemorySnapshotStore_NestedMapValues(t *testing.T) {
	store := NewMemorySnapshotStore()
	ctx := context.Background()

	data := map[string]any{
		"nested": map[string]any{"inner": "value"},
	}
	err := store.Save(ctx, "agent-1", data)
	require.NoError(t, err)

	snap, err := store.Load(ctx, "agent-1")
	require.NoError(t, err)
	assert.Equal(t, "value", snap["nested"].(map[string]any)["inner"])
}

func TestMemorySnapshotStore_ConcurrentSaveLoad_NoRace(t *testing.T) {
	store := NewMemorySnapshotStore()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		id := "agent-1"
		go func(idx int) {
			defer wg.Done()
			_ = store.Save(ctx, id, map[string]any{"idx": idx})
		}(i)

		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = store.Load(ctx, "agent-1")
		}()
	}
	wg.Wait()

	snap, err := store.Load(ctx, "agent-1")
	require.NoError(t, err)
	require.NotNil(t, snap)
}

func TestMemorySnapshotStore_ConcurrentSaveDelete_NoRace(t *testing.T) {
	store := NewMemorySnapshotStore()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id := "agent-1"
			_ = store.Save(ctx, id, map[string]any{"idx": idx})
		}(i)

		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = store.Delete(ctx, "agent-1")
		}()
	}
	wg.Wait()
}
