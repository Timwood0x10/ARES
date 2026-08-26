package ares_bootstrap

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/storage/postgres"
	"golang.org/x/sync/errgroup"
)

// fakeQueue implements embeddingQueueClient for testing.
type fakeQueue struct {
	mu         sync.Mutex
	tasks      []*postgres.EmbeddingTask
	completed  []string
	failed     []map[string]string // taskID -> errMsg
	reconciled int

	// fetchErr, if non-nil, is returned by FetchPendingTasks.
	fetchErr error
	// markErr, if non-nil, is returned by MarkCompleted/MarkFailed.
	markErr error
	// reconcileErr, if non-nil, is returned by Reconcile.
	reconcileErr error
}

func (q *fakeQueue) FetchPendingTasks(_ context.Context, limit int) ([]*postgres.EmbeddingTask, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.fetchErr != nil {
		return nil, q.fetchErr
	}
	if limit > len(q.tasks) {
		limit = len(q.tasks)
	}
	batch := q.tasks[:limit]
	q.tasks = q.tasks[limit:]
	return batch, nil
}

func (q *fakeQueue) MarkCompleted(_ context.Context, taskID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.markErr != nil {
		return q.markErr
	}
	q.completed = append(q.completed, taskID)
	return nil
}

func (q *fakeQueue) MarkFailed(_ context.Context, taskID, errMessage string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.markErr != nil {
		return q.markErr
	}
	q.failed = append(q.failed, map[string]string{"task_id": taskID, "error": errMessage})
	return nil
}

func (q *fakeQueue) Reconcile(_ context.Context, _ time.Duration) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.reconciled++
	return q.reconcileErr
}

// fakeEmbedder implements embeddingEmbedder for testing.
type fakeEmbedder struct {
	mu     sync.Mutex
	embeds []string
	vec    []float64
	err    error
}

func (e *fakeEmbedder) Embed(_ context.Context, text string) ([]float64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.embeds = append(e.embeds, text)
	if e.err != nil {
		return nil, e.err
	}
	if e.vec != nil {
		return e.vec, nil
	}
	return []float64{0.1, 0.2, 0.3}, nil
}

func (e *fakeEmbedder) GetModel() string { return "test-model" }

// fakeWriter captures writeEmbedding calls.
type fakeWriter struct {
	mu     sync.Mutex
	writes []writeCall
	err    error
}

type writeCall struct {
	taskID   string
	tenantID string
	table    string
	vec      []float64
	model    string
	version  int
}

func (w *fakeWriter) writeEmbedding(_ context.Context, task *postgres.EmbeddingTask, vec []float64, model string, version int) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	w.writes = append(w.writes, writeCall{
		taskID:   task.TaskID,
		tenantID: task.TenantID,
		table:    task.Table,
		vec:      vec,
		model:    model,
		version:  version,
	})
	return nil
}

// TestProcessEmbeddingTask_Success verifies the happy path: embed → write
// back via the writer (using the TaskID == source-row-id contract) → mark
// the queue task completed.
func TestProcessEmbeddingTask_Success(t *testing.T) {
	queue := &fakeQueue{}
	emb := &fakeEmbedder{vec: []float64{0.4, 0.5}}
	writer := &fakeWriter{}

	task := &postgres.EmbeddingTask{
		TaskID:   "chunk-1", // source row id in knowledge_chunks_1024
		Table:    "knowledge_chunks_1024",
		Content:  "hello world",
		TenantID: "t1",
		Model:    "e5-large",
		Version:  1,
	}

	processEmbeddingTask(context.Background(), queue, emb, writer, task, slog.Default())

	if len(queue.completed) != 1 || queue.completed[0] != "chunk-1" {
		t.Fatalf("expected task chunk-1 marked completed, got %v", queue.completed)
	}
	if len(queue.failed) != 0 {
		t.Fatalf("expected no failed tasks, got %v", queue.failed)
	}

	writer.mu.Lock()
	defer writer.mu.Unlock()
	if len(writer.writes) != 1 {
		t.Fatalf("expected exactly one write-back, got %d", len(writer.writes))
	}
	call := writer.writes[0]
	if call.taskID != "chunk-1" || call.tenantID != "t1" || call.table != "knowledge_chunks_1024" {
		t.Errorf("write-back targeted wrong entity: %+v", call)
	}
	if call.model != "e5-large" || call.version != 1 {
		t.Errorf("unexpected model/version: %s/%d", call.model, call.version)
	}
	if len(call.vec) != 2 {
		t.Errorf("expected embedded vector passed through, got %v", call.vec)
	}
}

