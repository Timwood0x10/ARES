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
//	  - Only RETIRED records are eligible. IDLE (a live worker), SUSPENDED (a
//	    live worker that is paused — the reclamation sweep may retire it, but this
//	    never removes anything that is not already retired), and unknown ids are
//	    never touched.
//	  - The retention window is measured from Retire, not from spawn: an agent
//	    spawned an hour ago and retired a second ago is not a candidate.
//	  - Reaping is not a correctness mechanism, exactly like the task reaper: the
//	    record's only readers are observability paths, and Retire already released
//	    everything a running system needs (quota, execution body, revival
//	    snapshot).
//	  - The process-tree edges go WITH the record. An edge to a reaped id is a
//	    reference to nothing (Children is a public accessor — dashboard views and
//	    tests walk it), and the map itself is per-parent state that only ever grew:
//	    every spawn with a ParentID appended a child list entry, and neither Kill
//	    nor Retire ever removed one, so a long-lived process accumulated a
//	    provenance graph of agents that no longer existed. Both halves are dropped
//	    here — the record's own outgoing list and its id inside its parent's list —
//	    which is what bounds the structure to the agents the registry still holds,
//	    the same bound this reaper puts on the registry itself. The provenance of a
//	    REAPED node is intentionally gone rather than dangling: the record it
//	    described is gone too, at which point the edge is not history, it is a
//	    lookup that cannot succeed. Provenance for KILLED agents (which are not
//	    reaped) is unaffected.
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

	// The registry records and their process-tree edges are dropped together, in
	// the same critical section: a reader must never observe an edge pointing at a
	// record the same call has already removed.
	doomedSet := make(map[string]struct{}, len(doomed))
	for _, id := range doomed {
		doomedSet[id] = struct{}{}
		delete(f.agents, id)
		// The record's own outgoing edges: the parent they hang off is gone. Its
		// children are NOT removed (invariant #1: parent death ≠ child death) —
		// they keep their own records, and only a child that is itself doomed gets
		// stripped from a list by the loop below.
		delete(f.children, id)
	}
	// Strip every doomed id from the lists that still name it.
	for parent, kids := range f.children {
		kept := kids[:0]
		for _, kid := range kids {
			if _, gone := doomedSet[kid]; gone {
				continue
			}
			kept = append(kept, kid)
		}
		kept = kept[:len(kept):len(kept)]
		if len(kept) == 0 {
			// An empty child list is deleted rather than left as an empty slice:
			// the entry is what grows with the process, and the readers treat
			// "absent" and "no children" identically.
			delete(f.children, parent)
			continue
		}
		f.children[parent] = kept
	}
	f.mu.Unlock()

	sort.Strings(doomed)
	return doomed
}
