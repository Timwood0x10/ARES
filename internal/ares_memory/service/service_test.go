package memoryapi

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"log/slog"
	"os"

	"github.com/Timwood0x10/ares/internal/ares_events"
	memory "github.com/Timwood0x10/ares/internal/ares_memory"
	"github.com/Timwood0x10/ares/internal/ares_memory/distillation"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/storage/postgres/embedding"
)

// ---------------------------------------------------------------------------
// Mocks
// ---------------------------------------------------------------------------

type mockMemoryManager struct {
	createSessionFn      func(ctx context.Context, userID string) (string, error)
	addMessageFn         func(ctx context.Context, sessionID, role, content string) error
	getMessagesFn        func(ctx context.Context, sessionID string) ([]memory.Message, error)
	deleteSessionFn      func(ctx context.Context, sessionID string) error
	distillTaskFn        func(ctx context.Context, taskID string) (*models.Task, error)
	storeDistilledTaskFn func(ctx context.Context, taskID string, task *models.Task) error
	searchSimilarTasksFn func(ctx context.Context, query string, limit int) ([]*models.Task, error)
}

func (m *mockMemoryManager) CreateSession(ctx context.Context, userID string) (string, error) {
	return m.createSessionFn(ctx, userID)
}
func (m *mockMemoryManager) AddMessage(ctx context.Context, sessionID, role, content string) error {
	return m.addMessageFn(ctx, sessionID, role, content)
}
func (m *mockMemoryManager) GetMessages(ctx context.Context, sessionID string) ([]memory.Message, error) {
	return m.getMessagesFn(ctx, sessionID)
}
func (m *mockMemoryManager) DeleteSession(ctx context.Context, sessionID string) error {
	return m.deleteSessionFn(ctx, sessionID)
}
func (m *mockMemoryManager) DistillTask(ctx context.Context, taskID string) (*models.Task, error) {
	return m.distillTaskFn(ctx, taskID)
}
func (m *mockMemoryManager) StoreDistilledTask(ctx context.Context, taskID string, task *models.Task) error {
	return m.storeDistilledTaskFn(ctx, taskID, task)
}
func (m *mockMemoryManager) SearchSimilarTasks(ctx context.Context, query string, limit int) ([]*models.Task, error) {
	return m.searchSimilarTasksFn(ctx, query, limit)
}

func (m *mockMemoryManager) AddStructuredMessage(ctx context.Context, sessionID string, msg memory.Message) error {
	return nil
}
func (m *mockMemoryManager) BuildPromptMessages(ctx context.Context, sessionID string) ([]memory.Message, error) {
	return nil, nil
}
func (m *mockMemoryManager) BuildContext(ctx context.Context, input string, sessionID string) (string, error) {
	return "", nil
}
func (m *mockMemoryManager) CreateTask(ctx context.Context, sessionID, userID, input string) (string, error) {
	return "", nil
}
func (m *mockMemoryManager) UpdateTaskOutput(ctx context.Context, taskID, output string) error {
	return nil
}
func (m *mockMemoryManager) GetLatestSessionForLeader(ctx context.Context, leaderID string) (string, error) {
	return "", nil
}
func (m *mockMemoryManager) Start(ctx context.Context) error                             { return nil }
func (m *mockMemoryManager) Stop(ctx context.Context) error                              { return nil }
func (m *mockMemoryManager) SetEventStore(store ares_events.EventStore, streamID string) {}

// ---------------------------------------------------------------------------
// Error variable tests
// ---------------------------------------------------------------------------

func init() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
}

func TestErrorVariables(t *testing.T) {
	assert.Equal(t, "invalid user ID", ErrInvalidUserID.Error())
	assert.Equal(t, "invalid session ID", ErrInvalidSessionID.Error())
	assert.Equal(t, "invalid role", ErrInvalidRole.Error())
	assert.Equal(t, "invalid content", ErrInvalidContent.Error())
	assert.Equal(t, "invalid task ID", ErrInvalidTaskID.Error())
	assert.Equal(t, "invalid query", ErrInvalidQuery.Error())
	assert.Equal(t, "invalid limit", ErrInvalidLimit.Error())
	assert.Equal(t, "session not found", ErrSessionNotFound.Error())
	assert.Equal(t, "task not found", ErrTaskNotFound.Error())
	assert.Equal(t, "invalid conversation ID", ErrInvalidConversationID.Error())
	assert.Equal(t, "no messages provided", ErrNoMessages.Error())
	assert.Equal(t, "invalid tenant ID", ErrInvalidTenantID.Error())
	assert.Equal(t, "invalid configuration", ErrInvalidConfig.Error())
	assert.Equal(t, "distillation failed", ErrDistillationFailed.Error())
	assert.Equal(t, "embedding generation failed", ErrEmbeddingFailed.Error())
	assert.Equal(t, "vector search failed", ErrVectorSearchFailed.Error())
	assert.Equal(t, "invalid memory ID", ErrInvalidMemoryID.Error())
	assert.Equal(t, "memory not found", ErrMemoryNotFound.Error())
	assert.Equal(t, "memory update failed", ErrMemoryUpdateFailed.Error())
	assert.Equal(t, "memory deletion failed", ErrMemoryDeleteFailed.Error())
}

// ---------------------------------------------------------------------------
// Service (memory manager wrapper) tests
// ---------------------------------------------------------------------------

func TestNewService(t *testing.T) {
	svc := NewService(nil)
	require.NotNil(t, svc)
}

func TestCreateSession(t *testing.T) {
	svc := NewService(&mockMemoryManager{
		createSessionFn: func(_ context.Context, userID string) (string, error) {
			return "sess-123", nil
		},
	})

	t.Run("success", func(t *testing.T) {
		id, err := svc.CreateSession(context.Background(), "user-1")
		require.NoError(t, err)
		assert.Equal(t, "sess-123", id)
	})

	t.Run("empty user ID", func(t *testing.T) {
		_, err := svc.CreateSession(context.Background(), "")
		assert.ErrorIs(t, err, ErrInvalidUserID)
	})

	t.Run("memory manager error", func(t *testing.T) {
		svcErr := NewService(&mockMemoryManager{
			createSessionFn: func(_ context.Context, userID string) (string, error) {
				return "", errors.New("db error")
			},
		})
		_, err := svcErr.CreateSession(context.Background(), "user-1")
		assert.ErrorContains(t, err, "db error")
	})
}

