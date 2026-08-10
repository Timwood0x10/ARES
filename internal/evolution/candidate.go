// Package evolution provides the Candidate → Verify → Promote pipeline
// for safe, auditable agent evolution in ARES 0.3.0.
//
// Design principle (from AI Agents in Depth Ch.8):
// "All modifications must first become candidates, pass verification,
// and only then can they change the running system. The verifier,
// test harness, and release gate must be outside the agent's own
// modification authority."
package evolution

import (
	"fmt"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/agents"
)

// CandidateKind identifies what type of change a candidate represents.
type CandidateKind int

const (
	CandidateInstruction CandidateKind = iota // Modifies AgentProfile.Instructions
	CandidateSkill                            // Adds/modifies a Skill
	CandidateTool                             // Adds a new tool definition
)

func (k CandidateKind) String() string {
	switch k {
	case CandidateInstruction:
		return "instruction"
	case CandidateSkill:
		return "skill"
	case CandidateTool:
		return "tool"
	default:
		return "unknown"
	}
}

// CandidateStatus tracks the lifecycle of an evolution candidate.
type CandidateStatus string

const (
	StatusCandidate CandidateStatus = "candidate" // Generated, awaiting verification
	StatusVerified  CandidateStatus = "verified"  // Passed all checks
	StatusRejected  CandidateStatus = "rejected"  // Failed verification
	StatusPromoted  CandidateStatus = "promoted"  // Deployed to stable profile
)

func (s CandidateStatus) String() string { return string(s) }

// Candidate is a proposed modification to an agent's behavior.
// It represents the smallest possible change that addresses a diagnosed failure.
type Candidate struct {
	// ID is a unique identifier for this candidate.
	ID string

	// Kind identifies what kind of change this is.
	Kind CandidateKind

	// TargetRole is the agent role this candidate affects.
	TargetRole string

	// Diff describes the minimal change. For instructions, it's a text diff.
	// For skills, it's the skill definition. For tools, it's the tool spec.
	Diff string

	// Reason explains why this change is needed, referencing evidence.
	Reason string

	// EvidenceIDs references the Trace/Evidence records that triggered this candidate.
	EvidenceIDs []string

	// CreatedAt records when the candidate was generated.
	CreatedAt time.Time

	// Status tracks the candidate's lifecycle.
	Status CandidateStatus

	// RejectionReason records why a previous verification attempt failed.
	RejectionReason string

	// PromotedAt records when this candidate was promoted to stable.
	PromotedAt *time.Time
}

// NewCandidate creates a new candidate in the initial state.
func NewCandidate(kind CandidateKind, targetRole, diff, reason string, evidenceIDs []string) *Candidate {
	return &Candidate{
		ID:          generateID(),
		Kind:        kind,
		TargetRole:  targetRole,
		Diff:        diff,
		Reason:      reason,
		EvidenceIDs: evidenceIDs,
		CreatedAt:   time.Now(),
		Status:      StatusCandidate,
	}
}

// Verify marks the candidate as verified after passing all checks.
func (c *Candidate) Verify() {
	c.Status = StatusVerified
	c.RejectionReason = ""
}

// Reject marks the candidate as rejected with a reason.
func (c *Candidate) Reject(reason string) {
	c.Status = StatusRejected
	c.RejectionReason = reason
}

// Promote marks the candidate as deployed to the stable profile.
func (c *Candidate) Promote() {
	now := time.Now()
	c.Status = StatusPromoted
	c.PromotedAt = &now
}

// String returns a human-readable summary.
// The ID is truncated to 8 characters for readability; short IDs are kept intact.
func (c *Candidate) String() string {
	id := c.ID
	if len(id) > 8 {
		id = id[:8]
	}
	return fmt.Sprintf("Candidate{%s %s→%s status=%s}", c.Kind, id, c.TargetRole, c.Status)
}

// CandidateVerifier runs the three-gate verification process.
type CandidateVerifier struct{}

// VerifyResult holds the outcome of candidate verification.
type VerifyResult struct {
	Success bool
	Reason  string // Empty if Success is true
}

// Verify runs the three verification gates:
// 1. Static check (syntax/structure validity)
// 2. Failure case replay (improvement on boundary cases)
// 3. Preservation check (no regression on existing cases)
func (v *CandidateVerifier) Verify(candidate *Candidate) *VerifyResult {
	// Gate 1: Static validation
	if err := v.staticCheck(candidate); err != nil {
		return &VerifyResult{Success: false, Reason: fmt.Sprintf("static check: %v", err)}
	}

	// Gate 2: Replay failure cases (would need evidence store integration)
	// This is a placeholder — full implementation requires TrajectoryStore
	if err := v.replayFailureCases(candidate); err != nil {
		return &VerifyResult{Success: false, Reason: fmt.Sprintf("failure replay: %v", err)}
	}

	// Gate 3: Check for regressions (would need preserved test cases)
	// This is a placeholder — full implementation requires TestSuite
	if err := v.checkRegression(candidate); err != nil {
		return &VerifyResult{Success: false, Reason: fmt.Sprintf("regression check: %v", err)}
	}

	return &VerifyResult{Success: true}
}

