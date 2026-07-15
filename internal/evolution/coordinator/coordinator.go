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
	Fitness   float64            `json:"fitness"`  // GA fitness score (0-100), 0 = unknown
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

	// MinFitnessThreshold: GA patches with fitness below this are rejected.
	// Scale: 0-100, matching population BestScore. 0 = no threshold.
	// Only applies to SourceGA. Other sources bypass fitness checks.
	MinFitnessThreshold float64

	// ApplyFitnessThreshold: GA patches with fitness >= this are auto-applied.
	// Scale: 0-100, matching population BestScore. 0 = disabled.
	// Only applies to SourceGA. Other sources bypass fitness checks.
	ApplyFitnessThreshold float64

	// SelfHealingEnabled enables automatic repair patch generation when
	// chaos faults are detected. When enabled, the Coordinator monitors
	// patch failures and generated repair proposals.
	// Default: false (disabled, must be explicitly enabled).
	SelfHealingEnabled bool `json:"self_healing_enabled" yaml:"self_healing_enabled"`

	// SelfHealingMaxRetries is the maximum number of self-healing attempts
	// before the Coordinator stops trying to repair a failing component.
	// Default: 3.
	SelfHealingMaxRetries int `json:"self_healing_max_retries" yaml:"self_healing_max_retries"`
}

