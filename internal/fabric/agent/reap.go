package agentfabric

import (
	"context"
	"sort"
	"time"
)

// DefaultRetiredAgentRetention is how long a retired agent's record stays
// queryable before ReapRetired may remove it. An hour is long enough for an
// operator (or an event-driven dashboard) to look up an agent that just left,
// and short enough that a long-lived process does not accumulate a tombstone per
// spawn forever.
const DefaultRetiredAgentRetention = time.Hour

// ReapRetired removes the registry records of agents that have been RETIRED for
// at least retention, and returns their ids in order. It is the agent-side
// counterpart of the task fabric's terminal-task reaper: retirement already
// releases the heavy state (resource claim, execution body, cognitive state), so
// what is left per retired agent is a small record, but "small" times every spawn
// of a process lifetime is still growth with no bound — reclamation alone
// retires, it never removes.
//
// The record's whole life, which is what makes this safe to run unattended:
//
//		(absent) --Spawn--> IDLE --Retire--> RETIRED --ReapRetired--> (absent)
//
//	  - Only RETIRED records are eligible. IDLE (a live worker), SUSPENDED (work
//	    waiting to be resumed), and unknown ids are never touched.
//	  - The retention window is measured from Retire, not from spawn: an agent
//	    spawned an hour ago and retired a second ago is not a candidate.
//	  - Reaping is not a correctness mechanism, exactly like the task reaper: the
//	    record's only readers are observability paths, and Retire already released
//	    everything a running system needs (quota, execution body, revival
//	    snapshot). A process-tree edge to a reaped parent survives as provenance,
//	    the same way it already does for Kill — invariant #1/#7 keep the causal
//	    graph readable after the node is gone.
//	  - A record whose retirement time is unset is never reaped. That is not
//	    defensive coding: retiredAt is the only age basis, and a zero value would
//	    read as "retired at the beginning of time", which would make reaping a
//	    fresh tombstone.
//
// A retention of zero or less disables reaping and returns nil. Counts are small
// and the scan is in-memory, but a cancelled ctx stops it: this runs on the
// kernel loop's tick, and a shutting-down loop should not keep rewriting the
// registry.
func (f *Fabric) ReapRetired(ctx context.Context, retention time.Duration) []string {
	if retention <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return nil
	default:
	}

	f.mu.Lock()
	now := f.now()
	var doomed []string
	for id, a := range f.agents {
		a.mu.RLock()
		state, retiredAt := a.State, a.retiredAt
		a.mu.RUnlock()
		if state != StateRetired || retiredAt.IsZero() {
			continue
		}
		if now.Sub(retiredAt) < retention {
			continue
		}
		doomed = append(doomed, id)
	}
	for _, id := range doomed {
		delete(f.agents, id)
	}
	f.mu.Unlock()

	sort.Strings(doomed)
	return doomed
}