func TestAddMessage(t *testing.T) {
	svc := NewService(&mockMemoryManager{
		addMessageFn: func(_ context.Context, sessionID, role, content string) error {
			return nil
		},
	})

	t.Run("success", func(t *testing.T) {
		err := svc.AddMessage(context.Background(), "sess-1", "user", "hello")
		require.NoError(t, err)
	})

	t.Run("empty session ID", func(t *testing.T) {
		err := svc.AddMessage(context.Background(), "", "user", "hello")
		assert.ErrorIs(t, err, ErrInvalidSessionID)
	})

	t.Run("session ID too long", func(t *testing.T) {
		long := string(make([]byte, maxSessionIDLen+1))
		err := svc.AddMessage(context.Background(), long, "user", "hello")
		assert.ErrorIs(t, err, ErrInvalidSessionID)
	})

	t.Run("empty role", func(t *testing.T) {
		err := svc.AddMessage(context.Background(), "sess-1", "", "hello")
		assert.ErrorIs(t, err, ErrInvalidRole)
	})

	t.Run("empty content", func(t *testing.T) {
		err := svc.AddMessage(context.Background(), "sess-1", "user", "")
		assert.ErrorIs(t, err, ErrInvalidContent)
	})

	t.Run("memory manager error", func(t *testing.T) {
		svcErr := NewService(&mockMemoryManager{
			addMessageFn: func(_ context.Context, sessionID, role, content string) error {
				return errors.New("store error")
			},
		})
		err := svcErr.AddMessage(context.Background(), "sess-1", "user", "hello")
		assert.ErrorContains(t, err, "store error")
	})
}

func TestGetMessages(t *testing.T) {
	now := time.Now()

	svc := NewService(&mockMemoryManager{
		getMessagesFn: func(_ context.Context, sessionID string) ([]memory.Message, error) {
			return []memory.Message{
				{Role: "user", Content: "hi", Time: now},
				{Role: "assistant", Content: "hello", Time: now},
			}, nil
		},
	})

	t.Run("success", func(t *testing.T) {
		msgs, err := svc.GetMessages(context.Background(), "sess-1")
		require.NoError(t, err)
		require.Len(t, msgs, 2)
		assert.Equal(t, "user", msgs[0].Role)
		assert.Equal(t, "hi", msgs[0].Content)
		assert.Equal(t, "hello", msgs[1].Content)
	})

	t.Run("empty session ID", func(t *testing.T) {
		_, err := svc.GetMessages(context.Background(), "")
		assert.ErrorIs(t, err, ErrInvalidSessionID)
	})

	t.Run("session ID too long", func(t *testing.T) {
		long := string(make([]byte, maxSessionIDLen+1))
		_, err := svc.GetMessages(context.Background(), long)
		assert.ErrorIs(t, err, ErrInvalidSessionID)
	})

	t.Run("empty result set", func(t *testing.T) {
		svcEmpty := NewService(&mockMemoryManager{
			getMessagesFn: func(_ context.Context, sessionID string) ([]memory.Message, error) {
				return []memory.Message{}, nil
			},
		})
		msgs, err := svcEmpty.GetMessages(context.Background(), "sess-1")
		require.NoError(t, err)
		assert.Empty(t, msgs)
	})

	t.Run("nil result set", func(t *testing.T) {
		svcNil := NewService(&mockMemoryManager{
			getMessagesFn: func(_ context.Context, sessionID string) ([]memory.Message, error) {
				return nil, nil
			},
		})
		msgs, err := svcNil.GetMessages(context.Background(), "sess-1")
		require.NoError(t, err)
		assert.Empty(t, msgs)
	})

	t.Run("memory manager error", func(t *testing.T) {
		svcErr := NewService(&mockMemoryManager{
			getMessagesFn: func(_ context.Context, sessionID string) ([]memory.Message, error) {
				return nil, errors.New("fetch error")
			},
		})
		_, err := svcErr.GetMessages(context.Background(), "sess-1")
		assert.ErrorContains(t, err, "fetch error")
	})
}

func TestDeleteSession(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		svc := NewService(&mockMemoryManager{
			deleteSessionFn: func(_ context.Context, sessionID string) error {
				return nil
			},
		})
		err := svc.DeleteSession(context.Background(), "sess-1")
		require.NoError(t, err)
	})

	t.Run("empty session ID", func(t *testing.T) {
		svc := NewService(&mockMemoryManager{})
		err := svc.DeleteSession(context.Background(), "")
		assert.ErrorIs(t, err, ErrInvalidSessionID)
	})

	t.Run("nil memory manager", func(t *testing.T) {
		svc := NewService(nil)
		err := svc.DeleteSession(context.Background(), "sess-1")
		assert.ErrorContains(t, err, "memory manager not configured")
	})

	t.Run("memory manager error", func(t *testing.T) {
		svc := NewService(&mockMemoryManager{
			deleteSessionFn: func(_ context.Context, sessionID string) error {
				return errors.New("delete error")
			},
		})
		err := svc.DeleteSession(context.Background(), "sess-1")
		assert.ErrorContains(t, err, "delete error")
	})
}

