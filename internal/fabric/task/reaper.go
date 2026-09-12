package taskfabric

import (
	"strings"
	"time"
)

// Reaper periodically harvests terminal fabric tasks so the in-memory task
// map does not grow monotonically across a server lifetime
// ("fabric does not auto-reap terminal tasks"). The reaper is a housekeeping loop, NOT a
// correctness mechanism — terminal tasks are garbage once their results have
// been read by the caller.
//
// Harvesting rules:
//   - Only COMPLETED and FAILED tasks are eligible (READY/LEASED/RUNNING/
//     SUSPENDED are live or resumable and must survive).
//   - Each prefix scope ("sess/<id>/", "peer-plan-") limits harvesting to its
//     own ID family and carries its own grace period. Non-session families
//     (collab-*, peer-task-*) stay invisible unless added via
//     WithAdditionalPrefix — the collab GC loop (cmd/ares/collab_graph.go)
//     already handles its own prefix.
//   - A grace period prevents harvesting a task that just completed — the
//     planner/answer path may still be reading its envelope when the state
//     transition lands. The default grace is 30s.
//
// The reaper runs as a managed background loop (like runCollabGCLoop), exited
// by closing its done channel. It is NOT on the scheduler drain path.
//
// Keep-set: wall-clock grace alone is unsafe for a session that
// outlives the grace period — its early predecessors would be harvested while
// the planner still reads their envelopes for context assembly (decision C).
// A wired keep predicate makes the session registry the authority: a task
// whose owning session is still live is NEVER harvested, no matter its age;
// grace then only protects the read window racing a session's release. The
// predicate applies to every scope; for non-session ID families (which no
// session owns) it reports false, so grace alone decides.
type Reaper struct {
	fabric *Fabric
	scopes []reaperScope
	// keep reports whether a task's owning session is still live. Nil =
	// grace-only harvesting (legacy semantics, safe only when sessions are
	// shorter than the grace window).
	keep func(taskID string) bool
}

// reaperScope is one harvested ID family with its own grace window.
type reaperScope struct {
	prefix string
	grace  time.Duration
}

// NewReaper creates a terminal-task reaper scoped to the given session
// prefix. The prefix is the node-ID stem of L2 session tasks
// ("sess/<sessionID>/"); non-matching tasks are skipped. A gracePeriod of 0
// defaults to 30s.
func NewReaper(fabric *Fabric, prefix string, gracePeriod time.Duration) *Reaper {
	return NewReaperWithKeep(fabric, prefix, gracePeriod, nil)
}

// NewReaperWithKeep creates a reaper whose harvesting is additionally gated
// by a keep predicate: Sweep skips every task for which keep returns true
// (the owning session is still live), regardless of the grace period. A nil
// keep degrades to grace-only harvesting (NewReaper semantics).
func NewReaperWithKeep(fabric *Fabric, prefix string, gracePeriod time.Duration, keep func(taskID string) bool) *Reaper {
	if gracePeriod <= 0 {
		gracePeriod = 30 * time.Second
	}
	return &Reaper{
		fabric: fabric,
		scopes: []reaperScope{{prefix: prefix, grace: gracePeriod}},
		keep:   keep,
	}
}

// WithAdditionalPrefix returns a reaper that ALSO harvests terminal tasks
// under the given prefix, with the given (independent) grace window. It
// exists for submission roots (peer-plan-N): they are not session-scoped, so
// a sess-only reaper never reclaims them and every Submit left a permanent
// entry behind in a long-lived process. Their grace is deliberately longer
// than the session default — the wait loop reads the plan task's token usage
// after the answer arrives, which routinely outlives the session grace.
//
// The returned reaper shares the fabric and keep predicate with the receiver;
// the receiver is not modified (a zero-value-scope reaper stays unusable).
func (r *Reaper) WithAdditionalPrefix(prefix string, gracePeriod time.Duration) *Reaper {
	if r == nil || prefix == "" {
		return r
	}
	if gracePeriod <= 0 {
		gracePeriod = 30 * time.Second
	}
	return &Reaper{
		fabric: r.fabric,
		scopes: append(append([]reaperScope(nil), r.scopes...), reaperScope{prefix: prefix, grace: gracePeriod}),
		keep:   r.keep,
	}
}

