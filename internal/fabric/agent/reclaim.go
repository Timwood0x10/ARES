package agentfabric

import (
	"context"
	"sort"
	"time"
)

// AgentReclaimPolicy bounds how long an idle agent may hold its resource claim
// and its place in the pool.
//
// Why a sweep exists at all (defect A-factory3 / P1-6): Spawn allocates from the
// quota and ONLY Kill and Retire release it, and production calls neither — every
// spawned agent therefore held its claim for the life of the process. With a
// budget configured (WithResourceBudget) the pool reaches its ceiling and every
// later Spawn — the spawn_agent tool, a recovery replacement, an evolution
// spawn — fails with ErrResourceQuotaExceeded and keeps failing until a restart.
// The same path leaks the agents themselves: each one carries a Cognition (LLM
// client, tool bundle), so the pool grows whether or not a budget is set.
type AgentReclaimPolicy struct {
	// IdleTTL is how long an agent may go without STARTING a quantum before it
	// becomes reclaimable. Zero or negative DISABLES reclamation of IDLE agents:
	// that is the explicit off switch for an operator who wants every agent kept.
	IdleTTL time.Duration
	// SuspendedTTL is the same window for a SUSPENDED (paused) agent, and it is a
	// separate knob because suspension is a DIFFERENT fact: an idle agent is one
	// nothing asked to do anything, while a suspended one had work and was
	// deliberately paused mid-flight (its task keeps its lease and its checkpoint).
	// Reclaiming the second therefore waits longer by default — long enough that a
	// pause an operator is still looking at is not swept out from under them.
	//
	// Zero or negative DISABLES reclaiming suspended agents, which is the
	// pre-fix behaviour and the safe direction to fail: a kept agent costs its
	// resources, a prematurely retired one costs the paused work.
	//
	// Without this arm a suspended agent is a PERMANENT leak: nothing schedules it
	// (the candidate pool requires IDLE), nothing resumes it (Resume has no
	// production caller), and the reaper only ever removes RETIRED records — so it
	// held its resource claim and its Cognition for the life of the process.
	// Retiring it is safe by the same mechanism Retire already relies on: a
	// suspended task KEEPS its lease (taskfabric.Yield only transitions the state),
	// so the lease-expiry path requeues it to READY for another agent to resume
	// (fabric_lifecycle.go's CheckExpiredLeases states this explicitly), and the
	// drain picks it up through ResumableTasks.
	SuspendedTTL time.Duration
	// KeepPerCapability is the floor reclamation must leave standing for each
	// capability, evaluated after the retirements. It exists because retiring the
	// last agent able to serve a capability trades a quota leak for a different
	// silent stall: tasks needing it would find no candidate
	// (ErrNoCapableCandidate) and sit READY forever. Zero is treated as 1 — a
	// floor of "nobody left" is never intended.
	KeepPerCapability int
}

// DefaultAgentReclaimPolicy is the production default: ten minutes of
// inactivity, keeping one holder of every capability alive.
//
// On by default, deliberately: the defect is unbounded growth, so a sweep that
// runs only when someone remembers to enable it stays a leak in every deployment
// that does not. Ten minutes is orders of magnitude longer than a quantum (the
// scheduler's units are seconds) and far shorter than a process lifetime — the
// window where the pool grows but no work is in flight.
//
// Cost of a sweep: O(pool) to scan, plus one retired event per reclaimed agent.
// Steady state reclaims only what aged past the window, so the batches are small;
// the FIRST sweep after enabling may reclaim everything already idle, which is
// why it runs inside the kernel loop's own tick rather than on demand.
func DefaultAgentReclaimPolicy() AgentReclaimPolicy {
	return AgentReclaimPolicy{
		IdleTTL: 10 * time.Minute,
		// Three times the idle window: a pause is deliberate, and the agent's
		// suspended task outlives it only until its lease expires.
		SuspendedTTL:      30 * time.Minute,
		KeepPerCapability: 1,
	}
}

// IdleAgent is the read-only record of one reclaimed agent: enough for a log
// line or a test to see what happened, without handing out a live *Agent.
//
// The name is the sweep's historical one (ReclaimIdle) and is kept for the
// callers that already consume it; it must NOT be read as "the agent was IDLE" —
// State says which of the two covered states the sweep found.
type IdleAgent struct {
	// Identity is the reclaimed agent's id.
	Identity string
	// State is the state the agent was in when the sweep decided (IDLE or
	// SUSPENDED). Reported because the two mean different things to an operator
	// reading the log: one was unused, the other had paused work.
	State AgentState
	// IdleFor is how long it had been inactive when the sweep decided.
	IdleFor time.Duration
	// Capabilities are the capabilities whose floor was evaluated against this
	// agent — what it was contributing to the pool.
	Capabilities []string
	// Resources is the claim the retirement released back to the quota.
	Resources map[string]float64
}