func TestDistillTask(t *testing.T) {
	task := &models.Task{TaskID: "task-1"}

	t.Run("success", func(t *testing.T) {
		svc := NewService(&mockMemoryManager{
			distillTaskFn: func(_ context.Context, taskID string) (*models.Task, error) {
				return task, nil
			},
			storeDistilledTaskFn: func(_ context.Context, taskID string, tsk *models.Task) error {
				return nil
			},
		})
		err := svc.DistillTask(context.Background(), "task-1")
		require.NoError(t, err)
	})

	t.Run("empty task ID", func(t *testing.T) {
		svc := NewService(&mockMemoryManager{})
		err := svc.DistillTask(context.Background(), "")
		assert.ErrorIs(t, err, ErrInvalidTaskID)
	})

	t.Run("distill error", func(t *testing.T) {
		svc := NewService(&mockMemoryManager{
			distillTaskFn: func(_ context.Context, taskID string) (*models.Task, error) {
				return nil, errors.New("distill error")
			},
		})
		err := svc.DistillTask(context.Background(), "task-1")
		assert.ErrorContains(t, err, "distill error")
	})

	t.Run("store error", func(t *testing.T) {
		svc := NewService(&mockMemoryManager{
			distillTaskFn: func(_ context.Context, taskID string) (*models.Task, error) {
				return task, nil
			},
			storeDistilledTaskFn: func(_ context.Context, taskID string, tsk *models.Task) error {
				return errors.New("store error")
			},
		})
		err := svc.DistillTask(context.Background(), "task-1")
		assert.ErrorContains(t, err, "store error")
	})
}

func TestSearchSimilarTasks(t *testing.T) {
	now := time.Now()
	tasks := []*models.Task{
		{TaskID: "t1", Payload: map[string]any{"input": "in1", "output": "out1", "context": "ctx1"}, CreatedAt: now},
		{TaskID: "t2", Payload: map[string]any{"input": "in2"}, CreatedAt: now},
	}

	t.Run("success", func(t *testing.T) {
		svc := NewService(&mockMemoryManager{
			searchSimilarTasksFn: func(_ context.Context, q string, limit int) ([]*models.Task, error) {
				return tasks, nil
			},
		})
		results, err := svc.SearchSimilarTasks(context.Background(), "hello", 5)
		require.NoError(t, err)
		require.Len(t, results, 2)
		assert.Equal(t, "t1", results[0].TaskID)
		assert.Equal(t, "in1", results[0].Input)
		assert.Equal(t, "out1", results[0].Output)
		assert.Equal(t, "ctx1", results[0].Context)
		assert.Equal(t, "in2", results[1].Input)
		assert.Empty(t, results[1].Output)
		assert.Empty(t, results[1].Context)
	})

	t.Run("empty query", func(t *testing.T) {
		svc := NewService(&mockMemoryManager{})
		_, err := svc.SearchSimilarTasks(context.Background(), "", 5)
		assert.ErrorIs(t, err, ErrInvalidQuery)
	})

	t.Run("invalid limit", func(t *testing.T) {
		svc := NewService(&mockMemoryManager{})
		_, err := svc.SearchSimilarTasks(context.Background(), "hello", 0)
		assert.ErrorIs(t, err, ErrInvalidLimit)
		_, err = svc.SearchSimilarTasks(context.Background(), "hello", -1)
		assert.ErrorIs(t, err, ErrInvalidLimit)
	})

	t.Run("nil payload", func(t *testing.T) {
		svc := NewService(&mockMemoryManager{
			searchSimilarTasksFn: func(_ context.Context, q string, limit int) ([]*models.Task, error) {
				return []*models.Task{{TaskID: "t3", Payload: nil}}, nil
			},
		})
		results, err := svc.SearchSimilarTasks(context.Background(), "hello", 5)
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Empty(t, results[0].Input)
		assert.Empty(t, results[0].Output)
		assert.Empty(t, results[0].Context)
	})

	t.Run("empty result set", func(t *testing.T) {
		svc := NewService(&mockMemoryManager{
			searchSimilarTasksFn: func(_ context.Context, q string, limit int) ([]*models.Task, error) {
				return []*models.Task{}, nil
			},
		})
		results, err := svc.SearchSimilarTasks(context.Background(), "hello", 5)
		require.NoError(t, err)
		assert.Empty(t, results)
	})

	t.Run("memory manager error", func(t *testing.T) {
		svc := NewService(&mockMemoryManager{
			searchSimilarTasksFn: func(_ context.Context, q string, limit int) ([]*models.Task, error) {
				return nil, errors.New("search error")
			},
		})
		_, err := svc.SearchSimilarTasks(context.Background(), "hello", 5)
		assert.ErrorContains(t, err, "search error")
	})
}

func TestGetPayloadString(t *testing.T) {
	p := map[string]any{"input": "abc", "count": 42, "nested": map[string]any{}}

	assert.Equal(t, "abc", getPayloadString(p, "input"))
	assert.Empty(t, getPayloadString(p, "missing"))
	assert.Empty(t, getPayloadString(p, "count"))
	assert.Empty(t, getPayloadString(p, "nested"))
	assert.Empty(t, getPayloadString(nil, "input"))
}

// ---------------------------------------------------------------------------
// DistillationServiceImpl tests
// ---------------------------------------------------------------------------

type mockDistillExpRepo struct {
	searchByVectorFn    func(ctx context.Context, vector []float64, tenantID string, limit int) ([]distillation.Experience, error)
	getByMemoryTypeFn   func(ctx context.Context, tenantID string, memoryType distillation.MemoryType) ([]distillation.Experience, error)
	countByMemoryTypeFn func(ctx context.Context, tenantID string, memoryType distillation.MemoryType) (int, error)
	updateFn            func(ctx context.Context, experience *distillation.Experience) error
	deleteFn            func(ctx context.Context, id string) error
	deleteBatchFn       func(ctx context.Context, ids []string) error
	createFn            func(ctx context.Context, experience *distillation.Experience) error
}

