package eval

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── ResultVerifier (ground truth) ───────────────

func TestResultVerifier_AllPassed(t *testing.T) {
	verifier := NewResultVerifier()
	ev := verifier.Verify("task-1", "coder", []ResultCheck{
		{Name: "unit_test_auth", Status: "passed", Detail: "3/3 tests"},
		{Name: "tool_refund_called", Status: "passed", Detail: "ok"},
	})
	require.NotNil(t, ev)
	assert.Equal(t, VerdictPass, ev.Verdict)
	assert.Equal(t, 1.0, ev.Confidence)
	assert.False(t, ev.HasFailure())
	assert.Equal(t, "result_verifier", ev.Source)
	require.Len(t, ev.Dimensions, 1)
	assert.Equal(t, "task_result", ev.Dimensions[0].Name)
	assert.True(t, ev.Dimensions[0].Pass)
}

func TestResultVerifier_AnyFailed(t *testing.T) {
	verifier := NewResultVerifier()
	ev := verifier.Verify("task-1", "coder", []ResultCheck{
		{Name: "unit_test_auth", Status: "passed"},
		{Name: "db_state", Status: "failed", Detail: "row missing"},
	})
	require.NotNil(t, ev)
	assert.Equal(t, VerdictFail, ev.Verdict)
	assert.True(t, ev.HasFailure())
	assert.NotEmpty(t, ev.Flag)
	assert.Contains(t, ev.Flag, "failed")
	// Dimension pass flag is false because at least one check failed.
	assert.False(t, ev.Dimensions[0].Pass)
}

func TestResultVerifier_MissingIsUncertain(t *testing.T) {
	verifier := NewResultVerifier()
	ev := verifier.Verify("task-1", "coder", []ResultCheck{
		{Name: "unit_test_auth", Status: "passed"},
		{Name: "file_check", Status: "missing", Detail: "no file produced"},
	})
	require.NotNil(t, ev)
	assert.Equal(t, VerdictUncertain, ev.Verdict)
	assert.NotEmpty(t, ev.Flag)
}

func TestResultVerifier_EmptyChecks(t *testing.T) {
	verifier := NewResultVerifier()
	ev := verifier.Verify("task-1", "coder", nil)
	require.NotNil(t, ev)
	assert.Equal(t, VerdictUncertain, ev.Verdict)
	assert.Equal(t, 0.0, ev.Confidence)
	assert.Contains(t, ev.Flag, "no result checks")
}

func TestResultVerifier_EmptyStatusTreatedAsMissing(t *testing.T) {
	verifier := NewResultVerifier()
	ev := verifier.Verify("task-1", "coder", []ResultCheck{
		{Name: "unit_test_auth"}, // empty status
	})
	require.NotNil(t, ev)
	assert.Equal(t, VerdictUncertain, ev.Verdict)
	require.Len(t, ev.Dimensions[0].Evidence, 1)
	assert.Equal(t, "missing", ev.Dimensions[0].Evidence[0].Status)
}

func TestResultVerifier_SkippedIsUncertain(t *testing.T) {
	verifier := NewResultVerifier()
	ev := verifier.Verify("task-1", "coder", []ResultCheck{
		{Name: "integration_test", Status: "skipped"},
	})
	require.NotNil(t, ev)
	assert.Equal(t, VerdictUncertain, ev.Verdict)
}

// ── ProcessVerifier (mid-level rules) ───────────────

func TestProcessVerifier_AllAllowed(t *testing.T) {
	verifier := NewProcessVerifier()
	ev := verifier.Verify("task-1", "coder", []ProcessCheck{
		{Rule: "no_pii_leak", Allowed: true, Detail: "no PII in output"},
		{Rule: "permission_scope", Allowed: true, Detail: "within scope"},
	})
	require.NotNil(t, ev)
	assert.Equal(t, VerdictPass, ev.Verdict)
	assert.Equal(t, 1.0, ev.Confidence)
	assert.False(t, ev.HasFailure())
	assert.Equal(t, "process_verifier", ev.Source)
	require.Len(t, ev.Dimensions, 1)
	assert.Equal(t, "rule_compliance", ev.Dimensions[0].Name)
	assert.True(t, ev.Dimensions[0].Pass)
}

