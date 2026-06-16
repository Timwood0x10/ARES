package events

import (
	"context"
	"log/slog"
	"sync"
)

// CompactableEventStore wraps an EventStore to automatically trigger compaction
// when a stream exceeds the configured event threshold.
//
// Usage pattern:
//
//	store := NewCompactableEventStore(
//	    pgStore,           // underlying EventStore (Postgres or Memory)
//	    summaryRepo,       // SummaryRepository for persisting summaries
//	    trimStore,         // TrimAwareStore for deleting compacted events
//	    DefaultCompactionConfig(),
//	)
//	// Use store.Append() as normal — compaction runs automatically in background.
type CompactableEventStore struct {
	EventStore
	compactor *Compactor
	trimStore TrimAwareStore
	mu        sync.Mutex

	// Track which streams have been recently checked to avoid redundant checks.
	// Key: streamID, value: last version at which compaction was checked.
	lastChecked map[string]int64
}

// NewCompactableEventStore creates a new auto-compacting event store wrapper.
//
// Parameters:
//   - store: The underlying EventStore (PostgresEventStore or MemoryEventStore)
//   - repo: Repository for persisting event summaries
//   - trimStore: Optional TrimAwareStore for trimming compacted events (nil = no trimming)
//   - config: Compaction configuration
func NewCompactableEventStore(
	store EventStore,
	repo SummaryRepository,
	trimStore TrimAwareStore,
	config CompactionConfig,
) *CompactableEventStore {
	c := &CompactableEventStore{
		EventStore:  store,
		trimStore:   trimStore,
		lastChecked: make(map[string]int64),
	}

	c.compactor = NewCompactor(store, repo, config)

	// If a trim store is provided and trimming is enabled, wire it into the compactor.
	if trimStore != nil && config.EnableTrimming {
		c.compactor = c.compactor.WithTrimStore(trimStore)
	}

	return c
}

// Append writes events to the store and then checks if compaction is needed.
// Compaction runs synchronously but only when the threshold is exceeded,
// and only once per threshold boundary per stream (debounced).
func (s *CompactableEventStore) Append(
	ctx context.Context,
	streamID string,
	events []*Event,
	expectedVersion int64,
) error {
	err := s.EventStore.Append(ctx, streamID, events, expectedVersion)
	if err != nil {
		return err
	}

	// Async compaction check — don't block the Append caller.
	go s.maybeCompact(streamID)

	return nil
}

// maybeCompact checks if a stream needs compaction and runs it if so.
// Uses debouncing to avoid redundant checks on every Append.
func (s *CompactableEventStore) maybeCompact(streamID string) {
	s.mu.Lock()
	version, err := s.StreamVersion(context.Background(), streamID)
	if err != nil {
		s.mu.Unlock()
		slog.Debug("compaction: failed to get version", "stream_id", streamID, "error", err)
		return
	}

	lastCheck := s.lastChecked[streamID]
	threshold := s.compactor.config.Threshold

	// Only check compaction if we've crossed a new threshold boundary.
	// This avoids checking on every single append.
	if version <= int64(threshold) || version-lastCheck < int64(threshold)/4 {
		s.mu.Unlock()
		return
	}
	s.lastChecked[streamID] = version
	s.mu.Unlock()

	ctx := context.Background()
	didCompact, err := s.compactor.CheckAndCompact(ctx, streamID)
	if err != nil {
		slog.Error("compaction: automatic compaction failed",
			"stream_id", streamID,
			"error", err,
		)
		return
	}

	if didCompact && s.trimStore != nil && s.compactor.config.EnableTrimming {
		// After successful compaction, trim old events from the live store.
		summaries, err := s.compactor.repo.FindByStreamID(ctx, streamID)
		if err == nil && len(summaries) > 0 {
			latest := summaries[len(summaries)-1]
			if _, trimErr := s.trimStore.TrimBefore(ctx, streamID, latest.EndVersion); trimErr != nil {
				slog.Warn("compaction: post-compaction trim failed", "error", trimErr)
			}
		}
	}

	if didCompact {
		slog.Info("compaction: automatic compaction completed",
			"stream_id", streamID,
			"current_version", version,
		)
	}
}

// ForceCompact forces immediate compaction of a stream regardless of thresholds.
func (s *CompactableEventStore) ForceCompact(ctx context.Context, streamID string) (bool, error) {
	return s.compactor.ForceCompact(ctx, streamID)
}

// CleanupSummaries removes expired summaries based on TTL.
func (s *CompactableEventStore) CleanupSummaries(ctx context.Context) (int64, error) {
	return s.compactor.CleanupOldSummaries(ctx)
}

// GetSummariesForStream returns all summaries for a given stream.
func (s *CompactableEventStore) GetSummariesForStream(ctx context.Context, streamID string) ([]*EventSummary, error) {
	return s.compactor.repo.FindByStreamID(ctx, streamID)
}

// GetSummariesForAgent returns all summaries for an agent across all tasks.
func (s *CompactableEventStore) GetSummariesForAgent(ctx context.Context, agentID string) ([]*EventSummary, error) {
	return s.compactor.repo.FindByAgentID(ctx, agentID)
}

// WithCustomSummarizer replaces the default rule-based summarizer with a custom one
// (e.g., an LLM-powered summarizer for richer summaries).
func (s *CompactableEventStore) WithCustomSummarizer(summarizer EventSummarizer) *CompactableEventStore {
	s.compactor.summarizer = summarizer
	return s
}
