package ares_bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/runtime/eval"
)

// The M-G1 gate-chain default-strength contract: an ARMED gate never
// silently degrades, and a built gate is runtime-strict by default.

// stubLLMClient satisfies the eval LLM client shape.
type stubLLMClient struct{}

func (stubLLMClient) Generate(ctx context.Context, prompt string) (string, error) {
	return "0.5", nil
}

func writeEvalSuite(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "suite.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

const evalSuiteYAML = `
name: preserved
test_cases:
  - id: c1
    input: "Summarize the report."
`

// ARMED (suite configured) but infrastructure missing must FAIL bootstrap —
// the pre-M-G behavior silently ran without G3 when strict=false.
func TestBuildEvalGateArmedWithoutInfraFailsBootstrap(t *testing.T) {
	t.Run("no_registry", func(t *testing.T) {
		_, err := buildEvalGate(nil, stubLLMClient{}, writeEvalSuite(t, evalSuiteYAML), 0, false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "armed")
		assert.Contains(t, err.Error(), "infrastructure is missing")
	})

	t.Run("no_client", func(t *testing.T) {
		_, err := buildEvalGate(&eval.EvaluatorRegistry{}, nil, writeEvalSuite(t, evalSuiteYAML), 0, false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "infrastructure is missing")
	})
}

// A built gate defaults to runtime StrictMode=true; eval_strict=false is the
// explicit escape hatch that turns it off.
func TestBuildEvalGateStrictModeDefaultOn(t *testing.T) {
	path := writeEvalSuite(t, evalSuiteYAML)
	registry := eval.NewEvaluatorRegistry()

	gate, err := buildEvalGate(registry, stubLLMClient{}, path, 0, false)
	require.NoError(t, err)
	require.NotNil(t, gate)
	assert.True(t, gate.StrictModeEnabled(), "M-G1 default: strict even with eval_strict=false (the knob only governs absence)")

	// The absence-fatal arm of eval_strict.
	_, err = buildEvalGate(registry, stubLLMClient{}, "", 0, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no eval_suite")

	// No suite + no strict = honest absence sentinel.
	_, err = buildEvalGate(registry, stubLLMClient{}, "", 0, false)
	assert.ErrorIs(t, err, errEvalGateNotConfigured)
}
