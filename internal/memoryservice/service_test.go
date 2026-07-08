package memoryservice

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/api/core"
	"github.com/Timwood0x10/ares/internal/ares_events"
	memory "github.com/Timwood0x10/ares/internal/ares_memory"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/truncate"
)

type mockMemoryRepo struct {
	mock.Mock
}

func (m *mockMemoryRepo) CreateSession(ctx context.Context, session *core.Session) error {
	args := m.Called(ctx, session)
	return args.Error(0)
}

func (m *mockMemoryRepo) GetSession(ctx context.Context, sessionID string) (*core.Session, error) {
	args := m.Called(ctx, sessionID)
	return args.Get(0).(*core.Session), args.Error(1)
}

func (m *mockMemoryRepo) UpdateSession(ctx context.Context, session *core.Session) error {
	args := m.Called(ctx, session)
	return args.Error(0)
}

func (m *mockMemoryRepo) DeleteSession(ctx context.Context, sessionID string) error {
	args := m.Called(ctx, sessionID)
	return args.Error(0)
}

func (m *mockMemoryRepo) AddMessage(ctx context.Context, message *core.Message) error {
	args := m.Called(ctx, message)
	return args.Error(0)
}

func (m *mockMemoryRepo) GetMessages(ctx context.Context, sessionID string, pagination *core.PaginationRequest) ([]*core.Message, error) {
	args := m.Called(ctx, sessionID, pagination)
	return args.Get(0).([]*core.Message), args.Error(1)
}

func (m *mockMemoryRepo) StoreDistilledTask(ctx context.Context, task *core.DistilledTask) error {
	args := m.Called(ctx, task)
	return args.Error(0)
}

func (m *mockMemoryRepo) GetDistilledTask(ctx context.Context, taskID string) (*core.DistilledTask, error) {
	args := m.Called(ctx, taskID)
	return args.Get(0).(*core.DistilledTask), args.Error(1)
}

func (m *mockMemoryRepo) SearchSimilarTasks(ctx context.Context, query *core.SearchQuery) ([]*core.SearchResult, error) {
	args := m.Called(ctx, query)
	return args.Get(0).([]*core.SearchResult), args.Error(1)
}

type mockMemoryMgr struct {
	mock.Mock
}

func (m *mockMemoryMgr) CreateSession(ctx context.Context, userID string) (string, error) {
	args := m.Called(ctx, userID)
	return args.String(0), args.Error(1)
}

func (m *mockMemoryMgr) AddMessage(ctx context.Context, sessionID, role, content string) error {
	return m.Called(ctx, sessionID, role, content).Error(0)
}

func (m *mockMemoryMgr) GetMessages(ctx context.Context, sessionID string) ([]memory.Message, error) {
	args := m.Called(ctx, sessionID)
	return args.Get(0).([]memory.Message), args.Error(1)
}

func (m *mockMemoryMgr) AddStructuredMessage(ctx context.Context, sessionID string, msg memory.Message) error {
	return m.Called(ctx, sessionID, msg).Error(0)
}

func (m *mockMemoryMgr) BuildPromptMessages(ctx context.Context, sessionID string) ([]memory.Message, error) {
	args := m.Called(ctx, sessionID)
	return args.Get(0).([]memory.Message), args.Error(1)
}

func (m *mockMemoryMgr) DeleteSession(ctx context.Context, sessionID string) error {
	return m.Called(ctx, sessionID).Error(0)
}

func (m *mockMemoryMgr) BuildContext(ctx context.Context, input string, sessionID string) (string, error) {
	args := m.Called(ctx, input, sessionID)
	return args.String(0), args.Error(1)
}

func (m *mockMemoryMgr) CreateTask(ctx context.Context, sessionID, userID, input string) (string, error) {
	args := m.Called(ctx, sessionID, userID, input)
	return args.String(0), args.Error(1)
}

func (m *mockMemoryMgr) UpdateTaskOutput(ctx context.Context, taskID, output string) error {
	return m.Called(ctx, taskID, output).Error(0)
}

func (m *mockMemoryMgr) DistillTask(ctx context.Context, taskID string) (*models.Task, error) {
	args := m.Called(ctx, taskID)
	return args.Get(0).(*models.Task), args.Error(1)
}

func (m *mockMemoryMgr) StoreDistilledTask(ctx context.Context, taskID string, distilled *models.Task) error {
	return m.Called(ctx, taskID, distilled).Error(0)
}

