package arena

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goagentx/internal/runtime"
)

func setupHandler(rt RuntimeProvider, dag DAGProvider) (*Handler, *Service) {
	inj := NewInjector(rt, dag)
	svc := NewService(inj, nil)
	h := NewHandler(svc)
	return h, svc
}

func TestHandleKillLeader_Success(t *testing.T) {
	rt := &mockRuntime{
		listAgentsFn: func() []runtime.AgentInfo {
			return []runtime.AgentInfo{{ID: "leader-1", Type: "leader"}}
		},
	}
	h, _ := setupHandler(rt, nil)

	req := httptest.NewRequest("POST", "/arena/leader/kill", nil)
	rec := httptest.NewRecorder()
	h.handleKillLeader(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result Result
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	assert.True(t, result.Success)
}

func TestHandleKillLeader_NoLeader(t *testing.T) {
	rt := &mockRuntime{
		listAgentsFn: func() []runtime.AgentInfo {
			return []runtime.AgentInfo{{ID: "worker-1", Type: "sub"}}
		},
	}
	h, _ := setupHandler(rt, nil)

	req := httptest.NewRequest("POST", "/arena/leader/kill", nil)
	rec := httptest.NewRecorder()
	h.handleKillLeader(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	var result Result
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	assert.False(t, result.Success)
	assert.Contains(t, result.Error, "leader agent not found")
}

func TestHandleKillAgent_Success(t *testing.T) {
	rt := &mockRuntime{}
	h, _ := setupHandler(rt, nil)

	req := httptest.NewRequest("POST", "/arena/agent/agent-1/kill", nil)
	req.SetPathValue("id", "agent-1")
	rec := httptest.NewRecorder()
	h.handleKillAgent(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result Result
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	assert.True(t, result.Success)
	assert.Equal(t, "agent-1", result.Action.TargetID)
}

func TestHandleKillAgent_MissingID(t *testing.T) {
	h, _ := setupHandler(nil, nil)

	req := httptest.NewRequest("POST", "/arena/agent//kill", nil)
	// No path value set.
	rec := httptest.NewRecorder()
	h.handleKillAgent(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleRemoveNode_Success(t *testing.T) {
	dag := &mockDAG{}
	h, _ := setupHandler(nil, dag)

	req := httptest.NewRequest("POST", "/arena/node/node-1/remove", nil)
	req.SetPathValue("id", "node-1")
	rec := httptest.NewRecorder()
	h.handleRemoveNode(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result Result
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	assert.True(t, result.Success)
}

func TestHandleRemoveNode_MissingID(t *testing.T) {
	h, _ := setupHandler(nil, nil)

	req := httptest.NewRequest("POST", "/arena/node//remove", nil)
	rec := httptest.NewRecorder()
	h.handleRemoveNode(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleRemoveEdge_Success(t *testing.T) {
	dag := &mockDAG{}
	h, _ := setupHandler(nil, dag)

	body := `{"from":"a","to":"b"}`
	req := httptest.NewRequest("POST", "/arena/edge/remove", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.handleRemoveEdge(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var result Result
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	assert.True(t, result.Success)
}

func TestHandleRemoveEdge_InvalidJSON(t *testing.T) {
	h, _ := setupHandler(nil, nil)

	req := httptest.NewRequest("POST", "/arena/edge/remove", bytes.NewBufferString("not json"))
	rec := httptest.NewRecorder()
	h.handleRemoveEdge(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleRemoveEdge_MissingFields(t *testing.T) {
	h, _ := setupHandler(nil, nil)

	body := `{"from":"a"}`
	req := httptest.NewRequest("POST", "/arena/edge/remove", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	h.handleRemoveEdge(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHandleStats(t *testing.T) {
	rt := &mockRuntime{}
	h, _ := setupHandler(rt, nil)

	req := httptest.NewRequest("GET", "/arena/stats", nil)
	rec := httptest.NewRecorder()
	h.handleStats(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var stats Stats
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &stats))
	assert.Equal(t, 0, stats.TotalActions)
}

func TestHandleHistory_Empty(t *testing.T) {
	h, _ := setupHandler(nil, nil)

	req := httptest.NewRequest("GET", "/arena/history", nil)
	rec := httptest.NewRecorder()
	h.handleHistory(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var history []Result
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &history))
	assert.Empty(t, history)
}

func TestHandleHistory_WithData(t *testing.T) {
	rt := &mockRuntime{}
	h, svc := setupHandler(rt, nil)

	// Execute an action first.
	svc.Execute(context.Background(), Action{
		ID: "hist-1", Type: ActionKillAgent, TargetID: "a-1",
	})

	req := httptest.NewRequest("GET", "/arena/history", nil)
	rec := httptest.NewRecorder()
	h.handleHistory(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var history []Result
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &history))
	assert.Len(t, history, 1)
}

func TestValidateAction_KillLeader(t *testing.T) {
	err := ValidateAction(Action{Type: ActionKillLeader})
	assert.NoError(t, err)
}

func TestValidateAction_KillAgent_NoTarget(t *testing.T) {
	err := ValidateAction(Action{Type: ActionKillAgent})
	assert.Error(t, err)
	assert.ErrorContains(t, err, "target_id")
}

func TestValidateAction_KillAgent_WithTarget(t *testing.T) {
	err := ValidateAction(Action{Type: ActionKillAgent, TargetID: "a-1"})
	assert.NoError(t, err)
}

func TestValidateAction_RemoveNode_NoTarget(t *testing.T) {
	err := ValidateAction(Action{Type: ActionRemoveNode})
	assert.Error(t, err)
}

func TestValidateAction_RemoveEdge_MissingFields(t *testing.T) {
	err := ValidateAction(Action{Type: ActionRemoveEdge, SourceID: "a"})
	assert.Error(t, err)
}

func TestValidateAction_RemoveEdge_Complete(t *testing.T) {
	err := ValidateAction(Action{Type: ActionRemoveEdge, SourceID: "a", TargetID: "b"})
	assert.NoError(t, err)
}

func TestValidateAction_EmptyType(t *testing.T) {
	err := ValidateAction(Action{})
	assert.Error(t, err)
	assert.ErrorContains(t, err, "type is required")
}

func TestValidateAction_UnknownType(t *testing.T) {
	err := ValidateAction(Action{Type: "chaos_monkey"})
	assert.Error(t, err)
	assert.ErrorContains(t, err, "unknown action type")
}

func TestParseActionType(t *testing.T) {
	tests := []struct {
		input    string
		expected ActionType
		wantErr  bool
	}{
		{"kill_leader", ActionKillLeader, false},
		{"kill_agent", ActionKillAgent, false},
		{"remove_node", ActionRemoveNode, false},
		{"remove_edge", ActionRemoveEdge, false},
		{"KILL_LEADER", ActionKillLeader, false},
		{"unknown", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := ParseActionType(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestRoutePath(t *testing.T) {
	assert.Equal(t, "POST /arena/leader/kill", RoutePath(ActionKillLeader))
	assert.Equal(t, "POST /arena/agent/{id}/kill", RoutePath(ActionKillAgent))
	assert.Equal(t, "POST /arena/node/{id}/remove", RoutePath(ActionRemoveNode))
	assert.Equal(t, "POST /arena/edge/remove", RoutePath(ActionRemoveEdge))
	assert.Empty(t, RoutePath("unknown"))
}

func TestRecoverMiddleware_NoPanic(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := RecoverMiddleware(inner)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRecoverMiddleware_WithPanic(t *testing.T) {
	inner := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic(errors.New("test panic"))
	})
	handler := RecoverMiddleware(inner)

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestRegisterRoutes(t *testing.T) {
	h, _ := setupHandler(nil, nil)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// Verify that all routes are registered by making requests.
	routes := []struct {
		method string
		path   string
	}{
		{"POST", "/arena/leader/kill"},
		{"GET", "/arena/stats"},
		{"GET", "/arena/history"},
	}

	for _, r := range routes {
		req := httptest.NewRequest(r.method, r.path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		// Should not be 404 (route exists).
		assert.NotEqual(t, http.StatusNotFound, rec.Code, "route not found: %s %s", r.method, r.path)
	}
}
