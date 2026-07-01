package core

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------- WorkflowStatus ----------

func TestWorkflowStatusValues(t *testing.T) {
	tests := []struct {
		status WorkflowStatus
		want   string
	}{
		{WorkflowStatusPending, "pending"},
		{WorkflowStatusRunning, "running"},
		{WorkflowStatusCompleted, "completed"},
		{WorkflowStatusFailed, "failed"},
		{WorkflowStatusCancelled, "cancelled"},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			assert.Equal(t, tt.want, string(tt.status))
		})
	}
}

func TestWorkflowStatusUniqueness(t *testing.T) {
	statuses := map[string]bool{
		string(WorkflowStatusPending):   true,
		string(WorkflowStatusRunning):   true,
		string(WorkflowStatusCompleted): true,
		string(WorkflowStatusFailed):    true,
		string(WorkflowStatusCancelled): true,
	}
	assert.Len(t, statuses, 5)
}

// ---------- WorkflowEventType ----------

func TestWorkflowEventTypeValues(t *testing.T) {
	tests := []struct {
		typ  WorkflowEventType
		want int
	}{
		{WorkflowEventStarted, 0},
		{WorkflowEventStepStarted, 1},
		{WorkflowEventStepCompleted, 2},
		{WorkflowEventStepFailed, 3},
		{WorkflowEventCompleted, 4},
		{WorkflowEventFailed, 5},
	}
	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			assert.Equal(t, tt.want, int(tt.typ))
		})
	}
}

// ---------- Workflow JSON Round-Trips ----------

func TestWorkflowRequestJSONRoundTrip(t *testing.T) {
	in := WorkflowRequest{
		WorkflowID: "wf-1",
		Input:      "hello",
		Variables:  map[string]string{"key": "val"},
		Timeout:    30 * time.Second,
	}
	data, err := json.Marshal(in)
	require.NoError(t, err)
	var out WorkflowRequest
	err = json.Unmarshal(data, &out)
	require.NoError(t, err)
	assert.Equal(t, in.WorkflowID, out.WorkflowID)
	assert.Equal(t, in.Input, out.Input)
	assert.Equal(t, in.Variables, out.Variables)
	assert.Equal(t, in.Timeout, out.Timeout)
}

func TestWorkflowRequestZeroValue(t *testing.T) {
	var req WorkflowRequest
	assert.Empty(t, req.WorkflowID)
	assert.Empty(t, req.Input)
	assert.Nil(t, req.Variables)
	assert.Zero(t, req.Timeout)
	// JSON round-trip
	data, err := json.Marshal(req)
	require.NoError(t, err)
	var out WorkflowRequest
	err = json.Unmarshal(data, &out)
	require.NoError(t, err)
	assert.Equal(t, req, out)
}

func TestWorkflowResponseJSONRoundTrip(t *testing.T) {
	in := WorkflowResponse{
		ExecutionID: "exec-1",
		WorkflowID:  "wf-1",
		Status:      WorkflowStatusCompleted,
		Output:      map[string]interface{}{"result": "ok"},
		Steps: []*StepResult{
			{StepID: "step-1", Name: "Step 1", Status: WorkflowStatusCompleted, Output: "done"},
		},
		Error:    "",
		Duration: 5 * time.Second,
	}
	data, err := json.Marshal(in)
	require.NoError(t, err)
	var out WorkflowResponse
	err = json.Unmarshal(data, &out)
	require.NoError(t, err)
	assert.Equal(t, in.ExecutionID, out.ExecutionID)
	assert.Equal(t, in.WorkflowID, out.WorkflowID)
	assert.Equal(t, in.Status, out.Status)
	assert.Equal(t, in.Error, out.Error)
	assert.Equal(t, in.Duration, out.Duration)
	require.Len(t, out.Steps, 1)
	assert.Equal(t, "step-1", out.Steps[0].StepID)
}

func TestWorkflowResponseZeroValue(t *testing.T) {
	var resp WorkflowResponse
	assert.Empty(t, resp.ExecutionID)
	assert.Empty(t, resp.WorkflowID)
	assert.Equal(t, WorkflowStatus(""), resp.Status)
	assert.Nil(t, resp.Output)
	assert.Nil(t, resp.Steps)
	assert.Zero(t, resp.Duration)
	data, err := json.Marshal(resp)
	require.NoError(t, err)
	var out WorkflowResponse
	err = json.Unmarshal(data, &out)
	require.NoError(t, err)
	assert.Equal(t, resp, out)
}

func TestWorkflowDefinitionJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	steps := []*StepDef{
		{ID: "s1", Name: "Search", AgentType: "search", Input: "query", DependsOn: nil, Timeout: 10 * time.Second},
		{ID: "s2", Name: "Analyze", AgentType: "analyze", Input: "results", DependsOn: []string{"s1"}, Timeout: 20 * time.Second},
	}
	in := WorkflowDefinition{
		ID:          "wf-1",
		Name:        "Test Workflow",
		Version:     "1.0",
		Description: "A workflow for testing",
		Steps:       steps,
		Variables:   map[string]string{"var": "val"},
		Metadata:    map[string]string{"author": "tester"},
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	data, err := json.Marshal(in)
	require.NoError(t, err)
	var out WorkflowDefinition
	err = json.Unmarshal(data, &out)
	require.NoError(t, err)
	assert.Equal(t, in.ID, out.ID)
	assert.Equal(t, in.Name, out.Name)
	assert.Equal(t, in.Version, out.Version)
	assert.Equal(t, in.Description, out.Description)
	assert.Equal(t, in.Variables, out.Variables)
	assert.Equal(t, in.Metadata, out.Metadata)
	assert.Equal(t, in.CreatedAt.Unix(), out.CreatedAt.Unix())
	assert.Equal(t, in.UpdatedAt.Unix(), out.UpdatedAt.Unix())
	require.Len(t, out.Steps, 2)
	assert.Equal(t, steps[0].ID, out.Steps[0].ID)
	assert.Equal(t, steps[0].DependsOn, out.Steps[0].DependsOn)
	assert.Equal(t, steps[1].DependsOn, out.Steps[1].DependsOn)
}