func (m *mockDistillExpRepo) SearchByVector(ctx context.Context, vector []float64, tenantID string, limit int) ([]distillation.Experience, error) {
	return m.searchByVectorFn(ctx, vector, tenantID, limit)
}
func (m *mockDistillExpRepo) GetByMemoryType(ctx context.Context, tenantID string, memoryType distillation.MemoryType) ([]distillation.Experience, error) {
	return m.getByMemoryTypeFn(ctx, tenantID, memoryType)
}
func (m *mockDistillExpRepo) CountByMemoryType(ctx context.Context, tenantID string, memoryType distillation.MemoryType) (int, error) {
	return m.countByMemoryTypeFn(ctx, tenantID, memoryType)
}
func (m *mockDistillExpRepo) Update(ctx context.Context, experience *distillation.Experience) error {
	return m.updateFn(ctx, experience)
}
func (m *mockDistillExpRepo) Delete(ctx context.Context, id string) error {
	return m.deleteFn(ctx, id)
}
func (m *mockDistillExpRepo) DeleteBatch(ctx context.Context, ids []string) error {
	return m.deleteBatchFn(ctx, ids)
}
func (m *mockDistillExpRepo) Create(ctx context.Context, experience *distillation.Experience) error {
	return m.createFn(ctx, experience)
}

type mockEmbedder struct{}

func (m *mockEmbedder) Embed(_ context.Context, _ string) ([]float64, error) {
	return []float64{0.1, 0.2, 0.3}, nil
}
func (m *mockEmbedder) EmbedWithPrefix(_ context.Context, _, _ string) ([]float64, error) {
	return []float64{0.1, 0.2, 0.3}, nil
}
func (m *mockEmbedder) EmbedBatch(_ context.Context, _ []string) ([][]float64, error) {
	return [][]float64{{0.1, 0.2, 0.3}}, nil
}
func (m *mockEmbedder) HealthCheck(_ context.Context) error { return nil }
func (m *mockEmbedder) GetModel() string                    { return "test" }
func (m *mockEmbedder) GetTimeout() time.Duration           { return time.Second }

var _ embedding.EmbeddingService = (*mockEmbedder)(nil)

type mockAPIRepo struct {
	searchByVectorFn    func(ctx context.Context, vector []float64, tenantID string, limit int) ([]*Experience, error)
	getByMemoryTypeFn   func(ctx context.Context, tenantID string, memoryType MemoryType) ([]*Experience, error)
	countByMemoryTypeFn func(ctx context.Context, tenantID string, memoryType MemoryType) (int, error)
	updateFn            func(ctx context.Context, experience *Experience) error
	deleteFn            func(ctx context.Context, id string) error
	deleteBatchFn       func(ctx context.Context, ids []string) error
	createFn            func(ctx context.Context, experience *Experience) error
	getInternalRepoFn   func() interface{}
}

func (m *mockAPIRepo) SearchByVector(ctx context.Context, vector []float64, tenantID string, limit int) ([]*Experience, error) {
	return m.searchByVectorFn(ctx, vector, tenantID, limit)
}
func (m *mockAPIRepo) GetByMemoryType(ctx context.Context, tenantID string, memoryType MemoryType) ([]*Experience, error) {
	return m.getByMemoryTypeFn(ctx, tenantID, memoryType)
}
func (m *mockAPIRepo) CountByMemoryType(ctx context.Context, tenantID string, memoryType MemoryType) (int, error) {
	return m.countByMemoryTypeFn(ctx, tenantID, memoryType)
}
func (m *mockAPIRepo) Update(ctx context.Context, experience *Experience) error {
	return m.updateFn(ctx, experience)
}
func (m *mockAPIRepo) Delete(ctx context.Context, id string) error {
	return m.deleteFn(ctx, id)
}
func (m *mockAPIRepo) DeleteBatch(ctx context.Context, ids []string) error {
	return m.deleteBatchFn(ctx, ids)
}
func (m *mockAPIRepo) Create(ctx context.Context, experience *Experience) error {
	return m.createFn(ctx, experience)
}
func (m *mockAPIRepo) GetInternalRepository() interface{} {
	return m.getInternalRepoFn()
}

func TestNewDistillationService(t *testing.T) {
	t.Run("with distiller", func(t *testing.T) {
		distiller := distillation.NewDistiller(
			distillation.DefaultDistillationConfig(),
			&mockEmbedder{},
			&mockDistillExpRepo{
				searchByVectorFn: func(_ context.Context, _ []float64, _ string, _ int) ([]distillation.Experience, error) {
					return nil, nil
				},
			},
		)
		ds := NewDistillationService(distiller)
		require.NotNil(t, ds)
		assert.NotNil(t, ds.GetConfig())
		assert.NotNil(t, ds.GetMetrics())
	})

	t.Run("nil distiller", func(t *testing.T) {
		ds := NewDistillationService(nil)
		assert.Nil(t, ds)
	})
}

func TestNewDistillationServiceWithEmbedder(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		repo := &mockAPIRepo{}
		ds, err := NewDistillationServiceWithEmbedder(nil, &mockEmbedder{}, repo)
		require.NoError(t, err)
		require.NotNil(t, ds)
		assert.NotNil(t, ds.GetConfig())
		assert.NotNil(t, ds.GetDistiller())
	})

	t.Run("nil config uses defaults", func(t *testing.T) {
		repo := &mockAPIRepo{}
		ds, err := NewDistillationServiceWithEmbedder(nil, &mockEmbedder{}, repo)
		require.NoError(t, err)
		cfg := ds.GetConfig()
		assert.Equal(t, 0.6, cfg.MinImportance)
	})
}

func TestDistillationServiceImpl_GetDistiller(t *testing.T) {
	distiller := distillation.NewDistiller(
		distillation.DefaultDistillationConfig(),
		&mockEmbedder{},
		&mockDistillExpRepo{
			searchByVectorFn: func(_ context.Context, _ []float64, _ string, _ int) ([]distillation.Experience, error) {
				return nil, nil
			},
		},
	)
	ds := NewDistillationService(distiller)
	d := ds.GetDistiller()
	assert.NotNil(t, d)
	_, ok := d.(*distillation.Distiller)
	assert.True(t, ok)
}

