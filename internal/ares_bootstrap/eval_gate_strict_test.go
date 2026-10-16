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
		_, err := buildEvalGate(nil, stubLLMClient{}, evalGateConfig{SuitePath: writeEvalSuite(t, evalSuiteYAML)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "armed")
		assert.Contains(t, err.Error(), "infrastructure is missing")
	})

	t.Run("no_client", func(t *testing.T) {
		_, err := buildEvalGate(&eval.EvaluatorRegistry{}, nil, evalGateConfig{SuitePath: writeEvalSuite(t, evalSuiteYAML)})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "infrastructure is missing")
	})
}

// TestBuildEvalGate_DefaultedSuiteDegradesInsteadOfFailingBootstrap is the contract
// that makes defaulting eval_suite safe at all.
//
// An EXPLICITLY configured suite keeps the fail-closed behaviour above: an operator who
// armed a gate must not get a silently weaker pipeline from a typo or an unset API key.
// A DEFAULTED suite is the opposite case — nobody asked for it — so a missing piece
// (infrastructure, or the repository file a deployed binary does not have) must degrade
// to "no G3" with a Warning. Without this, shipping a default would turn "no eval LLM
// key" into a boot failure for every deployment.
func TestBuildEvalGate_DefaultedSuiteDegradesInsteadOfFailingBootstrap(t *testing.T) {
	path := writeEvalSuite(t, evalSuiteYAML)

	t.Run("defaulted suite without infrastructure degrades", func(t *testing.T) {
		_, err := buildEvalGate(nil, nil, evalGateConfig{SuitePath: path, SuiteDefaulted: true})
		assert.ErrorIs(t, err, errEvalGateNotConfigured, "a defaulted suite must degrade, not fail bootstrap")
	})

	t.Run("defaulted suite with a missing file degrades", func(t *testing.T) {
		_, err := buildEvalGate(eval.NewEvaluatorRegistry(), stubLLMClient{},
			evalGateConfig{SuitePath: filepath.Join(t.TempDir(), "absent.yaml"), SuiteDefaulted: true})
		assert.ErrorIs(t, err, errEvalGateNotConfigured, "a default path the deployment lacks must not brick startup")
	})

	t.Run("explicitly configured suite still fails closed", func(t *testing.T) {
		_, err := buildEvalGate(eval.NewEvaluatorRegistry(), stubLLMClient{},
			evalGateConfig{SuitePath: filepath.Join(t.TempDir(), "absent.yaml")})
		require.Error(t, err)
		assert.NotErrorIs(t, err, errEvalGateNotConfigured, "a typo in eval_suite must be loud")
	})
}

// TestResolveEvalSuite pins how the default is chosen, including the case that makes it
// safe: the default is only used when the file is actually THERE.
func TestResolveEvalSuite(t *testing.T) {
	// An explicit value wins even when it does not exist: a typo must fail loudly
	// downstream rather than be silently swapped for the default.
	if path, defaulted := resolveEvalSuite("config/suites/mine.yaml"); path != "config/suites/mine.yaml" || defaulted {
		t.Errorf("explicit suite = (%q, %v), want it used verbatim", path, defaulted)
	}

	// This package's working directory has no test/eval corpus: honest absence.
	if path, defaulted := resolveEvalSuite(""); path != "" || defaulted {
		t.Errorf("absent default = (%q, %v), want no suite", path, defaulted)
	}

	// A working directory that DOES have the default uses it, flagged as defaulted.
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, filepath.Dir(defaultEvalSuitePath)), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, defaultEvalSuitePath), []byte(evalSuiteYAML), 0o600))
	previous, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() {
		// Registered after t.TempDir's own cleanup, so it runs first and the directory
		// is still there when we leave it.
		require.NoError(t, os.Chdir(previous))
	})

	path, defaulted := resolveEvalSuite("")
	if path != defaultEvalSuitePath || !defaulted {
		t.Errorf("resolved default = (%q, %v), want (%q, true)", path, defaulted, defaultEvalSuitePath)
	}
}

// A built gate is ALWAYS runtime-strict (M-G1): eval_strict=false must NOT
// relax a built gate — the knob governs only whether a MISSING suite is
// fatal. Also pins the absence arm of eval_strict.
func TestBuildEvalGateStrictModeDefaultOn(t *testing.T) {
	path := writeEvalSuite(t, evalSuiteYAML)
	registry := eval.NewEvaluatorRegistry()

	gate, err := buildEvalGate(registry, stubLLMClient{}, evalGateConfig{SuitePath: path})
	require.NoError(t, err)
	require.NotNil(t, gate)
	assert.True(t, gate.StrictModeEnabled(), "M-G1 default: strict even with eval_strict=false (the knob only governs absence)")

	// The absence-fatal arm of eval_strict.
	_, err = buildEvalGate(registry, stubLLMClient{}, evalGateConfig{Strict: true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no eval_suite")

	// No suite + no strict = honest absence sentinel.
	_, err = buildEvalGate(registry, stubLLMClient{}, evalGateConfig{})
	assert.ErrorIs(t, err, errEvalGateNotConfigured)
}
