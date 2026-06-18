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

	"goagentx/internal/errors"
	"goagentx/internal/events"
	memembed "goagentx/internal/memory/embedding"
	"goagentx/internal/storage/postgres/embedding"
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
	metrics     atomicMetrics  // Thread-safe atomic counters
	distillWg   sync.WaitGroup // Tracks event subscription goroutines
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
	}
}

// SetEmbeddingPipeline configures the unified embedding pipeline for conflict detection.
// When set, DistillConversation uses the pipeline with canonical spec builders
// instead of calling the raw embedder directly.
func (d *Distiller) SetEmbeddingPipeline(pipeline memembed.EmbeddingPipeline) {
	d.pipeline = pipeline
}

// DistillConversation distills memories from a conversation.
// This is the main entry point for the distillation process.
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

	// Step 1: Extract experiences
	slog.DebugContext(ctx, "[Memory Distillation] Extracting experiences from conversation",
		"conversation_id", conversationID)
	experiences := d.extractor.ExtractExperiences(messages)
	if len(experiences) == 0 {
		slog.InfoContext(ctx, "[Memory Distillation] WARNING No experiences extracted from conversation",
			"conversation_id", conversationID,
			"reason", "filtered as noise")
		d.metrics.FilteredNoise.Add(1)
		return []Memory{}, nil
	}
	slog.InfoContext(ctx, "[Memory Distillation] Experiences extracted",
		"conversation_id", conversationID,
		"experience_count", len(experiences))

	// Step 2: Classify experiences and create memory candidates
	slog.DebugContext(ctx, "[Memory Distillation] Classifying and scoring experiences",
		"conversation_id", conversationID)
	var memories []Memory
	for idx, exp := range experiences {
		// Security filter (always apply)
		if !SecurityFilter(exp.Problem) || !SecurityFilter(exp.Solution) {
			slog.DebugContext(ctx, "[Memory Distillation] Experience filtered by security filter",
				"conversation_id", conversationID,
				"experience_index", idx,
				"reason", "security violation")
			d.metrics.FilteredSecurity.Add(1)
			continue
		}

		// Classify memory type FIRST (before noise filtering)
		memoryType := d.classifier.ClassifyMemory(&exp)

		// Noise filter: skip for user profiles, apply for others
		// User profiles contain personal info and should not be filtered as noise
		if memoryType != MemoryProfile && d.noiseFilter.IsNoise(exp.Solution) {
			slog.DebugContext(ctx, "[Memory Distillation] Experience filtered as noise",
				"conversation_id", conversationID,
				"experience_index", idx,
				"memory_type", memoryType.String(),
				"reason", "content noise")
			d.metrics.FilteredNoise.Add(1)
			continue
		}

		// Score importance
		problem := exp.Problem
		solution := exp.Solution
		score := d.scorer.ScoreMemory(memoryType, problem, solution)

		// Update confidence with importance score
		exp.Confidence = score

		// Skip low importance memories
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

		// Create memory with UUID
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
				"tenant_id":         tenantID,
				"user_id":           userID,
			},
		}

		slog.DebugContext(ctx, "[Memory Distillation] Memory candidate created",
			"conversation_id", conversationID,
			"experience_index", idx,
			"memory_type", memoryType.String(),
			"importance_score", score,
			"content_preview", truncateString(memory.Content, 50))

		memories = append(memories, memory)
	}

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

	// Step 3: Top-N filtering (before conflict detection for performance)
	if d.config.TopNBeforeConflict && len(memories) > d.config.MaxMemoriesPerDistillation {
		// Convert to experiences for scoring
		var exps []Experience
		for _, mem := range memories {
			// Extract problem and solution with type assertion and error handling
			problem, problemOk := mem.Metadata["problem"].(string)
			if !problemOk {
				slog.WarnContext(ctx, "[Memory Distillation] Problem metadata is not a string", "conversation_id", conversationID)
				problem = "" // Use empty string as fallback
			}

			solution, solutionOk := mem.Metadata["solution"].(string)
			if !solutionOk {
				slog.WarnContext(ctx, "[Memory Distillation] Solution metadata is not a string", "conversation_id", conversationID)
				solution = "" // Use empty string as fallback
			}

			exps = append(exps, Experience{
				Problem:    problem,
				Solution:   solution,
				Confidence: mem.Importance,
			})
		}

		filtered := d.scorer.TopNFilter(exps, d.config.MaxMemoriesPerDistillation)

		// Rebuild memories from filtered experiences
		memories = memories[:len(filtered)]
		for i, exp := range filtered {
			memories[i].Importance = exp.Confidence
			memories[i].Metadata["confidence"] = exp.Confidence
		}
	}

	// Step 4: Conflict detection and resolution with vector generation
	slog.InfoContext(ctx, "[Memory Distillation] Generating embeddings and detecting conflicts",
		"conversation_id", conversationID,
		"memory_count", len(memories))
	var finalMemories []Memory
	for idx, memory := range memories {
		// Generate embedding for conflict detection and retrieval.
		// Use the unified embedding pipeline when available.
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
				slog.WarnContext(ctx, "[Memory Distillation] ERROR Failed to build embedding spec",
					"conversation_id", conversationID,
					"memory_index", idx,
					"error", specErr.Error(),
					"action", "skipping this memory")
				continue
			}
			embedding, err = d.pipeline.Embed(ctx, spec)
		} else {
			embeddingText := fmt.Sprintf("%s → %s", memory.Metadata["problem"], memory.Metadata["solution"])
			embedding, err = d.embedder.EmbedWithPrefix(ctx, embeddingText, "memory:")
		}
		if err != nil {
			slog.WarnContext(ctx, "[Memory Distillation] ERROR Failed to generate embedding for memory",
				"conversation_id", conversationID,
				"memory_index", idx,
				"memory_type", memory.Type.String(),
				"error", err.Error(),
				"action", "skipping this memory")
			continue
		}
		memory.Vector = embedding

		slog.InfoContext(ctx, "[Memory Distillation] Embedding generated successfully",
			"conversation_id", conversationID,
			"memory_index", idx,
			"memory_type", memory.Type.String(),
			"vector_dimensions", len(embedding),
			"importance_score", memory.Importance)

		// Detect conflicts with existing memories
		// Extract problem and solution with type assertion and error handling
		problem, problemOk := memory.Metadata["problem"].(string)
		if !problemOk {
			slog.WarnContext(ctx, "[Memory Distillation] Problem metadata is not a string", "conversation_id", conversationID)
			problem = "" // Use empty string as fallback
		}

		solution, solutionOk := memory.Metadata["solution"].(string)
		if !solutionOk {
			slog.WarnContext(ctx, "[Memory Distillation] Solution metadata is not a string", "conversation_id", conversationID)
			solution = "" // Use empty string as fallback
		}

		exp := &Experience{
			Problem:    problem,
			Solution:   solution,
			Confidence: memory.Importance,
		}

		slog.DebugContext(ctx, "[Memory Distillation] Detecting conflicts",
			"conversation_id", conversationID,
			"memory_index", idx)

		conflict, err := d.resolver.DetectConflict(ctx, memory.Vector, tenantID)
		if err != nil {
			slog.WarnContext(ctx, "[Memory Distillation] WARNING Failed to detect memory conflicts",
				"conversation_id", conversationID,
				"memory_index", idx,
				"error", err.Error(),
				"action", "proceeding without conflict check")
		}
		if conflict != nil {
			// Resolve conflict based on confidence/importance
			strategy := d.resolver.ResolveConflict(exp, conflict)
			slog.InfoContext(ctx, "[Memory Distillation] Memory conflict detected and resolved",
				"conversation_id", conversationID,
				"memory_index", idx,
				"strategy", string(strategy),
				"new_confidence", exp.Confidence,
				"old_confidence", conflict.Confidence,
				"conflict_content", truncateString(conflict.Problem, 50))
			d.metrics.ConflictResolved.Add(1)

			// Apply the resolution strategy
			switch strategy {
			case ReplaceOld:
				// Replace the old memory with the new one
				finalMemories = append(finalMemories, memory)
				slog.DebugContext(ctx, "[Memory Distillation] Replaced old memory with new one",
					"conversation_id", conversationID,
					"memory_index", idx)
			case KeepBoth:
				// Keep both memories - add the old one back and then the new one
				// Convert the conflicting experience back to memory format
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
				slog.DebugContext(ctx, "[Memory Distillation] Kept both old and new memories",
					"conversation_id", conversationID,
					"memory_index", idx)
			default:
				// Fallback to keeping the new memory
				finalMemories = append(finalMemories, memory)
				slog.WarnContext(ctx, "[Memory Distillation] WARNING Unknown resolution strategy, defaulting to keep new memory",
					"conversation_id", conversationID,
					"memory_index", idx,
					"strategy", string(strategy))
			}
		} else {
			// No conflict, keep the memory
			finalMemories = append(finalMemories, memory)
		}
	}

	// Step 5: Final Top-N filtering (after conflict resolution)
	if len(finalMemories) > d.config.MaxMemoriesPerDistillation {
		sort.Slice(finalMemories, func(i, j int) bool {
			return finalMemories[i].Importance > finalMemories[j].Importance
		})
		finalMemories = finalMemories[:d.config.MaxMemoriesPerDistillation]
	}

	// Step 6: Enforce solution cap

	slog.DebugContext(ctx, "[Memory Distillation] Enforcing solution cap",

		"conversation_id", conversationID,

		"tenant_id", tenantID,

		"current_memories", len(finalMemories))

	err := d.enforceSolutionCap(ctx, tenantID)

	if err != nil {

		slog.WarnContext(ctx, "[Memory Distillation] WARNING Failed to enforce solution cap",

			"tenant_id", tenantID,

			"error", err.Error())

	}

	d.metrics.SuccessTotal.Add(1)

	d.metrics.MemoriesCreated.Add(int64(len(finalMemories)))

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

	solutions, err := d.repo.GetByMemoryType(ctx, tenantID, MemoryKnowledge)
	if err != nil {
		return errors.Wrap(err, "failed to get solution count")
	}

	if len(solutions) <= d.config.MaxSolutionsPerTenant {
		return nil
	}

	slog.WarnContext(ctx, "solution count exceeds cap, pruning lowest importance memories",
		"tenant_id", tenantID,
		"current_count", len(solutions),
		"max_count", d.config.MaxSolutionsPerTenant,
	)

	sort.Slice(solutions, func(i, j int) bool {
		return solutions[i].Confidence < solutions[j].Confidence
	})

	deleteCount := len(solutions) - d.config.MaxSolutionsPerTenant
	for i := 0; i < deleteCount; i++ {
		if err := d.repo.Delete(ctx, solutions[i].Problem); err != nil {
			slog.WarnContext(ctx, "failed to delete solution during pruning",
				"problem", solutions[i].Problem,
				"error", err)
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
// distills memories from incoming events.
//
// Args:
//
//	ctx - operation context. Cancelling it closes the subscription.
//	store - the event store to subscribe to. If nil, this method is a no-op.
func (d *Distiller) SubscribeAndDistill(ctx context.Context, store events.EventStore) {
	if store == nil {
		return
	}
	ch, err := store.Subscribe(ctx, events.EventFilter{
		Types: []events.EventType{
			events.EventMessageAdded,
			events.EventTaskCompleted,
		},
	})
	if err != nil {
		slog.Error("failed to subscribe to events for distillation", "error", err)
		return
	}

	// Track goroutine lifecycle so callers can wait for drain.
	d.distillWg.Add(1)
	go func() {
		defer d.distillWg.Done()
		for event := range ch {
			d.processEvent(ctx, event)
		}
	}()
}

// processEvent handles a single event for distillation.
//
// Args:
//
//	ctx - operation context.
//	event - the event to process. If nil, this method is a no-op.
func (d *Distiller) processEvent(ctx context.Context, event *events.Event) {
	if event == nil {
		return
	}
	switch event.Type {
	case events.EventMessageAdded:
		slog.Debug("distiller received message event",
			"stream_id", event.StreamID,
			"role", event.Payload["role"],
		)
	case events.EventTaskCompleted:
		slog.Debug("distiller received task completion",
			"stream_id", event.StreamID,
			"task_id", event.Payload["task_id"],
		)
		// TODO: trigger distillation from event payload (expected by 2026-07-01)
	default:
		slog.Debug("distiller ignoring event type", "type", event.Type)
	}
}

// truncateString truncates a string to the specified maximum length.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
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
