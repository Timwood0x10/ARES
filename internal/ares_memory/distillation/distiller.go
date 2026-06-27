// Package distillation provides memory distillation functionality for agent experience extraction.
package distillation

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	memembed "github.com/Timwood0x10/ares/internal/ares_memory/embedding"
	truncpkg "github.com/Timwood0x10/ares/internal/ares_memory/internal/truncate"
	"github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/storage/postgres/embedding"
)

// DistillationConfig holds configuration for the distillation process.
type DistillationConfig struct {
	// MinImportance is the minimum importance score for memories to be kept.
	MinImportance float64

	// ConflictThreshold is the similarity threshold for conflict detection.
	ConflictThreshold float64

	// MaxMemoriesPerDistillation is the maximum number of memories to keep per distillation.
	MaxMemoriesPerDistillation int

	// MaxSolutionsPerTenant is the global cap on solution memories per tenant.
	MaxSolutionsPerTenant int

	// EnableCodeFilter enables code block filtering.
	EnableCodeFilter bool

	// EnableStacktraceFilter enables stacktrace filtering.
	EnableStacktraceFilter bool

	// EnableLogFilter enables log filtering.
	EnableLogFilter bool

	// EnableMarkdownTableFilter enables markdown table filtering.
	EnableMarkdownTableFilter bool

	// EnableCrossTurnExtraction enables cross-turn conversation extraction.
	EnableCrossTurnExtraction bool

	// EnableLengthBonus enables length bonus in importance scoring.
	EnableLengthBonus bool

	// LengthThreshold is the threshold for length bonus.
	LengthThreshold int

	// LengthBonus is the bonus value for length threshold.
	LengthBonus float64

	// TopNBeforeConflict enables top-N filtering before conflict detection.
	TopNBeforeConflict bool

	// ConflictSearchLimit is the limit for vector search in conflict detection.
	ConflictSearchLimit int

	// PrecisionOverRecall prioritizes precision over recall.
	PrecisionOverRecall bool
}

// DefaultDistillationConfig returns the default configuration for distillation.
func DefaultDistillationConfig() *DistillationConfig {
	return &DistillationConfig{
		MinImportance:              0.6,
		ConflictThreshold:          0.85,
		MaxMemoriesPerDistillation: 3,
		MaxSolutionsPerTenant:      5000,
		EnableCodeFilter:           true,
		EnableStacktraceFilter:     true,
		EnableLogFilter:            true,
		EnableMarkdownTableFilter:  true,
		EnableCrossTurnExtraction:  true,
		EnableLengthBonus:          true,
		LengthThreshold:            60,
		LengthBonus:                0.1,
		TopNBeforeConflict:         true,
		ConflictSearchLimit:        5,
		PrecisionOverRecall:        true,
	}
}

// DistillationMetrics holds metrics for the distillation process.
type DistillationMetrics struct {
	AttemptTotal     int64
	SuccessTotal     int64
	FilteredNoise    int64
	FilteredSecurity int64
	ConflictResolved int64
	MemoriesCreated  int64
}

// atomicMetrics holds atomic counters for metrics.
type atomicMetrics struct {
	AttemptTotal     atomic.Int64
	SuccessTotal     atomic.Int64
	FilteredNoise    atomic.Int64
	FilteredSecurity atomic.Int64
	ConflictResolved atomic.Int64
	MemoriesCreated  atomic.Int64
}

// String returns a string representation of the atomic metrics.
func (a *atomicMetrics) String() string {
	return fmt.Sprintf("attempts=%d,success=%d,filtered_noise=%d,filtered_security=%d,conflicts_resolved=%d,memories_created=%d",
		a.AttemptTotal.Load(), a.SuccessTotal.Load(), a.FilteredNoise.Load(), a.FilteredSecurity.Load(), a.ConflictResolved.Load(), a.MemoriesCreated.Load())
}

// String returns a string representation of the metrics.
func (m *DistillationMetrics) String() string {
	return fmt.Sprintf("attempts=%d,success=%d,filtered_noise=%d,filtered_security=%d,conflicts_resolved=%d,memories_created=%d",
		m.AttemptTotal, m.SuccessTotal, m.FilteredNoise, m.FilteredSecurity, m.ConflictResolved, m.MemoriesCreated)
}