func (m *mockMemoryMgr) SearchSimilarTasks(ctx context.Context, query string, limit int) ([]*models.Task, error) {
	args := m.Called(ctx, query, limit)
	return args.Get(0).([]*models.Task), args.Error(1)
}

func (m *mockMemoryMgr) GetLatestSessionForLeader(ctx context.Context, leaderID string) (string, error) {
	args := m.Called(ctx, leaderID)
	return args.String(0), args.Error(1)
}

func (m *mockMemoryMgr) Start(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

func (m *mockMemoryMgr) Stop(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

func (m *mockMemoryMgr) SetEventStore(store ares_events.EventStore, streamID string) {
	m.Called(store, streamID)
}

func TestNewService(t *testing.T) {
	t.Run("nil config", func(t *testing.T) {
		s, err := NewService(nil)
		require.Nil(t, s)
		require.ErrorIs(t, err, ErrInvalidConfig)
	})
	t.Run("valid config", func(t *testing.T) {
		s, err := NewService(&Config{})
		require.NoError(t, err)
		require.NotNil(t, s)
		require.NotNil(t, s.config)
	})
	t.Run("nil repos allowed", func(t *testing.T) {
		s, err := NewService(&Config{})
		require.NoError(t, err)
		require.Nil(t, s.repo)
		require.Nil(t, s.memoryMgr)
	})
}

func TestService_CreateSession(t *testing.T) {
	ctx := context.Background()

	t.Run("nil config", func(t *testing.T) {
		s := &Service{}
		id, err := s.CreateSession(ctx, nil)
		require.Empty(t, id)
		require.ErrorIs(t, err, ErrInvalidConfig)
	})
	t.Run("empty user ID", func(t *testing.T) {
		s := &Service{}
		id, err := s.CreateSession(ctx, &core.SessionConfig{UserID: ""})
		require.Empty(t, id)
		require.ErrorIs(t, err, ErrInvalidUserID)
	})
	t.Run("repo error", func(t *testing.T) {
		repo := new(mockMemoryRepo)
		repo.On("CreateSession", ctx, mock.AnythingOfType("*core.Session")).Return(errAssertAnError)
		s := &Service{repo: repo}
		id, err := s.CreateSession(ctx, &core.SessionConfig{UserID: "u1", TenantID: "t1"})
		require.Empty(t, id)
		require.Error(t, err)
	})
	t.Run("success", func(t *testing.T) {
		repo := new(mockMemoryRepo)
		repo.On("CreateSession", ctx, mock.AnythingOfType("*core.Session")).Return(nil)
		s := &Service{repo: repo}
		id, err := s.CreateSession(ctx, &core.SessionConfig{UserID: "u1", TenantID: "t1"})
		require.NotEmpty(t, id)
		require.NoError(t, err)
	})
	t.Run("with expiration", func(t *testing.T) {
		repo := new(mockMemoryRepo)
		var captured *core.Session
		repo.On("CreateSession", ctx, mock.AnythingOfType("*core.Session")).Run(func(args mock.Arguments) {
			captured = args.Get(1).(*core.Session)
		}).Return(nil)
		s := &Service{repo: repo}
		_, err := s.CreateSession(ctx, &core.SessionConfig{UserID: "u1", ExpiresIn: time.Hour})
		require.NoError(t, err)
		require.NotNil(t, captured.ExpiresAt)
	})
}

func TestService_GetSession(t *testing.T) {
	ctx := context.Background()

	t.Run("empty ID", func(t *testing.T) {
		s := &Service{}
		sess, err := s.GetSession(ctx, "")
		require.Nil(t, sess)
		require.ErrorIs(t, err, ErrInvalidSessionID)
	})
	t.Run("session not found", func(t *testing.T) {
		repo := new(mockMemoryRepo)
		repo.On("GetSession", ctx, "bad").Return((*core.Session)(nil), ErrSessionNotFound)
		s := &Service{repo: repo}
		sess, err := s.GetSession(ctx, "bad")
		require.Nil(t, sess)
		require.ErrorIs(t, err, ErrSessionNotFound)
	})
	t.Run("success", func(t *testing.T) {
		repo := new(mockMemoryRepo)
		expected := &core.Session{ID: "s1", UserID: "u1"}
		repo.On("GetSession", ctx, "s1").Return(expected, nil)
		s := &Service{repo: repo}
		sess, err := s.GetSession(ctx, "s1")
		require.NoError(t, err)
		require.Equal(t, "s1", sess.ID)
	})
}

func TestService_DeleteSession(t *testing.T) {
	ctx := context.Background()

	t.Run("empty ID", func(t *testing.T) {
		s := &Service{}
		err := s.DeleteSession(ctx, "")
		require.ErrorIs(t, err, ErrInvalidSessionID)
	})
	t.Run("repo error on get", func(t *testing.T) {
		repo := new(mockMemoryRepo)
		repo.On("GetSession", ctx, "s1").Return((*core.Session)(nil), errAssertAnError)
		s := &Service{repo: repo}
		err := s.DeleteSession(ctx, "s1")
		require.Error(t, err)
	})
	t.Run("success", func(t *testing.T) {
		repo := new(mockMemoryRepo)
		repo.On("GetSession", ctx, "s1").Return(&core.Session{ID: "s1"}, nil)
		repo.On("DeleteSession", ctx, "s1").Return(nil)
		s := &Service{repo: repo}
		err := s.DeleteSession(ctx, "s1")
		require.NoError(t, err)
	})
}

func TestService_AddMessage(t *testing.T) {
	ctx := context.Background()

	t.Run("empty session ID", func(t *testing.T) {
		s := &Service{}
		err := s.AddMessage(ctx, "", core.MessageRoleUser, "hi")
		require.ErrorIs(t, err, ErrInvalidSessionID)
	})
	t.Run("empty role", func(t *testing.T) {
		s := &Service{}
		err := s.AddMessage(ctx, "s1", "", "hi")
		require.ErrorIs(t, err, ErrInvalidRole)
	})
	t.Run("empty content", func(t *testing.T) {
		s := &Service{}
		err := s.AddMessage(ctx, "s1", core.MessageRoleUser, "")
		require.ErrorIs(t, err, ErrInvalidContent)
	})
	t.Run("success", func(t *testing.T) {
		repo := new(mockMemoryRepo)
		repo.On("GetSession", ctx, "s1").Return(&core.Session{ID: "s1"}, nil)
		repo.On("AddMessage", ctx, mock.AnythingOfType("*core.Message")).Return(nil)
		s := &Service{repo: repo}
		err := s.AddMessage(ctx, "s1", core.MessageRoleUser, "hello")
		require.NoError(t, err)
	})
}

func TestService_GetMessages(t *testing.T) {
	ctx := context.Background()

	t.Run("empty session ID", func(t *testing.T) {
		s := &Service{}
		msgs, err := s.GetMessages(ctx, "", nil)
		require.Nil(t, msgs)
		require.ErrorIs(t, err, ErrInvalidSessionID)
	})
	t.Run("nil pagination defaults", func(t *testing.T) {
		repo := new(mockMemoryRepo)
		repo.On("GetSession", ctx, "s1").Return(&core.Session{ID: "s1"}, nil)
		var captured *core.PaginationRequest
		repo.On("GetMessages", ctx, "s1", mock.AnythingOfType("*core.PaginationRequest")).Run(func(args mock.Arguments) {
			captured = args.Get(2).(*core.PaginationRequest)
		}).Return([]*core.Message{}, nil)
		s := &Service{repo: repo}
		_, err := s.GetMessages(ctx, "s1", nil)
		require.NoError(t, err)
		require.Equal(t, 1, captured.Page)
		require.Equal(t, 100, captured.PageSize)
	})
	t.Run("success", func(t *testing.T) {
		repo := new(mockMemoryRepo)
		repo.On("GetSession", ctx, "s1").Return(&core.Session{ID: "s1"}, nil)
		expected := []*core.Message{{ID: "m1", Content: "hi"}}
		repo.On("GetMessages", ctx, "s1", mock.AnythingOfType("*core.PaginationRequest")).Return(expected, nil)
		s := &Service{repo: repo}
		msgs, err := s.GetMessages(ctx, "s1", &core.PaginationRequest{Page: 1, PageSize: 50})
		require.NoError(t, err)
		require.Len(t, msgs, 1)
	})
}

func TestService_DistillTask(t *testing.T) {
	ctx := context.Background()

	t.Run("empty task ID", func(t *testing.T) {
		s := &Service{}
		task, err := s.DistillTask(ctx, "")
		require.Nil(t, task)
		require.ErrorIs(t, err, ErrInvalidTaskID)
	})
	t.Run("with memory manager", func(t *testing.T) {
		repo := new(mockMemoryRepo)
		mgr := new(mockMemoryMgr)
		payload := map[string]any{"input": "in", "output": "out", "context": "ctx"}
		taskModel := &models.Task{TaskID: "t1", TaskType: models.AgentTypeLeader, Payload: payload}
		mgr.On("DistillTask", ctx, "t1").Return(taskModel, nil)
		var stored *core.DistilledTask
		repo.On("StoreDistilledTask", ctx, mock.AnythingOfType("*core.DistilledTask")).Run(func(args mock.Arguments) {
			stored = args.Get(1).(*core.DistilledTask)
		}).Return(nil)
		s := &Service{repo: repo, memoryMgr: mgr}
		task, err := s.DistillTask(ctx, "t1")
		require.NoError(t, err)
		require.NotNil(t, task)
		require.Equal(t, "t1", task.TaskID)
		require.Equal(t, "in", task.Input)
		require.Equal(t, "out", task.Output)
		require.Equal(t, "ctx", task.Context)
		require.NotEmpty(t, task.Summary)
		require.Contains(t, task.Tags, "leader")
		require.NotNil(t, stored)
	})
	t.Run("without memory manager", func(t *testing.T) {
		repo := new(mockMemoryRepo)
		var stored *core.DistilledTask
		repo.On("StoreDistilledTask", ctx, mock.AnythingOfType("*core.DistilledTask")).Run(func(args mock.Arguments) {
			stored = args.Get(1).(*core.DistilledTask)
		}).Return(nil)
		s := &Service{repo: repo}
		task, err := s.DistillTask(ctx, "t1")
		require.NoError(t, err)
		require.Equal(t, "Task distillation without memory manager", task.Summary)
		require.NotNil(t, stored)
	})
}

func TestService_SearchSimilarTasks(t *testing.T) {
	ctx := context.Background()

	t.Run("nil query", func(t *testing.T) {
		s := &Service{}
		results, err := s.SearchSimilarTasks(ctx, nil)
		require.Nil(t, results)
		require.ErrorIs(t, err, ErrInvalidConfig)
	})
	t.Run("empty query text", func(t *testing.T) {
		s := &Service{}
		results, err := s.SearchSimilarTasks(ctx, &core.SearchQuery{Query: ""})
		require.Nil(t, results)
		require.ErrorIs(t, err, ErrInvalidQuery)
	})
	t.Run("default limit", func(t *testing.T) {
		var captured *core.SearchQuery
		repo := new(mockMemoryRepo)
		repo.On("SearchSimilarTasks", ctx, mock.AnythingOfType("*core.SearchQuery")).Run(func(args mock.Arguments) {
			captured = args.Get(1).(*core.SearchQuery)
		}).Return([]*core.SearchResult{}, nil)

		s := &Service{repo: repo}
		_, err := s.SearchSimilarTasks(ctx, &core.SearchQuery{Query: "test", Limit: 0})
		require.NoError(t, err)
		require.Equal(t, 10, captured.Limit)
	})
	t.Run("success", func(t *testing.T) {
		repo := new(mockMemoryRepo)
		results := []*core.SearchResult{{TaskID: "t1", Score: 0.9}}
		repo.On("SearchSimilarTasks", ctx, &core.SearchQuery{Query: "test", Limit: 5}).Return(results, nil)
		s := &Service{repo: repo}
		res, err := s.SearchSimilarTasks(ctx, &core.SearchQuery{Query: "test", Limit: 5})
		require.NoError(t, err)
		require.Len(t, res, 1)
		require.Equal(t, "t1", res[0].TaskID)
	})
}

func TestGenerateSummary(t *testing.T) {
	s := &Service{}
	require.Equal(t, "Empty task", s.generateSummary("", ""))
	require.Equal(t, "Input: hello", s.generateSummary("hello", ""))
	require.Equal(t, "Input: hello | Output: world", s.generateSummary("hello", "world"))
	require.Equal(t, "Output: world", s.generateSummary("", "world"))
}

func TestGenerateTags(t *testing.T) {
	s := &Service{}
	tags := s.generateTags("recommendation", "", "")
	require.Contains(t, tags, "recommendation")

	largeInput := make([]byte, 150)
	for i := range largeInput {
		largeInput[i] = 'a'
	}
	tags = s.generateTags("test", string(largeInput), "")
	require.Contains(t, tags, "long_input")

	tags = s.generateTags("test", "something failed", "")
	require.Contains(t, tags, "error_handling")

	tags = s.generateTags("test", "search for something", "")
	require.Contains(t, tags, "retrieval")

	tags = s.generateTags("test", "generate code", "")
	require.Contains(t, tags, "generation")
}

func TestTruncateString(t *testing.T) {
	require.Equal(t, "short", truncate.WithEllipsis("short", 10))
	result := truncate.WithEllipsis("this is a long string", 10)
	require.Equal(t, "this is...", result)
	require.Len(t, result, 10)
}

var errAssertAnError = errors.New("expected error")

func TestMemoryRepository(t *testing.T) {
	ctx := context.Background()
	r := NewMemoryRepository()

	t.Run("CreateSession and GetSession", func(t *testing.T) {
		sess := &core.Session{ID: "s1", UserID: "u1", Status: "active"}
		err := r.CreateSession(ctx, sess)
		require.NoError(t, err)

		got, err := r.GetSession(ctx, "s1")
		require.NoError(t, err)
		require.Equal(t, "u1", got.UserID)

		err = r.CreateSession(ctx, sess)
		require.Error(t, err)
	})
	t.Run("GetSession not found", func(t *testing.T) {
		_, err := r.GetSession(ctx, "nonexistent")
		require.ErrorIs(t, err, ErrSessionNotFound)
	})
	t.Run("UpdateSession", func(t *testing.T) {
		err := r.UpdateSession(ctx, &core.Session{ID: "s1", UserID: "u1-updated"})
		require.NoError(t, err)
		got, _ := r.GetSession(ctx, "s1")
		require.Equal(t, "u1-updated", got.UserID)
	})
	t.Run("AddMessage and GetMessages", func(t *testing.T) {
		msg := &core.Message{SessionID: "s1", Content: "hello", Role: "user"}
		err := r.AddMessage(ctx, msg)
		require.NoError(t, err)

		msgs, err := r.GetMessages(ctx, "s1", nil)
		require.NoError(t, err)
		require.Len(t, msgs, 1)
		require.Equal(t, "hello", msgs[0].Content)
	})
	t.Run("DeleteSession", func(t *testing.T) {
		r2 := NewMemoryRepository()
		err := r2.CreateSession(ctx, &core.Session{ID: "del1", UserID: "u1"})
		require.NoError(t, err)
		err = r2.AddMessage(ctx, &core.Message{SessionID: "del1", Content: "x", Role: "user"})
		require.NoError(t, err)

		err = r2.DeleteSession(ctx, "del1")
		require.NoError(t, err)

		_, err = r2.GetSession(ctx, "del1")
		require.ErrorIs(t, err, ErrSessionNotFound)

		msgs, _ := r2.GetMessages(ctx, "del1", nil)
		require.Empty(t, msgs)
	})
	t.Run("StoreDistilledTask and GetDistilledTask", func(t *testing.T) {
		task := &core.DistilledTask{TaskID: "t1", Summary: "test"}
		err := r.StoreDistilledTask(ctx, task)
		require.NoError(t, err)

		got, err := r.GetDistilledTask(ctx, "t1")
		require.NoError(t, err)
		require.Equal(t, "test", got.Summary)

		_, err = r.GetDistilledTask(ctx, "nonexistent")
		require.ErrorIs(t, err, ErrTaskNotFound)
	})
	t.Run("SearchSimilarTasks", func(t *testing.T) {
		r3 := NewMemoryRepository()
		err := r3.StoreDistilledTask(ctx, &core.DistilledTask{
			TaskID: "search1", Tags: []string{"important"}, Input: "data",
		})
		require.NoError(t, err)
		err = r3.StoreDistilledTask(ctx, &core.DistilledTask{
			TaskID: "search2", Tags: []string{"other"}, Input: "more",
		})
		require.NoError(t, err)

		results, err := r3.SearchSimilarTasks(ctx, &core.SearchQuery{
			Query: "test",
			Tags:  []string{"important"},
			Limit: 10,
		})
		require.NoError(t, err)
		require.Len(t, results, 2)
		ids := []string{results[0].TaskID, results[1].TaskID}
		require.Contains(t, ids, "search1")
		require.Contains(t, ids, "search2")
	})
}