func TestDistillationServiceImpl_DistillConversation(t *testing.T) {
	distiller := distillation.NewDistiller(
		distillation.DefaultDistillationConfig(),
		&mockEmbedder{},
		&mockDistillExpRepo{
			searchByVectorFn: func(_ context.Context, _ []float64, _ string, _ int) ([]distillation.Experience, error) {
				return nil, nil
			},
		},
	)
	ds := NewDistillationService(distiller)
	msgs := []ConversationMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi there"},
	}

	t.Run("success", func(t *testing.T) {
		mems, err := ds.DistillConversation(context.Background(), "conv-1", msgs, "tenant-1", "user-1")
		require.NoError(t, err)
		assert.NotNil(t, mems)
	})

	t.Run("empty conversation ID", func(t *testing.T) {
		_, err := ds.DistillConversation(context.Background(), "", msgs, "tenant-1", "user-1")
		assert.ErrorIs(t, err, ErrInvalidConversationID)
	})

	t.Run("no messages", func(t *testing.T) {
		_, err := ds.DistillConversation(context.Background(), "conv-1", nil, "tenant-1", "user-1")
		assert.ErrorIs(t, err, ErrNoMessages)
	})

	t.Run("empty tenant ID", func(t *testing.T) {
		_, err := ds.DistillConversation(context.Background(), "conv-1", msgs, "", "user-1")
		assert.ErrorIs(t, err, ErrInvalidTenantID)
	})
}

func TestDistillationServiceImpl_GetMetrics(t *testing.T) {
	t.Run("with distiller", func(t *testing.T) {
		distiller := distillation.NewDistiller(
			distillation.DefaultDistillationConfig(),
			&mockEmbedder{},
			&mockDistillExpRepo{
				searchByVectorFn: func(_ context.Context, _ []float64, _ string, _ int) ([]distillation.Experience, error) {
					return nil, nil
				},
			},
		)
		ds := NewDistillationService(distiller)
		m := ds.GetMetrics()
		assert.NotNil(t, m)
		assert.Equal(t, int64(0), m.AttemptTotal)
	})
}

func TestDistillationServiceImpl_ResetMetrics(t *testing.T) {
	t.Run("with distiller", func(t *testing.T) {
		distiller := distillation.NewDistiller(
			distillation.DefaultDistillationConfig(),
			&mockEmbedder{},
			&mockDistillExpRepo{
				searchByVectorFn: func(_ context.Context, _ []float64, _ string, _ int) ([]distillation.Experience, error) {
					return nil, nil
				},
			},
		)
		ds := NewDistillationService(distiller)

		require.NotPanics(t, func() { ds.ResetMetrics() })
		m := ds.GetMetrics()
		assert.Equal(t, int64(0), m.AttemptTotal)
	})
}

func TestDistillationServiceImpl_GetConfig(t *testing.T) {
	distiller := distillation.NewDistiller(
		distillation.DefaultDistillationConfig(),
		&mockEmbedder{},
		&mockDistillExpRepo{
			searchByVectorFn: func(_ context.Context, _ []float64, _ string, _ int) ([]distillation.Experience, error) {
				return nil, nil
			},
		},
	)
	ds := NewDistillationService(distiller)
	cfg := ds.GetConfig()
	require.NotNil(t, cfg)
	assert.Equal(t, 0.6, cfg.MinImportance)
	assert.Equal(t, 3, cfg.MaxMemoriesPerDistillation)
}

func TestDistillationServiceImpl_UpdateConfig(t *testing.T) {
	distiller := distillation.NewDistiller(
		distillation.DefaultDistillationConfig(),
		&mockEmbedder{},
		&mockDistillExpRepo{
			searchByVectorFn: func(_ context.Context, _ []float64, _ string, _ int) ([]distillation.Experience, error) {
				return nil, nil
			},
		},
	)
	ds := NewDistillationService(distiller)

	t.Run("valid config", func(t *testing.T) {
		newCfg := &DistillationConfig{MinImportance: 0.9, MaxMemoriesPerDistillation: 5}
		err := ds.UpdateConfig(newCfg)
		require.NoError(t, err)
		assert.Equal(t, 0.9, ds.GetConfig().MinImportance)
	})

	t.Run("nil config", func(t *testing.T) {
		err := ds.UpdateConfig(nil)
		assert.ErrorIs(t, err, ErrInvalidConfig)
	})
}

func TestDistillationServiceImpl_UpdateMemory(t *testing.T) {
	t.Run("empty memory ID", func(t *testing.T) {
		ds := &DistillationServiceImpl{}
		err := ds.UpdateMemory(context.Background(), "", map[string]interface{}{"key": "val"})
		assert.ErrorIs(t, err, ErrInvalidMemoryID)
	})

	t.Run("no updates", func(t *testing.T) {
		ds := &DistillationServiceImpl{}
		err := ds.UpdateMemory(context.Background(), "mem-1", map[string]interface{}{})
		require.NoError(t, err)
	})

	t.Run("nil repo", func(t *testing.T) {
		ds := &DistillationServiceImpl{repo: nil}
		err := ds.UpdateMemory(context.Background(), "mem-1", map[string]interface{}{"key": "val"})
		assert.ErrorIs(t, err, ErrMemoryUpdateFailed)
	})

	t.Run("repo without internal update support", func(t *testing.T) {
		ds := &DistillationServiceImpl{
			repo: &mockAPIRepo{
				getInternalRepoFn: func() interface{} {
					return "not-a-repo"
				},
			},
		}
		err := ds.UpdateMemory(context.Background(), "mem-1", map[string]interface{}{"key": "val"})
		assert.ErrorContains(t, err, "does not support memory updates")
	})

	t.Run("repo with internal update support", func(t *testing.T) {
		ds := &DistillationServiceImpl{
			repo: &mockAPIRepo{
				getInternalRepoFn: func() interface{} {
					return &mockInternalMemoryRepo{}
				},
			},
		}
		err := ds.UpdateMemory(context.Background(), "mem-1", map[string]interface{}{"key": "val"})
		require.NoError(t, err)
	})

	t.Run("repo with internal update that fails", func(t *testing.T) {
		ds := &DistillationServiceImpl{
			repo: &mockAPIRepo{
				getInternalRepoFn: func() interface{} {
					return &mockInternalMemoryRepo{updateErr: fmt.Errorf("internal error")}
				},
			},
		}
		err := ds.UpdateMemory(context.Background(), "mem-1", map[string]interface{}{"key": "val"})
		assert.ErrorContains(t, err, "internal error")
	})
}

