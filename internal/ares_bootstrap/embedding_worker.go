package ares_bootstrap

import (
	"context"
	"log/slog"
	"time"

	"github.com/Timwood0x10/ares/internal/storage/postgres"
	"github.com/Timwood0x10/ares/internal/storage/postgres/embedding"
	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
	"github.com/Timwood0x10/ares/internal/storage/postgres/repositories"
)

// tableKnowledgeChunks is the knowledge chunk table consumed by the
// embedding worker's write-back dispatch (shared with the expiry cleaner
// and vector wiring in this package). It aliases the storage-layer constant so
// the physical name has a single source of truth.
const tableKnowledgeChunks = storage_models.KnowledgeChunksTable

// EmbeddingWorkerConfig controls the embedding worker polling intervals.
// EmbeddingWorkerConfig carries the tuning knobs for the embedding worker
// and its housekeeping loops.
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
	// DeadLetterRetention is how long dead-letter entries are kept before
	// the reconciler purges them. Default 30d — the operator's window to
	// inspect or RequeueDeadLetter before automatic cleanup.
	DeadLetterRetention time.Duration
	// DeadLetterReplayBatch is how many never-replayed dead letters one
	// reconciler tick may put back in the queue. Default 50.
	//
	// Bounded on purpose: a provider outage dead-letters everything at once, and
	// replaying the whole backlog in a single tick would spend the embedding
	// quota in one burst while the provider may still be down. Each entry is
	// replayed at most ONCE ever (the queue stamps it), so a small batch per tick
	// still drains the backlog completely — just at a rate the provider can
	// absorb.
	DeadLetterReplayBatch int
}

