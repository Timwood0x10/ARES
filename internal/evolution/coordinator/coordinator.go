// Package coordinator provides the central decision-maker for runtime patches.
//
// Coordinator does NOT know where patches come from (GA, Chaos, LLM, Human, K8s Operator).
// Coordinator ONLY decides: Apply? Reject? Delay?
//
// Architecture:
//
//	Any Source → PatchProposal → Coordinator → Decision → Apply / Reject / Delay
package coordinator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/evolution/patch"
)

// PatchSource identifies the origin of a patch proposal.
type PatchSource string

const (
	SourceGA    PatchSource = "genome" // Genetic Algorithm
	SourceChaos PatchSource = "chaos"  // Chaos Engineering
	SourceAKF   PatchSource = "akf"    // Knowledge Runtime
	SourceHuman PatchSource = "human"  // Manual operator
	SourceLLM   PatchSource = "llm"    // LLM suggestion
	SourceK8s   PatchSource = "k8s"    // Kubernetes Operator
	SourceRule  PatchSource = "rule"   // Rule Engine
)

// PatchProposal is what the Coordinator receives.
// It wraps a RuntimePatch with metadata for the decision process.
type PatchProposal struct {
	Patch     patch.RuntimePatch `json:"patch"`
	Source    PatchSource        `json:"source"`
	Reason    string             `json:"reason"`   // why this patch was proposed
	Priority  int                `json:"priority"` // 1-10, higher = more urgent
	Timestamp time.Time          `json:"timestamp"`
}

// Decision is the Coordinator's output.
type Decision int

const (
	DecisionApply  Decision = iota // Apply the patch now
	DecisionReject                 // Reject the patch
	DecisionDelay                  // Revisit later
)

// String returns a human-readable name for the decision.
func (d Decision) String() string {
	switch d {
	case DecisionApply:
		return "apply"
	case DecisionReject:
		return "reject"
	case DecisionDelay:
		return "delay"
	default:
		return fmt.Sprintf("unknown(%d)", int(d))
	}
}

// PatchDecision pairs a proposal with a decision.
type PatchDecision struct {
	Proposal PatchProposal
	Decision Decision
	Reason   string // why this decision was made
}

// PolicyGenome is the Coordinator's decision strategy — also evolvable.
type PolicyGenome struct {
	// AutoApplyThreshold: patches with priority >= this are auto-applied.
	AutoApplyThreshold int

	// MaxPatchesPerMinute: rate limit to prevent cascade failures.
	MaxPatchesPerMinute int

	// RequireMultiSource: if true, require >= 2 sources to agree.
	RequireMultiSource bool

	// Blacklist: sources to temporarily ignore.
	Blacklist []PatchSource
}

// DefaultPolicy returns a sensible default Coordinator policy.
func DefaultPolicy() PolicyGenome {
	return PolicyGenome{
		AutoApplyThreshold:  8,
		MaxPatchesPerMinute: 4,
		RequireMultiSource:  false,
	}
}

// PatchResult records the outcome of a patch application.
type PatchResult struct {
	Proposal  PatchProposal
	AppliedAt time.Time
	Error     error
}

// EvolutionCoordinator collects PatchProposals from all sources and decides
// whether to apply, defer, or reject each patch.
//
// Coordinator does NOT know:
//   - How patches are generated (GA? Chaos? LLM? Human?)
//   - What a Genome is
//   - How Mutation or Crossover works
//
// Coordinator ONLY knows:
//   - A patch has been proposed
//   - Should I apply it now, delay it, or reject it?
type EvolutionCoordinator struct {
	mu           sync.RWMutex
	policy       PolicyGenome    // decision strategy (evolvable)
	proposals    []PatchProposal // pending proposals
	decisions    []PatchDecision // decision history
	patchHistory []PatchResult   // apply results
	patchReg     *patch.Registry // registry for applying patches
}

// NewEvolutionCoordinator creates a new EvolutionCoordinator.
func NewEvolutionCoordinator(policy PolicyGenome, patchReg *patch.Registry) *EvolutionCoordinator {
	return &EvolutionCoordinator{
		policy:   policy,
		patchReg: patchReg,
	}
}

