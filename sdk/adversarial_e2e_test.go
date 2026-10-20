package sdk

// adversarial_e2e_test.go is the end-to-end proof for defect P0-1(ii): a REAL
// session run through the SDK's own L2 core produces an answer whose provenance
// the runtime records, the deterministic floor scores it accordingly, and the
// fitness aggregator reads that number out of the evidence — no injected
// provenance string anywhere in the chain.
//
// The harness respects the architectural lock that NewExecution may only be built
// at the three known sites: it reuses the core the SDK already assembles
// (ensureL2) instead of constructing one.
//
// THREE cases run here — the gap ending, the depth-capped ending and the control.
// All three are produced by the runtime, never faked: the only inputs the harness
// controls are the model's scripted turns and the supported seams (a tool
// registered before the L2 core is assembled, the plan-depth bound), which is the
// whole point of this file.
//
// The truncated case was skipped for a while, and the reason turned out to be a
// DEFECT rather than a missing fixture (see its own doc comment): the depth-guard
// marker never survived the answer node's stringly-typed arg wire, so the
// depth-capped tier did not exist in production at all.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/agentruntime"
	tools "github.com/Timwood0x10/ares/internal/apitools"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/evidence"
	llmcore "github.com/Timwood0x10/ares/internal/llmcore"
	evolution "github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
	"github.com/Timwood0x10/ares/internal/runtime/eval"
	"github.com/Timwood0x10/ares/internal/tools/resources/base"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// generousJudge is a rubric judge that loves every answer (all dimensions at
// their ceiling). It is deliberately maximally generous: the floor is only
// proven if it overrules a judge that would otherwise score a degraded ending
// full marks.
const generousJudge = `{"correctness": 3, "completeness": 3, "efficiency": 2, "safety": 2, "reason": "reads well"}`

// scriptedJudge is an eval.LLMClient (one method) returning a fixed rubric.
type scriptedJudge struct{ response string }

func (s *scriptedJudge) Generate(context.Context, string) (string, error) { return s.response, nil }

// echoToolName is the tool the truncated case's model keeps asking for.
const echoToolName = "echo"

// echoTool is the minimal EXECUTABLE tool the truncated case needs: without one
// the planner never grows a tool node, so the session answers on the first round
// and the growth bound is never reached (measured — that is why this tool
// exists).
//
// It adapts to the SDK REGISTRY's contract (string capabilities, map parameters,
// apitools.Result) rather than the resources/core one, because the registry is
// what the planner's binder is bridged from. Name and Description come from the
// embedded BaseTool.
type echoTool struct{ *base.BaseTool }

func newEchoTool() *echoTool {
	return &echoTool{BaseTool: base.NewBaseToolWithCapabilities(
		echoToolName,
		"Echo the given text back",
		core.CategoryExternal,
		[]core.Capability{core.CapabilityExternal},
		nil,
	)}
}

func (e *echoTool) Parameters() map[string]any { return nil }

func (e *echoTool) Capabilities() []string { return []string{"external"} }

func (e *echoTool) Execute(context.Context, map[string]any) (tools.Result, error) {
	return tools.Result{Success: true, Data: map[string]any{"echo": "ok"}}, nil
}

// registerEchoTool puts the tool in the runtime registry BEFORE the L2 core is
// assembled, which is the only moment that makes it both visible to the planner
// (the binder bridges the registry then) and runnable (the scheduler's executors
// are built from the same registry at that point).
func registerEchoTool(rt *Runtime) {
	if err := rt.toolReg.Register(newEchoTool()); err != nil {
		panic("register echo tool: " + err.Error())
	}
}

