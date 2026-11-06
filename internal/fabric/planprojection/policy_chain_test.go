package planprojection

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
)

// TestProjectStep_CarriesTheL1Policy closes the chain the L1 configuration depends on.
//
// The catalog says max_attempts/task_timeout, the planner writes them onto the L2 Step, and this
// projection is the ONLY place they can reach the fabric. Each link has its own test; this one
// asserts the join, because a break here would look exactly like "the configuration was ignored".
func TestProjectStep_CarriesTheL1Policy(t *testing.T) {
	step := &engine.Step{
		ID:        "s1",
		AgentType: "tool/rg",
		Timeout:   30 * time.Second,
		// Two retries = three attempts, which is what the operator wrote as max_attempts: 3.
		RetryPolicy: &engine.RetryPolicy{MaxAttempts: 2, InitialDelay: 250 * time.Millisecond},
	}

	plan := ProjectStep(step)

	assert.Equal(t, 2, plan.MaxRetries, "retries reach the fabric unchanged")
	assert.Equal(t, 250*time.Millisecond, plan.BackoffBase)
	assert.Equal(t, 30*time.Second, plan.Deadline,
		"the timeout must travel as a RELATIVE budget; CompilePlan resolves it to the task deadline")
}

// TestProjectStep_NoPolicyKeepsTheDefault: a tool the operator did not configure must project the
// same way it did before this feature existed — no retry policy, no deadline.
func TestProjectStep_NoPolicyKeepsTheDefault(t *testing.T) {
	plan := ProjectStep(&engine.Step{ID: "s1", AgentType: "tool/git"})

	assert.Zero(t, plan.MaxRetries)
	assert.Zero(t, plan.Deadline)
}
