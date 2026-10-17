package agentfabric

// OrphanAudit is the account of agents a retirement left behind (defect A-factory4 / P2-7,
// the reconciliation half).
//
// WHY THIS EXISTS AS AN AUDIT RATHER THAN A CASCADE: retiring an agent does NOT retire the
// agents it spawned — `Agent.Parent` is PROVENANCE, not a lifecycle relation — so they stay
// live and keep their resource claims. That is a deliberate contract, and the retirement
// event already names the orphans it created (see PayloadKeyOrphanedSpawns). What no one
// could see is the ACCUMULATION: a spawn/retire loop leaves a growing set of agents nothing
// reclaims, and their claims are invisible because each individual retirement looks
// reasonable. This is the number that makes it visible, so an operator can decide between
// reclaiming them, fixing the spawner, or raising the budget.
type OrphanAudit struct {
	// Agents is how many live agents have a parent that is gone (retired, or absent from
	// the fabric entirely — a restored store can lose the parent record).
	Agents int
	// Resources is the sum of the claims those agents still hold, per resource name. This
	// is the half that costs money: the agents are idle, but their claims are not.
	Resources map[string]float64
	// Sample names up to N orphan ids, for a log line that can be acted on. The count is
	// the fact; the sample is how someone starts looking.
	Sample []string
}

// AuditOrphans reports the live agents whose spawning parent is gone.
//
// sample bounds the id list (<= 0 means none): an audit is meant to be CHEAP and periodic,
// and the useful log line is "N agents, M resources, e.g. these three" rather than a list
// that grows with the leak it is reporting.
//
// It takes the fabric lock and walks the live agents: the same work the reclaim sweep
// already does, so calling it on the sweep's cadence adds no new asymptotic cost.
func (f *Fabric) AuditOrphans(sample int) OrphanAudit {
	if sample < 0 {
		sample = 0
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	audit := OrphanAudit{}
	for id, a := range f.agents {
		a.mu.RLock()
		parent, state := a.Parent, a.State
		claim := a.resources
		a.mu.RUnlock()

		if parent == "" || state == StateRetired {
			continue // a root agent, or one that is already released
		}
		orphan := false
		if p, ok := f.agents[parent]; !ok {
			// The parent is not here at all: a restored fabric can be missing the record,
			// and the child is just as stranded as if the parent had been retired.
			orphan = true
		} else {
			p.mu.RLock()
			retired := p.State == StateRetired
			p.mu.RUnlock()
			orphan = retired
		}
		if !orphan {
			continue
		}

		audit.Agents++
		if audit.Resources == nil {
			audit.Resources = make(map[string]float64, 4)
		}
		for name, amount := range claim {
			if amount > 0 {
				audit.Resources[name] += amount
			}
		}
		if len(audit.Sample) < sample {
			audit.Sample = append(audit.Sample, id)
		}
	}
	return audit
}