// runAdversarialCase drives ONE case through a real SDK session and returns the
// judge's scores next to the evidence the run left in the store.
//
// Each case gets its OWN evidence store: the aggregator averages every
// dimension_eval record in the window, so sharing one store between the gap case
// and its control would average 0 with 1 and prove nothing about either.
func runAdversarialCase(t *testing.T, caseID string, sdkResponses []*llmcore.GenerateResponse, prepare func(*Runtime), extraOpts ...Option) (eval.EvalScore, *evolution.WindowResult, eval.TestResult) {
	t.Helper()
	ctx := context.Background()

	opts := append([]Option{WithOllama("llama3.2"), WithoutMemory(), WithTrace(false)}, extraOpts...)
	rt := NewRuntime(opts...)
	// prepare runs BEFORE the L2 core is assembled: the registry is bridged into
	// the planner's binder (and the scheduler's executors are built) at that
	// moment, so a tool registered afterwards would be invisible or unrunnable.
	if prepare != nil {
		prepare(rt)
	}
	t.Cleanup(rt.Close)
	rt.llmSvc = &mockLLMSvc{responses: sdkResponses}

	execCore := rt.ensureL2()
	require.NotNil(t, execCore, "the SDK must assemble its L2 core for a real run")
	exec, err := agentruntime.NewSessionEvalExecutor(execCore, agentruntime.AwaitOptions{Wait: 60 * time.Second})
	require.NoError(t, err)

	store := evidence.NewMemoryStore()
	judge, err := eval.NewLLMJudgeEvaluator(&scriptedJudge{response: generousJudge},
		eval.WithDimensionAveraging(), eval.WithEvidenceStore(store))
	require.NoError(t, err)
	registry := eval.NewEvaluatorRegistry()
	require.NoError(t, registry.Register("llm_judge", judge))

	runner, err := eval.NewAgentTestRunner(exec)
	require.NoError(t, err)
	runner.SetRegistry(registry)

	loaded, err := eval.NewLoader().Load("../test/eval/adversarial.yaml")
	require.NoError(t, err)
	var target *eval.TestCase
	for i := range loaded.TestCases {
		if loaded.TestCases[i].ID == caseID {
			target = &loaded.TestCases[i]
		}
	}
	require.NotNil(t, target, "the corpus must declare case %s", caseID)

	// One case per run, through the shared runner: it records the executor's
	// provenance on the result and then judges it.
	suite := eval.TestSuite{Name: caseID, TestCases: []eval.TestCase{*target}}
	results, scores, err := runner.RunAndEvaluate(ctx, suite, "llm_judge")
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Len(t, scores, 1)
	require.Len(t, scores[0], 1)

	agg := evolution.NewRuntimeFitnessAggregator(store, evolution.DefaultAggregatorConfig())
	window := agg.Window(ctx, "")
	return scores[0][0], &window, results[0]
}

// TestAdversarialE2E_GapEndingIsFlooredAndVisible runs the gap case for real: the
// scripted model never produces content and never stops, so the planner's answer
// node carries no body and the runtime records body source gap.
func TestAdversarialE2E_GapEndingIsFlooredAndVisible(t *testing.T) {
	// Empty content on every call: no tool calls, no final answer, and nothing
	// for synthesis to work with -> the gap ending. The queue is long on purpose
	// (the mock's fallback is a non-empty string, which would turn the gap into a
	// supplied answer).
	empty := make([]*llmcore.GenerateResponse, 32)
	for i := range empty {
		empty[i] = &llmcore.GenerateResponse{}
	}

	scores, window, result := runAdversarialCase(t, "adv-gap-001", empty, nil)

	assert.Equal(t, ares_events.AnswerBodySourceGap, result.AnswerBodySource,
		"the runtime must record the gap ending the model produced")
	require.NotNil(t, window.PerSource)

	// The threshold is the SHARED rule, not a literal: gap scores its penalty.
	gapFloor, degraded := ares_events.AnswerQualityPenalty(ares_events.AnswerBodySourceGap, false)
	require.True(t, degraded)

	stat, ok := window.PerSource["dimension_eval"]
	require.True(t, ok, "the accuracy signal must reach the aggregator (P0-1(ii))")
	assert.LessOrEqual(t, stat.Mean, gapFloor+0.05,
		"a gap ending must not be rewarded, however well the judge liked its text")
	assert.LessOrEqual(t, scores.Score, gapFloor+0.05,
		"and the floor must be visible in the score handed back, not only in the evidence")
}

// TestAdversarialE2E_ControlEndingKeepsItsScore is the control the suite demands:
// the SAME wiring must score a genuine answer high. Without it, every assertion
// above would also pass for an implementation that simply scored everything low.
func TestAdversarialE2E_ControlEndingKeepsItsScore(t *testing.T) {
	scores, window, result := runAdversarialCase(t, "adv-control-001", []*llmcore.GenerateResponse{
		{Content: "ready"},
	}, nil)

	assert.Equal(t, ares_events.AnswerBodySourceSupplied, result.AnswerBodySource,
		"a genuine answer is recorded as supplied")
	require.NotNil(t, window.PerSource)

	stat, ok := window.PerSource["dimension_eval"]
	require.True(t, ok, "the control's accuracy evidence must reach the aggregator too")
	assert.Greater(t, stat.Mean, 0.8, "a genuine answer must keep the judge's score")
	assert.Greater(t, scores.Score, 0.8)
}