func TestWorkflowDefinitionZeroValue(t *testing.T) {
	var def WorkflowDefinition
	assert.Empty(t, def.ID)
	assert.Empty(t, def.Name)
	assert.Nil(t, def.Steps)
	assert.Nil(t, def.Variables)
	assert.Nil(t, def.Metadata)
	data, err := json.Marshal(def)
	require.NoError(t, err)
	var out WorkflowDefinition
	err = json.Unmarshal(data, &out)
	require.NoError(t, err)
	assert.Equal(t, def.ID, out.ID)
	assert.Equal(t, def.Name, out.Name)
	assert.Nil(t, out.Steps)
	assert.Nil(t, out.Variables)
}

func TestWorkflowSummaryJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	in := WorkflowSummary{
		ID:          "wf-1",
		Name:        "Test WF",
		Description: "desc",
		StepCount:   3,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	data, err := json.Marshal(in)
	require.NoError(t, err)
	var out WorkflowSummary
	err = json.Unmarshal(data, &out)
	require.NoError(t, err)
	assert.Equal(t, in.ID, out.ID)
	assert.Equal(t, in.Name, out.Name)
	assert.Equal(t, in.StepCount, out.StepCount)
	assert.Equal(t, in.CreatedAt.Unix(), out.CreatedAt.Unix())
	assert.Equal(t, in.UpdatedAt.Unix(), out.UpdatedAt.Unix())
}

func TestWorkflowSummaryZeroValue(t *testing.T) {
	var s WorkflowSummary
	assert.Empty(t, s.ID)
	assert.Zero(t, s.StepCount)
}

func TestStepDefJSONRoundTrip(t *testing.T) {
	in := StepDef{
		ID:        "step-1",
		Name:      "Step One",
		AgentType: "worker",
		Input:     "do something",
		DependsOn: []string{"prev-step"},
		Timeout:   15 * time.Second,
	}
	data, err := json.Marshal(in)
	require.NoError(t, err)
	var out StepDef
	err = json.Unmarshal(data, &out)
	require.NoError(t, err)
	assert.Equal(t, in.ID, out.ID)
	assert.Equal(t, in.Name, out.Name)
	assert.Equal(t, in.AgentType, out.AgentType)
	assert.Equal(t, in.Input, out.Input)
	assert.Equal(t, in.DependsOn, out.DependsOn)
	assert.Equal(t, in.Timeout, out.Timeout)
}

func TestStepDefZeroValue(t *testing.T) {
	var s StepDef
	assert.Empty(t, s.ID)
	assert.Nil(t, s.DependsOn)
	assert.Zero(t, s.Timeout)
}

func TestStepResultJSONRoundTrip(t *testing.T) {
	in := StepResult{
		StepID:   "step-1",
		Name:     "Step One",
		Status:   WorkflowStatusCompleted,
		Output:   "success output",
		Error:    "",
		Duration: 3 * time.Second,
	}
	data, err := json.Marshal(in)
	require.NoError(t, err)
	var out StepResult
	err = json.Unmarshal(data, &out)
	require.NoError(t, err)
	assert.Equal(t, in.StepID, out.StepID)
	assert.Equal(t, in.Name, out.Name)
	assert.Equal(t, in.Status, out.Status)
	assert.Equal(t, in.Output, out.Output)
	assert.Equal(t, in.Error, out.Error)
	assert.Equal(t, in.Duration, out.Duration)
}

func TestStepResultZeroValue(t *testing.T) {
	var s StepResult
	assert.Empty(t, s.StepID)
	assert.Equal(t, WorkflowStatus(""), s.Status)
	assert.Zero(t, s.Duration)
}

func TestWorkflowEventJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	cases := []struct {
		name string
		evt  WorkflowEvent
	}{
		{
			"started event",
			WorkflowEvent{
				Type:        WorkflowEventStarted,
				ExecutionID: "exec-1",
				WorkflowID:  "wf-1",
				Status:      WorkflowStatusRunning,
				Timestamp:   now,
			},
		},
		{
			"step started event",
			WorkflowEvent{
				Type:        WorkflowEventStepStarted,
				ExecutionID: "exec-1",
				WorkflowID:  "wf-1",
				StepID:      "step-1",
				StepName:    "Step One",
				Status:      WorkflowStatusRunning,
				Timestamp:   now,
			},
		},
		{
			"step completed event",
			WorkflowEvent{
				Type:        WorkflowEventStepCompleted,
				ExecutionID: "exec-1",
				WorkflowID:  "wf-1",
				StepID:      "step-1",
				StepName:    "Step One",
				Status:      WorkflowStatusCompleted,
				Output:      "done",
				Timestamp:   now,
			},
		},
		{
			"step failed event",
			WorkflowEvent{
				Type:        WorkflowEventStepFailed,
				ExecutionID: "exec-1",
				WorkflowID:  "wf-1",
				StepID:      "step-1",
				Error:       "something went wrong",
				Timestamp:   now,
			},
		},
		{
			"completed event",
			WorkflowEvent{
				Type:        WorkflowEventCompleted,
				ExecutionID: "exec-1",
				WorkflowID:  "wf-1",
				Status:      WorkflowStatusCompleted,
				Output:      "all done",
				Timestamp:   now,
			},
		},
		{
			"failed event",
			WorkflowEvent{
				Type:        WorkflowEventFailed,
				ExecutionID: "exec-1",
				WorkflowID:  "wf-1",
				Status:      WorkflowStatusFailed,
				Error:       "wf error",
				Timestamp:   now,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.evt)
			require.NoError(t, err)
			var out WorkflowEvent
			err = json.Unmarshal(data, &out)
			require.NoError(t, err)
			assert.Equal(t, tc.evt.Type, out.Type)
			assert.Equal(t, tc.evt.ExecutionID, out.ExecutionID)
			assert.Equal(t, tc.evt.WorkflowID, out.WorkflowID)
			assert.Equal(t, tc.evt.StepID, out.StepID)
			assert.Equal(t, tc.evt.StepName, out.StepName)
			assert.Equal(t, tc.evt.Status, out.Status)
			assert.Equal(t, tc.evt.Output, out.Output)
			assert.Equal(t, tc.evt.Error, out.Error)
			assert.Equal(t, tc.evt.Timestamp.Unix(), out.Timestamp.Unix())
		})
	}
}

func TestWorkflowEventZeroValue(t *testing.T) {
	var evt WorkflowEvent
	assert.Equal(t, WorkflowEventType(0), evt.Type)
	assert.Empty(t, evt.ExecutionID)
	assert.Empty(t, evt.StepID)
	assert.Empty(t, evt.Output)
	assert.Empty(t, evt.Error)
	assert.True(t, evt.Timestamp.IsZero())
}

// ---------- WorkflowService interface ----------

func TestWorkflowServiceInterface(t *testing.T) {
	var _ WorkflowService = (*mockWorkflowService)(nil)
}

type mockWorkflowService struct{}

func (m *mockWorkflowService) Execute(_ context.Context, _ *WorkflowRequest) (*WorkflowResponse, error) {
	return nil, nil
}
func (m *mockWorkflowService) ExecuteStream(_ context.Context, _ *WorkflowRequest) (<-chan WorkflowEvent, error) {
	return nil, nil
}
func (m *mockWorkflowService) ListWorkflows(_ context.Context) ([]*WorkflowSummary, error) {
	return nil, nil
}
func (m *mockWorkflowService) GetWorkflow(_ context.Context, _ string) (*WorkflowDefinition, error) {
	return nil, nil
}

// ---------- Runtime types ----------

func TestRuntimeConfigConstruction(t *testing.T) {
	cfg := RuntimeConfig{
		HealthCheckInterval: 5 * time.Second,
		MaxRestartsPerAgent: 3,
		MaxReplayEvents:     1000,
		AgentStopTimeout:    10 * time.Second,
		OverallStopTimeout:  30 * time.Second,
		RestoreTimeout:      15 * time.Second,
	}
	assert.Equal(t, 5*time.Second, cfg.HealthCheckInterval)
	assert.Equal(t, 3, cfg.MaxRestartsPerAgent)
	assert.Equal(t, 1000, cfg.MaxReplayEvents)
	assert.Equal(t, 10*time.Second, cfg.AgentStopTimeout)
	assert.Equal(t, 30*time.Second, cfg.OverallStopTimeout)
	assert.Equal(t, 15*time.Second, cfg.RestoreTimeout)
}

func TestRuntimeConfigZeroValue(t *testing.T) {
	var cfg RuntimeConfig
	assert.Zero(t, cfg.HealthCheckInterval)
	assert.Zero(t, cfg.MaxRestartsPerAgent)
	assert.Zero(t, cfg.MaxReplayEvents)
}

func TestDefaultRuntimeConfig(t *testing.T) {
	cfg := DefaultRuntimeConfig()
	require.NotNil(t, cfg)
	assert.Equal(t, 5*time.Second, cfg.HealthCheckInterval)
	assert.Equal(t, 0, cfg.MaxRestartsPerAgent)
	assert.Equal(t, 1000, cfg.MaxReplayEvents)
	assert.Equal(t, 10*time.Second, cfg.AgentStopTimeout)
	assert.Equal(t, 30*time.Second, cfg.OverallStopTimeout)
	assert.Equal(t, 15*time.Second, cfg.RestoreTimeout)
}

func TestRuntimeStatsConstruction(t *testing.T) {
	stats := RuntimeStats{
		ActiveAgents:  5,
		TotalRestarts: 2,
		Uptime:        1 * time.Hour,
		BackgroundTasks: map[string]int64{
			"health-check": 10,
		},
	}
	assert.Equal(t, 5, stats.ActiveAgents)
	assert.Equal(t, 2, stats.TotalRestarts)
	assert.Equal(t, 1*time.Hour, stats.Uptime)
	assert.Equal(t, int64(10), stats.BackgroundTasks["health-check"])
}

func TestRuntimeStatsZeroValue(t *testing.T) {
	var s RuntimeStats
	assert.Zero(t, s.ActiveAgents)
	assert.Zero(t, s.TotalRestarts)
	assert.Zero(t, s.Uptime)
	assert.Nil(t, s.BackgroundTasks)
}

