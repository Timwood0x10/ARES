package events

import (
	"context"
	"sort"
	"sync"
	"time"
)

// MemorySummaryRepository is an in-memory implementation of SummaryRepository.
// It stores event summaries in a thread-safe map, keyed by summary ID.
// Designed for testing and demo mode where PostgreSQL is not available.
type MemorySummaryRepository struct {
	mu        sync.RWMutex
	summaries map[string]*EventSummary
	byStream  map[string][]string // streamID → summary IDs, ordered by start_version asc
}

// NewMemorySummaryRepository creates an empty in-memory summary repository.
func NewMemorySummaryRepository() *MemorySummaryRepository {
	return &MemorySummaryRepository{
		summaries: make(map[string]*EventSummary),
		byStream:  make(map[string][]string),
	}
}

// Save persists an event summary to memory.
func (r *MemorySummaryRepository) Save(_ context.Context, summary *EventSummary) error {
	if summary.ID == "" {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.summaries[summary.ID] = summary

	// Append to stream index, maintaining version order.
	ids := r.byStream[summary.StreamID]
	insertIdx := sort.Search(len(ids), func(i int) bool {
		existing, ok := r.summaries[ids[i]]
		if !ok {
			return true
		}
		return existing.StartVersion > summary.StartVersion
	})
	ids = append(ids, "")
	copy(ids[insertIdx+1:], ids[insertIdx:])
	ids[insertIdx] = summary.ID
	r.byStream[summary.StreamID] = ids

	return nil
}

// FindByStreamID returns all summaries for a stream, ordered by start_version ascending.
func (r *MemorySummaryRepository) FindByStreamID(_ context.Context, streamID string) ([]*EventSummary, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := r.byStream[streamID]
	result := make([]*EventSummary, 0, len(ids))
	for _, id := range ids {
		if s, ok := r.summaries[id]; ok {
			result = append(result, s)
		}
	}
	return result, nil
}

// FindByAgentAndTask returns summaries matching both agent and task.
func (r *MemorySummaryRepository) FindByAgentAndTask(_ context.Context, agentID, taskID string) ([]*EventSummary, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*EventSummary
	for _, s := range r.summaries {
		if s == nil {
			continue
		}
		if s.AgentID == agentID && s.TaskID == taskID {
			result = append(result, s)
		}
	}
	// Sort by start_version ascending.
	sort.Slice(result, func(i, j int) bool {
		return result[i].StartVersion < result[j].StartVersion
	})
	return result, nil
}

// FindByAgentID returns all summaries for an agent across all streams/tasks.
func (r *MemorySummaryRepository) FindByAgentID(_ context.Context, agentID string) ([]*EventSummary, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*EventSummary
	for _, s := range r.summaries {
		if s == nil {
			continue
		}
		if s.AgentID == agentID {
			result = append(result, s)
		}
	}
	// Sort by created_at descending (most recent first).
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	return result, nil
}

// FindLatestByStreamID returns the most recent summary for a stream.
func (r *MemorySummaryRepository) FindLatestByStreamID(_ context.Context, streamID string) (*EventSummary, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := r.byStream[streamID]
	if len(ids) == 0 {
		return nil, nil
	}
	latest := r.summaries[ids[len(ids)-1]]
	return latest, nil
}

// Delete removes a summary by ID.
func (r *MemorySummaryRepository) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	s, exists := r.summaries[id]
	if !exists {
		return nil
	}

	// Remove from byStream index.
	ids := r.byStream[s.StreamID]
	for i, sid := range ids {
		if sid == id {
			r.byStream[s.StreamID] = append(ids[:i], ids[i+1:]...)
			break
		}
	}

	delete(r.summaries, id)
	return nil
}

// DeleteOlderThan removes summaries created before the given threshold.
func (r *MemorySummaryRepository) DeleteOlderThan(_ context.Context, threshold time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	var deleted int64
	for id, s := range r.summaries {
		if s == nil {
			continue
		}
		if s.CreatedAt.Before(threshold) {
			// Remove from byStream index.
			ids := r.byStream[s.StreamID]
			for i, sid := range ids {
				if sid == id {
					r.byStream[s.StreamID] = append(ids[:i], ids[i+1:]...)
					break
				}
			}
			delete(r.summaries, id)
			deleted++
		}
	}
	return deleted, nil
}
