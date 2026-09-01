package ares_bootstrap

import (
	"context"
	"log/slog"
	"time"

	aresexp "github.com/Timwood0x10/ares/internal/ares_experience"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
	"github.com/Timwood0x10/ares/internal/storage/postgres/embedding"
	"github.com/Timwood0x10/ares/internal/storage/postgres/repositories"
)

// tableKnowledgeChunks is the knowledge chunk table consumed by the
// embedding worker's write-back dispatch (shared with the expiry cleaner
// and vector wiring in this package).
const tableKnowledgeChunks = "knowledge_chunks_1024"

// EmbeddingWorkerConfig controls the embedding worker polling intervals.
type EmbeddingWorkerConfig struct {
	// PollInterval is how often the worker polls for pending tasks.
	// Default 5s.
	PollInterval time.Duration
	// ReconcileInterval is how often the reconciler scans for orphaned
	// embedding rows. Default 10min.
	ReconcileInterval time.Duration
	// ReconcileThreshold is the age threshold for considering an embedding
	// task orphaned. Default 30min.
	ReconcileThreshold time.Duration
}

// defaultEmbeddingWorkerConfig returns sensible defaults.
func defaultEmbeddingWorkerConfig() EmbeddingWorkerConfig {
	return EmbeddingWorkerConfig{
		PollInterval:       5 * time.Second,
		ReconcileInterval:  10 * time.Minute,
		ReconcileThreshold: 30 * time.Minute,
	}
}

// embeddingEmbedder is the minimal interface the worker needs from the
// embedding client. *embedding.EmbeddingClient satisfies this.
type embeddingEmbedder interface {
	Embed(ctx context.Context, text string) ([]float64, error)
	GetModel() string
}

// embeddingQueueClient is the minimal interface the worker needs from the
// embedding queue. *postgres.EmbeddingQueue satisfies this.
type embeddingQueueClient interface {
	FetchPendingTasks(ctx context.Context, limit int) ([]*postgres.EmbeddingTask, error)
	MarkCompleted(ctx context.Context, taskID string) error
	MarkFailed(ctx context.Context, taskID string, errMessage string) error
	Reconcile(ctx context.Context, threshold time.Duration) error
}

// embeddingWriter resolves the correct repo.UpdateEmbedding call based on
// the task's table name. Each repo is optional; if a table has no matching
// repo, the task is marked failed with a clear error.
type embeddingWriter struct {
	knowledgeRepo  *repositories.KnowledgeRepository
	experienceRepo *repositories.ExperienceRepository
}

// embeddingWriterInterface abstracts the write-back dispatch so tests can
// inject fakes (the concrete embeddingWriter satisfies it implicitly).
type embeddingWriterInterface interface {
	writeEmbedding(ctx context.Context, task *postgres.EmbeddingTask, vec []float64, model string, version int) error
}

// startEmbeddingWorker launches a background goroutine on comp.bgGroup that
// polls the embedding queue, computes embeddings, and writes them back to
// the source table. All dependencies are best-effort: any nil dependency
// causes the worker to be skipped (not started). The goroutine exits when
// ctx is cancelled.
func startEmbeddingWorker(
	ctx context.Context,
	comp *Components,
	queue embeddingQueueClient,
	embClient embeddingEmbedder,
	writer embeddingWriterInterface,
	embCfg *postgres.EmbeddingConfig,
	workerCfg EmbeddingWorkerConfig,
) {
	if comp == nil {
		return
	}
	if queue == nil || embClient == nil {
		slog.Info("bootstrap: embedding worker skipped (queue or embedding client nil)")
		return
	}
	if embCfg == nil {
		embCfg = postgres.DefaultEmbeddingConfig()
	}
	if workerCfg.PollInterval <= 0 {
		workerCfg = defaultEmbeddingWorkerConfig()
	}

	logger := slog.With("component", "embedding_worker")

	comp.bgGroup.Go(func() error {
		ticker := time.NewTicker(workerCfg.PollInterval)
		defer ticker.Stop()

		logger.InfoContext(ctx, "embedding worker started",
			"poll_interval", workerCfg.PollInterval.String(),
			"batch_size", embCfg.MaxBatchSize)

		for {
			select {
			case <-ctx.Done():
				logger.InfoContext(ctx, "embedding worker stopping (context cancelled)")
				return nil
			case <-ticker.C:
				processEmbeddingBatch(ctx, queue, embClient, writer, embCfg, logger)
			}
		}
	})
}

// startEmbeddingReconciler launches a background goroutine that periodically
// calls Reconcile to find orphaned embedding tasks. Best-effort like the
// worker: nil dependencies cause a skip.
func startEmbeddingReconciler(
	ctx context.Context,
	comp *Components,
	queue embeddingQueueClient,
	workerCfg EmbeddingWorkerConfig,
) {
	if comp == nil || queue == nil {
		return
	}
	if workerCfg.ReconcileInterval <= 0 {
		workerCfg = defaultEmbeddingWorkerConfig()
	}

	logger := slog.With("component", "embedding_reconciler")

	comp.bgGroup.Go(func() error {
		ticker := time.NewTicker(workerCfg.ReconcileInterval)
		defer ticker.Stop()

		logger.InfoContext(ctx, "embedding reconciler started",
			"interval", workerCfg.ReconcileInterval.String(),
			"threshold", workerCfg.ReconcileThreshold.String())

		for {
			select {
			case <-ctx.Done():
				logger.InfoContext(ctx, "embedding reconciler stopping (context cancelled)")
				return nil
			case <-ticker.C:
				if err := queue.Reconcile(ctx, workerCfg.ReconcileThreshold); err != nil {
					logger.WarnContext(ctx, "embedding reconcile failed", "error", err)
				}
			}
		}
	})
}

