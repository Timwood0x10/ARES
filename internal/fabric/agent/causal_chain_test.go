package agentfabric

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/causalchain"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/fabric/planprojection"
	"github.com/Timwood0x10/ares/internal/fabric/task"
	llmcore "github.com/Timwood0x10/ares/internal/llmcore"
	resources "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// TestIntentRidesTheActionItProduced is the regression test for the defect this
// work exists for: the rebuilt history used to replay EVERY action with
// Content == "", so a successor quantum saw a sequence of effects with no
// reasons. The intent must now be on the assistant message that carries the
// call, in execution order — and must be stated once per decision, not once per
// tool call the decision produced.
func TestIntentRidesTheActionItProduced(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fabric := taskfabric.NewFabric()
	reg := NewSessionRegistry()
	const sessionID = "causal-chain-intent"
	g, err := reg.InitSession(sessionID, "root prompt", nil, nil)
	require.NoError(t, err)

	const stated = "grep first:  the parser is suspected, and the lexer is only reachable through it"
	grepID := SessionNodeID(sessionID, 1, "grep", 0)
	readID := SessionNodeID(sessionID, 1, "read", 1)
	planNode := SessionNodeID(sessionID, 1, "plan", 0)

	require.NoError(t, g.AddToolNode(ctx, grepID, "grep",
		map[string]any{"query": "tokenizer", intentKey: stated, planMetadataKey: sessionID}, g.Root()))
	require.NoError(t, g.AddToolNode(ctx, readID, "read",
		map[string]any{"path": "/p/lexer.go", intentKey: stated, planMetadataKey: sessionID}, grepID))
	require.NoError(t, g.AddToolNode(ctx, planNode, "plan",
		map[string]any{planMetadataKey: sessionID}, readID))

	for id, out := range map[string]string{
		g.Root(): "root prompt",
		grepID:   "lexer.go:12: tokenizer",
		readID:   "package lexer",
	} {
		require.NoError(t, fabric.Create(&taskfabric.Task{ID: id, Capability: "x"}))
		driveTaskToCompleted(t, ctx, fabric, id, out)
	}

	planner, err := NewPlannerCognition(PlannerDeps{
		ChatClient: &l1ChatAlwaysGrep{},
		ToolBinder: &plannerTestBinder{},
		Sessions:   reg,
		Fabric:     fabric,
		Logger:     slog.Default(),
	})
	require.NoError(t, err)

	task := models.NewTask(planNode, planAgentType, nil)
	task.SessionID = sessionID
	msgs, err := planner.(*plannerCognition).assembleContext(ctx, task, g, historyPlanning)
	require.NoError(t, err)

	require.Len(t, msgs, 5, "root + two action/observation pairs")
	assert.Equal(t, "assistant", msgs[1].Role)
	assert.Equal(t, string(causalchain.NormalizeIntent(stated, intentMaxRunes)), msgs[1].Content,
		"the FIRST action of a decision carries its why")
	assert.Equal(t, "assistant", msgs[3].Role)
	assert.Empty(t, msgs[3].Content,
		"the repeat of the same why is collapsed — one decision, one statement")
	require.NotEmpty(t, msgs[1].ToolCalls)
	assert.NotContains(t, msgs[1].ToolCalls[0].Function.Arguments, intentKey,
		"intent is causality, not a tool argument: it must not leak into the reconstructed call")
	assert.Contains(t, msgs[1].ToolCalls[0].Function.Arguments, "tokenizer")
}

// TestIntentSurvivesAnObservationBudgetCut pins the split treatment: the
// observation is the bulk and gets normalized, the intent is the reasoning and
// is kept verbatim. Compressing both with one byte limit is the bug.
func TestIntentSurvivesAnObservationBudgetCut(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fabric := taskfabric.NewFabric()
	reg := NewSessionRegistry()
	const sessionID = "causal-chain-budget"
	g, err := reg.InitSession(sessionID, "root prompt", nil, nil)
	require.NoError(t, err)

	huge := strings.Repeat("a very long line of source code\n", 400)
	const stated = "read the whole file because the failing symbol is not in the first 200 lines"
	readID := SessionNodeID(sessionID, 1, "read", 0)
	planNode := SessionNodeID(sessionID, 1, "plan", 0)
	require.NoError(t, g.AddToolNode(ctx, readID, "read",
		map[string]any{"path": "/p/big.go", intentKey: stated, planMetadataKey: sessionID}, g.Root()))
	require.NoError(t, g.AddToolNode(ctx, planNode, "plan",
		map[string]any{planMetadataKey: sessionID}, readID))

	for id, out := range map[string]string{g.Root(): "root prompt", readID: huge} {
		require.NoError(t, fabric.Create(&taskfabric.Task{ID: id, Capability: "x"}))
		driveTaskToCompleted(t, ctx, fabric, id, out)
	}

	planner, err := NewPlannerCognition(PlannerDeps{
		ChatClient:         &l1ChatAlwaysGrep{},
		ToolBinder:         &plannerTestBinder{},
		Sessions:           reg,
		Fabric:             fabric,
		Logger:             slog.Default(),
		HistoryObservation: ObservationReplayPolicy{RawRunes: 500},
	})
	require.NoError(t, err)

	task := models.NewTask(planNode, planAgentType, nil)
	task.SessionID = sessionID
	msgs, err := planner.(*plannerCognition).assembleContext(ctx, task, g, historyPlanning)
	require.NoError(t, err)
	require.Len(t, msgs, 3)

	assert.Equal(t, string(causalchain.NormalizeIntent(stated, intentMaxRunes)), msgs[1].Content,
		"the why is cheap and irreplaceable: it is never normalized away")
	assert.Contains(t, msgs[2].Content, "sha256:", "the observation is reduced to a replayable form")
	assert.Contains(t, msgs[2].Content, "ref:"+readID, "and says where the full text lives")
	assert.Less(t, len([]rune(msgs[2].Content)), 600, "normalized to the replayable minimum")
}

