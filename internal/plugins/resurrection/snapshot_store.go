package resurrection

import (
	"context"
	"fmt"
	"sync"

	"github.com/Timwood0x10/ares/internal/agents/base"
	apperrors "github.com/Timwood0x10/ares/internal/errors"
)

// ErrSnapshotNotFound is returned when no snapshot exists for the given agent.
// Wraps apperrors.ErrNotFound for generic checks via errors.Is(err, apperrors.ErrNotFound).
var ErrSnapshotNotFound = fmt.Errorf("snapshot not found: %w", apperrors.ErrNotFound)

// Ensure MemorySnapshotStore implements SnapshotStore.
var _ base.SnapshotStore = (*MemorySnapshotStore)(nil)

// MemorySnapshotStore is an in-memory implementation of base.SnapshotStore.
// Snapshot data is protected by a read-write mutex and deep-copied on
// every Save/Load to prevent data races with the caller.
type MemorySnapshotStore struct {
	mu        sync.RWMutex
	snapshots map[string]map[string]any
}

// NewMemorySnapshotStore creates a new MemorySnapshotStore.
func NewMemorySnapshotStore() *MemorySnapshotStore {
	return &MemorySnapshotStore{
		snapshots: make(map[string]map[string]any),
	}
}

// Save persists a snapshot for the given agent. Deep-copies the map to
// prevent the caller from mutating the stored data after the call returns.
func (s *MemorySnapshotStore) Save(_ context.Context, agentID string, snapshot map[string]any) error {
	if agentID == "" || snapshot == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cpy := make(map[string]any, len(snapshot))
	for k, v := range snapshot {
		cpy[k] = v
	}
	s.snapshots[agentID] = cpy
	return nil
}

// Load retrieves the latest snapshot for the given agent.
// Returns nil, nil if no snapshot exists for the agent.
// The returned map is a deep copy to prevent data races.
func (s *MemorySnapshotStore) Load(_ context.Context, agentID string) (map[string]any, error) {
	if agentID == "" {
		return nil, ErrSnapshotNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap, ok := s.snapshots[agentID]
	if !ok || snap == nil {
		return nil, ErrSnapshotNotFound
	}
	cpy := make(map[string]any, len(snap))
	for k, v := range snap {
		cpy[k] = v
	}
	return cpy, nil
}

// Delete removes the snapshot for the given agent. No-op if the agent
// has no snapshot.
func (s *MemorySnapshotStore) Delete(_ context.Context, agentID string) error {
	if agentID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.snapshots, agentID)
	return nil
}