// Distiller is the unified distillation engine that orchestrates all components.
type Distiller struct {
	config      *DistillationConfig
	extractor   *ExperienceExtractor
	classifier  *MemoryClassifier
	scorer      *ImportanceScorer
	resolver    *ConflictResolver
	noiseFilter *NoiseFilter
	embedder    embedding.EmbeddingService
	pipeline    memembed.EmbeddingPipeline
	repo        ExperienceRepository
	expStore    ExperienceStore // Optional: writes distilled memories to experience store
	metrics     atomicMetrics   // Thread-safe atomic counters
	distillWg   sync.WaitGroup  // Tracks event subscription goroutines
	distillEg   *errgroup.Group // Manages async distillation goroutines

	// OnTaskCompleted is called when a task completion event is received.
	// If set, the distiller invokes it with the task ID from the event payload.
	// The handler should trigger the full distillation pipeline for the task.
	OnTaskCompleted func(ctx context.Context, taskID string)
}

// NewDistiller creates a new Distiller instance.
//
// Args:
//
//	config - distillation configuration.
//	embedder - embedding service for generating vectors.
//	repo - experience repository for storage and retrieval.
//
// Returns:
//
//	*Distiller - configured distiller instance.
func NewDistiller(config *DistillationConfig, embedder embedding.EmbeddingService, repo ExperienceRepository) *Distiller {
	if config == nil {
		config = DefaultDistillationConfig()
	}

	// Create noise filter with configuration
	noiseFilterConfig := &NoiseFilterConfig{
		EnableCodeFilter:          config.EnableCodeFilter,
		EnableStacktraceFilter:    config.EnableStacktraceFilter,
		EnableLogFilter:           config.EnableLogFilter,
		EnableMarkdownTableFilter: config.EnableMarkdownTableFilter,
	}

	return &Distiller{
		config:      config,
		extractor:   NewExperienceExtractorWithConfig(config.EnableCrossTurnExtraction),
		classifier:  NewMemoryClassifier(),
		scorer:      NewImportanceScorerWithConfig(config.MinImportance, config.EnableLengthBonus),
		resolver:    NewConflictResolverWithConfig(repo, config.ConflictThreshold, config.ConflictSearchLimit),
		noiseFilter: NewNoiseFilterWithConfig(noiseFilterConfig),
		embedder:    embedder,
		repo:        repo,
		metrics:     atomicMetrics{},
		distillEg:   &errgroup.Group{},
	}
}

// SetEmbeddingPipeline configures the unified embedding pipeline for conflict detection.
// When set, DistillConversation uses the pipeline with canonical spec builders
// instead of calling the raw embedder directly.
func (d *Distiller) SetEmbeddingPipeline(pipeline memembed.EmbeddingPipeline) {
	d.pipeline = pipeline
}

// WithExperienceStore configures the optional experience store for syncing memories.
// When set, DistillConversation will also write distilled memories as experiences.
//
// Args:
//
//	store - the experience store implementation. May be nil to disable syncing.
func (d *Distiller) WithExperienceStore(store ExperienceStore) {
	d.expStore = store
}

// memWithEmbedding holds a memory with its embedding validity status.
type memWithEmbedding struct {
	mem   Memory
	valid bool
}

// extractPhase extracts raw experiences from conversation messages.
//
// Args:
//
//	ctx - operation context.
//	conversationID - unique identifier for the conversation.
//	messages - conversation messages.
//
// Returns:
//
//	[]Experience - extracted experiences.
//	error - any error encountered.
func (d *Distiller) extractPhase(ctx context.Context, conversationID string, messages []Message) ([]Experience, error) {
	slog.DebugContext(ctx, "[Memory Distillation] Extracting experiences from conversation",
		"conversation_id", conversationID)

	experiences := d.extractor.ExtractExperiences(messages)
	if len(experiences) == 0 {
		slog.InfoContext(ctx, "[Memory Distillation] WARNING No experiences extracted from conversation",
			"conversation_id", conversationID,
			"reason", "filtered as noise")
		d.metrics.FilteredNoise.Add(1)
		return []Experience{}, nil
	}

	slog.InfoContext(ctx, "[Memory Distillation] Experiences extracted",
		"conversation_id", conversationID,
		"experience_count", len(experiences))

	return experiences, nil
}

