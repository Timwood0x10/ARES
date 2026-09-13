package sdk

import (
	"context"
	"errors"
	"testing"
)

// TestWithHumanInputIsRefusedNotIgnored pins E-2: the human-in-the-loop
// approval hook must never be silently dropped.
//
// Regression (docs/reviews/0.3.1-final-deep-review.md §5.2 E-2): WithHumanInput
// stored the callback in agentConfig.humanInput, which was only ever copied
// into Agent.humanInput — no code path invoked it. Since the B3 convergence
// every run goes through the shared L2 session core, which has no per-tool-call
// approval interception point, so a caller who attached the hook to gate
// destructive tools got no gate at all while believing it was in force.
//
// The honest behaviour is to refuse the run: a hard error is recoverable, a
// missing safety gate is not.
func TestWithHumanInputIsRefusedNotIgnored(t *testing.T) {
	approver := func(context.Context, string, map[string]any) (bool, error) { return true, nil }

	cfg := defaultAgentConfig()
	WithHumanInput(approver)(cfg)
	if cfg.humanInput == nil {
		t.Fatal("WithHumanInput must still store the hook (API compatibility)")
	}

	agent := &Agent{name: "gated", humanInput: approver}

	t.Run("run", func(t *testing.T) {
		_, err := agent.Run(context.Background(), "hi")
		if !errors.Is(err, ErrHumanInputUnsupported) {
			t.Fatalf("Run with WithHumanInput must fail loudly, got %v", err)
		}
	})

	t.Run("stream", func(t *testing.T) {
		ch, err := agent.Stream(context.Background(), "hi")
		if ch != nil {
			t.Fatal("Stream must not hand back a channel when the approval gate cannot be honoured")
		}
		if !errors.Is(err, ErrHumanInputUnsupported) {
			t.Fatalf("Stream with WithHumanInput must fail loudly, got %v", err)
		}
	})
}

// TestRunWithoutHumanInputIsNotBlocked locks the guard's scope: an agent built
// without the option must reach its ordinary wiring checks, not the
// unsupported-gate error.
func TestRunWithoutHumanInputIsNotBlocked(t *testing.T) {
	agent := &Agent{name: "plain"}

	_, err := agent.Run(context.Background(), "hi")
	if err == nil {
		t.Fatal("an un-wired runtime must still error")
	}
	if errors.Is(err, ErrHumanInputUnsupported) {
		t.Fatal("the human-input guard must not fire when the option was never set")
	}
}