func TestRuntimeInterface(t *testing.T) {
	var _ Runtime = (*mockRuntime)(nil)
}

type mockRuntime struct{}

func (m *mockRuntime) RegisterAgent(_ Agent, _ AgentFactory)                          {}
func (m *mockRuntime) StartAgent(_ context.Context, _ Agent) error                    { return nil }
func (m *mockRuntime) StopAgent(_ context.Context, _ string) error                    { return nil }
func (m *mockRuntime) GetAgent(_ string) Agent                                        { return Agent{} }
func (m *mockRuntime) RestartAgent(_ context.Context, _ string) error                 { return nil }
func (m *mockRuntime) RestoreAgent(_ context.Context, _ string, _ AgentFactory) error { return nil }
func (m *mockRuntime) NotifyAgentDead(_ string, _ string)                             {}
func (m *mockRuntime) Start(_ context.Context) error                                  { return nil }
func (m *mockRuntime) Stop() error                                                    { return nil }
func (m *mockRuntime) Stats() RuntimeStats                                            { return RuntimeStats{} }

func TestAgentFactoryType(t *testing.T) {
	fn := AgentFactory(func() Agent {
		return Agent{ID: "factory-agent", Name: "Factory Agent"}
	})
	a := fn()
	assert.Equal(t, "factory-agent", a.ID)
	assert.Equal(t, "Factory Agent", a.Name)
}

// ---------- Arena types ----------

func TestFaultTypeValues(t *testing.T) {
	tests := []struct {
		ft   FaultType
		want string
	}{
		{FaultKillLeader, "kill_leader"},
		{FaultKillAgent, "kill_agent"},
		{FaultRemoveNode, "remove_node"},
		{FaultRemoveEdge, "remove_edge"},
		{FaultPauseAgent, "pause_agent"},
		{FaultResumeAgent, "resume_agent"},
		{FaultSlowAgent, "slow_agent"},
		{FaultKillOrchestrator, "kill_orchestrator"},
		{FaultNetworkPartition, "network_partition"},
		{FaultToolTimeout, "tool_timeout"},
		{FaultMemoryCorrupt, "memory_corrupt"},
		{FaultMCPDisconnect, "mcp_disconnect"},
		{FaultLLMFailure, "llm_failure"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, string(tt.ft))
		})
	}
}

func TestFaultTypeUniqueness(t *testing.T) {
	m := map[string]bool{
		string(FaultKillLeader):       true,
		string(FaultKillAgent):        true,
		string(FaultRemoveNode):       true,
		string(FaultRemoveEdge):       true,
		string(FaultPauseAgent):       true,
		string(FaultResumeAgent):      true,
		string(FaultSlowAgent):        true,
		string(FaultKillOrchestrator): true,
		string(FaultNetworkPartition): true,
		string(FaultToolTimeout):      true,
		string(FaultMemoryCorrupt):    true,
		string(FaultMCPDisconnect):    true,
		string(FaultLLMFailure):       true,
	}
	assert.Len(t, m, 13)
}

func TestArenaConfigConstruction(t *testing.T) {
	cfg := ArenaConfig{
		Duration:     10 * time.Minute,
		FaultTypes:   []string{"kill_agent", "network_partition"},
		TargetIDs:    []string{"agent-1", "agent-2"},
		ScenarioPath: "/tmp/scenario.yaml",
	}
	assert.Equal(t, 10*time.Minute, cfg.Duration)
	assert.Equal(t, []string{"kill_agent", "network_partition"}, cfg.FaultTypes)
	assert.Equal(t, []string{"agent-1", "agent-2"}, cfg.TargetIDs)
	assert.Equal(t, "/tmp/scenario.yaml", cfg.ScenarioPath)
}

func TestArenaConfigZeroValue(t *testing.T) {
	var cfg ArenaConfig
	assert.Zero(t, cfg.Duration)
	assert.Nil(t, cfg.FaultTypes)
	assert.Nil(t, cfg.TargetIDs)
	assert.Empty(t, cfg.ScenarioPath)
}

func TestDefaultArenaConfig(t *testing.T) {
	cfg := DefaultArenaConfig()
	require.NotNil(t, cfg)
	assert.Equal(t, 5*time.Minute, cfg.Duration)
	assert.Equal(t, []string{"kill_agent", "network_partition", "latency_spike"}, cfg.FaultTypes)
	assert.Nil(t, cfg.TargetIDs)
}

func TestArenaReportConstruction(t *testing.T) {
	report := ArenaReport{
		Score: ResilienceScore{
			Overall:   85.0,
			Recovery:  90.0,
			Stability: 80.0,
			Details:   map[string]float64{"kill_agent": 100.0},
		},
		Events: []ArenaEvent{
			{Type: "injected", Target: "agent-1", Detail: "kill_agent injected"},
		},
		Duration:        5 * time.Minute,
		FaultsInjected:  3,
		AgentsRecovered: 3,
	}
	assert.Equal(t, 85.0, report.Score.Overall)
	assert.Equal(t, 3, report.FaultsInjected)
	assert.Equal(t, 3, report.AgentsRecovered)
	require.Len(t, report.Events, 1)
	assert.Equal(t, "injected", report.Events[0].Type)
}

