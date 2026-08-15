package ares_skills

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ExperienceStore persists relevance priors (design §11: reuse the
// refine.Store style — Get/Set by key). A nil store keeps Experience purely
// in memory.
type ExperienceStore interface {
	// Load returns all persisted records (empty slice when none).
	Load(ctx context.Context) ([]ExperienceRecord, error)
	// Save atomically persists the full record set.
	Save(ctx context.Context, records []ExperienceRecord) error
}

// Experience is the Learned Source: it records task-pattern -> skill relevance
// priors (design §11). It never invokes skills — it only biases future
// discovery ranking. A learned skill is indexable but never auto-executed.
type Experience struct {
	mu      sync.RWMutex
	records []ExperienceRecord
	// maxRecords caps the in-memory record set.
	maxRecords int
	// store, when non-nil, persists records across restarts.
	store ExperienceStore
}

// NewExperience creates an in-memory Experience store.
//
// Returns:
//   - *Experience: ready to record and query relevance priors.
func NewExperience() *Experience {
	return &Experience{maxRecords: 1000}
}

// NewExperienceWithStore creates an Experience backed by a persistent store
// and pre-loads any previously saved records. Load errors are non-fatal: the
// store starts empty and the next Record retries persistence.
//
// Args:
//   - ctx: context for the initial load.
//   - store: the persistent store (nil means in-memory only).
//
// Returns:
//   - *Experience: ready to use, populated from the store when possible.
func NewExperienceWithStore(ctx context.Context, store ExperienceStore) *Experience {
	e := NewExperience()
	e.store = store
	if store != nil {
		if loaded, err := store.Load(ctx); err == nil {
			e.records = loaded
		}
	}
	return e
}

// Record stores or updates a {skill, task_pattern, success_rate} prior.
// Re-recording the same (skill, pattern) pair replaces its success rate.
//
// Args:
//   - skill: the skill ID.
//   - taskPattern: the task pattern.
//   - successRate: observed success rate, clamped to [0,1].
//
// Returns:
//   - error: wrapped error when arguments are empty.
func (e *Experience) Record(skill, taskPattern string, successRate float64) error {
	if skill == "" || taskPattern == "" {
		return fmt.Errorf("ares_skills: experience record needs skill and task pattern")
	}
	if successRate < 0 {
		successRate = 0
	}
	if successRate > 1 {
		successRate = 1
	}
	rec := ExperienceRecord{
		Skill:       skill,
		TaskPattern: taskPattern,
		SuccessRate: successRate,
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	for i, r := range e.records {
		if r.Skill == skill && r.TaskPattern == taskPattern {
			e.records[i] = rec
			return e.persistLocked()
		}
	}
	if len(e.records) >= e.maxRecords {
		// Drop the oldest record to bound memory.
		e.records = append(e.records[1:], rec)
		return e.persistLocked()
	}
	e.records = append(e.records, rec)
	return e.persistLocked()
}

// persistLocked writes the current record set to the store when one is
// attached. Caller must hold the write lock.
//
// Returns:
//   - error: wrapped store error, or nil (no store = no-op).
func (e *Experience) persistLocked() error {
	if e.store == nil {
		return nil
	}
	records := make([]ExperienceRecord, len(e.records))
	copy(records, e.records)
	if err := e.store.Save(context.Background(), records); err != nil {
		return fmt.Errorf("ares_skills: persist experience: %w", err)
	}
	return nil
}

// BestMatch returns the highest-success-rate skill for a task pattern, or
// ok=false when nothing matches.
//
// Args:
//   - taskPattern: the task pattern to match (substring match on the stored pattern).
//
// Returns:
//   - ExperienceRecord: the best prior.
//   - bool: true when a match exists.
func (e *Experience) BestMatch(taskPattern string) (ExperienceRecord, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	needle := strings.ToLower(taskPattern)
	best := ExperienceRecord{}
	found := false
	for _, r := range e.records {
		if strings.Contains(strings.ToLower(r.TaskPattern), needle) ||
			strings.Contains(needle, strings.ToLower(r.TaskPattern)) {
			if !found || r.SuccessRate > best.SuccessRate {
				best = r
				found = true
			}
		}
	}
	return best, found
}

// List returns a deterministic snapshot of all records, sorted by skill then
// task pattern.
//
// Returns:
//   - []ExperienceRecord: a copy of the record set.
func (e *Experience) List() []ExperienceRecord {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]ExperienceRecord, len(e.records))
	copy(out, e.records)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Skill != out[j].Skill {
			return out[i].Skill < out[j].Skill
		}
		return out[i].TaskPattern < out[j].TaskPattern
	})
	return out
}

// Count returns the number of recorded priors.
//
// Returns:
//   - int: record count.
func (e *Experience) Count() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.records)
}