// ReclaimIdle retires every agent that is IDLE or SUSPENDED, not busy, named in
// no live lease, and inactive longer than the policy window that applies to its
// state — as far as the per-capability floor allows. It returns the retired
// agents in id order. (reclaimTTLFor holds the state table and the reason each
// state is covered.)
//
// inUse names the agent ids the caller KNOWS to be busy with real work: in
// production the owners of live task leases (taskfabric.LeaseSnapshot). They are
// never reclaimed, and they count towards the capability floor. This is a
// parameter rather than something the agent fabric derives because task
// ownership lives in the OTHER fabric — the agent fabric cannot see it, and
// guessing from the busy flag alone would miss an agent whose task was dispatched
// but whose quantum has not started yet.
//
// Retirement goes through retireLocked, deliberately: that is the single release
// path Retire uses, it is idempotent, and it emits the same agent.retired event
// an operator-initiated retire does — reclamation adds a trigger, not a second
// bookkeeping scheme.
//
// The scan, the floor decision and the release all happen in ONE f.mu critical
// section, with the activity test re-evaluated there under the agent lock.
// Deciding outside the lock and retiring later would let a task be dispatched in
// between, retiring an agent whose quantum is already running and handing its
// quota to another spawn while that work still holds the resources.
//
// Residual window, stated rather than hidden: a dispatcher that selected this
// agent BEFORE the sweep took f.mu can still mark it busy afterwards, so the
// agent would be retired with a quantum about to start and its freed quota could
// be handed to another spawn. Closing that fully needs the dispatcher to
// re-validate the agent's state when it marks it busy — a scheduler change, not a
// fabric one. The lease filter above is what makes the window practically empty:
// a dispatched quantum holds its task lease before it executes, so an agent with
// work in flight is in inUse.
//
// The policy is a parameter rather than fabric state so one caller can sweep
// harder or softer without reconfiguring the fabric. ReclaimIdle is safe to call
// concurrently with itself and with every other fabric operation.
func (f *Fabric) ReclaimIdle(ctx context.Context, policy AgentReclaimPolicy, inUse map[string]struct{}) []IdleAgent {
	if policy.IdleTTL <= 0 && policy.SuspendedTTL <= 0 {
		return nil // both arms off: the pre-fix "keep everything" behaviour
	}
	keep := policy.KeepPerCapability
	if keep < 1 {
		keep = 1
	}

	var reclaimed []IdleAgent
	var retired []*Agent
	// orphansOf[i] belongs to retired[i]: the live agents that one left behind
	// (P2-7). Parallel rather than a struct because the only consumer is the
	// post-unlock recording loop below, which walks both together.
	var orphansOf [][]string

	f.mu.Lock()
	// One clock read for the whole sweep: the scan and the in-lock re-check must
	// judge the same instant, or a candidate could pass the first test and fail
	// the second purely because time moved.
	now := f.now()
	candidates := f.idleCandidatesLocked(now, policy)
	// The floor counts the agents that are NOT candidates, then grows as the
	// sweep decides to KEEP one. Evaluating it against the survivors instead of
	// the current pool is what stops a batch of same-capability candidates from
	// collectively retiring a capability's last holders.
	holders := make(map[string]int)
	for id, a := range f.agents {
		_, isCandidate := candidates[id]
		_, claimed := inUse[id]
		// The floor baseline is every live agent the sweep will NOT retire: the
		// non-candidates, plus the in-use candidates (they are certainly alive
		// and staying). Counting in-use agents here rather than when the loop
		// reaches them matters: they must protect the capability floor from the
		// very first decision, otherwise an idle sibling processed first would
		// see an empty floor and be kept too — leaving both alive.
		if isCandidate && !claimed {
			continue
		}
		for _, cap := range a.Capabilities {
			holders[cap]++
		}
	}

	for _, id := range sortedCandidateIDs(candidates) {
		c := candidates[id]
		if _, claimed := inUse[id]; claimed {
			// Someone is using it: it is already part of the floor baseline
			// above, and it is not a reclamation target.
			continue
		}
		breaches := false
		for _, cap := range c.Capabilities {
			if holders[cap] < keep {
				breaches = true
				break
			}
		}
		if breaches {
			// Keep it: it is now part of the floor for the candidates after it.
			for _, cap := range c.Capabilities {
				holders[cap]++
			}
			continue
		}
		// Re-check activity HERE, in the same critical section as the release:
		// the busy flag can flip between the scan above and this point, and the
		// lock order (f.mu then a.mu) is the one IsIdle already establishes.
		a := f.agents[id]
		if a == nil {
			continue
		}
		a.mu.RLock()
		state, busy, lastActive := a.State, a.busy, a.lastActiveAt
		a.mu.RUnlock()
		ttl, covered := reclaimTTLFor(state, policy)
		if !covered || busy || now.Sub(lastActive) <= ttl {
			continue
		}
		// SINGLE-NODE, unlike Retire's cascade, and deliberately so.
		//
		// Retire is an explicit "this agent is done" and, since 0.3.4, takes its subtree
		// with it. Reclamation is "this agent has been idle" — a statement about ONE agent.
		// A parent can be idle while a child it spawned is working, so cascading here would
		// kill live work because its ancestor was quiet. What reclamation leaves behind is
		// reported by AuditOrphans, which is the right instrument for "still held, nothing
		// walking it".
		ta, orphans, err := f.retireLocked(id)
		if err != nil {
			// Lost a race with another trigger (already retired, or killed since
			// the scan). Not an error for the sweep: the claim is back either way.
			continue
		}
		reclaimed = append(reclaimed, c)
		retired = append(retired, ta)
		orphansOf = append(orphansOf, orphans)
	}
	f.mu.Unlock()

	// Events are emitted after the unlock: recording does I/O.
	for i, a := range retired {
		f.recordAgentRetired(ctx, a, orphansOf[i], "")
	}
	return reclaimed
}