// TestAdversarialE2E_TruncatedEndingTakesTheCheaperTier runs the depth-capped case
// for real: the session iterates, the planner meets its growth bound, and the
// ending is reported, floored and aggregated with no injected provenance.
//
// Reaching the bound takes a session that ITERATES, which needs the requested tool
// node to actually run (with nothing to run, the planner answers on the first
// round). So the case registers a real tool before the L2 core is assembled and
// keeps asking for it until the bound forces the answer.
//
// The remaining obstacle was a DEFECT, not a fixture gap (P0-1(ii), second half,
// found by the audit that asked why this test was skipped): the depth guard
// stamped its marker into the answer node's args, but node args are stored
// STRINGIFIED (the engine's node metadata is a map[string]string) while the reader
// asserted .(bool) against that string. The marker was therefore written by every
// depth-capped session and read by none — the truncated tier was unreachable in
// production, in the fitness observer AND in the eval floor, and this test could
// not observe the tier it exists to pin. It passes unchanged now that
// answerArgTruncated reads the shape the wire carries; it is the end-to-end guard
// for that fix.
//
// The assertion is the MIDDLE tier on purpose: a truncated answer must score
// between the gap penalty and a genuine answer. Collapsing it to zero would erase
// the distinction between "produced nothing" and "something real came out under
// duress" — the distinction the shared rule encodes.
func TestAdversarialE2E_TruncatedEndingTakesTheCheaperTier(t *testing.T) {
	// The tool must EXECUTE for the session to iterate at all (see above).
	toolCall := &llmcore.GenerateResponse{
		ToolCalls: []llmcore.ToolCall{{
			ID: "call-1", Type: "function", Function: llmcore.FunctionCall{Name: echoToolName},
		}},
	}
	// MEASURED call order with MaxPlanDepth(1), so the script is exactly the two
	// calls the run makes rather than a queue it happens to walk past:
	//   #1 the planner's round-1 turn: requests the tool, so round 2 meets the
	//      bound — the guard is checked BEFORE the model is called, which is why
	//      there is no second planner turn here;
	//   #2 the answer's synthesis call: the depth-capped node is grown content-LESS
	//      on purpose, so its body can only come from synthesis. An empty response
	//      here lands the ending in the GAP tier (penalty 0, which outranks the
	//      truncation marker) and the case would assert the wrong tier.
	responses := []*llmcore.GenerateResponse{
		toolCall,
		{Content: "the synthesised answer"},
	}

	scores, window, result := runAdversarialCase(t, "adv-trunc-001", responses, registerEchoTool, WithMaxPlanDepth(1))

	assert.True(t, result.Truncated, "the depth guard must have forced the answer")
	require.NotNil(t, window.PerSource)

	stat, ok := window.PerSource["dimension_eval"]
	require.True(t, ok, "the depth-capped ending must leave accuracy evidence too")

	truncatedFloor, degraded := ares_events.AnswerQualityPenalty("", true)
	require.True(t, degraded, "a truncated answer is a degraded answer")
	assert.LessOrEqual(t, stat.Mean, truncatedFloor+0.05,
		"a depth-capped answer must not keep a full score")
	assert.GreaterOrEqual(t, stat.Mean, 0.05,
		"and it must stay ABOVE the gap tier: it produced something real")
	assert.LessOrEqual(t, scores.Score, truncatedFloor+0.05)
}

// TestWithMaxPlanDepth_Contract pins the seam's VALIDATION — the half that does
// not need a session. The other half, the bound's effect on a real session, used
// to be asserted here as unreachable; it is not, and it is covered above by
// TestAdversarialE2E_TruncatedEndingTakesTheCheaperTier, which drives a session
// to the bound for real. Kept separate on purpose: this test must stay green even
// if the L2 wiring changes, because it is about the option, not the runtime.
func TestWithMaxPlanDepth_Contract(t *testing.T) {
	require.Error(t, WithMaxPlanDepth(-1)(&config{}), "a negative depth is refused rather than clamped")

	cfg := &config{}
	require.NoError(t, WithMaxPlanDepth(3)(cfg))
	assert.Equal(t, 3, cfg.maxPlanDepth)

	// Zero is the documented "planner default" and must stay accepted.
	require.NoError(t, WithMaxPlanDepth(0)(&config{}))
}
