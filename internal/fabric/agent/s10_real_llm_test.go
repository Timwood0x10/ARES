//go:build realllm

// This file is excluded from normal builds and from CI on purpose: it depends on a live model.
//
// Run it with credentials in the environment — never in the repository:
//
//	ARES_REAL_LLM_BASE_URL=... ARES_REAL_LLM_API_KEY=... ARES_REAL_LLM_MODEL=... \
//	  go test -tags realllm ./internal/fabric/agent/ -run TestS10_RealModel -count=1 -v
//
// What it proves that the scripted tests cannot: a real model, shown the catalogue's prior, chooses
// the tool and the planner grows a node from ITS call rather than from a fixture that already knew
// the answer.
package agentfabric

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/core/models"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
	"github.com/Timwood0x10/ares/internal/llm"
)

func TestS10_RealModel(t *testing.T) {
	baseURL := os.Getenv("ARES_REAL_LLM_BASE_URL")
	apiKey := os.Getenv("ARES_REAL_LLM_API_KEY")
	model := os.Getenv("ARES_REAL_LLM_MODEL")
	if baseURL == "" || apiKey == "" || model == "" {
		t.Skip("set ARES_REAL_LLM_BASE_URL / ARES_REAL_LLM_API_KEY / ARES_REAL_LLM_MODEL to run")
	}

	client, err := llm.NewClient(&llm.Config{
		Provider: "openai",
		APIKey:   apiKey,
		BaseURL:  baseURL,
		Model:    model,
		Timeout:  120,
	})
	require.NoError(t, err)
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fabric := taskfabric.NewFabric()
	reg := NewSessionRegistry()
	const sessionID = "real-llm-s10"
	_, err = reg.InitSession(sessionID, "what is left to do in the notes?", nil,
		func(context.Context, *engine.MutableDAG) func() { return func() {} })
	require.NoError(t, err)

	planner, err := NewPlannerCognition(PlannerDeps{
		ChatClient: client,
		ToolBinder: &searchBinder{root: t.TempDir()},
		Sessions:   reg,
		Fabric:     fabric,
		L1DAG:      l1DAGWithPolicy(t, "search the notes with grep before answering", 2, "45s"),
		Logger:     slog.Default(),
	})
	require.NoError(t, err)

	planTask := models.NewTask(SessionNodeID(sessionID, 0, "plan", 0), models.AgentType("ares/plan"), nil)
	planTask.SessionID = sessionID
	planTask.Payload = map[string]any{
		"input":         "what is left to do in the notes? use the grep tool with query TODO",
		planMetadataKey: sessionID,
	}

	_, err = planner.ExecuteStep(ctx, planTask)
	require.NoError(t, err)

	g, err := reg.GetSession(sessionID)
	require.NoError(t, err)

	grepID := SessionNodeID(sessionID, 1, "grep", 0)
	assert.True(t, g.HasNode(grepID),
		"a real model, told what grep is for, must call it — no node means it answered from memory")
}
