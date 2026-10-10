package experience

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/llm"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
	"github.com/Timwood0x10/ares/internal/storage/postgres/embedding"
	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
)

// fakeLLMServer returns a canned OpenRouter-style completion so
// DistillationService.extractExperience never hits a real LLM.
func fakeLLMServer(t *testing.T, content string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": content}},
			},
		})
	}))
	return srv
}

// fakeEmbedServer returns a canned embedding response for a fixed dimension.
func fakeEmbedServer(t *testing.T, dim int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embedding": make([]float64, dim),
			"dimension": dim,
		})
	}))
	return srv
}

// fakeExpRepo records Create/Update calls and assigns IDs, satisfying
// ExperienceRepositoryInterface for unit tests.
type fakeExpRepo struct {
	created          []*storage_models.Experience
	updated          []*storage_models.Experience
	embeddingUpdates []embeddingUpdate
	idSeq            int
}

// embeddingUpdate captures one narrow vector write-back.
type embeddingUpdate struct {
	TenantID  string
	ID        string
	Embedding []float64
	Model     string
	Version   int
}

func (f *fakeExpRepo) Create(_ context.Context, exp *storage_models.Experience) error {
	f.idSeq++
	exp.ID = "exp-" + string(rune('0'+f.idSeq))
	f.created = append(f.created, exp)
	return nil
}

func (f *fakeExpRepo) Update(_ context.Context, exp *storage_models.Experience) error {
	f.updated = append(f.updated, exp)
	return nil
}

func (f *fakeExpRepo) UpdateEmbedding(
	_ context.Context,
	tenantID, id string,
	vec []float64,
	model string,
	version int,
) error {
	f.embeddingUpdates = append(f.embeddingUpdates, embeddingUpdate{
		TenantID:  tenantID,
		ID:        id,
		Embedding: vec,
		Model:     model,
		Version:   version,
	})
	return nil
}
func (f *fakeExpRepo) GetByID(_ context.Context, _, _ string) (*storage_models.Experience, error) {
	return nil, nil
}
func (f *fakeExpRepo) Delete(_ context.Context, _, _ string) error { return nil }
func (f *fakeExpRepo) SearchByVector(_ context.Context, _ []float64, _ string, _ int) ([]*storage_models.Experience, error) {
	return nil, nil
}
func (f *fakeExpRepo) SearchByKeyword(_ context.Context, _, _ string, _ int) ([]*storage_models.Experience, error) {
	return nil, nil
}
func (f *fakeExpRepo) IncrementUsageCount(_ context.Context, _, _ string) error { return nil }
func (f *fakeExpRepo) DecrementRank(_ context.Context, _, _ string) error       { return nil }
func (f *fakeExpRepo) ListByType(_ context.Context, _, _ string, _ int) ([]*storage_models.Experience, error) {
	return nil, nil
}
func (f *fakeExpRepo) ListByAgent(_ context.Context, _, _ string, _ int) ([]*storage_models.Experience, error) {
	return nil, nil
}

// fakeEnqueuer records enqueued tasks and can inject a failure.
type fakeEnqueuer struct {
	called []*EmbeddingTask
	err    error
}

func (f *fakeEnqueuer) Enqueue(_ context.Context, task *EmbeddingTask) error {
	f.called = append(f.called, task)
	return f.err
}

func newTestLLM(t *testing.T, content string) *llm.Client {
	t.Helper()
	srv := fakeLLMServer(t, content)
	t.Cleanup(srv.Close)
	client, err := llm.NewClient(&llm.Config{
		Provider: "openrouter",
		APIKey:   "test-key",
		BaseURL:  srv.URL,
		Model:    "test-model",
		Timeout:  5,
	})
	require.NoError(t, err)
	return client
}

func distillableTask() *TaskResult {
	return &TaskResult{
		Task:     "A sufficiently long task description",
		Result:   "A result description that is long enough to qualify",
		Context:  "some context",
		TenantID: "tenant-1",
		AgentID:  "agent-1",
		Success:  true,
	}
}

const llmExtractionContent = "Problem: how to sort things\nSolution: use quicksort\nConstraints: stable"

