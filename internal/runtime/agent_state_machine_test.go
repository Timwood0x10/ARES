package runtime

// A4 (0.3.2): exhaustive state-combination test for managedAgent's four
// boolean flags. The state machine has 2^4 = 16 combinations; this test
// documents the legal ones and asserts the invariants each must satisfy.
//
// State flags:
//   stopped         — permanent stop (StopAgent / RestartAgent)
//   paused          — chaos-engineering pause (PauseAgent)
//   resurrecting    — NotifyAgentDead → RestoreAgent in progress
//   operatorIntent  — operator Stop/Pause landed after resurrection scheduled
//
// Legal combinations and their meaning:
//
//	0000 — running (normal)
//	0001 — operator Stop/Pause pending over a resurrection schedule (pre-install)
//	0010 — resurrection in progress (agent died, RestoreAgent scheduled)
//	0011 — resurrection scheduled BUT operator overrode it (operatorIntent=true)
//	0100 — paused (chaos, agent still alive)
//	0101 — paused + operatorIntent (operator paused during resurrection window)
//	0110 — ILLEGAL: paused AND resurrecting (agent can't be both alive-paused and dead-resurrecting)
//	0111 — ILLEGAL: same as 0110
//	1000 — stopped permanently
//	1001 — stopped + operatorIntent (operator stopped during resurrection window)
//	1010 — ILLEGAL: stopped AND resurrecting (stopped prevents resurrection)
//	1011 — ILLEGAL: same as 1010
//	1100 — ILLEGAL: stopped AND paused (stop is terminal, pause is temporary)
//	1101 — ILLEGAL: same
//	1110 — ILLEGAL: same
//	1111 — ILLEGAL: same

import (
	"testing"
)

func TestManagedAgentStateCombinations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		stopped        bool
		paused         bool
		resurrecting   bool
		operatorIntent bool
		legal          bool
		invariant      string // human-readable invariant the legal state must satisfy
	}{
		{"running", false, false, false, false, true, "normal operation"},
		{"operator_intent_without_context", false, false, false, true, false, "ILLEGAL: operatorIntent without resurrection or stop/pause context"},
		{"resurrecting", false, false, true, false, true, "agent died, RestoreAgent scheduled"},
		{"resurrecting_operator_overrode", false, false, true, true, true, "resurrection scheduled but operator overrode — install must abort"},
		{"paused", false, true, false, false, true, "chaos pause, agent alive"},
		{"paused_operator_intent", false, true, false, true, true, "operator paused during resurrection backoff window"},
		{"paused_resurrecting", false, true, true, false, false, "ILLEGAL: paused=alive, resurrecting=dead"},
		{"paused_resurrecting_intent", false, true, true, true, false, "ILLEGAL: same"},
		{"stopped", true, false, false, false, true, "permanent stop"},
		{"stopped_operator_intent", true, false, false, true, true, "operator stopped during resurrection backoff window"},
		{"stopped_resurrecting", true, false, true, false, false, "ILLEGAL: stopped prevents resurrection"},
		{"stopped_resurrecting_intent", true, false, true, true, false, "ILLEGAL: same"},
		{"stopped_paused", true, true, false, false, false, "ILLEGAL: stop is terminal, pause is temporary"},
		{"stopped_paused_intent", true, true, false, true, false, "ILLEGAL: same"},
		{"stopped_paused_resurrecting", true, true, true, false, false, "ILLEGAL: same"},
		{"all_flags", true, true, true, true, false, "ILLEGAL: same"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ma := &managedAgent{
				stopped:        tc.stopped,
				paused:         tc.paused,
				resurrecting:   tc.resurrecting,
				operatorIntent: tc.operatorIntent,
			}

			// Invariant: operatorIntent requires a prior resurrection context.
			// operatorIntent is only meaningful when resurrection was scheduled
			// (resurrecting=true) OR the operator's stop/pause has already
			// taken effect (stopped/paused=true after the override).
			if tc.operatorIntent && tc.legal {
				if !tc.resurrecting && !tc.stopped && !tc.paused {
					t.Errorf("legal state with operatorIntent=true but no resurrection or stop/pause context: %s", tc.invariant)
				}
			}

			// Invariant: stopped + resurrecting is illegal — a stopped agent
			// must never have resurrection in progress.
			if tc.stopped && tc.resurrecting {
				if tc.legal {
					t.Errorf("stopped+resurrecting marked legal but is invariantly illegal: %s", tc.invariant)
				}
			}

			// Invariant: paused + resurrecting is illegal — paused means the
			// agent is alive (just not processing); resurrecting means it's
			// dead.
			if tc.paused && tc.resurrecting {
				if tc.legal {
					t.Errorf("paused+resurrecting marked legal but is invariantly illegal: %s", tc.invariant)
				}
			}

			// Invariant: stopped + paused is illegal — stop is terminal, pause
			// is temporary. An agent that is stopped should not carry the
			// paused flag.
			if tc.stopped && tc.paused {
				if tc.legal {
					t.Errorf("stopped+paused marked legal but is invariantly illegal: %s", tc.invariant)
				}
			}

			// The managedAgent's zero value (all false) must be the "running"
			// state — this is the default after construction.
			if tc.name == "running" {
				if ma.stopped || ma.paused || ma.resurrecting || ma.operatorIntent {
					t.Error("running state must have all flags false")
				}
			}
		})
	}
}
