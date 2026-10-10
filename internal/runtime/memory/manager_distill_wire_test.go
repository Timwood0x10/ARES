package memory

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/runtime/memory/distillation"
)

func TestSetDistillationEngineRejectsNilDeps(t *testing.T) {
	mgr, mgrErr := NewMemoryManager(DefaultMemoryConfig())
	require.NoError(t, mgrErr)
	mm, ok := mgr.(*memoryManager)
	require.True(t, ok, "NewMemoryManager must return *memoryManager")

	require.Error(t, mm.SetDistillationEngine(nil, &testExpRepo{}),
		"nil embedder must be rejected")
	require.Error(t, mm.SetDistillationEngine(&testEmbedder{}, nil),
		"nil experience repository must be rejected")
}

func TestDistillationBackedMethodsRequireEngine(t *testing.T) {
	ctx := context.Background()
	mgr, mgrErr := NewMemoryManager(DefaultMemoryConfig())
	require.NoError(t, mgrErr)

	_, searchErr := mgr.SearchSimilarTasks(ctx, "deploy", 5)
	require.ErrorIs(t, searchErr, ErrDistillationEngineNotInitialized,
		"SearchSimilarTasks on an engine-less manager must return the sentinel")

	storeErr := mgr.StoreDistilledTask(ctx, "task-1", &models.Task{
		TaskID:  "task-1",
		Payload: map[string]any{"input": "in", "output": "out"},
	})
	require.ErrorIs(t, storeErr, ErrDistillationEngineNotInitialized,
		"StoreDistilledTask on an engine-less manager must return the sentinel")
}

func TestSetDistillationEngineEnablesSearchAndStore(t *testing.T) {
	ctx := context.Background()
	mgr, mgrErr := NewMemoryManager(DefaultMemoryConfig())
	require.NoError(t, mgrErr)
	mm, ok := mgr.(*memoryManager)
	require.True(t, ok)

	repo := &testExpRepo{experiences: []distillation.Experience{{
		Type:       distillation.MemoryKnowledge,
		Problem:    "deploy service",
		Solution:   "use rolling update",
		Confidence: 0.9,
	}}}
	require.NoError(t, mm.SetDistillationEngine(&testEmbedder{}, repo),
		"injection with valid deps must succeed")

	tasks, searchErr := mgr.SearchSimilarTasks(ctx, "deploy", 5)
	require.NoError(t, searchErr, "search must work after injection")
	require.Len(t, tasks, 1)
	require.Equal(t, "deploy service", tasks[0].Payload["input"])

	storeErr := mgr.StoreDistilledTask(ctx, "task-2", &models.Task{
		TaskID: "task-2",
		Payload: map[string]any{
			"input":  "how to deploy the service",
			"output": "use a rolling update strategy",
		},
	})
	require.NoError(t, storeErr, "store must work after injection")
}

func TestSetDistillationEngineRejectsDoubleInjection(t *testing.T) {
	mgr, mgrErr := NewMemoryManagerWithDistiller(DefaultMemoryConfig(), &testEmbedder{}, &testExpRepo{})
	require.NoError(t, mgrErr)
	mm, ok := mgr.(*memoryManager)
	require.True(t, ok)

	require.Error(t, mm.SetDistillationEngine(&testEmbedder{}, &testExpRepo{}),
		"a manager built by NewMemoryManagerWithDistiller must reject re-injection")
}