// processEmbeddingBatch fetches one batch of pending tasks and processes
// each task sequentially. Errors are logged but never abort the loop;
// individual task failures are marked failed in the queue.
func processEmbeddingBatch(
	ctx context.Context,
	queue embeddingQueueClient,
	embClient embeddingEmbedder,
	writer embeddingWriterInterface,
	embCfg *postgres.EmbeddingConfig,
	logger *slog.Logger,
) {
	tasks, err := queue.FetchPendingTasks(ctx, embCfg.MaxBatchSize)
	if err != nil {
		logger.WarnContext(ctx, "fetch pending embedding tasks failed", "error", err)
		return
	}
	if len(tasks) == 0 {
		return
	}

	logger.DebugContext(ctx, "processing embedding batch", "tasks", len(tasks))

	for _, task := range tasks {
		if ctx.Err() != nil {
			return
		}
		processEmbeddingTask(ctx, queue, embClient, writer, task, logger)
	}
}

// processEmbeddingTask handles a single embedding task: embed the content,
// write the vector back to the source table, then mark the task completed
// or failed.
func processEmbeddingTask(
	ctx context.Context,
	queue embeddingQueueClient,
	embClient embeddingEmbedder,
	writer embeddingWriterInterface,
	task *postgres.EmbeddingTask,
	logger *slog.Logger,
) {
	vec, err := embClient.Embed(ctx, task.Content)
	if err != nil {
		failMsg := err.Error()
		if markErr := queue.MarkFailed(ctx, task.TaskID, failMsg); markErr != nil {
			logger.ErrorContext(ctx, "mark embedding task failed",
				"task_id", task.TaskID, "error", markErr)
		}
		logger.WarnContext(ctx, "embedding failed for task",
			"task_id", task.TaskID, "table", task.Table, "error", err)
		return
	}

	model := task.Model
	if model == "" {
		model = embClient.GetModel()
	}

	// Determine version from the task or default to 1.
	version := task.Version
	if version == 0 {
		version = 1
	}

	if err := writer.writeEmbedding(ctx, task, vec, model, version); err != nil {
		if markErr := queue.MarkFailed(ctx, task.TaskID, err.Error()); markErr != nil {
			logger.ErrorContext(ctx, "mark embedding task failed after write error",
				"task_id", task.TaskID, "error", markErr)
		}
		logger.WarnContext(ctx, "write embedding failed for task",
			"task_id", task.TaskID, "table", task.Table, "error", err)
		return
	}

	if err := queue.MarkCompleted(ctx, task.TaskID); err != nil {
		logger.ErrorContext(ctx, "mark embedding task completed failed",
			"task_id", task.TaskID, "error", err)
		return
	}

	logger.DebugContext(ctx, "embedding task completed",
		"task_id", task.TaskID, "table", task.Table)
}

// writeEmbedding dispatches to the correct repository based on the task's
// table name. Returns an error if the table is unknown or the corresponding
// repo is nil.
//
// task.TaskID is the source row id per the queue contract (see
// postgres.EmbeddingTask): it is passed directly to UpdateEmbedding, which
// scopes by tenant and errors with ErrRecordNotFound when no row matches.
func (w embeddingWriter) writeEmbedding(
	ctx context.Context,
	task *postgres.EmbeddingTask,
	vec []float64,
	model string,
	version int,
) error {
	switch task.Table {
	case tableKnowledgeChunks:
		if w.knowledgeRepo == nil {
			return errNoRepo(tableKnowledgeChunks)
		}
		return w.knowledgeRepo.UpdateEmbedding(ctx, task.TenantID, task.TaskID, vec, model, version)
	case aresexp.ExperienceTableName:
		if w.experienceRepo == nil {
			return errNoRepo(aresexp.ExperienceTableName)
		}
		return w.experienceRepo.UpdateEmbedding(ctx, task.TenantID, task.TaskID, vec, model, version)
	default:
		return errNoRepo(task.Table)
	}
}

// errNoRepo returns a descriptive error for an unmapped table.
func errNoRepo(table string) error {
	return &noRepoError{table: table}
}

type noRepoError struct{ table string }

func (e *noRepoError) Error() string {
	return "embedding worker: no repository configured for table " + e.table
}

// wireEmbeddingWorker connects the embedding worker and reconciler into
// the bootstrap background group. It is best-effort: nil pool, nil embClient,
// or nil repos cause the worker to be skipped with an info log.
func wireEmbeddingWorker(
	ctx context.Context,
	comp *Components,
	pool *postgres.Pool,
	embClient *embedding.EmbeddingClient,
	knowledgeRepo *repositories.KnowledgeRepository,
	experienceRepo *repositories.ExperienceRepository,
) {
	if pool == nil || embClient == nil {
		slog.Info("bootstrap: embedding worker not wired (pool or embedding client nil)")
		return
	}

	embCfg := postgres.DefaultEmbeddingConfig()
	queue := postgres.NewEmbeddingQueue(pool, embCfg)
	writer := embeddingWriter{
		knowledgeRepo:  knowledgeRepo,
		experienceRepo: experienceRepo,
	}
	workerCfg := defaultEmbeddingWorkerConfig()

	startEmbeddingWorker(ctx, comp, queue, embClient, writer, embCfg, workerCfg)
	startEmbeddingReconciler(ctx, comp, queue, workerCfg)
}
