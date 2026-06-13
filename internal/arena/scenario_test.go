package arena

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goagentx/internal/runtime"
)

func TestRunScenario_Success(t *testing.T) {
	rt := &mockRuntime{
		listAgentsFn: func() []runtime.AgentInfo {
			return []runtime.AgentInfo{{ID: "leader-1", Type: "leader"}}
		},
	}
	inj := NewInjector(rt, nil)
	svc := NewService(inj, nil)

	scenario := Scenario{
		Name: "test_scenario",
		Actions: []ScheduledAction{
			{
				Delay: 0,
				Action: Action{
					ID:       "sc-1",
					Type:     ActionKillAgent,
					TargetID: "agent-1",
				},
			},
			{
				Delay: 10 * time.Millisecond,
				Action: Action{
					ID:   "sc-2",
					Type: ActionKillLeader,
				},
			},
		},
	}

	results, err := RunScenario(context.Background(), svc, scenario)
	require.NoError(t, err)
	assert.Len(t, results, 2)
	assert.True(t, results[0].Success)
	assert.True(t, results[1].Success)
}

func TestRunScenario_NilService(t *testing.T) {
	scenario := Scenario{Name: "test"}
	_, err := RunScenario(context.Background(), nil, scenario)
	assert.Error(t, err)
	assert.ErrorContains(t, err, "service is nil")
}

func TestRunScenario_EmptyName(t *testing.T) {
	svc := newTestService(nil, nil, nil)
	scenario := Scenario{}
	_, err := RunScenario(context.Background(), svc, scenario)
	assert.Error(t, err)
	assert.ErrorContains(t, err, "name is empty")
}

func TestRunScenario_CancelledContext(t *testing.T) {
	rt := &mockRuntime{}
	svc := newTestService(rt, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	scenario := Scenario{
		Name: "cancelled",
		Actions: []ScheduledAction{
			{
				Delay: 1 * time.Second,
				Action: Action{
					ID:       "sc-1",
					Type:     ActionKillAgent,
					TargetID: "agent-1",
				},
			},
		},
	}

	results, err := RunScenario(ctx, svc, scenario)
	assert.ErrorIs(t, err, context.Canceled)
	// No results should have been executed.
	assert.Empty(t, results)
}

func TestRunScenario_InvalidAction(t *testing.T) {
	svc := newTestService(nil, nil, nil)

	scenario := Scenario{
		Name: "invalid",
		Actions: []ScheduledAction{
			{
				Action: Action{
					ID:   "sc-bad",
					Type: ActionKillAgent,
					// Missing TargetID.
				},
			},
		},
	}

	results, err := RunScenario(context.Background(), svc, scenario)
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.False(t, results[0].Success)
	assert.Contains(t, results[0].Error, "target_id")
}

func TestRunScenario_MixedResults(t *testing.T) {
	rt := &mockRuntime{
		stopAgentFn: func(_ context.Context, id string) error {
			if id == "fail-agent" {
				return errors.New("agent crashed")
			}
			return nil
		},
	}
	svc := newTestService(rt, nil, nil)

	scenario := Scenario{
		Name: "mixed",
		Actions: []ScheduledAction{
			{
				Action: Action{
					ID: "sc-ok", Type: ActionKillAgent, TargetID: "ok-agent",
				},
			},
			{
				Action: Action{
					ID: "sc-fail", Type: ActionKillAgent, TargetID: "fail-agent",
				},
			},
		},
	}

	results, err := RunScenario(context.Background(), svc, scenario)
	require.NoError(t, err)
	assert.Len(t, results, 2)
	assert.True(t, results[0].Success)
	assert.False(t, results[1].Success)
}

func TestRunScenario_EmptyActions(t *testing.T) {
	svc := newTestService(nil, nil, nil)

	scenario := Scenario{
		Name:    "empty",
		Actions: nil,
	}

	results, err := RunScenario(context.Background(), svc, scenario)
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestRunScenario_WithDAGActions(t *testing.T) {
	dag := &mockDAG{}
	svc := newTestService(nil, dag, nil)

	scenario := Scenario{
		Name: "dag_chaos",
		Actions: []ScheduledAction{
			{
				Action: Action{
					ID: "sc-node", Type: ActionRemoveNode, TargetID: "node-1",
				},
			},
			{
				Action: Action{
					ID: "sc-edge", Type: ActionRemoveEdge, SourceID: "a", TargetID: "b",
				},
			},
		},
	}

	results, err := RunScenario(context.Background(), svc, scenario)
	require.NoError(t, err)
	assert.Len(t, results, 2)
	assert.True(t, results[0].Success)
	assert.True(t, results[1].Success)
	assert.Equal(t, []string{"node-1"}, dag.getRemovedNodes())
	assert.Len(t, dag.getRemovedEdges(), 1)
}
