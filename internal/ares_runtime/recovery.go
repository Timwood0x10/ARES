// Package runtime provides shared recovery utilities for agent state restoration.
package runtime

import (
	"context"
	"log/slog"

	"github.com/Timwood0x10/ares/internal/agents/base"
)

// RecoverSnapshotOrEvents attempts snapshot-first recovery for agent state.
// If the snapshot store is nil or returns nil/errored, falls back to calling
// eventFn to build state from events.
//
// Args:
//
//	ctx - context for snapshot load operation.
//	store - snapshot store implementation (may be nil).
//	agentID - identifier of the agent to recover.
//	eventFn - fallback function that builds state from events when no snapshot is available.
//
// Returns:
//
//	state - recovered state map, or nil if both snapshot and event fallback return nothing.
func RecoverSnapshotOrEvents(ctx context.Context, store base.SnapshotStore, agentID string, eventFn func() map[string]any) map[string]any {
	if store != nil {
		snap, err := store.Load(ctx, agentID)
		if err != nil {
			slog.Warn("runtime: snapshot load failed, falling back to events",
				"agent_id", agentID, "error", err,
			)
			return eventFn()
		}
		if snap != nil {
			slog.Info("runtime: recovered from snapshot",
				"agent_id", agentID, "fields", len(snap),
			)
			return snap
		}
	}
	return eventFn()
}
