// package integration provides end-to-end integration tests with real PostgreSQL.
package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/embedding"
	memory "github.com/Timwood0x10/ares/internal/runtime/memory"
	"github.com/Timwood0x10/ares/internal/runtime/memory/distillation"
)

// testEmbedder is a minimal EmbeddingService mock for in-memory tests.
type testEmbedder struct{}

func (t *testEmbedder) Embed(_ context.Context, _ string) ([]float64, error) {
	return []float64{0.1, 0.2, 0.3}, nil
}

func (t *testEmbedder) EmbedWithPrefix(_ context.Context, _, _ string) ([]float64, error) {
	return []float64{0.1, 0.2, 0.3}, nil
}

func (t *testEmbedder) EmbedBatch(_ context.Context, _ []string) ([][]float64, error) {
	return [][]float64{{0.1, 0.2, 0.3}}, nil
}

func (t *testEmbedder) HealthCheck(_ context.Context) error { return nil }
func (t *testEmbedder) GetModel() string                    { return "test-model" }
func (t *testEmbedder) GetTimeout() time.Duration           { return time.Second }

var _ embedding.EmbeddingService = (*testEmbedder)(nil)

// testExpRepo is a minimal ExperienceRepository mock for in-memory tests.
type testExpRepo struct {
	experiences []distillation.Experience
}

func (r *testExpRepo) SearchByVector(_ context.Context, _ []float64, _ string, _ int) ([]distillation.Experience, error) {
	return r.experiences, nil
}

func (r *testExpRepo) GetByMemoryType(_ context.Context, _ string, _ distillation.MemoryType) ([]distillation.Experience, error) {
	return r.experiences, nil
}

func (r *testExpRepo) CountByMemoryType(_ context.Context, _ string, _ distillation.MemoryType) (int, error) {
	return len(r.experiences), nil
}

func (r *testExpRepo) Update(_ context.Context, _ *distillation.Experience) error { return nil }
func (r *testExpRepo) Delete(_ context.Context, _ string) error                   { return nil }
func (r *testExpRepo) DeleteBatch(_ context.Context, _ []string) error            { return nil }

func (r *testExpRepo) Create(_ context.Context, experience *distillation.Experience) error {
	r.experiences = append(r.experiences, *experience)
	return nil
}

var _ distillation.ExperienceRepository = (*testExpRepo)(nil)

// TestInMemoryMemoryManagerSessionPipeline verifies the in-memory MemoryManager
// session pipeline: CreateSession -> AddMessage -> GetMessages -> BuildContext.
func TestInMemoryMemoryManagerSessionPipeline(t *testing.T) {
	ctx := context.Background()

	config := memory.DefaultMemoryConfig()
	config.MaxHistory = 5

	mgr, err := memory.NewMemoryManager(config)
	require.NoError(t, err)
	require.NotNil(t, mgr)

	require.NoError(t, mgr.Start(ctx))
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, mgr.Stop(stopCtx))
	}()

	// Create a session.
	sessionID, err := mgr.CreateSession(ctx, "user-1")
	require.NoError(t, err)
	require.NotEmpty(t, sessionID)

	// Add messages.
	require.NoError(t, mgr.AddMessage(ctx, sessionID, "user", "Hello"))
	require.NoError(t, mgr.AddMessage(ctx, sessionID, "assistant", "Hi there!"))
	require.NoError(t, mgr.AddMessage(ctx, sessionID, "user", "Help me find a dress"))

	// Get messages.
	messages, err := mgr.GetMessages(ctx, sessionID)
	require.NoError(t, err)
	// Session memory stores messages including a system message from CreateSession.
	assert.GreaterOrEqual(t, len(messages), 3, "expected at least 3 messages")

	// Build context.
	contextStr, err := mgr.BuildContext(ctx, "What about red dresses?", sessionID)
	require.NoError(t, err)
	assert.Contains(t, contextStr, "What about red dresses?", "expected context to include current input")
}

// TestInMemoryMemoryManagerTaskPipeline verifies the in-memory MemoryManager
// task pipeline: CreateTask -> UpdateTaskOutput -> DistillTask -> StoreDistilledTask.
func TestInMemoryMemoryManagerTaskPipeline(t *testing.T) {
	ctx := context.Background()

	config := memory.DefaultMemoryConfig()
	config.VectorDim = 128

	mgr, err := memory.NewMemoryManagerWithDistiller(config, &testEmbedder{}, &testExpRepo{})
	require.NoError(t, err)
	require.NotNil(t, mgr)

	require.NoError(t, mgr.Start(ctx))
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, mgr.Stop(stopCtx))
	}()

	// Create a session.
	sessionID, err := mgr.CreateSession(ctx, "user-1")
	require.NoError(t, err)

	// Create a task.
	taskID, err := mgr.CreateTask(ctx, sessionID, "user-1", "Find blue sneakers")
	require.NoError(t, err)
	require.NotEmpty(t, taskID)

	// Update task output.
	require.NoError(t, mgr.UpdateTaskOutput(ctx, taskID, "Found 5 blue sneaker options"))

	// Distill the task.
	distilled, err := mgr.DistillTask(ctx, taskID)
	require.NoError(t, err)
	require.NotNil(t, distilled)

	// Store the distilled task.
	require.NoError(t, mgr.StoreDistilledTask(ctx, taskID, distilled))

	// Search for similar tasks.
	similar, err := mgr.SearchSimilarTasks(ctx, "blue sneakers", 5)
	require.NoError(t, err)
	assert.NotNil(t, similar, "expected non-nil similar tasks result")
}