// TestDistill_WithEmbeddingEnqueuerEnqueuesAsyncBackfill verifies the async
// embedding path:
// when an async enqueuer is wired the service persists the row without a vector
// and enqueues a backfill task (rather than embedding synchronously).
func TestDistill_WithEmbeddingEnqueuerEnqueuesAsyncBackfill(t *testing.T) {
	llmClient := newTestLLM(t, llmExtractionContent)
	repo := &fakeExpRepo{}
	enq := &fakeEnqueuer{}

	svc := NewDistillationService(llmClient, nil, repo, WithEmbeddingEnqueuer(enq))

	exp, err := svc.Distill(context.Background(), distillableTask())
	require.NoError(t, err)

	// The row was created without a vector.
	require.Len(t, repo.created, 1)
	assert.Empty(t, repo.created[0].Embedding)
	assert.Empty(t, exp.Embedding)

	// A backfill task was enqueued with the row id as TaskID.
	require.Len(t, enq.called, 1)
	assert.Equal(t, exp.ID, enq.called[0].TaskID)
	assert.Equal(t, "how to sort things", enq.called[0].Content)
	assert.Equal(t, "tenant-1", enq.called[0].TenantID)
}

// TestDistill_DoubleFailureLogsErrorAndKeepsRow pins D1: when the async
// enqueue AND the synchronous fallback embed both fail, the row is still
// persisted (distillation itself must not fail — the experience is valid
// without a vector), but the failure must be OBSERVABLE at ERROR level with
// the row identity. A NULL vector is a permanent recall gap whenever the
// reconciler is not running, and SearchByVector is fail-closed on NULL, so it
// misses silently — a Warn would hide a real defect.
func TestDistill_DoubleFailureLogsErrorAndKeepsRow(t *testing.T) {
	llmClient := newTestLLM(t, llmExtractionContent)

	// A closed loopback server makes the synchronous fallback embed fail
	// deterministically (connection refused) without touching the network.
	deadSrv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	deadURL := deadSrv.URL
	deadSrv.Close()
	embClient := embedding.NewEmbeddingClient(deadURL, "test-model", nil, 200*time.Millisecond)

	repo := &fakeExpRepo{}
	enq := &fakeEnqueuer{err: errors.New("queue unavailable")}

	// The service snapshots slog.Default() at construction, so the capture
	// must be installed BEFORE NewDistillationService or the records below
	// would never arrive.
	logs := captureDefaultLogD1(t)
	svc := NewDistillationService(llmClient, embClient, repo, WithEmbeddingEnqueuer(enq))

	exp, err := svc.Distill(context.Background(), distillableTask())
	require.NoError(t, err, "a broken embedding path must not fail distillation itself")
	require.NotNil(t, exp)

	// The row survives without a vector — that is the documented trade-off.
	require.Len(t, repo.created, 1)
	assert.Empty(t, repo.created[0].Embedding, "no vector could be written")

	rec, ok := logs.findError("NULL vector")
	require.True(t, ok, "enqueue + sync backfill double failure must be logged at ERROR (D1)")
	assert.Equal(t, "tenant-1", logAttrString(rec, "tenant_id"))
	assert.NotEmpty(t, logAttrString(rec, "experience_id"), "the ERROR must identify the affected row")
}

// captureLogD1 records every slog entry emitted through the default logger.
// Guarded by a mutex because the default logger is process-wide.
type captureLogD1 struct {
	mu      sync.Mutex
	records []slog.Record
}

// Enabled reports that every level is captured.
func (c *captureLogD1) Enabled(context.Context, slog.Level) bool { return true }

// Handle appends a copy of the record (the record is only valid during the call).
func (c *captureLogD1) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, r.Clone())
	return nil
}

// WithAttrs returns the handler unchanged; attribute grouping is irrelevant here.
func (c *captureLogD1) WithAttrs([]slog.Attr) slog.Handler { return c }

// WithGroup returns the handler unchanged; group nesting is irrelevant here.
func (c *captureLogD1) WithGroup(string) slog.Handler { return c }

// findError returns the first ERROR record whose message contains substr.
func (c *captureLogD1) findError(substr string) (slog.Record, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.records {
		if r.Level == slog.LevelError && strings.Contains(r.Message, substr) {
			return r, true
		}
	}
	return slog.Record{}, false
}

// captureDefaultLogD1 installs a capturing handler as the default slog logger
// and restores the previous logger when the test ends.
func captureDefaultLogD1(t *testing.T) *captureLogD1 {
	t.Helper()
	c := &captureLogD1{}
	prev := slog.Default()
	slog.SetDefault(slog.New(c))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return c
}

// logAttrString returns the string value of the named attribute, or "" when the
// attribute is absent or not a string.
func logAttrString(r slog.Record, key string) string {
	got := ""
	r.Attrs(func(a slog.Attr) bool {
		if a.Key != key {
			return true
		}
		if a.Value.Kind() == slog.KindString {
			got = a.Value.String()
		}
		return false
	})
	return got
}