func TestArenaReportZeroValue(t *testing.T) {
	var r ArenaReport
	assert.Equal(t, 0.0, r.Score.Overall)
	assert.Nil(t, r.Events)
	assert.Zero(t, r.Duration)
	assert.Zero(t, r.FaultsInjected)
}

func TestResilienceScoreZeroValue(t *testing.T) {
	var s ResilienceScore
	assert.Zero(t, s.Overall)
	assert.Zero(t, s.Recovery)
	assert.Zero(t, s.Stability)
	assert.Nil(t, s.Details)
}

func TestArenaEventZeroValue(t *testing.T) {
	var e ArenaEvent
	assert.True(t, e.Timestamp.IsZero())
	assert.Empty(t, e.Type)
	assert.Empty(t, e.Target)
	assert.Empty(t, e.Detail)
}

func TestArenaInterface(t *testing.T) {
	var _ Arena = (*mockArena)(nil)
}

type mockArena struct{}

func (m *mockArena) InjectFault(_ context.Context, _ FaultType, _ string) error    { return nil }
func (m *mockArena) RunScenario(_ context.Context, _ string) (*ArenaReport, error) { return nil, nil }
func (m *mockArena) RunRandom(_ context.Context, _ time.Duration) (*ArenaReport, error) {
	return nil, nil
}
func (m *mockArena) Score() *ResilienceScore { return nil }
func (m *mockArena) ListAgents() []string    { return nil }
func (m *mockArena) Stop() error             { return nil }

// ---------- Evolution types ----------

func TestEvolutionConfigConstruction(t *testing.T) {
	cfg := EvolutionConfig{
		PopulationSize: 20,
		MaxGenerations: 50,
		MutationRate:   0.3,
		CrossoverRate:  0.7,
		EliteCount:     3,
		ScoringMethod:  "hybrid",
		ReportPath:     "/tmp/report.json",
	}
	assert.Equal(t, 20, cfg.PopulationSize)
	assert.Equal(t, 50, cfg.MaxGenerations)
	assert.Equal(t, 0.3, cfg.MutationRate)
	assert.Equal(t, 0.7, cfg.CrossoverRate)
	assert.Equal(t, 3, cfg.EliteCount)
	assert.Equal(t, "hybrid", cfg.ScoringMethod)
	assert.Equal(t, "/tmp/report.json", cfg.ReportPath)
}

func TestEvolutionConfigZeroValue(t *testing.T) {
	var cfg EvolutionConfig
	assert.Zero(t, cfg.PopulationSize)
	assert.Zero(t, cfg.MaxGenerations)
	assert.Zero(t, cfg.MutationRate)
	assert.Empty(t, cfg.ScoringMethod)
}

func TestDefaultEvolutionConfig(t *testing.T) {
	cfg := DefaultEvolutionConfig()
	require.NotNil(t, cfg)
	assert.Equal(t, 20, cfg.PopulationSize)
	assert.Equal(t, 50, cfg.MaxGenerations)
	assert.Equal(t, 0.3, cfg.MutationRate)
	assert.Equal(t, 0.7, cfg.CrossoverRate)
	assert.Equal(t, 3, cfg.EliteCount)
	assert.Equal(t, "hybrid", cfg.ScoringMethod)
	assert.Empty(t, cfg.ReportPath)
}

func TestEvolutionStrategyConstruction(t *testing.T) {
	s := EvolutionStrategy{
		ID:             "strat-1",
		ParentID:       "strat-0",
		Params:         map[string]any{"temp": 0.7},
		PromptTemplate: "You are a {{.role}}",
		Score:          0.95,
		MutationDesc:   "crossover",
		Generation:     5,
	}
	assert.Equal(t, "strat-1", s.ID)
	assert.Equal(t, "strat-0", s.ParentID)
	assert.Equal(t, map[string]any{"temp": 0.7}, s.Params)
	assert.Equal(t, 0.95, s.Score)
	assert.Equal(t, 5, s.Generation)
}

func TestEvolutionStrategyZeroValue(t *testing.T) {
	var s EvolutionStrategy
	assert.Empty(t, s.ID)
	assert.Nil(t, s.Params)
	assert.Zero(t, s.Score)
	assert.Zero(t, s.Generation)
}

func TestEvolutionResultConstruction(t *testing.T) {
	r := EvolutionResult{
		BestStrategy:   &EvolutionStrategy{ID: "best", Score: 0.99},
		Generation:     10,
		ScoreHistory:   []float64{0.5, 0.7, 0.9, 0.99},
		DiversityScore: 0.75,
		Duration:       1 * time.Hour,
	}
	require.NotNil(t, r.BestStrategy)
	assert.Equal(t, "best", r.BestStrategy.ID)
	assert.Equal(t, 10, r.Generation)
	assert.Equal(t, []float64{0.5, 0.7, 0.9, 0.99}, r.ScoreHistory)
	assert.Equal(t, 0.75, r.DiversityScore)
}

func TestEvolutionResultZeroValue(t *testing.T) {
	var r EvolutionResult
	assert.Nil(t, r.BestStrategy)
	assert.Zero(t, r.Generation)
	assert.Nil(t, r.ScoreHistory)
	assert.Zero(t, r.Duration)
}

func TestEvolutionStatsConstruction(t *testing.T) {
	s := EvolutionStats{
		TotalGenerations: 10,
		BestScore:        0.99,
		AvgScore:         0.75,
		PopulationSize:   20,
		DiversityScore:   0.6,
	}
	assert.Equal(t, 10, s.TotalGenerations)
	assert.Equal(t, 0.99, s.BestScore)
	assert.Equal(t, 0.75, s.AvgScore)
	assert.Equal(t, 20, s.PopulationSize)
	assert.Equal(t, 0.6, s.DiversityScore)
}

