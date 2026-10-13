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
	// becomes reclaimable. Zero or negative DISABLES reclamation: that is the
	// explicit off switch for an operator who wants every agent kept.
	IdleTTL time.Duration
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
func DefaultAgentReclaimPolicy() AgentReclaimPolicy {
	return AgentReclaimPolicy{IdleTTL: 10 * time.Minute, KeepPerCapability: 1}
}

// IdleAgent is the read-only record of one reclaimed agent: enough for a log
// line or a test to see what happened, without handing out a live *Agent.
type IdleAgent struct {
	// Identity is the reclaimed agent's id.
	Identity string
	// IdleFor is how long it had been inactive when the sweep decided.
	IdleFor time.Duration
	// Capabilities are the capabilities whose floor was evaluated against this
	// agent — what it was contributing to the pool.
	Capabilities []string
	// Resources is the claim the retirement released back to the quota.
	Resources map[string]float64
}

// ReclaimIdle retires every agent that is IDLE, not busy, and has been inactive
// longer than the policy window, as far as the per-capability floor allows. It
// returns the retired agents in id order.
//
// Retirement goes through the existing Retire, deliberately: that is the single
// release path the quota already trusts, it is idempotent, and it records the
// same agent.retired event an operator-initiated retire does — reclamation adds
// a trigger, not a second bookkeeping scheme.
//
// Agents holding a live task are not identified here (the agent fabric cannot
// see task ownership), and it does not need to: the busy flag covers a quantum
// in flight, and a retired agent's leased or suspended task is reclaimed by the
// task fabric's own lease recovery — the same path an operator retire relies on.
//
// The policy is a parameter rather than fabric state so one caller can sweep
// harder or softer without reconfiguring the fabric. ReclaimIdle is safe to call
// concurrently with itself and with every other fabric operation.
func (f *Fabric) ReclaimIdle(ctx context.Context, policy AgentReclaimPolicy) []IdleAgent {
	if policy.IdleTTL <= 0 {
		return nil
	}
	keep := policy.KeepPerCapability
	if keep < 1 {
		keep = 1
	}

	f.mu.Lock()
	candidates := f.idleCandidatesLocked(f.now(), policy.IdleTTL)
	// The floor counts the agents that are NOT candidates, then grows as the
	// sweep decides to KEEP a candidate. Evaluating it against the survivors
	// instead of the current pool is what keeps a batch of same-capability
	// candidates from collectively retiring a capability's last holders.
	holders := make(map[string]int)
	for id, a := range f.agents {
		if candidates[id].Identity != "" {
			continue
		}
		for _, cap := range a.Capabilities {
			holders[cap]++
		}
	}
	f.mu.Unlock()

	reclaimed := make([]IdleAgent, 0, len(candidates))
	for _, id := range sortedCandidateIDs(candidates) {
		c := candidates[id]
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
		if err := f.Retire(ctx, id); err != nil {
			// Lost a race (already retired or killed between the scan and here).
			// Not an error for the sweep: the goal — the claim is back — is met
			// either way, and the next sweep sees the truth.
			continue
		}
		reclaimed = append(reclaimed, c)
	}
	return reclaimed
}

// idleCandidatesLocked collects the agents eligible on the activity test alone,
// keyed by id. Caller must hold f.mu.
func (f *Fabric) idleCandidatesLocked(now time.Time, ttl time.Duration) map[string]IdleAgent {
	out := make(map[string]IdleAgent)
	for id, a := range f.agents {
		a.mu.RLock()
		idle := a.State == StateIdle && !a.busy && now.Sub(a.lastActiveAt) > ttl
		idleFor := now.Sub(a.lastActiveAt)
		a.mu.RUnlock()
		if !idle {
			continue
		}
		claim := make(map[string]float64, len(a.resources))
		for name, amount := range a.resources {
			claim[name] = amount
		}
		caps := append([]string(nil), a.Capabilities...)
		out[id] = IdleAgent{Identity: id, IdleFor: idleFor, Capabilities: caps, Resources: claim}
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
