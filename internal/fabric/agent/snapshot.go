package agentfabric

import (
	"sync"
	"time"
)

// AgentSnapshot is the revival record captured when an agent dies with live
// cognition. It carries everything the recovery
// subsystem needs to revive an EQUIVALENT execution body in place:
//
//   - Cognitive: the agent's last CognitiveState, already carrying its
//     SchemaVersion (the versioned-envelope requirement
//     — a bare map is forbidden here).
//   - Capabilities: the declared capability set, so a revived body matches
//     the scheduler's candidate scoring exactly as the dead one did.
//   - Parent: provenance continuity — the revived agent keeps pointing at
//     its origin (Rule 2: spawn establishes provenance, never hierarchy).
type AgentSnapshot struct {
	Cognitive    CognitiveState
	Capabilities []string
	Parent       string
	// DiedAt is the fabric-clock instant the death was captured. When several
	// dead agents share a capability, recovery picks the MOST RECENT death —
	// the freshest cognition is the safest revival seed (map-iteration
	// order must not choose arbitrarily).
	DiedAt time.Time
	// StopReason is WHY the agent left (see StopReason): Kill records
	// StopCrashed. Recovery carries it into the restart decision and the
	// revival log, so "died because it was killed" is distinguishable from
	// "died and the restart budget refused to revive it" — two very different
	// operator stories that used to render identically.
	StopReason StopReason
}

// snapshotStore keeps the LAST snapshot per agent identity. Entries are
// cleared on Retire (terminal: no revival possible) and are meant to be
// consumed via ClearSnapshot after a successful in-place revival, keeping
// long-running processes bounded.
//
// Bounded growth: agents rotate continuously (spawn/kill) in peer mode, and
// a killed agent can never be Retired (Kill already removed it from the
// registry), so "cleared on Retire" never fires for the rotation path. The
// store therefore caps itself: when a save would exceed maxDeathSnapshots,
// the OLDEST entries (by DiedAt) are evicted — recovery wants the freshest
// cognition anyway.
type snapshotStore struct {
	mu   sync.RWMutex
	byID map[string]AgentSnapshot
}

// maxDeathSnapshots bounds how many dead-agent revival records the store
// keeps. Sized generously against the live population: recovery only ever
// revives a handful of agents, and findByCapability prefers the most recent
// death, so evicting the oldest tail loses nothing the recovery loop would
// have used.
const maxDeathSnapshots = 1024

// defaultDeathSnapshotRetention bounds how long a death snapshot is kept. It
// complements the count cap, which bounds entries but not bytes: a snapshot
// carries the dead agent's cognitive state, and a full store of them is real
// memory. An hour is far longer than the revival window (the recovery loop
// reacts to a death within seconds) and far shorter than a process lifetime.
const defaultDeathSnapshotRetention = time.Hour

func newSnapshotStore() *snapshotStore {
	return &snapshotStore{byID: make(map[string]AgentSnapshot)}
}

// save stores (or overwrites) the snapshot for agentID, evicting the oldest
// entries when the store would grow past the bound.
func (s *snapshotStore) save(id string, snap AgentSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Overwriting an existing entry never grows the store.
	if _, exists := s.byID[id]; !exists && len(s.byID) >= maxDeathSnapshots {
		// Evict the oldest DiedAt entries until there is room.
		for len(s.byID) >= maxDeathSnapshots {
			var oldestID string
			var oldest time.Time
			first := true
			for k, v := range s.byID {
				if first || v.DiedAt.Before(oldest) {
					oldestID, oldest, first = k, v.DiedAt, false
				}
			}
			if oldestID == "" {
				break
			}
			delete(s.byID, oldestID)
		}
	}
	s.byID[id] = snap
	// Age is bounded as well as count. The cap above limits ENTRIES, and each
	// entry carries a dead agent's cognitive state, so 1024 of them is not a
	// memory bound — it is only a growth bound. The reference clock is the
	// freshest death in the store (the store has no clock of its own, and this is
	// the only "now" it can observe); when nothing new dies, nothing accumulates
	// either, so the pruning is not time-sensitive.
	//
	// Dropping old snapshots is safe by construction: revival consumes one within
	// seconds of the death it reacts to, and findByCapability prefers the freshest
	// death anyway, so an entry past the window could never have been revived.
	cutoff := snap.DiedAt.Add(-defaultDeathSnapshotRetention)
	for k, v := range s.byID {
		if v.DiedAt.Before(cutoff) {
			delete(s.byID, k)
		}
	}
}

// load returns the stored snapshot for agentID.
func (s *snapshotStore) load(id string) (AgentSnapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap, ok := s.byID[id]
	return snap, ok
}

// clear drops the snapshot for agentID (consumed by revival, or terminal
// Retire).
func (s *snapshotStore) clear(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byID, id)
}

// findByCapability returns the most recent stored snapshot whose declared
// capabilities contain capability. Scan cost is bounded by the store cap
// (maxDeathSnapshots; entries are also cleared on consume/Retire), so a
// linear scan is fine.
func (s *snapshotStore) findByCapability(capability string) (string, AgentSnapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	bestID := ""
	var best AgentSnapshot
	for id, snap := range s.byID {
		matches := false
		for _, c := range snap.Capabilities {
			if c == capability {
				matches = true
				break
			}
		}
		if !matches {
			continue
		}
		if bestID == "" || snap.DiedAt.After(best.DiedAt) {
			bestID, best = id, snap
		}
	}
	if bestID == "" {
		return "", AgentSnapshot{}, false
	}
	return bestID, best, true
}

// captureFromAgent copies the revivable facts off an agent about to be
// removed by Kill. Callers must hold Fabric.mu; the cognitive field itself is
// additionally guarded by the agent's own lock, so we take it briefly to get
// a consistent copy (shared-state ownership documented at
// the field).
func captureFromAgent(a *Agent, diedAt time.Time, reason StopReason) AgentSnapshot {
	a.mu.RLock()
	defer a.mu.RUnlock()
	cog := a.cognitive
	if cog.SchemaVersion == 0 {
		cog.SchemaVersion = CognitiveStateSchemaVersion
	}
	caps := append([]string(nil), a.Capabilities...)
	return AgentSnapshot{
		Cognitive:    cog,
		Capabilities: caps,
		Parent:       a.Parent,
		DiedAt:       diedAt,
		StopReason:   reason,
	}
}

// LastSnapshot returns the snapshot captured when agentID died, if any.
// The recovery subsystem calls this before deciding between in-place revival
// and spawning a replacement (the arbitration rule).
func (f *Fabric) LastSnapshot(agentID string) (AgentSnapshot, bool) {
	return f.snapshots.load(agentID)
}

// ClearSnapshot drops the death snapshot for agentID. The recovery subsystem
// calls it after a successful in-place revival so a later death cannot reuse
// stale cognition; Retire also clears it (terminal state, no revival).
func (f *Fabric) ClearSnapshot(agentID string) {
	f.snapshots.clear(agentID)
}

// FindRevivableSnapshot returns a dead agent (id + snapshot) whose declared
// capabilities cover capability — the arbitration input: if such a victim
// exists and the restart budget allows, the recovery subsystem revives it in
// place instead of spawning a generic replacement.
func (f *Fabric) FindRevivableSnapshot(capability string) (string, AgentSnapshot, bool) {
	return f.snapshots.findByCapability(capability)
}