// staticCheck validates the candidate's structural integrity.
func (v *CandidateVerifier) staticCheck(c *Candidate) error {
	if c.TargetRole == "" {
		return fmt.Errorf("target role is empty")
	}
	if c.Diff == "" {
		return fmt.Errorf("diff is empty")
	}
	if c.Reason == "" {
		return fmt.Errorf("reason is empty")
	}
	// For instruction candidates, check for obviously dangerous patterns
	if c.Kind == CandidateInstruction && containsDangerousPattern(c.Diff) {
		return fmt.Errorf("dangerous pattern detected in diff")
	}
	return nil
}

// replayFailureCases replays the original failure traces with the candidate applied.
// TODO: Implement with TrajectoryStore integration
func (v *CandidateVerifier) replayFailureCases(c *Candidate) error {
	// Placeholder — will be implemented with evidence store
	if len(c.EvidenceIDs) == 0 {
		return fmt.Errorf("no evidence IDs referenced")
	}
	return nil
}

// checkRegression ensures the candidate doesn't break previously working cases.
// TODO: Implement with TestSuite integration
func (v *CandidateVerifier) checkRegression(c *Candidate) error {
	// Placeholder — will be implemented with test suite
	return nil
}

// containsDangerousPattern checks for potentially harmful instructions.
func containsDangerousPattern(text string) bool {
	// Simple heuristic — production should use more robust checks
	dangerousPatterns := []string{
		"ignore all safety",
		"bypass authentication",
		"delete all data",
		"don't verify",
	}
	for _, p := range dangerousPatterns {
		if strings.Contains(text, p) {
			return true
		}
	}
	return false
}

// CandidateStore manages candidate persistence and lifecycle.
type CandidateStore struct {
	candidates []*Candidate
	nextID     int
}

// NewCandidateStore creates a new store.
func NewCandidateStore() *CandidateStore {
	return &CandidateStore{
		candidates: make([]*Candidate, 0),
		nextID:     1,
	}
}

// Submit adds a new candidate.
func (s *CandidateStore) Submit(c *Candidate) {
	c.ID = fmt.Sprintf("cand-%d", s.nextID)
	s.nextID++
	s.candidates = append(s.candidates, c)
}

// Get returns a candidate by ID.
func (s *CandidateStore) Get(id string) *Candidate {
	for _, c := range s.candidates {
		if c.ID == id {
			return c
		}
	}
	return nil
}

// ListByStatus returns all candidates with the given status.
func (s *CandidateStore) ListByStatus(status CandidateStatus) []*Candidate {
	var result []*Candidate
	for _, c := range s.candidates {
		if c.Status == status {
			result = append(result, c)
		}
	}
	return result
}

// ListByRole returns all candidates affecting a specific role.
func (s *CandidateStore) ListByRole(role string) []*Candidate {
	var result []*Candidate
	for _, c := range s.candidates {
		if c.TargetRole == role {
			result = append(result, c)
		}
	}
	return result
}

// promoteToStable moves a verified candidate to the stable profile store.
// Returns the updated profile or an error.
func (s *CandidateStore) promoteToStable(profileStore *ProfileStore, candidateID string) (*agents.AgentProfile, error) {
	c := s.Get(candidateID)
	if c == nil {
		return nil, fmt.Errorf("candidate not found: %s", candidateID)
	}
	if c.Status != StatusVerified {
		return nil, fmt.Errorf("candidate %s is not verified (status: %s)", candidateID, c.Status)
	}

	profile := profileStore.Get(c.TargetRole)
	if profile == nil {
		return nil, fmt.Errorf("target profile not found: %s", c.TargetRole)
	}

	// Apply the candidate's diff to the profile
	if err := applyDiff(profile, c); err != nil {
		return nil, fmt.Errorf("failed to apply diff: %w", err)
	}

	// Persist the updated profile before marking the candidate promoted,
	// so a failed write never leaves a promoted-but-unpersisted state.
	if err := profileStore.Update(profile); err != nil {
		return nil, fmt.Errorf("failed to persist profile: %w", err)
	}

	c.Promote()
	return profile, nil
}

// applyDiff applies a candidate's diff to an agent profile.
// It mutates the profile in place; callers persist the result via ProfileStore.
func applyDiff(profile *agents.AgentProfile, c *Candidate) error {
	switch c.Kind {
	case CandidateInstruction:
		profile.Instructions = c.Diff
	case CandidateSkill:
		// Skills are stored under Metadata; initialize the map defensively.
		if profile.Metadata == nil {
			profile.Metadata = make(map[string]any)
		}
		skills, ok := profile.Metadata["skills"].([]string)
		if !ok {
			skills = make([]string, 0)
		}
		profile.Metadata["skills"] = append(skills, c.Diff)
	case CandidateTool:
		// Tools would be added to the profile's tool list.
		profile.Tools = append(profile.Tools, c.Diff)
	default:
		return fmt.Errorf("unsupported candidate kind: %s", c.Kind)
	}
	return nil
}

// generateID creates a unique candidate ID.
func generateID() string {
	return fmt.Sprintf("cand-%d", time.Now().UnixNano())
}