// TestDistill_WithoutEnqueuerEmbedsSynchronously proves backward compatibility:
// with no enqueuer the service embeds synchronously and persists the vector.
func TestDistill_WithoutEnqueuerEmbedsSynchronously(t *testing.T) {
	llmClient := newTestLLM(t, llmExtractionContent)
	embSrv := fakeEmbedServer(t, 4)
	t.Cleanup(embSrv.Close)
	embClient := embedding.NewEmbeddingClient(embSrv.URL, "test-model", nil, time.Second)

	repo := &fakeExpRepo{}
	svc := NewDistillationService(llmClient, embClient, repo)

	exp, err := svc.Distill(context.Background(), distillableTask())
	require.NoError(t, err)

	require.Len(t, repo.created, 1)
	assert.NotEmpty(t, repo.created[0].Embedding)
	assert.NotEmpty(t, exp.Embedding)
}

func TestShouldDistill(t *testing.T) {
	svc := NewDistillationService(nil, nil, &fakeExpRepo{})

	cases := []struct {
		name string
		task *TaskResult
		want bool
	}{
		{"nil task is not distillable", nil, false},
		{"short task is not distillable", &TaskResult{Task: "short", Result: "a result long enough to qualify"}, false},
		{"short result is not distillable", &TaskResult{Task: "A sufficiently long task description", Result: "short"}, false},
		{"valid task and result are distillable", distillableTask(), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := svc.ShouldDistill(context.Background(), tc.task); got != tc.want {
				t.Fatalf("ShouldDistill() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestDistill_ValidationErrors(t *testing.T) {
	svc := NewDistillationService(nil, nil, &fakeExpRepo{})

	t.Run("nil task", func(t *testing.T) {
		_, err := svc.Distill(context.Background(), nil)
		require.Error(t, err)
	})

	t.Run("empty tenant id", func(t *testing.T) {
		task := distillableTask()
		task.TenantID = ""
		_, err := svc.Distill(context.Background(), task)
		require.Error(t, err)
	})

	t.Run("llm unavailable stops extraction", func(t *testing.T) {
		_, err := svc.Distill(context.Background(), distillableTask())
		require.Error(t, err)
	})
}

func TestDistill_InvalidExtractedExperience(t *testing.T) {
	// LLM returns content without any labeled Problem/Solution section, so the
	// extraction is empty and Distill must reject it without persisting a row.
	llmClient := newTestLLM(t, "no labeled sections in this response")
	repo := &fakeExpRepo{}
	svc := NewDistillationService(llmClient, nil, repo)

	_, err := svc.Distill(context.Background(), distillableTask())
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid extracted experience")
	require.Len(t, repo.created, 0)
}

func TestDistill_EnqueueFailureWithoutEmbedClientReturnsRowForReconcile(t *testing.T) {
	// Enqueue fails AND no embedding client is wired: the synchronous
	// fallback (backfillEmbedding) cannot embed, but the row IS persisted —
	// returning an error here made callers retry Distill, which Created a
	// DUPLICATE vectorless row. The persisted row is handed back as-is;
	// Reconcile's experiences pass (embedding IS NULL, no live queue entry)
	// is the designed recovery path for exactly this state.
	llmClient := newTestLLM(t, llmExtractionContent)
	enq := &fakeEnqueuer{err: context.DeadlineExceeded}
	repo := &fakeExpRepo{}
	svc := NewDistillationService(llmClient, nil, repo, WithEmbeddingEnqueuer(enq))

	exp, err := svc.Distill(context.Background(), distillableTask())
	require.NoError(t, err)
	require.NotNil(t, exp)
	require.Len(t, repo.created, 1) // exactly ONE row — no duplicate on caller retry
}

func TestDistill_NoEnqueuerNoEmbedClientReturnsError(t *testing.T) {
	// Synchronous path with nil embedding client: embedAndCreate must reject
	// instead of panicking on the nil client.
	llmClient := newTestLLM(t, llmExtractionContent)
	svc := NewDistillationService(llmClient, nil, &fakeExpRepo{})

	_, err := svc.Distill(context.Background(), distillableTask())
	require.Error(t, err)
	require.Contains(t, err.Error(), "embedding client is not available")
}

func TestDistillBatch(t *testing.T) {
	t.Run("empty input returns empty", func(t *testing.T) {
		svc := NewDistillationService(nil, nil, &fakeExpRepo{})
		got, err := svc.DistillBatch(context.Background(), nil)
		require.NoError(t, err)
		require.Empty(t, got)
	})

	t.Run("skips failing tasks", func(t *testing.T) {
		llmClient := newTestLLM(t, llmExtractionContent)
		embSrv := fakeEmbedServer(t, 4)
		t.Cleanup(embSrv.Close)
		embClient := embedding.NewEmbeddingClient(embSrv.URL, "test-model", nil, time.Second)

		repo := &fakeExpRepo{}
		svc := NewDistillationService(llmClient, embClient, repo)

		good := distillableTask()
		bad := distillableTask()
		bad.TenantID = "" // fails validation inside Distill → skipped by the batch

		exps, err := svc.DistillBatch(context.Background(), []*TaskResult{good, bad})
		require.NoError(t, err)
		require.Len(t, exps, 1)
		require.Equal(t, "tenant-1", exps[0].TenantID)
	})
}

func TestDistill_EnqueueFailureFallsBackToSyncEmbed(t *testing.T) {
	// When the enqueue fails, the service falls back to a synchronous
	// embed + narrow vector write-back so the row never stays without a vector.
	llmClient := newTestLLM(t, llmExtractionContent)
	embSrv := fakeEmbedServer(t, 8)
	t.Cleanup(embSrv.Close)
	embClient := embedding.NewEmbeddingClient(embSrv.URL, "test-model", nil, time.Second)

	repo := &fakeExpRepo{}
	enq := &fakeEnqueuer{err: context.DeadlineExceeded}

	svc := NewDistillationService(llmClient, embClient, repo, WithEmbeddingEnqueuer(enq))

	exp, err := svc.Distill(context.Background(), distillableTask())
	require.NoError(t, err)

	// Row created once, then patched by the synchronous fallback. The fallback
	// must use UpdateEmbedding, not a full-row Update, so a concurrent worker
	// write to other columns is not clobbered.
	require.Len(t, repo.created, 1)
	require.Empty(t, repo.updated)
	require.Len(t, repo.embeddingUpdates, 1)
	assert.Equal(t, exp.ID, repo.embeddingUpdates[0].ID)
	assert.Equal(t, "tenant-1", repo.embeddingUpdates[0].TenantID)
	assert.NotEmpty(t, repo.embeddingUpdates[0].Embedding)
	assert.NotEmpty(t, exp.Embedding)
}

func TestDistill_AsyncPathStampsModelAndConfiguredVersion(t *testing.T) {
	// The async path knows the model name at insert time, so it must not leave
	// embedding_model empty until backfill, and the version must come from
	// config instead of a hardcoded literal.
	llmClient := newTestLLM(t, llmExtractionContent)
	embSrv := fakeEmbedServer(t, 4)
	t.Cleanup(embSrv.Close)
	embClient := embedding.NewEmbeddingClient(embSrv.URL, "stamped-model", nil, time.Second)

	repo := &fakeExpRepo{}
	enq := &fakeEnqueuer{}
	cfg := &postgres.EmbeddingConfig{DefaultModel: "stamped-model", DefaultVersion: 7}

	svc := NewDistillationService(llmClient, embClient, repo,
		WithEmbeddingEnqueuer(enq), WithEmbeddingConfig(cfg))

	_, err := svc.Distill(context.Background(), distillableTask())
	require.NoError(t, err)

	require.Len(t, repo.created, 1)
	assert.Equal(t, "stamped-model", repo.created[0].EmbeddingModel)
	assert.Equal(t, 7, repo.created[0].EmbeddingVersion)

	require.Len(t, enq.called, 1)
	assert.Equal(t, "stamped-model", enq.called[0].Model)
	assert.Equal(t, 7, enq.called[0].Version)
}

func TestDistill_EnqueueFailureIsLogged(t *testing.T) {
	// A permanently broken queue must be visible in logs; without this the async
	// path degrades to synchronous embedding silently.
	llmClient := newTestLLM(t, llmExtractionContent)
	embSrv := fakeEmbedServer(t, 4)
	t.Cleanup(embSrv.Close)
	embClient := embedding.NewEmbeddingClient(embSrv.URL, "test-model", nil, time.Second)

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	svc := NewDistillationService(llmClient, embClient, &fakeExpRepo{},
		WithEmbeddingEnqueuer(&fakeEnqueuer{err: context.DeadlineExceeded}))
	svc.logger = logger

	_, err := svc.Distill(context.Background(), distillableTask())
	require.NoError(t, err)

	logged := buf.String()
	assert.Contains(t, logged, "enqueue embedding backfill failed")
	assert.Contains(t, logged, "tenant-1")
}