type mockInternalMemoryRepo struct {
	updateErr error
}

func (m *mockInternalMemoryRepo) UpdateMemory(_ context.Context, _ string, _ map[string]interface{}) error {
	return m.updateErr
}

func TestDistillationServiceImpl_DeleteMemory(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		ds := &DistillationServiceImpl{
			repo: &mockAPIRepo{
				deleteFn: func(_ context.Context, id string) error {
					return nil
				},
			},
		}
		err := ds.DeleteMemory(context.Background(), "mem-1")
		require.NoError(t, err)
	})

	t.Run("empty memory ID", func(t *testing.T) {
		ds := &DistillationServiceImpl{}
		err := ds.DeleteMemory(context.Background(), "")
		assert.ErrorIs(t, err, ErrInvalidMemoryID)
	})

	t.Run("nil repo", func(t *testing.T) {
		ds := &DistillationServiceImpl{repo: nil}
		err := ds.DeleteMemory(context.Background(), "mem-1")
		assert.ErrorIs(t, err, ErrMemoryDeleteFailed)
	})

	t.Run("repo error", func(t *testing.T) {
		ds := &DistillationServiceImpl{
			repo: &mockAPIRepo{
				deleteFn: func(_ context.Context, id string) error {
					return errors.New("delete failed")
				},
			},
		}
		err := ds.DeleteMemory(context.Background(), "mem-1")
		assert.ErrorContains(t, err, "delete failed")
	})
}

func TestDistillationServiceImpl_SearchMemories(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		ds := &DistillationServiceImpl{
			embedder: &mockEmbedder{},
			repo: &mockAPIRepo{
				searchByVectorFn: func(_ context.Context, _ []float64, _ string, _ int) ([]*Experience, error) {
					return []*Experience{
						{Problem: "p1", Solution: "s1", Confidence: 0.9, ExtractionMethod: ExtractionDirect},
					}, nil
				},
			},
		}
		mems, err := ds.SearchMemories(context.Background(), "query", "tenant-1", 5)
		require.NoError(t, err)
		require.Len(t, mems, 1)
		assert.Contains(t, mems[0].Content, "p1")
		assert.Contains(t, mems[0].Content, "s1")
		assert.Equal(t, 0.9, mems[0].Importance)
	})

	t.Run("empty query", func(t *testing.T) {
		ds := &DistillationServiceImpl{}
		_, err := ds.SearchMemories(context.Background(), "", "tenant-1", 5)
		assert.ErrorIs(t, err, ErrInvalidQuery)
	})

	t.Run("invalid limit", func(t *testing.T) {
		ds := &DistillationServiceImpl{}
		_, err := ds.SearchMemories(context.Background(), "query", "tenant-1", 0)
		assert.ErrorIs(t, err, ErrInvalidLimit)
	})

	t.Run("nil embedder", func(t *testing.T) {
		ds := &DistillationServiceImpl{embedder: nil, repo: &mockAPIRepo{}}
		_, err := ds.SearchMemories(context.Background(), "query", "tenant-1", 5)
		assert.ErrorIs(t, err, ErrVectorSearchFailed)
	})

	t.Run("nil repo", func(t *testing.T) {
		ds := &DistillationServiceImpl{embedder: &mockEmbedder{}, repo: nil}
		_, err := ds.SearchMemories(context.Background(), "query", "tenant-1", 5)
		assert.ErrorIs(t, err, ErrVectorSearchFailed)
	})

	t.Run("embedder error", func(t *testing.T) {
		ds := &DistillationServiceImpl{
			embedder: &mockEmbedderErr{},
			repo:     &mockAPIRepo{},
		}
		_, err := ds.SearchMemories(context.Background(), "query", "tenant-1", 5)
		assert.ErrorContains(t, err, "embed error")
	})

	t.Run("repo search error", func(t *testing.T) {
		ds := &DistillationServiceImpl{
			embedder: &mockEmbedder{},
			repo: &mockAPIRepo{
				searchByVectorFn: func(_ context.Context, _ []float64, _ string, _ int) ([]*Experience, error) {
					return nil, errors.New("repo error")
				},
			},
		}
		_, err := ds.SearchMemories(context.Background(), "query", "tenant-1", 5)
		assert.ErrorContains(t, err, "repo error")
	})

	t.Run("empty success with no results", func(t *testing.T) {
		ds := &DistillationServiceImpl{
			embedder: &mockEmbedder{},
			repo: &mockAPIRepo{
				searchByVectorFn: func(_ context.Context, _ []float64, _ string, _ int) ([]*Experience, error) {
					return []*Experience{}, nil
				},
			},
		}
		mems, err := ds.SearchMemories(context.Background(), "query", "tenant-1", 5)
		require.NoError(t, err)
		assert.Empty(t, mems)
	})
}

type mockEmbedderErr struct{}

func (m *mockEmbedderErr) Embed(_ context.Context, _ string) ([]float64, error) {
	return nil, errors.New("embed error")
}
func (m *mockEmbedderErr) EmbedWithPrefix(_ context.Context, _, _ string) ([]float64, error) {
	return nil, errors.New("embed error")
}
func (m *mockEmbedderErr) EmbedBatch(_ context.Context, _ []string) ([][]float64, error) {
	return nil, errors.New("embed error")
}
func (m *mockEmbedderErr) HealthCheck(_ context.Context) error { return errors.New("embed error") }
func (m *mockEmbedderErr) GetModel() string                    { return "test" }
func (m *mockEmbedderErr) GetTimeout() time.Duration           { return time.Second }

var _ embedding.EmbeddingService = (*mockEmbedderErr)(nil)

