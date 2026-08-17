package taskfabric

import (
	"math"
	"strings"
)

// Candidate is an agent competing to acquire a task (design §8 of
// docs/zh/architecture/ares-runtime.md). Load and Confidence are expected in [0,1].
type Candidate struct {
	AgentID      string
	Capabilities []string
	// Load is the current utilization (1 = fully busy).
	Load float64
	// Confidence is the experience-derived prior (ares_skills Experience
	// BestMatch SuccessRate is a natural source).
	Confidence float64
	// Priority is the scheduling priority of the candidate (>= 0; 0 =
	// normal). It models OS-thread priority: a higher-priority agent gets a
	// proportional score boost, so the scheduler prefers it when capability /
	// load / confidence are comparable. The boost is multiplicative
	// (score × (1 + priority)); the default 0 keeps pre-priority behavior.
	Priority float64
}

// Score computes the capability-aware scheduling score:
//
//	score = capability_overlap × (1 - load) × confidence × (1 + priority)
//
// A candidate whose capabilities do not overlap the task's required
// capability scores 0 (never chosen for a task it cannot do). Load discounts
// busy agents; confidence prefers historically successful executors — the
// Skill-first / Experience design feeds this directly. Priority is the
// OS-thread analog: a higher-priority candidate wins ties it would otherwise
// lose (e.g. a busy high-priority agent can outscore an idle low-priority one
// when the priority boost exceeds the load discount).
//
// CONTRACT: Priority is documented as >= 0 (0 = normal). The boost is
// intentionally NOT clamped: a priority boost only multiplies the score
// within the capability-gated space, and an extreme priority is a legitimate
// operator signal (e.g. a dedicated agent that must win every dispatch). To
// bound the boost, callers clamp the priority they inject; clamping here
// would silently distort the operator's intent.
func Score(taskCapability string, c Candidate) float64 {
	overlap := capabilityOverlap(taskCapability, c.Capabilities)
	if overlap <= 0 {
		return 0
	}
	load := clamp01(c.Load)
	conf := clamp01(c.Confidence)
	boost := 1.0
	if c.Priority > 0 {
		boost = 1.0 + c.Priority
	}
	return overlap * (1 - load) * conf * boost
}

// Pick returns the best candidate (highest Score) for a task, or nil when no
// candidate is capable.
func Pick(taskCapability string, candidates []Candidate) *Candidate {
	var best *Candidate
	bestScore := 0.0
	for i := range candidates {
		s := Score(taskCapability, candidates[i])
		if s <= 0 {
			continue
		}
		if best == nil || s > bestScore {
			best = &candidates[i]
			bestScore = s
		}
	}
	return best
}

// capabilityOverlap is the fraction of the required capability segments (a
// slash-separated chain like "rust/unsafe-analysis") that the candidate's
// declared capabilities cover. A prefix match counts (agent declaring "rust"
// covers required "rust/unsafe-analysis"). An empty required capability means
// the task is unconstrained and open to any candidate (overlap = 1).
func capabilityOverlap(required string, have []string) float64 {
	if strings.TrimSpace(required) == "" {
		return 1.0
	}
	var req []string
	for _, part := range strings.Split(required, "/") {
		if part = strings.TrimSpace(part); part != "" {
			req = append(req, part)
		}
	}
	if len(req) == 0 {
		return 0
	}
	matched := 0
	for _, r := range req {
		for _, h := range have {
			if h == r || strings.HasPrefix(h, r+"/") {
				matched++
				break
			}
		}
	}
	return float64(matched) / float64(len(req))
}

// clamp01 bounds a value to [0,1].
func clamp01(v float64) float64 {
	return math.Max(0, math.Min(1, v))
}