func TestEvolutionStatsZeroValue(t *testing.T) {
	var s EvolutionStats
	assert.Zero(t, s.TotalGenerations)
	assert.Zero(t, s.BestScore)
	assert.Zero(t, s.AvgScore)
	assert.Zero(t, s.PopulationSize)
}

func TestLineageRecordConstruction(t *testing.T) {
	r := LineageRecord{
		ChildID:      "child-1",
		ParentIDs:    []string{"parent-1", "parent-2"},
		MutationType: "crossover",
		Generation:   3,
	}
	assert.Equal(t, "child-1", r.ChildID)
	assert.Equal(t, []string{"parent-1", "parent-2"}, r.ParentIDs)
	assert.Equal(t, "crossover", r.MutationType)
	assert.Equal(t, 3, r.Generation)
}

func TestLineageRecordZeroValue(t *testing.T) {
	var r LineageRecord
	assert.Empty(t, r.ChildID)
	assert.Nil(t, r.ParentIDs)
	assert.Empty(t, r.MutationType)
	assert.Zero(t, r.Generation)
}

func TestDreamCycleConfigConstruction(t *testing.T) {
	cfg := DreamCycleConfig{
		TriggerThreshold: 0.9,
		MaxCycles:        10,
		CycleTimeout:     30 * time.Minute,
	}
	assert.Equal(t, 0.9, cfg.TriggerThreshold)
	assert.Equal(t, 10, cfg.MaxCycles)
	assert.Equal(t, 30*time.Minute, cfg.CycleTimeout)
}

func TestDreamCycleConfigZeroValue(t *testing.T) {
	var cfg DreamCycleConfig
	assert.Zero(t, cfg.TriggerThreshold)
	assert.Zero(t, cfg.MaxCycles)
	assert.Zero(t, cfg.CycleTimeout)
}

func TestDefaultDreamCycleConfig(t *testing.T) {
	cfg := DefaultDreamCycleConfig()
	require.NotNil(t, cfg)
	assert.Equal(t, 0.8, cfg.TriggerThreshold)
	assert.Equal(t, 10, cfg.MaxCycles)
	assert.Equal(t, 30*time.Minute, cfg.CycleTimeout)
}

func TestDreamCycleStatusConstruction(t *testing.T) {
	now := time.Now()
	s := DreamCycleStatus{
		Running:         true,
		CyclesCompleted: 5,
		LastCycleTime:   now,
		LastResult:      &EvolutionResult{Generation: 5},
	}
	assert.True(t, s.Running)
	assert.Equal(t, 5, s.CyclesCompleted)
	assert.Equal(t, now, s.LastCycleTime)
	require.NotNil(t, s.LastResult)
	assert.Equal(t, 5, s.LastResult.Generation)
}

func TestDreamCycleStatusZeroValue(t *testing.T) {
	var s DreamCycleStatus
	assert.False(t, s.Running)
	assert.Zero(t, s.CyclesCompleted)
	assert.True(t, s.LastCycleTime.IsZero())
	assert.Nil(t, s.LastResult)
}

func TestEvolutionInterface(t *testing.T) {
	var _ Evolution = (*mockEvolution)(nil)
}

type mockEvolution struct{}

func (m *mockEvolution) Evolve(_ context.Context, _ int) (*EvolutionResult, error) { return nil, nil }
func (m *mockEvolution) RunIdleEvolution(_ context.Context, _ int) error           { return nil }
func (m *mockEvolution) LatestReport() (string, error)                             { return "", nil }
func (m *mockEvolution) BestStrategy() (*EvolutionStrategy, error)                 { return nil, nil }
func (m *mockEvolution) Stats() (*EvolutionStats, error)                           { return nil, nil }
func (m *mockEvolution) Lineages() ([]LineageRecord, error)                        { return nil, nil }
func (m *mockEvolution) SaveBestStrategy(_ string) error                           { return nil }
func (m *mockEvolution) Shutdown()                                                 {}

func TestDreamCycleInterface(t *testing.T) {
	var _ DreamCycle = (*mockDreamCycle)(nil)
}

type mockDreamCycle struct{}

func (m *mockDreamCycle) Start(_ context.Context) error                       { return nil }
func (m *mockDreamCycle) Stop() error                                         { return nil }
func (m *mockDreamCycle) Trigger(_ context.Context) (*EvolutionResult, error) { return nil, nil }
func (m *mockDreamCycle) Status() DreamCycleStatus                            { return DreamCycleStatus{} }

// ---------- CleanOptions & ContextCleaner ----------

func TestCleanOptionsZeroValue(t *testing.T) {
	var opts CleanOptions
	assert.Zero(t, opts.MaxUserLen)
	assert.Zero(t, opts.MaxAssistantLen)
	assert.Zero(t, opts.MaxToolLen)
	assert.Zero(t, opts.MaxSystemLen)
	assert.Zero(t, opts.MaxRawToolResultLength)
	assert.Zero(t, opts.MaxSummarizedTurns)
	assert.False(t, opts.KeepRawToolDetails)
	assert.Equal(t, CleaningMode(0), opts.Mode)
}

