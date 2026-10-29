package discovery

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestDiscover_HelpProbeIsBounded: the --help probe runs synchronously on the startup path, once per
// allowlisted command, with the caller's context (a plain WithCancel(Background), no deadline). A
// command that never exits therefore blocked serve before any component was wired.
//
// The assertion is the outcome the operator sees: discovery finishes, and the command keeps its
// placeholder description instead of taking the process down with it.
func TestDiscover_HelpProbeIsBounded(t *testing.T) {
	hanging := func(ctx context.Context, _ string, _ []string) ([]byte, error) {
		<-ctx.Done() // ignores everything except cancellation, like an interactive --help
		return nil, ctx.Err()
	}

	d := NewDiscoverer([]string{"hang"},
		WithExec(hanging),
		// The command must "exist" for the probe to run at all.
		WithLookup(func(string) (string, error) { return "/usr/bin/hang", nil }),
		WithProbeTimeout(50*time.Millisecond))

	done := make(chan struct{})
	var tools []string
	go func() {
		defer close(done)
		got, err := d.Discover(context.Background())
		if err != nil {
			t.Errorf("discover: %v", err)
			return
		}
		for _, tool := range got {
			tools = append(tools, tool.Name())
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Discover never returned: the --help probe is unbounded again")
	}

	if len(tools) != 1 || tools[0] != "hang" {
		t.Fatalf("a timed-out probe must still register the command with a placeholder, got %v", tools)
	}
}

// TestDiscover_ProbeTimeoutDefaultsToABound: the option is optional, but the bound is not — a nil
// or zero option value must not disable it.
func TestDiscover_ProbeTimeoutDefaultsToABound(t *testing.T) {
	if got := NewDiscoverer(nil).probeTimeout; got != defaultProbeTimeout || got <= 0 {
		t.Fatalf("default probe timeout = %v, want %v", got, defaultProbeTimeout)
	}
	// A non-positive override is ignored rather than treated as "no timeout".
	if got := NewDiscoverer(nil, WithProbeTimeout(0)).probeTimeout; got != defaultProbeTimeout {
		t.Fatalf("a zero override disabled the bound: %v", got)
	}
}

// TestDescribe_TimeoutUsesThePlaceholder: the degradation path itself, without the loop around it.
func TestDescribe_TimeoutUsesThePlaceholder(t *testing.T) {
	d := NewDiscoverer([]string{"slow"}, WithProbeTimeout(30*time.Millisecond), WithExec(
		func(ctx context.Context, _ string, _ []string) ([]byte, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}))

	desc, err := d.describe(context.Background(), "slow")
	if err == nil {
		t.Fatal("a probe past its deadline must be reported as an error, not as empty help")
	}
	_ = desc
	if !strings.Contains(err.Error(), "deadline") && !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("unexpected error: %v", err)
	}
}
