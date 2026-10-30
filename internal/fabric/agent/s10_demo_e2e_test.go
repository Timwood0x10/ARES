package agentfabric

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/core/models"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
	llmcore "github.com/Timwood0x10/ares/internal/llmcore"
	resources "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// searchBinder is a local search tool over a fixed directory: it reads the files and returns
// "path:line: text" for every match.
//
// It stands in for a discovered native command (rg/grep) deliberately. The demo has to run on any
// machine, and a host binary would make the result depend on the host rather than on the chain being
// demonstrated: resource → catalogue declaration → planner growth → tool execution.
type searchBinder struct {
	root   string
	result any
}

func (b *searchBinder) CallTool(_ context.Context, _ string, args map[string]any) (any, error) {
	query, _ := args["query"].(string)
	if query == "" {
		return nil, fmt.Errorf("search: query is required")
	}

	var matches []string
	err := filepath.Walk(b.root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil || info == nil || info.IsDir() {
			return walkErr
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, query) {
				matches = append(matches, fmt.Sprintf("%s: %s", filepath.Base(path), strings.TrimSpace(line)))
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	b.result = strings.Join(matches, "\n")
	return b.result, nil
}

func (b *searchBinder) ListTools() []string          { return []string{"grep"} }
func (b *searchBinder) IsToolIdempotent(string) bool { return true }
func (b *searchBinder) GetToolSchemas() []resources.ToolSchema {
	return (&plannerTestBinder{}).GetToolSchemas()
}

// TestS10_Demo_ResourceToExecutedResult is the demo artifact the plan asks for, in one runnable
// test: a fixed local text fixture, a search tool registered over it, the catalogue's declaration
// for that tool, the planner growing a node from a model call, and that node actually executing and
// returning the matching line.
//
// Offline and deterministic — no model, no network, no host binary. `make demo-l1` runs it.
func TestS10_Demo_ResourceToExecutedResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. The fixed fixture.
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "notes.txt"),
		[]byte("buy milk\nTODO: rotate the access token\ncall back later\n"), 0o644))

	// 2. The search tool over it.
	binder := &searchBinder{root: root}

	// 3. The catalogue's declaration for that tool.
	l1 := l1DAGWithPolicy(t, "search the notes before answering", 2, "45s")

	fabric := taskfabric.NewFabric()
	reg := NewSessionRegistry()
	const sessionID = "demo-s10"
	_, err := reg.InitSession(sessionID, "what is left to do in the notes?", nil,
		func(context.Context, *engine.MutableDAG) func() { return func() {} })
	require.NoError(t, err)

	chat := &promptRecordingChat{responses: []*llmcore.GenerateResponse{
		{ToolCalls: []llmcore.ToolCall{{ID: "call-1", Type: "function",
			Function: llmcore.FunctionCall{Name: "grep", Arguments: `{"query":"TODO"}`}}}},
	}}
	planner, err := NewPlannerCognition(PlannerDeps{
		ChatClient: chat,
		ToolBinder: binder,
		Sessions:   reg,
		Fabric:     fabric,
		L1DAG:      l1,
		Logger:     slog.Default(),
	})
	require.NoError(t, err)

	planTask := models.NewTask(SessionNodeID(sessionID, 0, "plan", 0), models.AgentType("ares/plan"), nil)
	planTask.SessionID = sessionID
	planTask.Payload = map[string]any{"input": "what is left to do in the notes?", planMetadataKey: sessionID}

	// 4. The model, told what the resource is for, asks for it; the planner grows the node.
	_, err = planner.ExecuteStep(ctx, planTask)
	require.NoError(t, err)

	g, err := reg.GetSession(sessionID)
	require.NoError(t, err)
	grepID := SessionNodeID(sessionID, 1, "grep", 0)
	require.True(t, g.HasNode(grepID), "the tool call must become a node")

	// 5. That node executes and returns the fixture's matching line.
	step := g.DAG().StepIndex()[grepID]
	require.NotNil(t, step)
	payload := make(map[string]any, len(step.Metadata))
	for key, value := range step.Metadata {
		if strings.HasPrefix(key, argMetadataPrefix) {
			payload[key] = value
		}
	}
	runTask := models.NewTask(grepID, models.AgentType("tool/grep"), nil)
	runTask.Payload = payload
	_, err = (&toolCognition{tool: "grep", binder: binder}).ExecuteStep(ctx, runTask)
	require.NoError(t, err)

	result, _ := binder.result.(string)
	assert.Contains(t, result, "TODO: rotate the access token",
		"the executed tool must return what the fixture contains")
	assert.Contains(t, result, "notes.txt", "and say which file it came from")
}
