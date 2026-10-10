package ares_bootstrap

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	aresconfig "github.com/Timwood0x10/ares/internal/ares_config"
	ares_memory "github.com/Timwood0x10/ares/internal/runtime/memory"
	"github.com/Timwood0x10/ares/internal/storage/postgres/embedding"
	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
)

// stubExperienceRepo is a minimal repositories.ExperienceRepositoryInterface
// for wiring tests: no database, zero-value results. It exists so
// wireRetrievers takes the embClient+expRepo branch without PostgreSQL.
type stubExperienceRepo struct{}

func (stubExperienceRepo) Create(context.Context, *storage_models.Experience) error {
	return nil
}

func (stubExperienceRepo) GetByID(context.Context, string, string) (*storage_models.Experience, error) {
	return nil, nil
}

func (stubExperienceRepo) Update(context.Context, *storage_models.Experience) error { return nil }

func (stubExperienceRepo) UpdateEmbedding(context.Context, string, string, []float64, string, int) error {
	return nil
}

func (stubExperienceRepo) Delete(context.Context, string, string) error { return nil }

func (stubExperienceRepo) SearchByVector(context.Context, []float64, string, int) ([]*storage_models.Experience, error) {
	return nil, nil
}

func (stubExperienceRepo) SearchByKeyword(context.Context, string, string, int) ([]*storage_models.Experience, error) {
	return nil, nil
}

func (stubExperienceRepo) IncrementUsageCount(context.Context, string, string) error { return nil }

func (stubExperienceRepo) DecrementRank(context.Context, string, string) error { return nil }

func (stubExperienceRepo) ListByType(context.Context, string, string, int) ([]*storage_models.Experience, error) {
	return nil, nil
}

func (stubExperienceRepo) ListByAgent(context.Context, string, string, int) ([]*storage_models.Experience, error) {
	return nil, nil
}

// TestWireRetrieversInjectsDistillationEngine locks the serve-path memory
// fix: bootstrap builds memory via NewMemoryManager (no distillation
// engine), and wireRetrievers must inject the engine once the embedding
// client and experience repo exist — otherwise memory_search and
// StoreDistilledTask fail with ErrDistillationEngineNotInitialized on every
// call, which surfaces to users as "memory not working".
func TestWireRetrieversInjectsDistillationEngine(t *testing.T) {
	ctx := context.Background()
	mgr, mgrErr := ares_memory.NewMemoryManager(ares_memory.DefaultMemoryConfig())
	require.NoError(t, mgrErr)

	_, before := mgr.SearchSimilarTasks(ctx, "deploy", 3)
	require.ErrorIs(t, before, ares_memory.ErrDistillationEngineNotInitialized,
		"baseline: engine-less manager must return the sentinel")

	// Non-nil embedding client + experience repo take the injection branch.
	// The client points at an unreachable address — injection performs no
	// network I/O; a post-injection search fails at embed time, not at the
	// engine guard, which is exactly the state change under test.
	embClient := embedding.NewEmbeddingClient("http://127.0.0.1:1", "test-model", nil, time.Second)
	cfg := &aresconfig.Config{}
	wireRetrievers(ctx, cfg, mgr, embClient, stubExperienceRepo{}, nil, nil, nil)

	_, after := mgr.SearchSimilarTasks(ctx, "deploy", 3)
	require.Error(t, after, "search still fails (dead embed endpoint)")
	require.NotErrorIs(t, after, ares_memory.ErrDistillationEngineNotInitialized,
		"after wireRetrievers the distillation engine must be armed")
}

// TestWireRetrieversSkipsEngineWithoutDeps pins the other side of the
// contract: minimal configs without embedding/experience deps must leave the
// manager untouched (no injection attempted, sentinel still returned).
func TestWireRetrieversSkipsEngineWithoutDeps(t *testing.T) {
	ctx := context.Background()
	mgr, mgrErr := ares_memory.NewMemoryManager(ares_memory.DefaultMemoryConfig())
	require.NoError(t, mgrErr)

	wireRetrievers(ctx, &aresconfig.Config{}, mgr, nil, nil, nil, nil, nil)

	_, searchErr := mgr.SearchSimilarTasks(ctx, "deploy", 3)
	require.ErrorIs(t, searchErr, ares_memory.ErrDistillationEngineNotInitialized,
		"without deps the engine must stay unarmed")
}
