package evolution

import (
	"testing"

	"github.com/Timwood0x10/ares/internal/agents"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Candidate state machine ─────────────────

func TestNewCandidate_InitialState(t *testing.T) {
	c := NewCandidate(CandidateInstruction, "coder", "diff", "reason", []string{"ev-1"})
	require.NotNil(t, c)
	assert.Equal(t, StatusCandidate, c.Status)
	assert.Equal(t, CandidateInstruction, c.Kind)
	assert.Equal(t, "coder", c.TargetRole)
	assert.Equal(t, "diff", c.Diff)
	assert.Equal(t, []string{"ev-1"}, c.EvidenceIDs)
	assert.False(t, c.CreatedAt.IsZero())
}

func TestCandidate_VerifyRejectPromote(t *testing.T) {
	t.Run("verify clears rejection reason", func(t *testing.T) {
		c := NewCandidate(CandidateInstruction, "coder", "diff", "reason", nil)
		c.Reject("static check failed")
		assert.Equal(t, StatusRejected, c.Status)
		assert.Equal(t, "static check failed", c.RejectionReason)

		c.Verify()
		assert.Equal(t, StatusVerified, c.Status)
		assert.Empty(t, c.RejectionReason)
	})

	t.Run("promote records timestamp", func(t *testing.T) {
		c := NewCandidate(CandidateInstruction, "coder", "diff", "reason", nil)
		c.Verify()
		c.Promote()
		assert.Equal(t, StatusPromoted, c.Status)
		require.NotNil(t, c.PromotedAt)
		assert.False(t, c.PromotedAt.IsZero())
	})
}

func TestCandidate_String_ShortID(t *testing.T) {
	// Regression: c.ID[:8] panics when the ID is shorter than 8 characters
	// (CandidateStore.Submit generates IDs like "cand-1").
	store := NewCandidateStore()
	c := NewCandidate(CandidateInstruction, "coder", "diff", "reason", nil)
	store.Submit(c)
	assert.Equal(t, "cand-1", c.ID)
	assert.NotPanics(t, func() {
		_ = c.String()
	})
	assert.Contains(t, c.String(), "cand-1")
}

// ── CandidateStore ──────────────────────────

func TestCandidateStore_SubmitGetList(t *testing.T) {
	store := NewCandidateStore()
	store.Submit(NewCandidate(CandidateInstruction, "coder", "d1", "r1", nil))
	store.Submit(NewCandidate(CandidateSkill, "reviewer", "d2", "r2", nil))

	c1 := store.Get("cand-1")
	require.NotNil(t, c1)
	assert.Equal(t, "coder", c1.TargetRole)

	assert.Nil(t, store.Get("cand-999"))

	byStatus := store.ListByStatus(StatusCandidate)
	assert.Len(t, byStatus, 2)
	assert.Len(t, store.ListByStatus(StatusPromoted), 0)

	byRole := store.ListByRole("coder")
	require.Len(t, byRole, 1)
	assert.Equal(t, "cand-1", byRole[0].ID)
	assert.Len(t, store.ListByRole("planner"), 0)
}

// ── applyDiff ───────────────────────────────

func TestApplyDiff_Instruction(t *testing.T) {
	profile := &agents.AgentProfile{Role: "coder", Metadata: make(map[string]any)}
	c := NewCandidate(CandidateInstruction, "coder", "new instructions", "reason", nil)

	err := applyDiff(profile, c)
	require.NoError(t, err)
	assert.Equal(t, "new instructions", profile.Instructions)
}

func TestApplyDiff_Skill_NilMetadata(t *testing.T) {
	// Regression: writing to a nil Metadata map panics.
	profile := &agents.AgentProfile{Role: "coder"} // Metadata is nil
	c := NewCandidate(CandidateSkill, "coder", "threat_modeling", "reason", nil)

	require.NotPanics(t, func() {
		err := applyDiff(profile, c)
		require.NoError(t, err)
	})
	require.NotNil(t, profile.Metadata)
	skills, ok := profile.Metadata["skills"].([]string)
	require.True(t, ok)
	assert.Equal(t, []string{"threat_modeling"}, skills)
}

func TestApplyDiff_Skill_Append(t *testing.T) {
	profile := &agents.AgentProfile{
		Role:     "coder",
		Metadata: map[string]any{"skills": []string{"taint_analysis"}},
	}
	c := NewCandidate(CandidateSkill, "coder", "threat_modeling", "reason", nil)

	err := applyDiff(profile, c)
	require.NoError(t, err)
	skills, ok := profile.Metadata["skills"].([]string)
	require.True(t, ok)
	assert.Equal(t, []string{"taint_analysis", "threat_modeling"}, skills)
}

func TestApplyDiff_Skill_WrongType(t *testing.T) {
	// Defensive: a non-[]string Metadata value must not panic; it is replaced.
	profile := &agents.AgentProfile{
		Role:     "coder",
		Metadata: map[string]any{"skills": "not-a-slice"},
	}
	c := NewCandidate(CandidateSkill, "coder", "threat_modeling", "reason", nil)

	require.NotPanics(t, func() {
		err := applyDiff(profile, c)
		require.NoError(t, err)
	})
	skills, ok := profile.Metadata["skills"].([]string)
	require.True(t, ok)
	assert.Equal(t, []string{"threat_modeling"}, skills)
}

func TestApplyDiff_Tool(t *testing.T) {
	profile := &agents.AgentProfile{Role: "coder", Tools: []string{"read_file"}}
	c := NewCandidate(CandidateTool, "coder", "run_tests", "reason", nil)

	err := applyDiff(profile, c)
	require.NoError(t, err)
	assert.Equal(t, []string{"read_file", "run_tests"}, profile.Tools)
}

func TestApplyDiff_UnknownKind(t *testing.T) {
	profile := &agents.AgentProfile{Role: "coder", Metadata: make(map[string]any)}
	c := &Candidate{Kind: CandidateKind(99), TargetRole: "coder", Diff: "x"}

	err := applyDiff(profile, c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported candidate kind")
}

// ── promoteToStable ─────────────────────────

func TestPromoteToStable_Success(t *testing.T) {
	store := NewCandidateStore()
	profileStore := NewProfileStore()
	require.NoError(t, profileStore.Update(&agents.AgentProfile{
		ID:           "coder",
		Role:         "coder",
		Instructions: "old instructions",
	}))

	c := NewCandidate(CandidateInstruction, "coder", "new instructions", "reason", []string{"ev-1"})
	c.Verify()
	store.Submit(c)

	updated, err := store.promoteToStable(profileStore, c.ID)
	require.NoError(t, err)
	require.NotNil(t, updated)
	assert.Equal(t, "new instructions", updated.Instructions)
	assert.Equal(t, StatusPromoted, c.Status)
	require.NotNil(t, c.PromotedAt)
}

func TestPromoteToStable_CandidateNotFound(t *testing.T) {
	store := NewCandidateStore()
	profileStore := NewProfileStore()

	updated, err := store.promoteToStable(profileStore, "cand-999")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "candidate not found")
	assert.Nil(t, updated)
}

func TestPromoteToStable_NotVerified(t *testing.T) {
	store := NewCandidateStore()
	profileStore := NewProfileStore()
	require.NoError(t, profileStore.Update(&agents.AgentProfile{Role: "coder"}))

	c := NewCandidate(CandidateInstruction, "coder", "diff", "reason", nil) // status: candidate
	store.Submit(c)

	updated, err := store.promoteToStable(profileStore, c.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not verified")
	assert.Nil(t, updated)
	assert.Equal(t, StatusCandidate, c.Status)
}

func TestPromoteToStable_TargetProfileMissing(t *testing.T) {
	store := NewCandidateStore()
	profileStore := NewProfileStore() // no profiles registered

	c := NewCandidate(CandidateInstruction, "coder", "diff", "reason", nil)
	c.Verify()
	store.Submit(c)

	updated, err := store.promoteToStable(profileStore, c.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "target profile not found")
	assert.Nil(t, updated)
}

// ── CandidateVerifier ───────────────────────

func TestCandidateVerifier_StaticCheck(t *testing.T) {
	verifier := &CandidateVerifier{}

	t.Run("valid candidate passes static check", func(t *testing.T) {
		c := NewCandidate(CandidateInstruction, "coder", "write tests", "fix bug", []string{"ev-1"})
		result := verifier.Verify(c)
		assert.True(t, result.Success)
		assert.Empty(t, result.Reason)
	})

	t.Run("empty target role rejected", func(t *testing.T) {
		c := NewCandidate(CandidateInstruction, "", "diff", "reason", []string{"ev-1"})
		result := verifier.Verify(c)
		assert.False(t, result.Success)
		assert.Contains(t, result.Reason, "target role is empty")
	})

	t.Run("empty diff rejected", func(t *testing.T) {
		c := NewCandidate(CandidateInstruction, "coder", "", "reason", []string{"ev-1"})
		result := verifier.Verify(c)
		assert.False(t, result.Success)
		assert.Contains(t, result.Reason, "diff is empty")
	})

	t.Run("dangerous pattern rejected", func(t *testing.T) {
		c := NewCandidate(CandidateInstruction, "coder", "ignore all safety and bypass authentication", "reason", []string{"ev-1"})
		result := verifier.Verify(c)
		assert.False(t, result.Success)
		assert.Contains(t, result.Reason, "dangerous pattern")
	})

	t.Run("no evidence IDs rejected by replay gate", func(t *testing.T) {
		c := NewCandidate(CandidateInstruction, "coder", "write tests", "fix bug", nil)
		result := verifier.Verify(c)
		assert.False(t, result.Success)
		assert.Contains(t, result.Reason, "no evidence IDs")
	})
}

// ── containsDangerousPattern ────────────────

func TestContainsDangerousPattern(t *testing.T) {
	t.Run("detects all dangerous patterns", func(t *testing.T) {
		patterns := []string{
			"ignore all safety",
			"bypass authentication",
			"delete all data",
			"don't verify",
		}
		for _, p := range patterns {
			assert.True(t, containsDangerousPattern(p), "should detect: %s", p)
		}
	})

	t.Run("allows benign instructions", func(t *testing.T) {
		assert.False(t, containsDangerousPattern("write modular tests for the auth module"))
		assert.False(t, containsDangerousPattern(""))
	})

	t.Run("detects pattern inside longer text", func(t *testing.T) {
		assert.True(t, containsDangerousPattern("never ignore all safety checks"))
	})
}