// TestIntentNeverReachesCallTool pins the same seam session_id crosses: the
// intent is causality metadata, so it must survive projecton onto the envelope
// (a replacement agent resumes the node and needs the why) WITHOUT entering the
// tool-argument namespace (a strict-schema MCP tool rejects undeclared args).
func TestIntentNeverReachesCallTool(t *testing.T) {
	ctx := context.Background()
	plan, err := NewL2Graph("sess/c1/root", "prompt", nil)
	require.NoError(t, err)
	require.NoError(t, plan.AddToolNode(ctx, "n1", "grep",
		map[string]any{"query": "x", intentKey: "because", planMetadataKey: "c1"}, "sess/c1/root"))

	step := plan.DAG().StepIndex()["n1"]
	require.Equal(t, "because", step.Metadata[intentKey], "the why is stored where the history rebuild reads it")
	require.NotContains(t, step.Metadata, argMetadataPrefix+intentKey,
		"an arg.-namespaced intent would reach CallTool as an undeclared argument")

	ps := planprojection.ProjectStep(step)
	require.Equal(t, "because", ps.Payload[intentKey], "the why rides the envelope to a replacement agent")

	binder := &strictArgBinder{}
	cog := NewRouterCognition(binder, slog.Default())
	task := models.NewTask("n1", "tool/grep", nil)
	task.Payload = ps.Payload
	_, err = cog.ExecuteStep(ctx, task)
	require.NoError(t, err, "intent must not reach CallTool as a tool argument")
	require.Equal(t, map[string]any{"query": "x"}, binder.got)
}

// strictArgBinder rejects any argument key it did not declare, like an MCP tool
// with additionalProperties:false.
type strictArgBinder struct{ got map[string]any }

func (b *strictArgBinder) CallTool(_ context.Context, name string, args map[string]any) (any, error) {
	for k := range args {
		if k != "query" {
			return nil, fmt.Errorf("strict: tool %s got undeclared arg %q", name, k)
		}
	}
	b.got = args
	return "ok", nil
}

func (b *strictArgBinder) ListTools() []string { return []string{"grep"} }

func (b *strictArgBinder) IsToolIdempotent(string) bool { return true }

func (b *strictArgBinder) GetToolSchemas() []resources.ToolSchema { return nil }

// TestEvidenceViewKeepsObservationsVerbatim is the other half of the split: the
// answer synthesizer writes the answer FROM the observations, so its context
// must not be normalized — same chain, different budget.
func TestEvidenceViewKeepsObservationsVerbatim(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fabric := taskfabric.NewFabric()
	reg := NewSessionRegistry()
	const sessionID = "causal-chain-evidence"
	g, err := reg.InitSession(sessionID, "root prompt", nil, nil)
	require.NoError(t, err)

	body := strings.Repeat("evidence line\n", 300)
	readID := SessionNodeID(sessionID, 1, "read", 0)
	answerNode := SessionNodeID(sessionID, 1, "answer", 0)
	require.NoError(t, g.AddToolNode(ctx, readID, "read",
		map[string]any{"path": "/p/e.txt", intentKey: "need the evidence", planMetadataKey: sessionID}, g.Root()))
	require.NoError(t, g.AddToolNode(ctx, answerNode, "answer",
		map[string]any{planMetadataKey: sessionID}, readID))

	for id, out := range map[string]string{g.Root(): "root prompt", readID: body} {
		require.NoError(t, fabric.Create(&taskfabric.Task{ID: id, Capability: "x"}))
		driveTaskToCompleted(t, ctx, fabric, id, out)
	}

	planner, err := NewPlannerCognition(PlannerDeps{
		ChatClient:         &l1ChatAlwaysGrep{},
		ToolBinder:         &plannerTestBinder{},
		Sessions:           reg,
		Fabric:             fabric,
		Logger:             slog.Default(),
		HistoryObservation: ObservationReplayPolicy{RawRunes: 100},
	})
	require.NoError(t, err)

	task := models.NewTask(answerNode, answerAgentType, nil)
	task.SessionID = sessionID
	msgs, err := planner.(*plannerCognition).assembleContext(ctx, task, g, historyEvidence)
	require.NoError(t, err)
	require.Len(t, msgs, 3)
	assert.Equal(t, body, msgs[2].Content, "the answer is written from the evidence: keep it verbatim")
	assert.Equal(t, "need the evidence", msgs[1].Content, "the why still rides the action")
}

