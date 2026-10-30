package agentfabric

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
	resources "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// TestAddToolNodeWithPolicy_TranslatesMaxAttempts pins the one arithmetic conversion in this path:
// configuration counts ATTEMPTS (the first execution included), while the engine's field is the
// number of RETRIES that may follow it. Getting this wrong by one is invisible until a tool is
// retried once more (or once fewer) than the operator declared.
func TestAddToolNodeWithPolicy_TranslatesMaxAttempts(t *testing.T) {
	cases := []struct {
		name        string
		policy      StepPolicy
		wantRetries *int
		wantTimeout time.Duration
	}{
		{"undeclared leaves the default", StepPolicy{}, nil, 0},
		{"one attempt means no retry", StepPolicy{MaxAttempts: 1}, intPtr(0), 0},
		{"three attempts means two retries", StepPolicy{MaxAttempts: 3}, intPtr(2), 0},
		{"timeout is carried as the step budget", StepPolicy{MaxAttempts: 2, Timeout: 30 * time.Second}, intPtr(1), 30 * time.Second},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, err := NewL2Graph("root", "prompt", nil)
			require.NoError(t, err)

			require.NoError(t, g.AddToolNodeWithPolicy(context.Background(), "n1", "rg", map[string]any{}, "root", tc.policy))

			step := stepByID(t, g, "n1")
			if tc.wantRetries == nil {
				assert.Nil(t, step.RetryPolicy, "an undeclared policy must not invent one")
			} else {
				require.NotNil(t, step.RetryPolicy)
				assert.Equal(t, *tc.wantRetries, step.RetryPolicy.MaxAttempts)
			}
			assert.Equal(t, tc.wantTimeout, step.Timeout)
		})
	}
}

// TestPlannerToolStepPolicy_ReadsOnlyDeclaredValues: the planner treats a missing, empty or
// out-of-range field as "not declared" rather than substituting a guess. Config.Validate rejects
// those at startup; guessing here would hide the fact that it did not run.
func TestPlannerToolStepPolicy_ReadsOnlyDeclaredValues(t *testing.T) {
	schema := resources.ToolSchema{Name: "rg"}
	binder := &tagBinder{schemas: []resources.ToolSchema{schema}}
	goodID := resources.ToolClassID("rg", resources.ToolArgShape(schema))

	badSchema := resources.ToolSchema{Name: "bad"}
	badID := resources.ToolClassID("bad", resources.ToolArgShape(badSchema))

	dag, err := engine.NewMutableDAG([]*engine.Step{
		{
			ID:        goodID,
			Name:      "rg",
			AgentType: "tool/rg",
			Metadata: map[string]string{
				l1MetaMaxAttempts: "2",
				l1MetaTaskTimeout: "45s",
			},
		},
		{
			ID:        badID,
			Name:      "bad",
			AgentType: "tool/bad",
			Metadata: map[string]string{
				l1MetaMaxAttempts: "0",
				l1MetaTaskTimeout: "not-a-duration",
			},
		},
	})
	require.NoError(t, err)

	binder.schemas = []resources.ToolSchema{schema, badSchema}
	c := &plannerCognition{binder: binder, l1: dag}

	policy := c.toolStepPolicy("rg")
	assert.Equal(t, 2, policy.MaxAttempts)
	assert.Equal(t, 45*time.Second, policy.Timeout)

	undecided := c.toolStepPolicy("bad")
	assert.Zero(t, undecided.MaxAttempts, "0 is out of range and must not be forwarded")
	assert.Zero(t, undecided.Timeout, "an unparseable duration must not become a guess")

	assert.Equal(t, StepPolicy{}, c.toolStepPolicy("unknown-tool"))
}

// stepByID finds one step in the L2 graph's DAG.
func stepByID(t *testing.T, g *L2Graph, id string) *engine.Step {
	t.Helper()
	for _, s := range g.DAG().Steps() {
		if s != nil && s.ID == id {
			return s
		}
	}
	t.Fatalf("step %q not found", id)
	return nil
}

func intPtr(n int) *int { return &n }