func TestCleanOptionsDefaultValues(t *testing.T) {
	opts := DefaultCleanOptions()
	assert.Equal(t, 200, opts.MaxUserLen)
	assert.Equal(t, 150, opts.MaxAssistantLen)
	assert.Equal(t, 50, opts.MaxToolLen)
	assert.Equal(t, 500, opts.MaxSystemLen)
	assert.Equal(t, 2000, opts.MaxRawToolResultLength)
	assert.Equal(t, 0, opts.MaxSummarizedTurns)
	assert.True(t, opts.KeepRawToolDetails)
	assert.Equal(t, CleaningModeDefault, opts.Mode)
}

func TestCleanerStatsNonZero(t *testing.T) {
	stats := CleanerStats{
		LLMCalls:                10,
		ToolCalls:               20,
		BytesSaved:              5000,
		HistoryIn:               100,
		HistoryOut:              50,
		DroppedToolMessages:     5,
		SummarizedToolMessages:  3,
		ActivePreservedMessages: 2,
		TurnsProcessed:          15,
	}
	assert.Equal(t, int64(10), stats.LLMCalls)
	assert.Equal(t, int64(20), stats.ToolCalls)
	assert.NotZero(t, stats.HistoryIn)
	assert.NotZero(t, stats.TurnsProcessed)
}

func TestContextCleanerInterface(t *testing.T) {
	var _ ContextCleaner = (*mockContextCleaner)(nil)
}

type mockContextCleaner struct{}

func (m *mockContextCleaner) Clean(_ []Message, _ ...CleanOptions) []Message          { return nil }
func (m *mockContextCleaner) CleanWithTurns(_ []Message, _ ...CleanOptions) []Message { return nil }
func (m *mockContextCleaner) Stats() CleanerStats                                     { return CleanerStats{} }
func (m *mockContextCleaner) ResetStats()                                             {}

// ---------- PaginationResponse JSON round-trip ----------

func TestPaginationResponseJSONRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   PaginationResponse
	}{
		{"first page", PaginationResponse{Total: 100, Page: 1, PageSize: 10, TotalPages: 10, HasMore: true}},
		{"last page", PaginationResponse{Total: 100, Page: 10, PageSize: 10, TotalPages: 10, HasMore: false}},
		{"empty", PaginationResponse{Total: 0, Page: 1, PageSize: 10, TotalPages: 0, HasMore: false}},
		{"zero value", PaginationResponse{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.in)
			require.NoError(t, err)
			var out PaginationResponse
			err = json.Unmarshal(data, &out)
			require.NoError(t, err)
			assert.Equal(t, tc.in.Total, out.Total)
			assert.Equal(t, tc.in.Page, out.Page)
			assert.Equal(t, tc.in.PageSize, out.PageSize)
			assert.Equal(t, tc.in.TotalPages, out.TotalPages)
			assert.Equal(t, tc.in.HasMore, out.HasMore)
		})
	}
}

// ---------- TaskResult JSON round-trip ----------

func TestTaskResultJSONRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   TaskResult
	}{
		{"success", TaskResult{TaskID: "t-1", AgentID: "a-1", Success: true, Data: map[string]interface{}{"result": "ok"}, CompletedAt: 100}},
		{"failure", TaskResult{TaskID: "t-2", AgentID: "a-2", Success: false, Error: "failed", CompletedAt: 200}},
		{"minimal", TaskResult{TaskID: "t-3", AgentID: "a-3"}},
		{"zero value", TaskResult{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.in)
			require.NoError(t, err)
			var out TaskResult
			err = json.Unmarshal(data, &out)
			require.NoError(t, err)
			assert.Equal(t, tc.in.TaskID, out.TaskID)
			assert.Equal(t, tc.in.AgentID, out.AgentID)
			assert.Equal(t, tc.in.Success, out.Success)
			assert.Equal(t, tc.in.Error, out.Error)
			assert.Equal(t, tc.in.CompletedAt, out.CompletedAt)
		})
	}
}

// ---------- Agent JSON round-trip ----------

func TestAgentJSONRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   Agent
	}{
		{"full", Agent{
			ID: "a-1", Name: "Test Agent", Type: "leader",
			Status: AgentStatusReady, SessionID: "s-1",
			Config:    map[string]interface{}{"key": "value", "num": 42.0},
			CreatedAt: 100, UpdatedAt: 200,
		}},
		{"minimal", Agent{ID: "a-2", Name: "Min", Status: AgentStatusInitializing}},
		{"zero value", Agent{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.in)
			require.NoError(t, err)
			var out Agent
			err = json.Unmarshal(data, &out)
			require.NoError(t, err)
			assert.Equal(t, tc.in.ID, out.ID)
			assert.Equal(t, tc.in.Name, out.Name)
			assert.Equal(t, tc.in.Type, out.Type)
			assert.Equal(t, tc.in.Status, out.Status)
			assert.Equal(t, tc.in.SessionID, out.SessionID)
			assert.Equal(t, tc.in.CreatedAt, out.CreatedAt)
			assert.Equal(t, tc.in.UpdatedAt, out.UpdatedAt)
		})
	}
}

// ---------- LLM types JSON round-trip ----------

