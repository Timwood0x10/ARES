package kernel

import (
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// WithRecoveryHint wires the stale-winner recovery trigger. fn is called
// when a leased task's winner has died and no capable replacement executor
// exists, so the task would otherwise stall for the full lease TTL. fn MUST
// NOT block: it runs on a drain goroutine.
//
// Args:
//   - fn: the non-blocking sweep trigger; nil disables the hint.
//
// Returns:
//   - *Scheduler: the receiver, for chaining.
func (s *Scheduler) WithRecoveryHint(fn func(taskID string)) *Scheduler {
	s.execMu.Lock()
	defer s.execMu.Unlock()
	s.recoveryHint = fn
	return s
}

// notifyRecovery fires the recovery hint under a read lock (the hint may be
// re-wired at runtime). Safe when no hint is wired.
func (s *Scheduler) notifyRecovery(taskID string) {
	s.execMu.RLock()
	hint := s.recoveryHint
	s.execMu.RUnlock()
	if hint != nil {
		hint(taskID)
	}
}

// hasRecoveryHint reports whether a recovery loop is wired to receive
// stale-winner nominations. The stale-winner path needs this BEFORE releasing:
// releasing clears the lease and thereby removes the task from
// CheckExpiredLeases' scope, so a release with no recovery consumer would
// strand the task instead of merely delaying it.
func (s *Scheduler) hasRecoveryHint() bool {
	s.execMu.RLock()
	defer s.execMu.RUnlock()
	return s.recoveryHint != nil
}

// unbindRecoveryExecutorAfterTerminal unregisters the recovery executor bound
// to taskID once the task reaches a terminal state, so the executor map does
// not grow unboundedly and the replacement is not offered as a candidate for
// other tasks. No-op while the task can still run again (READY/RUNNING/
// SUSPENDED).
func (s *Scheduler) unbindRecoveryExecutorAfterTerminal(taskID string) {
	tk2, tkErr := s.fabric.Task(taskID)
	if tkErr != nil {
		return
	}
	if tk2.State != taskfabric.StateCompleted && tk2.State != taskfabric.StateFailed {
		return
	}
	if boundID := s.unbindFor(taskID); boundID != "" {
		s.UnregisterExecutor(boundID)
		s.tracker.Forget(boundID)
		log.Info("kernel scheduler: unregistered recovery executor after task reached state", "executor", boundID, "task_id", taskID, "state", tk2.State)
	}
}

// reconcileFabricDeaths drops static executor registrations whose agent has
// disappeared from the wired Agent Fabric (kill/retire). Recovery-bound
// executors are skipped — they intentionally live outside the fabric and are
// cleaned up by the terminal-state unbind. No-op when no fabric is wired.
//
// It also forgets the LoadTracker entries of agents that no longer exist in
// ANY candidate source: the fabric population rotates continuously
// (spawn/kill), and without the sweep the per-agent stat maps grew without
// bound — one leaked entry (history, load, overrides) per agent generation.
// Entries are only forgotten for agents that (a) accumulated execution
// history or confidence overrides (a bare priority injection from the static
// peer config survives — that set is fixed, not rotating), and (b) are
// currently idle (load == 0), so an in-flight quantum's End still finds its
// slot.
func (s *Scheduler) reconcileFabricDeaths() {
	if s.agents == nil {
		return
	}
	if s.hybridStatic {
		// Hybrid mode (SDK): static registrations are real executors that
		// deliberately live outside the fabric (RegisterAgent, graph agents)
		// — there is no fabric copy to mirror, so a fabric-death sweep would
		// delete live executors and strand their tasks on
		// no-capable-candidate. The embedder unregisters explicitly.
		s.sweepDeadTrackerEntries()
		return
	}
	for id := range s.allExecutors() {
		if s.isBoundToAnyTask(id) {
			continue // recovery replacements are unregistered at terminal state
		}
		if _, err := s.agents.Get(id); err != nil {
			log.Info("kernel scheduler: unregistering executor — agent no longer in fabric (killed or retired)", "executor", id)
			s.UnregisterExecutor(id)
			// Forget only when the tracker shows no in-flight quantum
			// (Forget's contract: load > 0 must not be forgotten). A
			// killed-mid-quantum agent whose ID is then reused would
			// otherwise have its load slot cleared while the stale quantum
			// still runs — the late End decrements a fresh entry and opens
			// admission for a third concurrent quantum. sweepDeadTrackerEntries
			// collects the entry once the straggler's End lands.
			if s.tracker.Load(id) == 0 {
				s.tracker.Forget(id)
			}
		}
	}
	s.sweepDeadTrackerEntries()
}

// sweepDeadTrackerEntries removes tracker stats for agents that have neither
// a registration in the executor registry nor a live entry in the agent
// fabric, but did accumulate history (a straggler quantum's End recreates an
// entry after its agent died; the next sweep collects it).
func (s *Scheduler) sweepDeadTrackerEntries() {
	for _, a := range s.tracker.Snapshot().Agents {
		if a.Load != 0 {
			continue // in-flight quantum
		}
		hasHistory := a.Done > 0 || a.Ok > 0 || a.HasConfidenceOverride || len(a.CapabilityOverrides) > 0
		if !hasHistory {
			continue // static config entries (priority-only) are not rotating
		}
		if _, ok := s.LookupExecutor(a.AgentID); ok {
			continue
		}
		if _, err := s.agents.Get(a.AgentID); err == nil {
			continue // still live in the fabric
		}
		s.tracker.Forget(a.AgentID)
	}
}
