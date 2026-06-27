package ares_events

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	apperrors "github.com/Timwood0x10/ares/internal/errors"
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
) (*CompactableEventStore, error) {
	if store == nil {
		return nil, apperrors.New("store must not be nil")
	}
	if repo == nil {
		return nil, apperrors.New("summary repository must not be nil")
	}

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

	return c, nil
}

// compactionTimeout is the maximum duration allowed for a single compaction check.
const compactionTimeout = 30 * time.Second

// Append writes events to the store and then checks if compaction is needed.
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

	// Launch compaction check in background with a timeout context to prevent
	// runaway goroutines. Uses errgroup per coding standard (no bare go).
	compactCtx, cancel := context.WithTimeout(context.Background(), compactionTimeout)
	g, gCtx := errgroup.WithContext(compactCtx)
	g.Go(func() error {
		s.maybeCompact(gCtx, streamID)
		return nil
	})

	// Fire-and-forget: wait for group in background so caller is not blocked.
	go func() {
		_ = g.Wait()
		cancel()
	}()

	return nil
}

// Read returns events for a stream. When the underlying store returns empty
// but summaries exist for the stream, it falls back to returning the summaries
// as synthetic events. This prevents ReplaySession from breaking after compaction
// has trimmed old raw events.
func (s *CompactableEventStore) Read(ctx context.Context, streamID string, opts ReadOptions) ([]*Event, error) {
	events, err := s.EventStore.Read(ctx, streamID, opts)
	if err != nil {
		return nil, err
	}
	if len(events) > 0 {
		return events, nil
	}

	// Underlying store returned empty — check summaries as fallback.
	if s.compactor == nil || s.compactor.repo == nil {
		return events, nil
	}
	summaries, summaryErr := s.compactor.repo.FindByStreamID(ctx, streamID)
	if summaryErr != nil || len(summaries) == 0 {
		// No summaries either, return the original empty result.
		return events, nil
	}

	// Convert summaries to synthetic events.
	synthetic := make([]*Event, 0, len(summaries))
	for _, sum := range summaries {
		synthetic = append(synthetic, &Event{
			ID:       sum.ID,
			StreamID: sum.StreamID,
			Type:     EventType("event.summary"),
			Payload: map[string]any{
				"summary_text":  sum.SummaryText,
				"event_count":   sum.EventCount,
				"start_version": sum.StartVersion,
				"end_version":   sum.EndVersion,
				"outcome":       sum.Outcome,
			},
			Version:   sum.EndVersion,
			Timestamp: sum.CreatedAt,
		})
	}
	return synthetic, nil
}

// Debounce divisor: skip compaction check until version advances by at least
// threshold/4 since the last check, reducing redundant I/O on busy streams.
const compactionCheckDivisor = 4

// maybeCompact checks if a stream needs compaction and runs it if so.
// Uses debouncing to avoid redundant checks on every Append.
func (s *CompactableEventStore) maybeCompact(ctx context.Context, streamID string) {
	if s.compactor == nil {
		return
	}
	// Get version outside the lock to avoid holding mu during I/O.
	version, err := s.StreamVersion(ctx, streamID)
	if err != nil {
		log.Debug("compaction: failed to get version", "stream_id", streamID, "error", err)
		return
	}

	s.mu.Lock()
	lastCheck := s.lastChecked[streamID]
	threshold := s.compactor.config.Threshold

	if version <= int64(threshold) || version-lastCheck < int64(threshold)/compactionCheckDivisor {
		s.mu.Unlock()
		return
	}
	s.lastChecked[streamID] = version
	s.mu.Unlock()

	didCompact, err := s.compactor.CheckAndCompact(ctx, streamID)
	if err != nil {
		log.Error("compaction: automatic compaction failed",
			"stream_id", streamID,
			"error", err,
		)
		return
	}

	if didCompact && s.trimStore != nil && s.compactor.config.EnableTrimming && s.compactor.repo != nil {
		summaries, err := s.compactor.repo.FindByStreamID(ctx, streamID)
		if err == nil && len(summaries) > 0 {
			latest := summaries[len(summaries)-1]
			if _, trimErr := s.trimStore.TrimBefore(ctx, streamID, latest.EndVersion); trimErr != nil {
				log.Warn("compaction: post-compaction trim failed", "error", trimErr)
			}
		}
	}

	if didCompact {
		log.Info("compaction: automatic compaction completed",
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
	if s.compactor == nil || s.compactor.repo == nil {
		return nil, nil
	}
	return s.compactor.repo.FindByStreamID(ctx, streamID)
}

// GetSummariesForAgent returns all summaries for an agent across all tasks.
func (s *CompactableEventStore) GetSummariesForAgent(ctx context.Context, agentID string) ([]*EventSummary, error) {
	if s.compactor == nil || s.compactor.repo == nil {
		return nil, nil
	}
	return s.compactor.repo.FindByAgentID(ctx, agentID)
}

// WithCustomSummarizer replaces the default rule-based summarizer with a custom one
// (e.g., an LLM-powered summarizer for richer summaries).
func (s *CompactableEventStore) WithCustomSummarizer(summarizer EventSummarizer) *CompactableEventStore {
	s.compactor.summarizer = summarizer
	return s
}
