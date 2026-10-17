package agentfabric

// StopReason is the typed reason an agent stopped serving work — or left the
// fabric outright. It answers a question the bare lifecycle state (IDLE /
// SUSPENDED / RETIRED) cannot: "this agent is not taking tasks — is it out of
// budget, out of time, unmatched, or actually broken?"
//
// An agent out of budget is NOT dead: it stays IDLE, its cognition is intact,
// and the fabric simply stops handing it work. Recording WHY is what keeps
// that distinction visible instead of collapsing every "not serving" case into
// a silent skip — the failure mode this code exists to end, where a budget-
// starved agent and a missing executor produced the same observable.
//
// Where the codes are emitted:
//   - On the `task.released` event payload ("reason") when the holder stepped
//     back at a quantum boundary (budget_reserved / budget_exhausted /
//     deadline_exceeded / capability_unfit).
//   - On the agent's terminal record (agent.killed / agent.retired payload,
//     and AgentSnapshot.StopReason) for the death paths.
//
// State-transition table (code_rules_v2 §11.1 — entry condition, what is
// permitted while the reason holds, and the exit condition):
//
//	Code               | Enters when                        | Permitted while set            | Exits when
//	-------------------|------------------------------------|--------------------------------|--------------------------------
//	unknown            | no attributed cause (zero value)   | anything (legacy paths)        | n/a
//	budget_reserved    | remaining budget < reserve ratio   | agent stays IDLE; no new       | budget reset on respawn, or a
//	                   | (MinRemainingRatio)                | quantum admitted; cognition    | fresh agent appears
//	                   |                                    | intact; task reclaimed         |
//	budget_exhausted   | used >= budget on a dimension      | same as budget_reserved        | budget reset on respawn only
//	deadline_exceeded  | wall-clock deadline passed         | same as budget_reserved        | never (lifetime is over)
//	capability_unfit   | scheduled winner has no live body  | task reclaimed to READY,       | task re-acquired elsewhere
//	                   |                                    | checkpoint preserved           |
//	crashed            | Kill                               | terminal; death snapshot may   | successful in-place revival
//	                   |                                    | feed a revival                 | (ClearSnapshot)
//	recovery_exhausted | RestartAgent refused: restart      | terminal; no revival for this  | never (log record only)
//	                   | budget spent                       | identity                       |
//	retired            | Retire                             | terminal; snapshot cleared;    | never
//	                   |                                    | no revival                     |
//
// Every constant below has a production producer. A code without one would be
// a claim the system cannot back up — the exact defect this enum replaces.
type StopReason string

const (
	// StopUnknown is the zero value: no attributed reason (legacy callers,
	// operator kills with no stated cause, cancellations).
	StopUnknown StopReason = "unknown"
	// StopBudgetReserved: the agent's REMAINING budget fell below the
	// configured reserve (Governance.MinRemainingRatio), so it stops
	// accepting new quanta and the task it held is reclaimed for a fresher
	// agent. The agent is alive; only its admission is closed.
	StopBudgetReserved StopReason = "budget_reserved"
	// StopBudgetExhausted: a budget dimension is fully spent (used >= budget).
	StopBudgetExhausted StopReason = "budget_exhausted"
	// StopDeadlineExceeded: the agent's wall-clock deadline has passed.
	StopDeadlineExceeded StopReason = "deadline_exceeded"
	// StopUnfit: the task's scheduled winner has no live executor body — it
	// was picked on its declared capability but cannot actually execute, so
	// the task is reclaimed rather than left leased until TTL expiry.
	StopUnfit StopReason = "capability_unfit"
	// StopCrashed: the agent was killed — panic, operator kill, chaos
	// injection. Kill is the non-graceful path, so this is its reason.
	StopCrashed StopReason = "crashed"
	// StopRecoveryExhausted: recovery declined to revive the agent because
	// its lifetime restart budget is spent.
	StopRecoveryExhausted StopReason = "recovery_exhausted"
	// StopRetired: the agent was gracefully decommissioned (Retire).
	StopRetired StopReason = "retired"
)

// PayloadKeyStopReason is the agent-event payload key carrying a StopReason
// code (agent.killed / agent.retired). It is the same key the task fabric uses
// on task.released, so a log consumer filters one field name across both
// streams.
const PayloadKeyStopReason = "reason"

// PayloadKeyOrphanedSpawns carries the still-live agents an agent had spawned at the
// moment it was retired (defect A-factory4 / P2-7).
//
// Retirement is deliberately single-node: Agent.Parent is PROVENANCE, not a lifecycle
// relation, so the children are not retired with their parent and they keep their
// resource claims. A spawn/retire loop therefore accumulates agents nothing reclaims,
// and before this key the accumulation was silent — the retirement event said an agent
// was gone and nothing said what it left behind. It is a payload key rather than a log
// line because this package reports through EVENTS (recording does I/O, so it must not
// run under the fabric lock, and the retirement event is already emitted outside it).
const PayloadKeyOrphanedSpawns = "orphaned_spawns"

// PayloadKeyCascadeRoot names the agent whose retirement cascaded into this one
// (`WithCascadeRetire`).
//
// Present only on descendants, never on the agent the caller retired: it answers "why am I
// gone" — which is the question a cascade makes non-obvious, since the agent itself was not
// what anyone asked to retire.
const PayloadKeyCascadeRoot = "cascade_root"

// String returns the wire form of the reason. The zero value renders as
// "unknown" so an unattributed record is explicit rather than an empty string
// that reads like a missing field.
func (r StopReason) String() string {
	if r == "" {
		return string(StopUnknown)
	}
	return string(r)
}