// Policy returns the current decision policy.
func (ec *EvolutionCoordinator) Policy() PolicyGenome {
	ec.mu.RLock()
	defer ec.mu.RUnlock()
	return ec.policy
}

// UpdatePolicy replaces the decision policy at runtime.
func (ec *EvolutionCoordinator) UpdatePolicy(p PolicyGenome) {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	ec.policy = p
}

// Submit receives a patch proposal from any source.
func (ec *EvolutionCoordinator) Submit(proposal PatchProposal) {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	ec.proposals = append(ec.proposals, proposal)
}

// PendingCount returns the number of pending proposals.
func (ec *EvolutionCoordinator) PendingCount() int {
	ec.mu.RLock()
	defer ec.mu.RUnlock()
	return len(ec.proposals)
}

// DecisionHistory returns all decisions made so far.
func (ec *EvolutionCoordinator) DecisionHistory() []PatchDecision {
	ec.mu.RLock()
	defer ec.mu.RUnlock()
	decisions := make([]PatchDecision, len(ec.decisions))
	copy(decisions, ec.decisions)
	return decisions
}

// PatchHistory returns all patch application results.
func (ec *EvolutionCoordinator) PatchHistory() []PatchResult {
	ec.mu.RLock()
	defer ec.mu.RUnlock()
	results := make([]PatchResult, len(ec.patchHistory))
	copy(results, ec.patchHistory)
	return results
}

// Evaluate processes all pending proposals and applies accepted patches.
func (ec *EvolutionCoordinator) Evaluate(ctx context.Context) {
	ec.mu.Lock()
	pending := ec.proposals
	ec.proposals = nil
	ec.mu.Unlock()

	for _, proposal := range pending {
		decision := ec.decide(proposal)
		ec.mu.Lock()
		ec.decisions = append(ec.decisions, PatchDecision{
			Proposal: proposal,
			Decision: decision,
			Reason:   decisionReason(decision, proposal),
		})
		ec.mu.Unlock()

		if decision != DecisionApply {
			continue
		}

		// Apply the patch.
		err := ec.patchReg.Apply(ctx, proposal.Patch)
		ec.mu.Lock()
		ec.patchHistory = append(ec.patchHistory, PatchResult{
			Proposal:  proposal,
			AppliedAt: time.Now(),
			Error:     err,
		})
		ec.mu.Unlock()
	}
}

// Run is the Coordinator's main loop. Call it in a goroutine.
func (ec *EvolutionCoordinator) Run(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ec.Evaluate(ctx)
		}
	}
}

// decide implements the decision policy.
func (ec *EvolutionCoordinator) decide(proposal PatchProposal) Decision {
	ec.mu.RLock()
	policy := ec.policy
	ec.mu.RUnlock()

	// Check blacklist.
	for _, bl := range policy.Blacklist {
		if proposal.Source == bl {
			return DecisionReject
		}
	}

	// Rate limiting.
	recentCount := ec.countRecentPatches(1 * time.Minute)
	if recentCount >= policy.MaxPatchesPerMinute {
		return DecisionDelay
	}

	// Auto-apply high-priority patches.
	if proposal.Priority >= policy.AutoApplyThreshold {
		return DecisionApply
	}

	// Default: apply with caution.
	return DecisionApply
}

// countRecentPatches counts patch applications within the given duration.
func (ec *EvolutionCoordinator) countRecentPatches(d time.Duration) int {
	ec.mu.RLock()
	defer ec.mu.RUnlock()

	since := time.Now().Add(-d)
	var count int
	for _, r := range ec.patchHistory {
		if r.AppliedAt.After(since) {
			count++
		}
	}
	return count
}

// decisionReason returns a human-readable reason for the decision.
func decisionReason(d Decision, proposal PatchProposal) string {
	switch d {
	case DecisionApply:
		return fmt.Sprintf("applying patch %s from %s (priority %d)", proposal.Patch.Type, proposal.Source, proposal.Priority)
	case DecisionReject:
		return fmt.Sprintf("rejected patch %s from %s: rate limited or blacklisted", proposal.Patch.Type, proposal.Source)
	case DecisionDelay:
		return fmt.Sprintf("delayed patch %s from %s: too many recent patches", proposal.Patch.Type, proposal.Source)
	default:
		return "unknown decision"
	}
}
