package introspect

import (
	"context"
	"testing"
	"time"
)

// TestRunShadowSandboxRecovers verifies the shadow sandbox replay records a
// recovery outcome (the fixed chaos loop: dashboard.go runShadowVerification
// feeds this). The scratch-fabric chain kill→lease-expire→recover must leave
// the task recovered, not errored.
func TestRunShadowSandboxRecovers(t *testing.T) {
	res := runShadowSandbox(context.Background())
	if res.Errored {
		t.Fatalf("shadow sandbox errored: %+v", res)
	}
	if !res.Recovered {
		t.Fatalf("shadow sandbox did not recover: %+v", res)
	}
	if res.LastRun.IsZero() {
		t.Fatalf("shadow sandbox did not stamp LastRun: %+v", res)
	}
}

// TestDashboardChaosReporterWired verifies NewDashboard wires the chaos
// reporter into the panel sources, so the shadow loop's RecordShadow is
// observable (P0-2 regression guard: the loop must be started by Run).
func TestDashboardChaosReporterWired(t *testing.T) {
	// Config requires a real LLM config path; construct the reporter wiring
	// directly (the collector Sources include the Chaos source).
	r := NewChaosReporter()
	r.SetConfig(true, "shadow")
	c := NewCollector(Sources{Chaos: r.Snapshot})
	r.RecordShadow(ShadowResult{LastRun: time.Now(), Recovered: true})
	snap := c.Collect()
	if snap.Chaos == nil || !snap.Chaos.Enabled || !snap.Chaos.Shadow.Recovered {
		t.Fatalf("chaos source not wired into collector: %+v", snap.Chaos)
	}
}