func TestStartEmbeddingWorker_NilDepsSkipsWorker(t *testing.T) {
	var comp Components
	comp.bgGroup = errgroup.Group{}

	// nil queue → worker not started
	startEmbeddingWorker(context.Background(), &comp, nil, &fakeEmbedder{}, embeddingWriter{}, nil, defaultEmbeddingWorkerConfig())

	// WaitBackground should return immediately (no goroutine)
	done := make(chan struct{})
	go func() { comp.WaitBackground(); close(done) }()
	select {
	case <-done:
		// good
	case <-time.After(time.Second):
		t.Fatal("worker was started despite nil queue")
	}
}

func TestStartEmbeddingReconciler_NilDepsSkipsReconciler(t *testing.T) {
	var comp Components
	comp.bgGroup = errgroup.Group{}

	startEmbeddingReconciler(context.Background(), &comp, nil, defaultEmbeddingWorkerConfig())

	done := make(chan struct{})
	go func() { comp.WaitBackground(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reconciler was started despite nil queue")
	}
}

func TestStartEmbeddingReconciler_ContextCancellation(t *testing.T) {
	var comp Components
	comp.bgGroup = errgroup.Group{}

	ctx, cancel := context.WithCancel(context.Background())
	queue := &fakeQueue{}

	startEmbeddingReconciler(ctx, &comp, queue, EmbeddingWorkerConfig{
		ReconcileInterval:  50 * time.Millisecond,
		ReconcileThreshold: 10 * time.Minute,
	})

	// Let it tick once
	time.Sleep(150 * time.Millisecond)
	cancel()

	done := make(chan struct{})
	go func() { comp.WaitBackground(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reconciler did not stop after context cancellation")
	}

	if queue.reconciled == 0 {
		t.Error("expected at least one reconcile call")
	}
}

func TestStartEmbeddingWorker_ProcessesBatch(t *testing.T) {
	var comp Components
	comp.bgGroup = errgroup.Group{}

	ctx, cancel := context.WithCancel(context.Background())

	queue := &fakeQueue{
		tasks: []*postgres.EmbeddingTask{
			{TaskID: "t1", Table: "knowledge_chunks_1024", Content: "text1", TenantID: "t1"},
			{TaskID: "t2", Table: "experiences_1024", Content: "text2", TenantID: "t1"},
		},
	}
	emb := &fakeEmbedder{vec: []float64{0.5}}

	// Use a writer with nil repos — tasks will be marked failed because
	// no repo is configured. This tests the error path.
	writer := embeddingWriter{}

	embCfg := postgres.DefaultEmbeddingConfig()
	workerCfg := EmbeddingWorkerConfig{
		PollInterval:       50 * time.Millisecond,
		ReconcileInterval:  10 * time.Minute,
		ReconcileThreshold: 30 * time.Minute,
	}

	startEmbeddingWorker(ctx, &comp, queue, emb, writer, embCfg, workerCfg)

	// Let it process
	time.Sleep(200 * time.Millisecond)
	cancel()

	done := make(chan struct{})
	go func() { comp.WaitBackground(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop after context cancellation")
	}

	// Verify embedder was called for each task
	emb.mu.Lock()
	if len(emb.embeds) < 2 {
		t.Errorf("expected at least 2 embed calls, got %d", len(emb.embeds))
	}
	emb.mu.Unlock()

	// Verify tasks were marked failed (nil repos → writeEmbedding error)
	queue.mu.Lock()
	if len(queue.failed) < 2 {
		t.Errorf("expected at least 2 failed tasks, got %d", len(queue.failed))
	}
	queue.mu.Unlock()
}

func TestNoRepoError_Error(t *testing.T) {
	e := errNoRepo("unknown_table")
	if e.Error() == "" {
		t.Error("expected non-empty error message")
	}
}

func TestEmbeddingWriter_WriteEmbedding_UnknownTable(t *testing.T) {
	w := embeddingWriter{}
	task := &postgres.EmbeddingTask{
		TaskID:   "t1",
		Table:    "unknown_table",
		TenantID: "t1",
	}
	err := w.writeEmbedding(context.Background(), task, []float64{0.1}, "model", 1)
	if err == nil {
		t.Error("expected error for unknown table")
	}

	var nre *noRepoError
	if !errors.As(err, &nre) {
		t.Error("expected *noRepoError")
	}
}
