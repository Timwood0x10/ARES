// dream_cycle_cooldown_test.go locks the REVIEW 3.4#11 fix (landed in the
// CRITICAL/MEDIUM batch): the dream-cycle cooldown (lastCycle) is set at
// deployWinner ENTRY via defer, so early-return paths — guardrail reject,
// shadow reject, deploy failure — respect it too. Previously only the
// successful-deploy path set it, letting rejected candidates re-trigger
// immediately. This test pins the guardrail-reject path.
package evolution

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDreamCycleCooldownSetOnGuardrailRejection(t *testing.T) {
	defer discardLogs()()
	scheduler := NewEvolutionScheduler(nil, nil,
		WithEnabled(true),
		WithTrigger(TriggerOnIdle),
		WithMinInterval(time.Nanosecond),
	)
	mutator := &mockMutator{
		mutateFn: func(ctx context.Context, parent Strategy, n int) ([]Strategy, error) {
			return []Strategy{{ID: "winner-cand", Name: "WinnerV1", Version: 2, ParentID: parent.ID}}, nil
		},
	}
	tester := &mockTester{
		results: map[string]*RegressionResult{
			"winner-cand": {CandidateScore: 0.85, BaselineScore: 0.60, WinRate: 0.80, TotalTasks: 50},
		},
	}

	// Baseline above the winner's win rate: PostEvolveCheckForSource fires
	// baseline_regression → ShouldStop → deployWinner returns early.
	guardrails, err := NewEvolutionGuardrails(WithBaselineScore(0.99))
	require.NoError(t, err)

	dc, err := NewDreamCycle(scheduler, mutator, tester, &mockGenealogy{},
		WithDreamCycleConfig(DreamCycleConfig{
			Enabled:              true,
			MinTasksBeforeEvolve: 1,
			MaxMutations:         3,
			MinWinRate:           0.55,
			Cooldown:             time.Hour,
		}),
		WithDreamCycleGuardrails(guardrails),
	)
	require.NoError(t, err)

	dc.taskCount = int64(dc.config.MinTasksBeforeEvolve)
	for i := 0; i < 40; i++ {
		scheduler.RecordScore(1.0)
	}
	for i := 0; i < 10; i++ {
		scheduler.RecordScore(0.0)
	}

	require.NoError(t, dc.Run(context.Background(), CallbackData{AgentID: "agent-cooldown"}))

	assert.False(t, dc.lastCycle.IsZero(),
		"a guardrail-rejected cycle must still set the cooldown — otherwise the rejected candidate re-triggers on the next task")
}