// classifyAndScorePhase classifies experiences, applies filters, scores importance,
// and creates memory candidates.
//
// Args:
//
//	ctx - operation context.
//	conversationID - unique identifier for the conversation.
//	messages - original conversation messages for evidence extraction.
//	experiences - raw experiences to process.
//	tenantID - tenant ID for multi-tenancy.
//	userID - user ID for the conversation.
//
// Returns:
//
//	[]Memory - classified and scored memory candidates.
func (d *Distiller) classifyAndScorePhase(ctx context.Context, conversationID string, messages []Message, experiences []Experience, tenantID, userID string) []Memory {
	slog.DebugContext(ctx, "[Memory Distillation] Classifying and scoring experiences",
		"conversation_id", conversationID)

	var memories []Memory
	for idx, exp := range experiences {
		// Security filter (always apply).
		if !SecurityFilter(exp.Problem) || !SecurityFilter(exp.Solution) {
			slog.DebugContext(ctx, "[Memory Distillation] Experience filtered by security filter",
				"conversation_id", conversationID,
				"experience_index", idx,
				"reason", "security violation")
			d.metrics.FilteredSecurity.Add(1)
			continue
		}

		// Classify memory type FIRST (before noise filtering).
		memoryType := d.classifier.ClassifyMemory(&exp)

		// Noise filter: skip for user profiles, apply for others.
		if memoryType != MemoryProfile && d.noiseFilter.IsNoise(exp.Solution) {
			slog.DebugContext(ctx, "[Memory Distillation] Experience filtered as noise",
				"conversation_id", conversationID,
				"experience_index", idx,
				"memory_type", memoryType.String(),
				"reason", "content noise")
			d.metrics.FilteredNoise.Add(1)
			continue
		}

		// Score importance.
		problem := exp.Problem
		solution := exp.Solution
		score := d.scorer.ScoreMemory(memoryType, problem, solution)
		exp.Confidence = score

		// Skip low importance memories.
		if !d.scorer.ShouldKeep(score) {
			slog.DebugContext(ctx, "[Memory Distillation] Experience filtered by importance score",
				"conversation_id", conversationID,
				"experience_index", idx,
				"memory_type", memoryType.String(),
				"score", score,
				"threshold", d.config.MinImportance,
				"reason", "below importance threshold")
			d.metrics.FilteredNoise.Add(1)
			continue
		}

		// Create memory with UUID.
		evidence := extractEvidenceFromMessages(messages, extractTurnID(messages, problem))
		memory := Memory{
			ID:         uuid.New().String(),
			Type:       memoryType,
			Content:    FormatExperience(&exp),
			Importance: score,
			Source:     conversationID,
			CreatedAt:  time.Now(),
			Metadata: map[string]interface{}{
				"memory_type":       memoryType.String(),
				"conversation_id":   conversationID,
				"source":            "distillation",
				"confidence":        exp.Confidence,
				"extraction_method": string(exp.ExtractionMethod),
				"problem":           problem,
				"solution":          solution,
				"evidence":          evidence,
				"tenant_id":         tenantID,
				"user_id":           userID,
			},
		}

		slog.DebugContext(ctx, "[Memory Distillation] Memory candidate created",
			"conversation_id", conversationID,
			"experience_index", idx,
			"memory_type", memoryType.String(),
			"importance_score", score,
			"content_preview", truncpkg.WithEllipsis(memory.Content, 50))

		memories = append(memories, memory)
	}

	return memories
}

// topNBeforeConflictPhase applies Top-N filtering before conflict detection
// to improve performance when there are many candidates.
//
// Args:
//
//	ctx - operation context.
//	conversationID - unique identifier for the conversation.
//	memories - candidate memories to filter.
//
// Returns:
//
//	[]Memory - filtered memories.
func (d *Distiller) topNBeforeConflictPhase(ctx context.Context, conversationID string, memories []Memory) []Memory {
	if !d.config.TopNBeforeConflict || len(memories) <= d.config.MaxMemoriesPerDistillation {
		return memories
	}

	// Convert to experiences for scoring.
	var exps []Experience
	for _, mem := range memories {
		problem, problemOk := mem.Metadata["problem"].(string)
		if !problemOk {
			slog.WarnContext(ctx, "[Memory Distillation] Problem metadata is not a string", "conversation_id", conversationID)
			problem = ""
		}

		solution, solutionOk := mem.Metadata["solution"].(string)
		if !solutionOk {
			slog.WarnContext(ctx, "[Memory Distillation] Solution metadata is not a string", "conversation_id", conversationID)
			solution = ""
		}

		exps = append(exps, Experience{
			Problem:    problem,
			Solution:   solution,
			Confidence: mem.Importance,
		})
	}

	filtered := d.scorer.TopNFilter(exps, d.config.MaxMemoriesPerDistillation)

	// Rebuild memories from filtered experiences.
	memories = memories[:len(filtered)]
	for i, exp := range filtered {
		memories[i].Importance = exp.Confidence
		memories[i].Metadata["confidence"] = exp.Confidence
	}

	return memories
}