// TestRationalePrefersTheReasoningTrace: on a tool-call turn a reasoning model
// states its intent in the thinking trace, not in Content. The trace is the
// cause; keeping it is the whole point of the causal chain.
func TestRationalePrefersTheReasoningTrace(t *testing.T) {
	t.Parallel()

	got := rationaleFrom(&llmcore.GenerateResponse{
		ReasoningContent: "I should\n\n  grep the parser first,\nbecause the lexer cannot reach it.",
	})
	assert.Equal(t, "I should grep the parser first, because the lexer cannot reach it.", got,
		"the trace is used as the intent, with layout whitespace collapsed")

	// Content still wins when the model stated both: it is what the model chose
	// to SAY, and it is the shorter, more deliberate statement.
	got = rationaleFrom(&llmcore.GenerateResponse{
		Content:          "I will grep.",
		ReasoningContent: "long internal deliberation",
	})
	assert.Equal(t, "I will grep.", got)
}

// TestObservationBudgetSpellings pins each policy spelling to exactly one
// behaviour, so no digit can select a policy by accident:
//
//	zero value                       → package default, large output normalized
//	RawRunes positive                → that many runes verbatim
//	Verbatim true                    → nothing normalized
//	RawRunes negative (a typo)       → treated as the default, NOT as verbatim
func TestObservationBudgetSpellings(t *testing.T) {
	big := strings.Repeat("d", 2500) // over the 2000-rune default

	for _, tc := range []struct {
		name          string
		policy        ObservationReplayPolicy
		wantNormalize bool
	}{
		{name: "zero value means default budget", policy: ObservationReplayPolicy{}, wantNormalize: true},
		{name: "verbatim switch disables normalization", policy: ObservationReplayPolicy{Verbatim: true}, wantNormalize: false},
		{name: "budget larger than output means verbatim", policy: ObservationReplayPolicy{RawRunes: 5000}, wantNormalize: false},
		{name: "negative budget falls back to default, not verbatim", policy: ObservationReplayPolicy{RawRunes: -1}, wantNormalize: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			fabric := taskfabric.NewFabric()
			reg := NewSessionRegistry()
			sessionID := "budget-" + strings.ReplaceAll(tc.name, " ", "-")
			g, err := reg.InitSession(sessionID, "root prompt", nil, nil)
			require.NoError(t, err)

			readID := SessionNodeID(sessionID, 1, "read", 0)
			planNode := SessionNodeID(sessionID, 1, "plan", 0)
			require.NoError(t, g.AddToolNode(ctx, readID, "read",
				map[string]any{"path": "/b.go", intentKey: "because", planMetadataKey: sessionID}, g.Root()))
			require.NoError(t, g.AddToolNode(ctx, planNode, "plan",
				map[string]any{planMetadataKey: sessionID}, readID))
			for id, out := range map[string]string{g.Root(): "root prompt", readID: big} {
				require.NoError(t, fabric.Create(&taskfabric.Task{ID: id, Capability: "x"}))
				driveTaskToCompleted(t, ctx, fabric, id, out)
			}

			planner, err := NewPlannerCognition(PlannerDeps{
				ChatClient:         &l1ChatAlwaysGrep{},
				ToolBinder:         &plannerTestBinder{},
				Sessions:           reg,
				Fabric:             fabric,
				Logger:             slog.Default(),
				HistoryObservation: tc.policy,
			})
			require.NoError(t, err)

			task := models.NewTask(planNode, planAgentType, nil)
			task.SessionID = sessionID
			msgs, err := planner.(*plannerCognition).assembleContext(ctx, task, g, historyPlanning)
			require.NoError(t, err)
			require.Len(t, msgs, 3)

			if tc.wantNormalize {
				assert.Contains(t, msgs[2].Content, "sha256:")
				assert.Less(t, len([]rune(msgs[2].Content)), len([]rune(big)))
			} else {
				assert.Equal(t, big, msgs[2].Content)
			}
			assert.Equal(t, "because", msgs[1].Content, "the why is preserved in every spelling")
		})
	}
}