func TestLLMConfigJSONRoundTrip(t *testing.T) {
	in := LLMConfig{
		Provider: LLMProviderOpenAI, APIKey: "sk-test", BaseURL: "https://api.openai.com",
		Model: "gpt-4", Timeout: 30, Temperature: 0.7, MaxTokens: 2000,
		TopP: 1.0, FrequencyPenalty: 0.0, PresencePenalty: 0.0,
	}
	data, err := json.Marshal(in)
	require.NoError(t, err)
	var out LLMConfig
	err = json.Unmarshal(data, &out)
	require.NoError(t, err)
	assert.Equal(t, in.Provider, out.Provider)
	assert.Equal(t, in.APIKey, out.APIKey)
	assert.Equal(t, in.Model, out.Model)
	assert.Equal(t, in.Timeout, out.Timeout)
	assert.Equal(t, in.Temperature, out.Temperature)
	assert.Equal(t, in.MaxTokens, out.MaxTokens)
}

func TestLLMConfigZeroValue(t *testing.T) {
	var cfg LLMConfig
	assert.Equal(t, LLMProvider(""), cfg.Provider)
	assert.Zero(t, cfg.Timeout)
	assert.Zero(t, cfg.Temperature)
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	var out LLMConfig
	err = json.Unmarshal(data, &out)
	require.NoError(t, err)
	assert.Equal(t, cfg, out)
}

func TestGenerateRequestJSONRoundTrip(t *testing.T) {
	temp := 0.5
	maxTok := 100
	in := GenerateRequest{
		Messages: []*LLMMessage{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi", ToolCalls: []ToolCall{
				{ID: "call-1", Type: "function", Function: FunctionCall{Name: "test", Arguments: "{}"}},
			}},
		},
		Model: "gpt-4", Temperature: &temp, MaxTokens: &maxTok, Stream: false,
		Tools: []Tool{
			{Type: "function", Function: FunctionDefinition{Name: "tool1", Description: "A tool"}},
		},
	}
	data, err := json.Marshal(in)
	require.NoError(t, err)
	var out GenerateRequest
	err = json.Unmarshal(data, &out)
	require.NoError(t, err)
	assert.Equal(t, in.Model, out.Model)
	assert.Equal(t, in.Stream, out.Stream)
	require.Len(t, out.Messages, 2)
	assert.Equal(t, "user", out.Messages[0].Role)
	assert.Equal(t, "hello", out.Messages[0].Content)
	require.Len(t, out.Messages[1].ToolCalls, 1)
	assert.Equal(t, "call-1", out.Messages[1].ToolCalls[0].ID)
	assert.Equal(t, "tool1", out.Tools[0].Function.Name)
}

func TestGenerateRequestZeroValue(t *testing.T) {
	var req GenerateRequest
	assert.Nil(t, req.Messages)
	assert.Empty(t, req.Model)
	assert.Nil(t, req.Temperature)
	assert.Nil(t, req.MaxTokens)
	assert.False(t, req.Stream)
	assert.Nil(t, req.Tools)
}

func TestGenerateResponseJSONRoundTrip(t *testing.T) {
	in := GenerateResponse{
		Content: "Hello!", FinishReason: "stop",
		Usage: TokenUsage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
		ToolCalls: []ToolCall{
			{ID: "call-1", Type: "function", Function: FunctionCall{Name: "test", Arguments: `{"x":1}`}},
		},
		Model: "gpt-4",
	}
	data, err := json.Marshal(in)
	require.NoError(t, err)
	var out GenerateResponse
	err = json.Unmarshal(data, &out)
	require.NoError(t, err)
	assert.Equal(t, in.Content, out.Content)
	assert.Equal(t, in.FinishReason, out.FinishReason)
	assert.Equal(t, in.Model, out.Model)
	assert.Equal(t, 30, out.Usage.TotalTokens)
	require.Len(t, out.ToolCalls, 1)
	assert.Equal(t, "call-1", out.ToolCalls[0].ID)
}

func TestGenerateResponseZeroValue(t *testing.T) {
	var resp GenerateResponse
	assert.Empty(t, resp.Content)
	assert.Empty(t, resp.FinishReason)
	assert.Zero(t, resp.Usage.TotalTokens)
	assert.Nil(t, resp.ToolCalls)
	assert.Empty(t, resp.Model)
}

// ---------- CleanerStats JSON tags ----------

func TestCleanerStatsJSONTags(t *testing.T) {
	stats := CleanerStats{
		LLMCalls: 5, ToolCalls: 10, BytesSaved: 1000,
		HistoryIn: 50, HistoryOut: 30,
		DroppedToolMessages: 2, SummarizedToolMessages: 1,
		ActivePreservedMessages: 3, TurnsProcessed: 8,
	}
	data, err := json.Marshal(stats)
	require.NoError(t, err)
	var decoded map[string]interface{}
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)
	assert.Equal(t, float64(5), decoded["llm_calls"])
	assert.Equal(t, float64(10), decoded["tool_calls"])
	assert.Equal(t, float64(1000), decoded["bytes_saved"])
	assert.Equal(t, float64(50), decoded["history_in"])
	assert.Equal(t, float64(30), decoded["history_out"])
	assert.Equal(t, float64(2), decoded["dropped_tool_messages"])
	assert.Equal(t, float64(1), decoded["summarized_tool_messages"])
	assert.Equal(t, float64(3), decoded["active_preserved_messages"])
	assert.Equal(t, float64(8), decoded["turns_processed"])
}

func TestCleanerStatsZeroValueJSON(t *testing.T) {
	var stats CleanerStats
	data, err := json.Marshal(stats)
	require.NoError(t, err)
	var decoded map[string]interface{}
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)
	assert.Equal(t, float64(0), decoded["llm_calls"])
	assert.Equal(t, float64(0), decoded["tool_calls"])
}