// GracePeriod reports the effective read-window grace of the primary
// (first) scope after construction defaults are applied. Exposed for startup
// logging so operators can confirm what the reaper actually runs with.
// Additional prefixes keep their own grace — see WithAdditionalPrefix.
func (r *Reaper) GracePeriod() time.Duration {
	if r == nil || len(r.scopes) == 0 {
		return 0
	}
	return r.scopes[0].grace
}

// scopeFor returns the harvesting scope whose prefix matches the task ID, or
// nil when the task belongs to no harvested family.
func (r *Reaper) scopeFor(taskID string) *reaperScope {
	for i := range r.scopes {
		if r.scopes[i].prefix == "" || strings.HasPrefix(taskID, r.scopes[i].prefix) {
			return &r.scopes[i]
		}
	}
	return nil
}

// Sweep performs one harvesting pass: every terminal task whose ID starts
// with the reaper's prefix, whose owning session is NOT kept live by the
// keep predicate (when wired), and whose UpdatedAt is older than the grace
// period is deleted. Returns the number of tasks harvested.
//
// In-flight tasks (LEASED/RUNNING/SUSPENDED) are refused by Delete's guard
// and skipped — they finish naturally and become harvestable on the next
// sweep.
//
// Referenced tasks are skipped: depsCompletedLocked treats a missing
// dependency as unsatisfied forever, so harvesting a terminal predecessor
// while another task still lists it in Dependencies would strand that
// dependent permanently. The skip converges: once the referencing tasks
// themselves reach a terminal state and are harvested, a later sweep reclaims
// the predecessor (housekeeping may take a few passes, correctness never
// yields).
func (r *Reaper) Sweep() int {
	if r == nil || r.fabric == nil {
		return 0
	}
	removed := 0
	now := time.Now()
	// One pass over the dependency graph under a single fabric lock beats a
	// per-candidate Dependents scan (O(n) each, O(n²) per sweep).
	referenced := r.fabric.ReferencedDependencies()
	for _, id := range r.fabric.IDs() {
		scope := r.scopeFor(id)
		if scope == nil {
			continue
		}
		// Keep-set: a live session's tasks are its readable history
		// (decision C — the envelope is the only place output lives).
		// Age is irrelevant while the session is alive.
		if r.keep != nil && r.keep(id) {
			continue
		}
		tk, err := r.fabric.Task(id)
		if err != nil {
			continue
		}
		if tk.State != StateCompleted && tk.State != StateFailed {
			continue
		}
		// Grace period: a task that just transitioned to terminal may
		// still be read by the session's answer path. Per-scope: a
		// submission root must outlive the wait that reads its token usage.
		if now.Sub(tk.UpdatedAt) < scope.grace {
			continue
		}
		// Dangling-dependency guard: another task still waits on (or reads)
		// this one — deleting it now would strand the dependent forever.
		if _, ref := referenced[id]; ref {
			continue
		}
		if derr := r.fabric.Delete(id); derr == nil {
			removed++
		}
	}
	return removed
}

// Run starts the periodic harvesting loop. It exits when done is closed. The
// interval controls the sweep cadence; a shorter interval reclaims memory
// faster but scans the task map more often. The default (when interval <= 0)
// is 60s — matching the collab GC cadence.
//
// Each sweep that harvested anything is logged: deleting production tasks
// without a trace is exactly the silent action this codebase forbids.
func (r *Reaper) Run(done <-chan struct{}, interval time.Duration) {
	if interval <= 0 {
		interval = 60 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if n := r.Sweep(); n > 0 {
				prefixes := make([]string, 0, len(r.scopes))
				for _, sc := range r.scopes {
					prefixes = append(prefixes, sc.prefix)
				}
				log.Info("taskfabric: reaper harvested terminal tasks",
					"count", n, "prefixes", prefixes)
			}
		}
	}
}