// DefaultPolicy returns a sensible default Coordinator policy.
func DefaultPolicy() PolicyGenome {
	return PolicyGenome{
		AutoApplyThreshold:    8,
		MaxPatchesPerMinute:   4,
		MinFitnessThreshold:   30.0,
		ApplyFitnessThreshold: 60.0,
		SelfHealingEnabled:    false,
		SelfHealingMaxRetries: 3,
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

	// Self-healing state.
	healingAttempts map[string]int // target -> number of healing attempts
	healingResults  []HealingAttempt
}

// HealingAttempt records a self-healing attempt by the Coordinator.
type HealingAttempt struct {
	Target    string    `json:"target"`
	PatchType string    `json:"patch_type"`
	Attempt   int       `json:"attempt"`
	Success   bool      `json:"success"`
	Error     string    `json:"error,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// NewEvolutionCoordinator creates a new EvolutionCoordinator.
func NewEvolutionCoordinator(policy PolicyGenome, patchReg *patch.Registry) *EvolutionCoordinator {
	return &EvolutionCoordinator{
		policy:          policy,
		patchReg:        patchReg,
		healingAttempts: make(map[string]int),
		healingResults:  make([]HealingAttempt, 0),
	}
}

// ApplyEmergency applies a patch immediately, bypassing the decision process.
// Used for self-healing scenarios where a critical fault needs instant response.
// Returns the patch result or an error if the patch cannot be applied.
func (ec *EvolutionCoordinator) ApplyEmergency(ctx context.Context, patch patch.RuntimePatch) error {
	ec.mu.Lock()
	defer ec.mu.Unlock()

	proposal := PatchProposal{
		Patch:     patch,
		Source:    SourceChaos,
		Reason:    "emergency: self-healing immediate apply",
		Priority:  10, // Maximum priority
		Timestamp: time.Now(),
	}

	err := ec.patchReg.Apply(ctx, patch)
	ec.decisions = append(ec.decisions, PatchDecision{
		Proposal: proposal,
		Decision: DecisionApply,
		Reason:   "emergency: bypassed decision process",
	})
	ec.patchHistory = append(ec.patchHistory, PatchResult{
		Proposal:  proposal,
		AppliedAt: time.Now(),
		Error:     err,
	})
	return err
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

// NotifySelfHealingAttempt records a self-healing attempt. Returns true if
// the Coordinator should proceed, false if disabled or max retries exceeded.
// Once exceeded, the refusal is sticky: subsequent calls for the same target
// return false without appending another record, so healingResults is bounded.
func (ec *EvolutionCoordinator) NotifySelfHealingAttempt(target string, patchType string) bool {
	ec.mu.Lock()
	defer ec.mu.Unlock()

	if !ec.policy.SelfHealingEnabled {
		return false
	}

	// Sticky refusal: once exceeded, do not grow history further.
	if ec.healingAttempts[target] > ec.policy.SelfHealingMaxRetries {
		return false
	}

	ec.healingAttempts[target]++
	attempt := ec.healingAttempts[target]
	if attempt > ec.policy.SelfHealingMaxRetries {
		ec.healingResults = append(ec.healingResults, HealingAttempt{
			Target:    target,
			PatchType: patchType,
			Attempt:   attempt,
			Success:   false,
			Error:     "max retries exceeded",
			Timestamp: time.Now(),
		})
		return false
	}
	return true
}

// NotifySelfHealingOutcome records the result of a self-healing attempt.
// No-op when SelfHealingEnabled is false. The caller MUST have called
// NotifySelfHealingAttempt first for this target; if not, the outcome is
// recorded with an explicit "outcome recorded without attempt" marker so
// the misuse is observable in SelfHealingHistory rather than silently
// emitting Attempt: 0.
func (ec *EvolutionCoordinator) NotifySelfHealingOutcome(target string, patchType string, success bool, errMsg string) {
	ec.mu.Lock()
	defer ec.mu.Unlock()

	if !ec.policy.SelfHealingEnabled {
		return
	}

	attempt := ec.healingAttempts[target]
	err := errMsg
	if attempt == 0 {
		if err != "" {
			err += "; "
		}
		err += "outcome recorded without attempt"
	}

	ec.healingResults = append(ec.healingResults, HealingAttempt{
		Target:    target,
		PatchType: patchType,
		Attempt:   attempt,
		Success:   success,
		Error:     err,
		Timestamp: time.Now(),
	})
}

// SelfHealingHistory returns all self-healing attempts for observability.
func (ec *EvolutionCoordinator) SelfHealingHistory() []HealingAttempt {
	ec.mu.RLock()
	defer ec.mu.RUnlock()
	out := make([]HealingAttempt, len(ec.healingResults))
	copy(out, ec.healingResults)
	return out
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

// decide implements the decision policy.
// Source-specific routing:
//   - SourceGA: fitness-gated (apply ≥ ApplyFitnessThreshold, reject < MinFitnessThreshold)
//   - SourceChaos: emergency bypass via ApplyEmergency, not here
//   - SourceHuman/SourceLLM/other: fallback to priority + rate-limit rules
//   - Fitness == 0 (unset): treated as "no information" → fallback to priority rules
func (ec *EvolutionCoordinator) decide(proposal PatchProposal) Decision {
	ec.mu.RLock()
	policy := ec.policy
	ec.mu.RUnlock()

	// Rate limiting applies to all sources.
	recentCount := ec.countRecentPatches(1 * time.Minute)
	if recentCount >= policy.MaxPatchesPerMinute {
		return DecisionDelay
	}

	// GA source: fitness-gated decision.
	if proposal.Source == SourceGA && proposal.Fitness > 0 {
		if proposal.Fitness >= policy.ApplyFitnessThreshold {
			return DecisionApply
		}
		if proposal.Fitness < policy.MinFitnessThreshold {
			return DecisionReject
		}
		// Fitness between threshold and floor: delay for review.
		return DecisionDelay
	}

	// Non-GA sources or Fitness == 0: fallback to priority rules.
	if proposal.Priority >= policy.AutoApplyThreshold {
		return DecisionApply
	}

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
		if proposal.Source == SourceGA && proposal.Fitness > 0 {
			return fmt.Sprintf("applying patch %s from %s: fitness %.1f >= threshold", proposal.Patch.Type, proposal.Source, proposal.Fitness)
		}
		return fmt.Sprintf("applying patch %s from %s (priority %d)", proposal.Patch.Type, proposal.Source, proposal.Priority)
	case DecisionReject:
		if proposal.Source == SourceGA && proposal.Fitness > 0 {
			return fmt.Sprintf("rejected patch %s from %s: fitness %.1f < min threshold %.0f", proposal.Patch.Type, proposal.Source, proposal.Fitness, 30.0)
		}
		return fmt.Sprintf("rejected patch %s from %s: rate limited or blacklisted", proposal.Patch.Type, proposal.Source)
	case DecisionDelay:
		if proposal.Source == SourceGA && proposal.Fitness > 0 {
			return fmt.Sprintf("delayed patch %s from %s: fitness %.1f between threshold and floor", proposal.Patch.Type, proposal.Source, proposal.Fitness)
		}
		return fmt.Sprintf("delayed patch %s from %s: too many recent patches", proposal.Patch.Type, proposal.Source)
	default:
		return "unknown decision"
	}
}