// ---------------------------------------------------------------------------
// experienceRepositoryAdapter tests
// ---------------------------------------------------------------------------

func TestExperienceRepositoryAdapter_SearchByVector(t *testing.T) {
	adapter := &experienceRepositoryAdapter{
		apiRepo: &mockAPIRepo{
			searchByVectorFn: func(_ context.Context, _ []float64, _ string, _ int) ([]*Experience, error) {
				return []*Experience{
					{Problem: "p", Solution: "s", Confidence: 0.8, ExtractionMethod: ExtractionDirect},
				}, nil
			},
		},
	}
	results, err := adapter.SearchByVector(context.Background(), []float64{0.1}, "t", 1)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "p", results[0].Problem)
	assert.Equal(t, "s", results[0].Solution)
	assert.Equal(t, 0.8, results[0].Confidence)
	assert.Equal(t, distillation.ExtractionMethod("direct"), results[0].ExtractionMethod)
}

func TestExperienceRepositoryAdapter_SearchByVector_Error(t *testing.T) {
	adapter := &experienceRepositoryAdapter{
		apiRepo: &mockAPIRepo{
			searchByVectorFn: func(_ context.Context, _ []float64, _ string, _ int) ([]*Experience, error) {
				return nil, errors.New("search failed")
			},
		},
	}
	_, err := adapter.SearchByVector(context.Background(), []float64{0.1}, "t", 1)
	assert.ErrorContains(t, err, "search failed")
}

func TestExperienceRepositoryAdapter_GetByMemoryType(t *testing.T) {
	adapter := &experienceRepositoryAdapter{
		apiRepo: &mockAPIRepo{
			getByMemoryTypeFn: func(_ context.Context, _ string, _ MemoryType) ([]*Experience, error) {
				return []*Experience{{Problem: "p"}}, nil
			},
		},
	}
	results, err := adapter.GetByMemoryType(context.Background(), "t", distillation.MemoryKnowledge)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "p", results[0].Problem)
}

func TestExperienceRepositoryAdapter_GetByMemoryType_Error(t *testing.T) {
	adapter := &experienceRepositoryAdapter{
		apiRepo: &mockAPIRepo{
			getByMemoryTypeFn: func(_ context.Context, _ string, _ MemoryType) ([]*Experience, error) {
				return nil, errors.New("get failed")
			},
		},
	}
	_, err := adapter.GetByMemoryType(context.Background(), "t", distillation.MemoryKnowledge)
	assert.ErrorContains(t, err, "get failed")
}

func TestExperienceRepositoryAdapter_CountByMemoryType(t *testing.T) {
	adapter := &experienceRepositoryAdapter{
		apiRepo: &mockAPIRepo{
			countByMemoryTypeFn: func(_ context.Context, _ string, _ MemoryType) (int, error) {
				return 42, nil
			},
		},
	}
	n, err := adapter.CountByMemoryType(context.Background(), "t", distillation.MemoryKnowledge)
	require.NoError(t, err)
	assert.Equal(t, 42, n)
}

func TestExperienceRepositoryAdapter_Update(t *testing.T) {
	adapter := &experienceRepositoryAdapter{
		apiRepo: &mockAPIRepo{
			updateFn: func(_ context.Context, exp *Experience) error {
				assert.Equal(t, "prob", exp.Problem)
				return nil
			},
		},
	}
	err := adapter.Update(context.Background(), &distillation.Experience{Problem: "prob"})
	require.NoError(t, err)
}

func TestExperienceRepositoryAdapter_Delete(t *testing.T) {
	adapter := &experienceRepositoryAdapter{
		apiRepo: &mockAPIRepo{
			deleteFn: func(_ context.Context, id string) error {
				assert.Equal(t, "id-1", id)
				return nil
			},
		},
	}
	err := adapter.Delete(context.Background(), "id-1")
	require.NoError(t, err)
}

func TestExperienceRepositoryAdapter_DeleteBatch(t *testing.T) {
	adapter := &experienceRepositoryAdapter{
		apiRepo: &mockAPIRepo{
			deleteBatchFn: func(_ context.Context, ids []string) error {
				assert.Equal(t, []string{"a", "b"}, ids)
				return nil
			},
		},
	}
	err := adapter.DeleteBatch(context.Background(), []string{"a", "b"})
	require.NoError(t, err)
}

func TestExperienceRepositoryAdapter_Create(t *testing.T) {
	adapter := &experienceRepositoryAdapter{
		apiRepo: &mockAPIRepo{
			createFn: func(_ context.Context, exp *Experience) error {
				assert.Equal(t, "prob", exp.Problem)
				assert.Equal(t, ExtractionMethod("cross-turn"), exp.ExtractionMethod)
				return nil
			},
		},
	}
	err := adapter.Create(context.Background(), &distillation.Experience{
		Problem:          "prob",
		ExtractionMethod: distillation.ExtractionCrossTurn,
	})
	require.NoError(t, err)
}

func TestExperienceRepositoryAdapter_Create_Error(t *testing.T) {
	adapter := &experienceRepositoryAdapter{
		apiRepo: &mockAPIRepo{
			createFn: func(_ context.Context, exp *Experience) error {
				return errors.New("create failed")
			},
		},
	}
	err := adapter.Create(context.Background(), &distillation.Experience{})
	assert.ErrorContains(t, err, "create failed")
}

// ---------------------------------------------------------------------------
// Conversion helpers
// ---------------------------------------------------------------------------