// defaultEmbeddingWorkerConfig returns sensible defaults.
func defaultEmbeddingWorkerConfig() EmbeddingWorkerConfig {
	return EmbeddingWorkerConfig{
		PollInterval:        5 * time.Second,
		ReconcileInterval:   10 * time.Minute,
		ReconcileThreshold:  30 * time.Minute,
		DeadLetterRetention: 30 * 24 * time.Hour,

		DeadLetterReplayBatch: 50,
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
	MarkCompleted(ctx context.Context, tableName, taskID string) error
	MarkFailed(ctx context.Context, tableName, taskID, errMessage string) error
	Reconcile(ctx context.Context, threshold time.Duration) error
	PurgeDeadLetters(ctx context.Context, age time.Duration) (int64, error)
	// ReplayDeadLetters and CountDeadLetters are the D1/P1-4 pair: the first gives
	// never-replayed dead letters their one automatic second chance, the second
	// makes the remaining backlog — content with no embedding, invisible to every
	// retrieval query — impossible to miss.
	ReplayDeadLetters(ctx context.Context, limit int) (int64, error)
	CountDeadLetters(ctx context.Context) (postgres.DeadLetterBacklog, error)
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

	// GoBackground (not a bare bgGroup.Go) so a panic in a poll tick is
	// contained at the goroutine boundary and logged instead of taking the
	// process down — the same panic-recover boundary every other bootstrap
	// background worker runs under.
	comp.GoBackground(ctx, "embedding_worker", func(ctx context.Context) error {
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
	if workerCfg.DeadLetterReplayBatch <= 0 {
		// Per-field, not the whole struct: a caller that only set the interval
		// must not silently get a zero batch, which would disable replay.
		workerCfg.DeadLetterReplayBatch = defaultEmbeddingWorkerConfig().DeadLetterReplayBatch
	}

	logger := slog.With("component", "embedding_reconciler")

	// GoBackground for the same panic-recover reason as the worker above.
	comp.GoBackground(ctx, "embedding_reconciler", func(ctx context.Context) error {
		ticker := time.NewTicker(workerCfg.ReconcileInterval)
		defer ticker.Stop()

		// Per-loop state (not a package global): the reporter's memory of what it
		// already said belongs to this loop and would otherwise leak between runs.
		var deadLetters deadLetterReporter

		logger.InfoContext(ctx, "embedding reconciler started",
			"interval", workerCfg.ReconcileInterval.String(),
			"threshold", workerCfg.ReconcileThreshold.String(),
			"dead_letter_retention", workerCfg.DeadLetterRetention.String())

		for {
			select {
			case <-ctx.Done():
				logger.InfoContext(ctx, "embedding reconciler stopping (context cancelled)")
				return nil
			case <-ticker.C:
				if err := queue.Reconcile(ctx, workerCfg.ReconcileThreshold); err != nil {
					logger.WarnContext(ctx, "embedding reconcile failed", "error", err)
				}
				// Replay rides BEFORE the purge on purpose (defect D1): a pending
				// entry older than the retention window must get its one automatic
				// second chance before cleanup deletes the last queue copy of its
				// content. Purge-then-replay would let retention silently decide
				// that content is gone.
				replayEmbeddingDeadLetters(ctx, queue, workerCfg.DeadLetterReplayBatch, logger)
				// Dead-letter housekeeping rides the same tick: without a
				// purge the embedding_dead_letter table grows unbounded and
				// Reconcile's NOT EXISTS probes scan an ever-larger set on
				// every tick. Retention gives operators the inspect/requeue
				// window before entries are dropped.
				purged, err := queue.PurgeDeadLetters(ctx, workerCfg.DeadLetterRetention)
				if err != nil {
					logger.WarnContext(ctx, "embedding dead-letter purge failed", "error", err)
				} else if purged > 0 {
					logger.InfoContext(ctx, "embedding dead letters purged",
						"count", purged, "retention", workerCfg.DeadLetterRetention.String())
				}
				// The report is LAST so its numbers are what SURVIVES this tick:
				// content still without an embedding, and therefore still skipped
				// by every retrieval query without producing an error.
				deadLetters.report(ctx, queue, workerCfg.DeadLetterRetention, logger)
			}
		}
	})
}

// replayEmbeddingDeadLetters gives up to batch never-replayed dead letters their
// one automatic second chance (defect D1 / P1-4).
//
// It is a separate function from the tick so the policy is testable without a
// running goroutine, and it deliberately swallows nothing: a replay failure is
// logged, because a silent failure here is the same defect it exists to fix.
func replayEmbeddingDeadLetters(ctx context.Context, queue embeddingQueueClient, batch int, logger *slog.Logger) {
	replayed, err := queue.ReplayDeadLetters(ctx, batch)
	if err != nil {
		logger.WarnContext(ctx, "embedding dead-letter replay failed", "error", err)
		return
	}
	if replayed > 0 {
		logger.InfoContext(ctx, "embedding dead letters replayed",
			"count", replayed, "batch", batch)
	}
}

// deadLetterReporter reports the dead-letter backlog once per STATE.
//
// The backlog is a number that does not change between ticks: reported every tick
// (default 10 minutes) a permanently unembeddable row would emit ~144 identical
// Warnings a day, which is how a real alarm gets filtered out by whoever reads the
// logs. Reporting on change keeps the "content is invisible" fact loud the first
// time and quiet while nothing moves, and it still speaks up when the backlog grows,
// shrinks, or the count starts failing.
type deadLetterReporter struct {
	last     deadLetterState
	reported bool
}

// deadLetterState is the comparable fact a report is keyed on.
type deadLetterState struct {
	Pending  int64
	GivenUp  int64
	CountErr bool
}

// report emits the backlog when it differs from the last reported state.
func (r *deadLetterReporter) report(ctx context.Context, queue embeddingQueueClient, retention time.Duration, logger *slog.Logger) {
	backlog, err := queue.CountDeadLetters(ctx)
	state := deadLetterState{Pending: backlog.Pending, GivenUp: backlog.GivenUp, CountErr: err != nil}
	if r.reported && state == r.last {
		return // unchanged since the last tick: already said
	}
	r.last, r.reported = state, true

	if err != nil {
		logger.WarnContext(ctx, "embedding dead-letter count failed", "error", err)
		return
	}
	if backlog.Total() == 0 {
		return
	}
	logger.WarnContext(ctx, "content has no embedding and is invisible to retrieval",
		"total", backlog.Total(),
		"pending_replay", backlog.Pending,
		"given_up", backlog.GivenUp,
		"retention", retention.String())
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
		if markErr := queue.MarkFailed(ctx, task.Table, task.TaskID, failMsg); markErr != nil {
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
		if markErr := queue.MarkFailed(ctx, task.Table, task.TaskID, err.Error()); markErr != nil {
			logger.ErrorContext(ctx, "mark embedding task failed after write error",
				"task_id", task.TaskID, "error", markErr)
		}
		logger.WarnContext(ctx, "write embedding failed for task",
			"task_id", task.TaskID, "table", task.Table, "error", err)
		return
	}

	if err := queue.MarkCompleted(ctx, task.Table, task.TaskID); err != nil {
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
	case storage_models.ExperiencesTable:
		if w.experienceRepo == nil {
			return errNoRepo(storage_models.ExperiencesTable)
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
//
// queue and embCfg come from provideDistillation so producer and consumer share
// one queue instance and one config, instead of each side building its own.
func wireEmbeddingWorker(
	ctx context.Context,
	comp *Components,
	pool *postgres.Pool,
	embClient *embedding.EmbeddingClient,
	queue *postgres.EmbeddingQueue,
	embCfg *postgres.EmbeddingConfig,
	knowledgeRepo *repositories.KnowledgeRepository,
	experienceRepo *repositories.ExperienceRepository,
) {
	if pool == nil || embClient == nil || queue == nil {
		slog.Info("bootstrap: embedding worker not wired (pool, embedding client or queue nil)")
		return
	}
	if embCfg == nil {
		embCfg = postgres.DefaultEmbeddingConfig()
	}

	writer := embeddingWriter{
		knowledgeRepo:  knowledgeRepo,
		experienceRepo: experienceRepo,
	}
	workerCfg := defaultEmbeddingWorkerConfig()

	startEmbeddingWorker(ctx, comp, queue, embClient, writer, embCfg, workerCfg)
	startEmbeddingReconciler(ctx, comp, queue, workerCfg)
}