func TestProcessVerifier_AnyViolationFails(t *testing.T) {
	verifier := NewProcessVerifier()
	ev := verifier.Verify("task-1", "coder", []ProcessCheck{
		{Rule: "no_pii_leak", Allowed: true},
		{Rule: "action_order", Allowed: false, Detail: "tool called before auth"},
	})
	require.NotNil(t, ev)
	assert.Equal(t, VerdictFail, ev.Verdict)
	assert.True(t, ev.HasFailure())
	assert.Contains(t, ev.Flag, "violated")
	// Hard constraint: one violation fails the dimension regardless of ratio.
	assert.False(t, ev.Dimensions[0].Pass)
	assert.Equal(t, 0.5, ev.Confidence)
}

func TestProcessVerifier_EmptyChecks(t *testing.T) {
	verifier := NewProcessVerifier()
	ev := verifier.Verify("task-1", "coder", nil)
	require.NotNil(t, ev)
	assert.Equal(t, VerdictUncertain, ev.Verdict)
	assert.Equal(t, 0.0, ev.Confidence)
	assert.Contains(t, ev.Flag, "no process checks")
}

func TestProcessVerifier_EvidenceItems(t *testing.T) {
	verifier := NewProcessVerifier()
	ev := verifier.Verify("task-1", "coder", []ProcessCheck{
		{Rule: "no_pii_leak", Allowed: false, Detail: "email leaked"},
		{Rule: "permission_scope", Allowed: true},
	})
	require.NotNil(t, ev)
	require.Len(t, ev.Dimensions, 1)
	items := ev.Dimensions[0].Evidence
	require.Len(t, items, 2)
	assert.Equal(t, "failed", items[0].Status)
	assert.Equal(t, "passed", items[1].Status)
}

// TestResultVerifier_UnknownStatusFails is the #56 regression: an
// unrecognized check status used to fall through the switch and count as a
// pass, so garbage input masqueraded as a PASS. The bottom verification
// layer must prove success; an unknown status proves nothing and is treated
// as a failure (strict). No in-tree producer emits unknown statuses — the
// only statuses are the Status* constants.
func TestResultVerifier_UnknownStatusFails(t *testing.T) {
	verifier := NewResultVerifier()
	ev := verifier.Verify("task-1", "coder", []ResultCheck{
		{Name: "build", Status: StatusPassed},
		{Name: "weird", Status: "definitely-not-a-status"},
	})
	if ev.Verdict != VerdictFail {
		t.Fatalf("unknown status verdict = %v, want %v (must fail, not pass or stay uncertain)", ev.Verdict, VerdictFail)
	}
	// The raw status is preserved for auditability.
	if len(ev.Dimensions) == 0 || len(ev.Dimensions[0].Evidence) != 2 {
		t.Fatalf("expected 2 evidence items, got %+v", ev.Dimensions)
	}
	found := false
	for _, item := range ev.Dimensions[0].Evidence {
		if item.Name == "weird" && item.Status == "definitely-not-a-status" {
			found = true
		}
	}
	if !found {
		t.Error("unknown-status item must keep its raw status in the evidence")
	}
}

// TestResultVerifier_UnknownStatusAloneFails: even a single unknown check
// without any passing sibling must fail (not uncertain).
func TestResultVerifier_UnknownStatusAloneFails(t *testing.T) {
	verifier := NewResultVerifier()
	ev := verifier.Verify("task-2", "coder", []ResultCheck{
		{Name: "only", Status: "garbage"},
	})
	if ev.Verdict != VerdictFail {
		t.Fatalf("verdict = %v, want %v", ev.Verdict, VerdictFail)
	}
}