// embedPhase generates embeddings for all memories concurrently using errgroup.
//
// Args:
//
//	ctx - operation context.
//	conversationID - unique identifier for the conversation.
//	memories - memories to embed.
//
// Returns:
//
//	[]memWithEmbedding - embedded memories with validity flags.
func (d *Distiller) embedPhase(ctx context.Context, conversationID string, memories []Memory) []memWithEmbedding {
	embedded := make([]memWithEmbedding, len(memories))
	g, embedCtx := errgroup.WithContext(ctx)
	g.SetLimit(5)

	for idx := range memories {
		idx := idx
		g.Go(func() error {
			memory := memories[idx]
			var embedding []float64
			var err error
			if d.pipeline != nil {
				problem, _ := memory.Metadata["problem"].(string)
				solution, _ := memory.Metadata["solution"].(string)
				spec, specErr := d.pipeline.BuildSpec(memembed.KindMemoryExperience, memembed.MemoryExperienceInput{
					MemoryType: memory.Type.String(),
					Problem:    problem,
					Solution:   solution,
				})
				if specErr != nil {
					slog.WarnContext(embedCtx, "[Memory Distillation] Failed to build embedding spec",
						"conversation_id", conversationID, "memory_index", idx, "error", specErr)
					embedded[idx] = memWithEmbedding{valid: false}
					return nil
				}
				embedding, err = d.pipeline.Embed(embedCtx, spec)
			} else {
				embeddingText := fmt.Sprintf("%s → %s", memory.Metadata["problem"], memory.Metadata["solution"])
				embedding, err = d.embedder.EmbedWithPrefix(embedCtx, embeddingText, "memory:")
			}
			if err != nil {
				slog.WarnContext(embedCtx, "[Memory Distillation] Failed to generate embedding",
					"conversation_id", conversationID, "memory_index", idx, "error", err)
				embedded[idx] = memWithEmbedding{valid: false}
				return nil
			}
			memory.Vector = embedding
			embedded[idx] = memWithEmbedding{mem: memory, valid: true}
			slog.InfoContext(embedCtx, "[Memory Distillation] Embedding generated",
				"conversation_id", conversationID, "memory_index", idx,
				"memory_type", memory.Type.String(), "dimensions", len(embedding))
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		slog.ErrorContext(ctx, "[Memory Distillation] Embedding phase failed", "error", err)
	}

	return embedded
}

// resolveConflictsPhase detects and resolves conflicts among embedded memories.
//
// Args:
//
//	ctx - operation context.
//	conversationID - unique identifier for the conversation.
//	tenantID - tenant ID for multi-tenancy.
//	embedded - embedded memories with validity flags.
//
// Returns:
//
//	[]Memory - resolved memories after conflict handling.
func (d *Distiller) resolveConflictsPhase(ctx context.Context, conversationID, tenantID string, embedded []memWithEmbedding) []Memory {
	var finalMemories []Memory

	for idx, ew := range embedded {
		if !ew.valid {
			continue
		}
		memory := ew.mem

		problem, problemOk := memory.Metadata["problem"].(string)
		if !problemOk {
			problem = ""
		}
		solution, solutionOk := memory.Metadata["solution"].(string)
		if !solutionOk {
			solution = ""
		}

		exp := &Experience{
			Problem:    problem,
			Solution:   solution,
			Confidence: memory.Importance,
		}

		conflict, err := d.resolver.DetectConflict(ctx, memory.Vector, tenantID)
		if err != nil {
			slog.WarnContext(ctx, "[Memory Distillation] Failed to detect conflicts",
				"conversation_id", conversationID, "memory_index", idx, "error", err)
		}
		if conflict != nil {
			strategy := d.resolver.ResolveConflict(exp, conflict)
			slog.InfoContext(ctx, "[Memory Distillation] Conflict resolved",
				"conversation_id", conversationID, "memory_index", idx,
				"strategy", string(strategy))
			d.metrics.ConflictResolved.Add(1)

			switch strategy {
			case ReplaceOld:
				finalMemories = append(finalMemories, memory)
			case KeepBoth:
				oldMemory := Memory{
					ID:         uuid.New().String(),
					Content:    conflict.Problem,
					Metadata:   map[string]interface{}{"solution": conflict.Solution},
					Type:       memory.Type,
					Importance: conflict.Confidence,
					Vector:     conflict.Vector,
					CreatedAt:  time.Now(),
				}
				finalMemories = append(finalMemories, oldMemory)
				finalMemories = append(finalMemories, memory)
			default:
				finalMemories = append(finalMemories, memory)
				slog.WarnContext(ctx, "[Memory Distillation] WARNING Unknown resolution strategy, defaulting to keep new memory",
					"conversation_id", conversationID,
					"memory_index", idx,
					"strategy", string(strategy))
			}
		} else {
			finalMemories = append(finalMemories, memory)
		}
	}

	return finalMemories
}

// finalTopNPhase applies final Top-N filtering after conflict resolution.
//
// Args:
//
//	memories - resolved memories to filter.
//
// Returns:
//
//	[]Memory - filtered memories.
func (d *Distiller) finalTopNPhase(memories []Memory) []Memory {
	if len(memories) <= d.config.MaxMemoriesPerDistillation {
		return memories
	}

	sort.Slice(memories, func(i, j int) bool {
		return memories[i].Importance > memories[j].Importance
	})
	return memories[:d.config.MaxMemoriesPerDistillation]
}

// DistillConversation distills memories from a conversation.
// This is the main entry point for the distillation process.
// It orchestrates the full pipeline: extraction, classification, scoring,
// embedding, conflict resolution, and optional syncing to experience store.
//
// Args:
//
//	ctx - operation context.
//	conversationID - unique identifier for the conversation.
//	messages - conversation messages.
//	tenantID - tenant ID for multi-tenancy.
//	userID - user ID for the conversation.
//
// Returns:
//
//	[]Memory - distilled memories.
//	error - any error encountered.
func (d *Distiller) DistillConversation(ctx context.Context, conversationID string, messages []Message, tenantID, userID string) ([]Memory, error) {
	startTime := time.Now()
	slog.InfoContext(ctx, "[Memory Distillation] Starting distillation process",
		"conversation_id", conversationID,
		"tenant_id", tenantID,
		"user_id", userID,
		"message_count", len(messages),
		"timestamp", startTime.Format(time.RFC3339))

	d.metrics.AttemptTotal.Add(1)

	if ctx.Err() != nil {
		slog.ErrorContext(ctx, "[Memory Distillation] ERROR Context cancelled",
			"conversation_id", conversationID,
			"error", ctx.Err())
		return nil, fmt.Errorf("context cancelled: %w", ctx.Err())
	}

	// Phase 1: Extract experiences.
	experiences, err := d.extractPhase(ctx, conversationID, messages)
	if err != nil {
		return nil, err
	}

	// Phase 2: Classify and score experiences into memory candidates.
	memories := d.classifyAndScorePhase(ctx, conversationID, messages, experiences, tenantID, userID)
	if len(memories) == 0 {
		slog.InfoContext(ctx, "[Memory Distillation] WARNING No memories passed all filters",
			"conversation_id", conversationID,
			"initial_experiences", len(experiences))
		return []Memory{}, nil
	}

	slog.InfoContext(ctx, "[Memory Distillation] Memory candidates created",
		"conversation_id", conversationID,
		"candidate_count", len(memories),
		"filtered_count", len(experiences)-len(memories))

	// Phase 3: Top-N filtering before conflict detection.
	memories = d.topNBeforeConflictPhase(ctx, conversationID, memories)

	// Phase 4: Generate embeddings concurrently.
	slog.InfoContext(ctx, "[Memory Distillation] Generating embeddings and detecting conflicts",
		"conversation_id", conversationID,
		"memory_count", len(memories))

	embedded := d.embedPhase(ctx, conversationID, memories)

	// Phase 5: Conflict detection and resolution.
	finalMemories := d.resolveConflictsPhase(ctx, conversationID, tenantID, embedded)

	// Phase 6: Final Top-N filtering.
	finalMemories = d.finalTopNPhase(finalMemories)

	// Phase 7: Enforce solution cap.
	slog.DebugContext(ctx, "[Memory Distillation] Enforcing solution cap",
		"conversation_id", conversationID,
		"tenant_id", tenantID,
		"current_memories", len(finalMemories))

	if err := d.enforceSolutionCap(ctx, tenantID); err != nil {
		slog.WarnContext(ctx, "[Memory Distillation] WARNING Failed to enforce solution cap",
			"tenant_id", tenantID,
			"error", err.Error())
	}

	d.metrics.SuccessTotal.Add(1)
	d.metrics.MemoriesCreated.Add(int64(len(finalMemories)))

	// Phase 8: Sync to experience store if configured.
	if d.expStore != nil {
		if err := d.syncToExperienceStore(ctx, finalMemories, tenantID); err != nil {
			slog.WarnContext(ctx, "[Memory Distillation] Failed to sync to experience store",
				"conversation_id", conversationID,
				"error", err)
		}
	}

	slog.InfoContext(ctx, "[Memory Distillation] Distillation completed successfully",
		"conversation_id", conversationID,
		"tenant_id", tenantID,
		"user_id", userID,
		"final_memory_count", len(finalMemories),
		"importance_scores", formatImportanceScores(finalMemories),
		"memory_types", formatMemoryTypes(finalMemories),
		"metrics", d.metrics.String(),
		"duration_ms", time.Since(startTime).Milliseconds())

	return finalMemories, nil
}

// enforceSolutionCap enforces the global cap on solution memories per tenant.
// If the number of solution memories exceeds the cap, the lowest importance
// memories are marked for removal.
//
// Args:
//
//	ctx - operation context.
//	tenantID - tenant ID for multi-tenancy.
//
// Returns:
//
//	error - any error encountered.
func (d *Distiller) enforceSolutionCap(ctx context.Context, tenantID string) error {
	if d.repo == nil {
		return nil
	}

	// Count first to avoid loading all solutions when under cap.
	count, err := d.repo.CountByMemoryType(ctx, tenantID, MemoryKnowledge)
	if err != nil {
		return errors.Wrap(err, "failed to get solution count")
	}

	if count <= d.config.MaxSolutionsPerTenant {
		return nil
	}

	// Over cap: load only the excess lowest-confidence solutions.
	solutions, err := d.repo.GetByMemoryType(ctx, tenantID, MemoryKnowledge)
	if err != nil {
		return errors.Wrap(err, "failed to get solutions for pruning")
	}

	// Sort by confidence ascending and delete the lowest ones.
	sort.Slice(solutions, func(i, j int) bool {
		return solutions[i].Confidence < solutions[j].Confidence
	})

	deleteCount := count - d.config.MaxSolutionsPerTenant
	if deleteCount > len(solutions) {
		deleteCount = len(solutions)
	}

	ids := make([]string, deleteCount)
	for i := 0; i < deleteCount; i++ {
		ids[i] = solutions[i].Problem
	}

	slog.WarnContext(ctx, "solution count exceeds cap, pruning lowest importance memories",
		"tenant_id", tenantID,
		"current_count", count,
		"max_count", d.config.MaxSolutionsPerTenant,
		"delete_count", deleteCount,
	)

	if err := d.repo.DeleteBatch(ctx, ids); err != nil {
		// Fall back to individual deletes on batch failure.
		for i, id := range ids {
			if err := d.repo.Delete(ctx, id); err != nil {
				slog.WarnContext(ctx, "failed to delete solution during pruning",
					"problem", solutions[i].Problem,
					"error", err)
			}
		}
	}

	return nil
}

// GetMetrics returns the current distillation metrics.
//
// Thread-safety: Uses atomic operations to safely read metrics.
//
// Returns:
//
//	*DistillationMetrics - the metrics.
func (d *Distiller) GetMetrics() *DistillationMetrics {
	return &DistillationMetrics{
		AttemptTotal:     d.metrics.AttemptTotal.Load(),
		SuccessTotal:     d.metrics.SuccessTotal.Load(),
		FilteredNoise:    d.metrics.FilteredNoise.Load(),
		FilteredSecurity: d.metrics.FilteredSecurity.Load(),
		ConflictResolved: d.metrics.ConflictResolved.Load(),
		MemoriesCreated:  d.metrics.MemoriesCreated.Load(),
	}
}

// ResetMetrics resets the distillation metrics.
//
// Thread-safety: Uses atomic operations to safely reset metrics.
func (d *Distiller) ResetMetrics() {
	d.metrics.AttemptTotal.Store(0)
	d.metrics.SuccessTotal.Store(0)
	d.metrics.FilteredNoise.Store(0)
	d.metrics.FilteredSecurity.Store(0)
	d.metrics.ConflictResolved.Store(0)
	d.metrics.MemoriesCreated.Store(0)
}

// SubscribeAndDistill subscribes to an EventStore and automatically
// distills memories from incoming ares_events.
//
// Args:
//
//	ctx - operation context. Cancelling it closes the subscription.
//	store - the event store to subscribe to. If nil, this method is a no-op.
func (d *Distiller) SubscribeAndDistill(ctx context.Context, store ares_events.EventStore) {
	if store == nil {
		return
	}
	ch, err := store.Subscribe(ctx, ares_events.EventFilter{
		Types: []ares_events.EventType{
			ares_events.EventMessageAdded,
			ares_events.EventTaskCompleted,
		},
	})
	if err != nil {
		slog.Error("failed to subscribe to ares_events for distillation", "error", err)
		return
	}

	slog.InfoContext(ctx, "[Memory Distillation] Event subscription started")

	// Track goroutine lifecycle so callers can wait for drain.
	d.distillWg.Add(1)
	d.distillEg.Go(func() error {
		defer d.distillWg.Done()
		for {
			select {
			case <-ctx.Done():
				slog.InfoContext(ctx, "[Memory Distillation] Event subscription stopped by context")
				return ctx.Err()
			case event, ok := <-ch:
				if !ok {
					slog.InfoContext(ctx, "[Memory Distillation] Event channel closed")
					return nil
				}
				d.processEvent(ctx, event)
			}
		}
	})
}

// processEvent handles a single event for distillation.
//
// Args:
//
//	ctx - operation context.
//	event - the event to process. If nil, this method is a no-op.
func (d *Distiller) processEvent(ctx context.Context, event *ares_events.Event) {
	if event == nil {
		return
	}
	switch event.Type {
	case ares_events.EventMessageAdded:
		slog.Debug("distiller received message event",
			"stream_id", event.StreamID,
			"role", event.Payload["role"],
		)
	case ares_events.EventTaskCompleted:
		taskID, _ := event.Payload["task_id"].(string)
		slog.Debug("distiller received task completion",
			"stream_id", event.StreamID,
			"task_id", taskID,
		)
		if taskID != "" && d.OnTaskCompleted != nil {
			d.OnTaskCompleted(ctx, taskID)
		}
	default:
		slog.Debug("distiller ignoring event type", "type", event.Type)
	}
}

// formatImportanceScores formats importance scores for logging.
func formatImportanceScores(memories []Memory) string {
	if len(memories) == 0 {
		return "[]"
	}
	scores := make([]string, len(memories))
	for i, mem := range memories {
		scores[i] = fmt.Sprintf("%.2f", mem.Importance)
	}
	return "[" + strings.Join(scores, ", ") + "]"
}

// formatMemoryTypes formats memory types for logging.
func formatMemoryTypes(memories []Memory) string {
	if len(memories) == 0 {
		return "[]"
	}
	types := make([]string, len(memories))
	for i, mem := range memories {
		types[i] = string(mem.Type)
	}
	return fmt.Sprintf("%v", types)
}

// extractTurnID finds the TurnID of the user message that matches the given problem text.
// This is a lightweight lookup that avoids text matching on every message.
func extractTurnID(messages []Message, problem string) string {
	problemTrunc := truncpkg.Plain(problem, 50)
	for _, msg := range messages {
		if msg.Role == "user" && strings.Contains(msg.Content, problemTrunc) {
			return msg.TurnID
		}
	}
	return ""
}

// extractEvidenceFromMessages collects tool observation evidence from messages
// belonging to the given turn. Uses TurnID for precise structured association
// (not content text matching, which is fragile with truncated/duplicated text).
// Tool result content comes from cleaner-generated summaries, not raw regexp extraction.
func extractEvidenceFromMessages(messages []Message, turnID string) []string {
	if turnID == "" || len(messages) == 0 {
		return nil
	}

	var evidence []string
	for _, msg := range messages {
		if msg.TurnID != turnID {
			continue
		}
		switch msg.Role {
		case "tool_call":
			for _, tc := range msg.ToolCalls {
				if fn, ok := tc["function"].(map[string]interface{}); ok {
					if name, ok := fn["name"].(string); ok {
						id, _ := tc["id"].(string)
						if id != "" {
							evidence = append(evidence, fmt.Sprintf("Action %s: %s", id, name))
						} else {
							evidence = append(evidence, fmt.Sprintf("Action: %s", name))
						}
					}
				}
			}
		case "tool_result":
			if msg.Content != "" {
				// Content is already a cleaner-generated summary (from buildCleanedDistillationMessages),
				// not raw tool output. Truncate length only, no regexp extraction needed.
				if len(msg.Content) > 120 {
					evidence = append(evidence, fmt.Sprintf("Observed: %s...", msg.Content[:120]))
				} else {
					evidence = append(evidence, fmt.Sprintf("Observed: %s", msg.Content))
				}
			}
		}
	}
	if len(evidence) == 0 {
		return nil
	}
	return evidence
}

// syncToExperienceStore writes distilled memories to the experience store.
// It converts each memory to an experience using type mapping rules.
//
// Args:
//
//	ctx - operation context.
//	memories - the distilled memories to sync.
//	tenantID - tenant ID for multi-tenancy.
//
// Returns:
//
//	error - the first error encountered, or nil.
func (d *Distiller) syncToExperienceStore(ctx context.Context, memories []Memory, tenantID string) error {
	for _, mem := range memories {
		exp := d.convertMemoryToExperience(&mem, tenantID)
		if err := d.expStore.Create(ctx, exp); err != nil {
			return fmt.Errorf("sync memory %s to experience store: %w", mem.ID, err)
		}
		slog.DebugContext(ctx, "[Memory Distillation] Synced memory to experience store",
			"memory_id", mem.ID,
			"experience_type", exp.Type)
	}
	return nil
}

// convertMemoryToExperience converts a Memory to a StoredExperience using type mapping rules.
//
// Mapping rules:
//
//	MemoryKnowledge   → TypeSolution
//	MemoryInteraction → TypeSolution
//	MemoryPreference  → TypeHeuristic
//	MemoryProfile     → TypeStrategy
//
// Args:
//
//	mem - the memory to convert.
//	tenantID - tenant ID for multi-tenancy.
//
// Returns:
//
//	*StoredExperience - the converted experience.
func (d *Distiller) convertMemoryToExperience(mem *Memory, tenantID string) *StoredExperience {
	problem, _ := mem.Metadata["problem"].(string)
	solution, _ := mem.Metadata["solution"].(string)

	return &StoredExperience{
		TenantID: tenantID,
		Type:     memoryTypeToExperienceType(mem.Type),
		Problem:  problem,
		Solution: solution,
		Score:    mem.Importance,
		Source:   "memory_distillation",
		Metadata: map[string]interface{}{
			"memory_id":   mem.ID,
			"memory_type": mem.Type.String(),
			"content":     mem.Content,
			"source":      mem.Source,
			"importance":  mem.Importance,
			"created_at":  mem.CreatedAt.Format(time.RFC3339),
		},
	}
}

// Experience type constants for the experience store.
const (
	TypeSolution  = "solution"
	TypeHeuristic = "heuristic"
	TypeStrategy  = "strategy"
	TypeFailure   = "failure"
	TypeGeneral   = "general"
)

// memoryTypeToExperienceType maps Memory types to Experience types.
// This bridges the memory distillation system with the experience system.
//
// Args:
//
//	mt - the memory type.
//
// Returns:
//
//	string - the corresponding experience type.
func memoryTypeToExperienceType(mt MemoryType) string {
	switch mt {
	case MemoryKnowledge:
		return TypeSolution
	case MemoryInteraction:
		return TypeSolution
	case MemoryPreference:
		return TypeHeuristic
	case MemoryProfile:
		return TypeStrategy
	default:
		return TypeGeneral
	}
}