func TestConvertToAPIDistilledMemory(t *testing.T) {
	t.Run("nil input", func(t *testing.T) {
		assert.Nil(t, convertToAPIDistilledMemory(nil))
	})

	t.Run("with non-zero ExpiresAt", func(t *testing.T) {
		now := time.Now()
		mem := &distillation.Memory{
			ID: "mem-1", Type: distillation.MemoryKnowledge, Content: "content",
			Importance: 0.8, Source: "conv-1", CreatedAt: now, ExpiresAt: now.Add(time.Hour),
			Metadata: map[string]interface{}{"tenant_id": "t1", "user_id": "u1"},
		}
		api := convertToAPIDistilledMemory(mem)
		require.NotNil(t, api)
		assert.Equal(t, "mem-1", api.ID)
		assert.Equal(t, MemoryKnowledge, api.Type)
		assert.Equal(t, "content", api.Content)
		assert.Equal(t, 0.8, api.Importance)
		assert.Equal(t, "conv-1", api.Source)
		assert.Equal(t, "t1", api.TenantID)
		assert.Equal(t, "u1", api.UserID)
		assert.NotNil(t, api.ExpiresAt)
	})

	t.Run("with zero ExpiresAt", func(t *testing.T) {
		mem := &distillation.Memory{
			ExpiresAt: time.Time{},
			Metadata:  map[string]interface{}{},
		}
		api := convertToAPIDistilledMemory(mem)
		assert.Nil(t, api.ExpiresAt)
	})
}

func TestConvertFromAPIDistillationConfig(t *testing.T) {
	t.Run("nil config returns defaults", func(t *testing.T) {
		ic := convertFromAPIDistillationConfig(nil)
		require.NotNil(t, ic)
		assert.Equal(t, 0.6, ic.MinImportance)
	})

	t.Run("full conversion", func(t *testing.T) {
		api := &DistillationConfig{
			MinImportance:              0.7,
			ConflictThreshold:          0.9,
			MaxMemoriesPerDistillation: 5,
			MaxSolutionsPerTenant:      100,
			EnableCodeFilter:           false,
			EnableStacktraceFilter:     false,
			EnableLogFilter:            false,
			EnableMarkdownTableFilter:  false,
			EnableCrossTurnExtraction:  false,
			EnableLengthBonus:          false,
			LengthThreshold:            50,
			LengthBonus:                0.2,
			TopNBeforeConflict:         false,
			ConflictSearchLimit:        10,
			PrecisionOverRecall:        false,
		}
		ic := convertFromAPIDistillationConfig(api)
		assert.Equal(t, 0.7, ic.MinImportance)
		assert.Equal(t, 0.9, ic.ConflictThreshold)
		assert.Equal(t, 5, ic.MaxMemoriesPerDistillation)
		assert.Equal(t, 100, ic.MaxSolutionsPerTenant)
		assert.Equal(t, false, ic.EnableCodeFilter)
		assert.Equal(t, false, ic.EnableStacktraceFilter)
		assert.Equal(t, false, ic.EnableLogFilter)
		assert.Equal(t, false, ic.EnableMarkdownTableFilter)
		assert.Equal(t, false, ic.EnableCrossTurnExtraction)
		assert.Equal(t, false, ic.EnableLengthBonus)
		assert.Equal(t, 50, ic.LengthThreshold)
		assert.Equal(t, 0.2, ic.LengthBonus)
		assert.Equal(t, false, ic.TopNBeforeConflict)
		assert.Equal(t, 10, ic.ConflictSearchLimit)
		assert.Equal(t, false, ic.PrecisionOverRecall)
	})
}

func TestConvertToAPIDistillationMetrics(t *testing.T) {
	t.Run("nil input", func(t *testing.T) {
		m := convertToAPIDistillationMetrics(nil)
		require.NotNil(t, m)
		assert.Equal(t, int64(0), m.AttemptTotal)
	})

	t.Run("full conversion", func(t *testing.T) {
		internal := &distillation.DistillationMetrics{
			AttemptTotal: 10, SuccessTotal: 8, FilteredNoise: 2,
			FilteredSecurity: 1, ConflictResolved: 3, MemoriesCreated: 8,
		}
		m := convertToAPIDistillationMetrics(internal)
		assert.Equal(t, int64(10), m.AttemptTotal)
		assert.Equal(t, int64(8), m.SuccessTotal)
		assert.Equal(t, int64(2), m.FilteredNoise)
		assert.Equal(t, int64(1), m.FilteredSecurity)
		assert.Equal(t, int64(3), m.ConflictResolved)
		assert.Equal(t, int64(8), m.MemoriesCreated)
	})
}

func TestGetMetadataString(t *testing.T) {
	m := map[string]interface{}{"key1": "val1", "key2": 42, "key3": map[string]interface{}{}}

	assert.Equal(t, "val1", getMetadataString(m, "key1"))
	assert.Empty(t, getMetadataString(m, "missing"))
	assert.Empty(t, getMetadataString(m, "key2"))
	assert.Empty(t, getMetadataString(m, "key3"))
	assert.Empty(t, getMetadataString(nil, "key1"))
}

func TestDefaultDistillationConfig(t *testing.T) {
	cfg := defaultDistillationConfig()
	require.NotNil(t, cfg)
	assert.Equal(t, 0.6, cfg.MinImportance)
	assert.Equal(t, 0.85, cfg.ConflictThreshold)
	assert.Equal(t, 3, cfg.MaxMemoriesPerDistillation)
	assert.Equal(t, 5000, cfg.MaxSolutionsPerTenant)
	assert.True(t, cfg.EnableCodeFilter)
	assert.True(t, cfg.EnableStacktraceFilter)
	assert.True(t, cfg.EnableLogFilter)
	assert.True(t, cfg.EnableMarkdownTableFilter)
	assert.True(t, cfg.EnableCrossTurnExtraction)
	assert.True(t, cfg.EnableLengthBonus)
	assert.Equal(t, 60, cfg.LengthThreshold)
	assert.Equal(t, 0.1, cfg.LengthBonus)
	assert.True(t, cfg.TopNBeforeConflict)
	assert.Equal(t, 5, cfg.ConflictSearchLimit)
	assert.True(t, cfg.PrecisionOverRecall)
}

// ---------------------------------------------------------------------------
// Unused interface compliance checks
// ---------------------------------------------------------------------------

var _ memory.MemoryManager = (*mockMemoryManager)(nil)
var _ distillation.ExperienceRepository = (*mockDistillExpRepo)(nil)
