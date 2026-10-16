package agentsyscall

import (
	"context"
	"testing"
	"time"
)

// yieldingKernel builds a Kernel whose collaboration primitive always yields (the
// production shape: serve launches the request in the background).
func yieldingKernel(t *testing.T) *Kernel {
	t.Helper()
	return NewKernel(nil, nil, nil, nil, WithAskAgent(
		func(context.Context, string, string, string, any) (any, error) {
			return nil, ErrAskAgentYielding
		}))
}

// TestAskAgent_DuplicateCarriesTheWaitSchedule is item ④'s readout: when a re-ask is
// suppressed, the caller is told when the outstanding one was launched and when
// suppression lifts, so it can wait deliberately instead of re-asking blind. The
// deadline is DERIVED (launch + window) precisely so the caller does not need to know
// the deployment's timeout.
func TestAskAgent_DuplicateCarriesTheWaitSchedule(t *testing.T) {
	kernel := yieldingKernel(t)
	ctx := context.Background()
	ask := AskAgentArgs{To: "agent-b", Topic: "delegate", Payload: map[string]any{"q": "status?"}}

	first, err := kernel.AskAgent(ctx, ask)
	if err != nil {
		t.Fatalf("first ask: %v", err)
	}
	if first.Duplicate {
		t.Fatal("the first ask cannot be a duplicate")
	}
	// The pointers are set only for a suppressed ask: a zero time would otherwise
	// serialize as year 1 and read like a real launch.
	if first.PendingSince != nil || first.PendingUntil != nil {
		t.Errorf("a non-duplicate ask must not carry a schedule: since=%v until=%v", first.PendingSince, first.PendingUntil)
	}

	second, err := kernel.AskAgent(ctx, ask)
	if err != nil {
		t.Fatalf("second ask: %v", err)
	}
	if !second.Duplicate {
		t.Fatal("an identical ask inside the window must be suppressed")
	}
	if second.PendingSince == nil || second.PendingUntil == nil {
		t.Fatalf("a suppressed ask must carry the wait schedule: %+v", second)
	}
	if got := second.PendingUntil.Sub(*second.PendingSince); got != defaultAskDedupeWindow {
		t.Errorf("window = %v, want the default %v", got, defaultAskDedupeWindow)
	}
	if !second.PendingUntil.After(time.Now().Add(-time.Second)) {
		t.Errorf("the deadline must be in the future, got %v", second.PendingUntil)
	}
}

// TestSetAskDedupeWindow_ControlsSuppression is the single-source contract: production
// injects the collaboration timeout it actually uses, and this is what makes that
// injection observable rather than decorative. A window short enough to have elapsed
// must NOT suppress (a stale window would swallow a legitimate second ask).
func TestSetAskDedupeWindow_ControlsSuppression(t *testing.T) {
	kernel := yieldingKernel(t)
	ctx := context.Background()
	ask := AskAgentArgs{To: "agent-b", Topic: "delegate", Payload: map[string]any{"q": "status?"}}

	kernel.SetAskDedupeWindow(time.Nanosecond)
	if _, err := kernel.AskAgent(ctx, ask); err != nil {
		t.Fatalf("first ask: %v", err)
	}
	// The window has certainly elapsed by the time the second call runs, so suppression
	// must not fire — which is only true if the injected value is actually read.
	second, err := kernel.AskAgent(ctx, ask)
	if err != nil {
		t.Fatalf("second ask: %v", err)
	}
	if second.Duplicate {
		t.Error("an elapsed window must not suppress: the injected value was ignored")
	}

	// A long window suppresses, and the reported deadline uses the INJECTED value.
	kernel.SetAskDedupeWindow(time.Hour)
	if _, err := kernel.AskAgent(ctx, ask); err != nil {
		t.Fatalf("third ask: %v", err)
	}
	fourth, err := kernel.AskAgent(ctx, ask)
	if err != nil {
		t.Fatalf("fourth ask: %v", err)
	}
	if !fourth.Duplicate {
		t.Fatal("a one-hour window must suppress an immediate repeat")
	}
	if got := fourth.PendingUntil.Sub(*fourth.PendingSince); got != time.Hour {
		t.Errorf("reported window = %v, want the injected hour", got)
	}
}

// TestSetAskDedupeWindow_NonPositiveRestoresTheDefault: a misconfigured zero must not
// silently re-enable the retry amplification this window exists to stop.
func TestSetAskDedupeWindow_NonPositiveRestoresTheDefault(t *testing.T) {
	kernel := yieldingKernel(t)
	ctx := context.Background()
	ask := AskAgentArgs{To: "agent-b", Topic: "delegate", Payload: map[string]any{"q": "status?"}}

	kernel.SetAskDedupeWindow(0)
	if _, err := kernel.AskAgent(ctx, ask); err != nil {
		t.Fatalf("first ask: %v", err)
	}
	second, err := kernel.AskAgent(ctx, ask)
	if err != nil {
		t.Fatalf("second ask: %v", err)
	}
	if !second.Duplicate {
		t.Error("a zero window must fall back to the default, not disable suppression")
	}
	if got := second.PendingUntil.Sub(*second.PendingSince); got != defaultAskDedupeWindow {
		t.Errorf("window = %v, want the default %v", got, defaultAskDedupeWindow)
	}
}