// reclaimTTLFor reports the inactivity window that applies to an agent in state,
// and whether the sweep covers that state at all. It is the ONE place the covered
// states are named, so the candidate scan and the in-lock re-check cannot drift
// apart on which states the sweep may retire.
//
// The state table it encodes (the sweep's whole trigger surface):
//
//	state            covered?  window            why
//	IDLE             yes       policy.IdleTTL    not working, nothing asked for it
//	SUSPENDED        yes       policy.Suspended  paused mid-flight: retire it, its
//	                          TTL               task keeps its lease and is requeued
//	                                            by the lease-expiry path
//	RETIRED          no        —                 terminal; ReapRetired removes the
//	                                            record, the sweep must not touch it
//	(anything else)  no        —                 unknown states are never guessed
//
// An uncovered state and a non-positive window are the same answer: leave it
// alone.
func reclaimTTLFor(state AgentState, policy AgentReclaimPolicy) (time.Duration, bool) {
	switch state {
	case StateIdle:
		return policy.IdleTTL, policy.IdleTTL > 0
	case StatePaused:
		return policy.SuspendedTTL, policy.SuspendedTTL > 0
	default:
		return 0, false
	}
}

// idleCandidatesLocked collects the agents eligible on the activity test alone,
// keyed by id. Caller must hold f.mu.
func (f *Fabric) idleCandidatesLocked(now time.Time, policy AgentReclaimPolicy) map[string]IdleAgent {
	out := make(map[string]IdleAgent)
	for id, a := range f.agents {
		a.mu.RLock()
		state, busy, lastActive := a.State, a.busy, a.lastActiveAt
		a.mu.RUnlock()
		ttl, covered := reclaimTTLFor(state, policy)
		// busy excludes an agent whose quantum is in flight whatever its state
		// says: a suspension is taken at a quantum boundary, so a SUSPENDED agent
		// should not be busy, and if one is, the sweep waits rather than racing it.
		if !covered || busy || now.Sub(lastActive) <= ttl {
			continue
		}
		claim := make(map[string]float64, len(a.resources))
		for name, amount := range a.resources {
			claim[name] = amount
		}
		caps := append([]string(nil), a.Capabilities...)
		out[id] = IdleAgent{
			Identity:     id,
			State:        state,
			IdleFor:      now.Sub(lastActive),
			Capabilities: caps,
			Resources:    claim,
		}
	}
	return out
}

// sortedCandidateIDs returns the candidate ids in a deterministic order. The
// order is load-bearing: the floor grows as candidates are kept, so a stable
// order makes the same pool state produce the same retirements every sweep.
func sortedCandidateIDs(candidates map[string]IdleAgent) []string {
	out := make([]string, 0, len(candidates))
	for id := range candidates {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// QuotaSnapshot reports the pool's resource accounting: what is currently
// allocated and what the budget allows, per resource name. Both maps are copies,
// so a caller cannot mutate fabric state through them.
//
// It exists because a quota exhaustion used to be indistinguishable from a
// broken pool (defect P1-6): with no view of allocated-vs-budget, every
// ErrResourceQuotaExceeded reads as "spawning stopped" rather than "N claims are
// held by agents that will never be reclaimed". Export it as a gauge, or log it
// where the rejection is handled.
func (f *Fabric) QuotaSnapshot() (allocated, budget map[string]float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.quotaSnapshotLocked()
}

// quotaSnapshotLocked is QuotaSnapshot's lock-held form, so the rejection path in
// Spawn can report the same accounting without taking f.mu twice. Caller must
// hold f.mu.
func (f *Fabric) quotaSnapshotLocked() (allocated, budget map[string]float64) {
	allocated = make(map[string]float64, len(f.allocated))
	for name, amount := range f.allocated {
		allocated[name] = amount
	}
	budget = make(map[string]float64, len(f.resourceBudget))
	for name, max := range f.resourceBudget {
		budget[name] = max
	}
	return allocated, budget
}
