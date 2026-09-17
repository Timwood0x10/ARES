package evolution

// failClosedCode is the guardrail event code for the fail-closed state.
const failClosedCode GuardrailErrorCode = "G1_CONSTRUCTION_FAILED"

// NewFailClosedGuardrails creates a guardrail gate that always blocks. Used
// when the real guardrail construction fails so the system fails
// closed instead of silently allowing all candidates.
//
// The returned guardrail carries failClosed=true, which makes every
// PreEvolveCheck / PostEvolveCheck / ValidateToolSet return
// ShouldStop=true with the G1_CONSTRUCTION_FAILED event — immediately, with
// no dependence on population signals. Configuring aggressive thresholds
// (the previous approach) only blocked AFTER stagnation accumulated, so a
// healthy-looking first cycle passed: fail-open, not fail-closed.
func NewFailClosedGuardrails() *EvolutionGuardrails {
	g := &EvolutionGuardrails{
		BaselineScore:          0,
		MaxStagnantGenerations: 1, // blocks after gen 1 with no improvement
		MaxLineageShare:        0.8,
		MaxEvents:              1000,
		bestBySource:           make(map[string]float64),
		failClosed:             true,
	}
	return g
}

// ErrCodeG1ConstructionFailed is the guardrail event code emitted when
// guardrail construction fails and the system enters fail-closed mode.
//
// This is exported so the bootstrap layer and metrics can reference it
// consistently.
func ErrCodeG1ConstructionFailed() GuardrailErrorCode {
	return failClosedCode
}
