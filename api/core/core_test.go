package core

import (
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

// ---------- WorkflowEvent JSON round-trip ----------

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

// ---------- WorkflowRequest JSON round-trip ----------

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

// ---------- GenerateRequest JSON round-trip ----------

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

// ---------- FaultType constants ----------

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

// ---------- Default config values ----------

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

func TestDefaultArenaConfig(t *testing.T) {
	cfg := DefaultArenaConfig()
	require.NotNil(t, cfg)
	assert.Equal(t, 5*time.Minute, cfg.Duration)
	assert.Equal(t, []string{"kill_agent", "network_partition", "latency_spike"}, cfg.FaultTypes)
	assert.Nil(t, cfg.TargetIDs)
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

func TestDefaultDreamCycleConfig(t *testing.T) {
	cfg := DefaultDreamCycleConfig()
	require.NotNil(t, cfg)
	assert.Equal(t, 0.8, cfg.TriggerThreshold)
	assert.Equal(t, 10, cfg.MaxCycles)
	assert.Equal(t, 30*time.Minute, cfg.CycleTimeout)
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

// ---------- AgentFactoryType ----------

func TestAgentFactoryType(t *testing.T) {
	fn := AgentFactory(func() Agent {
		return Agent{ID: "factory-agent", Name: "Factory Agent"}
	})
	a := fn()
	assert.Equal(t, "factory-agent", a.ID)
	assert.Equal(t, "Factory Agent", a.Name)
}

// ---------- CleanerStats ----------

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

// ---------- CleanerStats zero-value JSON ----------

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
