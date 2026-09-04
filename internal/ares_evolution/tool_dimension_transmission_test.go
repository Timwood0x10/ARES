package evolution

import (
	"context"
	"testing"

	ares_events "github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
	"github.com/Timwood0x10/ares/internal/ares_evolution/scoring"
	"github.com/Timwood0x10/ares/internal/evidence"
	"github.com/Timwood0x10/ares/internal/toolprojection"
)

// This file holds the end-to-end assertion demanded by the Y.1 design review
// (§9.5, "minimum acceptance signal"):
//
//	Two strategies that differ ONLY in Params["tools"] must end up with a
//	DIFFERENT aggregate fitness, and that difference must be readable through
//	RuntimeFitnessAggregator.
//
// The reason this needs a dedicated test rather than trusting the pieces: the
// transmission chain has four independent links, and a break in ANY of them
// silently collapses both variants onto one score — evolution then keeps
// mutating the tool dimension while selection is blind to it, and every
// existing test stays green because each link is individually correct.
//
// The four links, asserted below in order:
//  1. identity — StrategyHash must separate the two, or the child hits the
//     parent's ScoreCache entry and inherits its score.
//  2. attribution — ComputeEvidenceKey must separate them, or their tool_call
//     evidence merges under one key.
//  3. armed channel — the tool_call weight must be > 0, or the channel is
//     inert by design (default 0) and the evidence cannot move anything.
//  4. aggregation — Window must actually report the difference.

// toolVariant builds a strategy that is identical to its sibling in every
// behaviorally relevant field EXCEPT the tool whitelist. Anything that differs
// beyond `tools` would make the test prove the wrong thing.
func toolVariant(id, tools string) *mutation.Strategy {
	return &mutation.Strategy{
		ID:             id,
		Version:        1,
		PromptTemplate: "identical prompt for both variants",
		Params: map[string]any{
			"temperature": 0.7,
			"top_k":       5,
			"tools":       tools,
		},
	}
}

// TestToolDimension_TransmitsToFitness is the Y.1 §9.5 acceptance signal. It
// walks the full chain from "the genome differs" to "the aggregator reports a
// different number", so a regression in any single link fails here instead of
// silently zeroing selection pressure on the tool dimension.
func TestToolDimension_TransmitsToFitness(t *testing.T) {
	goodTools := "web_search,calculator"
	badTools := "http_request"

	good := toolVariant("tools-good", goodTools)
	bad := toolVariant("tools-bad", badTools)

	// ── Link 1: identity. ────────────────────────────────────────────────
	// A shared hash means the ScoreCache (keyed by hash) hands the second
	// variant the first one's score, and the two become indistinguishable
	// before any evidence is even collected.
	goodHash, err := scoring.StrategyHash(good)
	if err != nil {
		t.Fatalf("hash good: %v", err)
	}
	badHash, err := scoring.StrategyHash(bad)
	if err != nil {
		t.Fatalf("hash bad: %v", err)
	}
	if goodHash == badHash {
		t.Fatalf("strategies differing only in Params[%q] share hash %d: the ScoreCache would return the sibling's score", "tools", goodHash)
	}

	// ── Link 2: attribution. ─────────────────────────────────────────────
	// A shared EvidenceKey merges the two variants' tool_call receipts, so a
	// good tool set and a bad one average into one indistinguishable pool.
	goodKey := good.ComputeEvidenceKey()
	badKey := bad.ComputeEvidenceKey()
	if goodKey == badKey {
		t.Fatalf("strategies differing only in Params[%q] share evidence key %q: their tool_call evidence would merge", "tools", goodKey)
	}

	// ── Link 3 + 4: armed channel and aggregation. ───────────────────────
	// Both variants look IDENTICAL on the task-outcome channel; the only
	// signal separating them is the tool_call channel. If the channel is
	// unarmed or the aggregator drops it, both means come out equal.
	store := evidence.NewMemoryStore()
	appendChannelFitness(t, store, "outcome-good", observerEvidenceSource, good.ID, 1.0)
	appendChannelFitness(t, store, "outcome-bad", observerEvidenceSource, bad.ID, 1.0)
	// The good tool set succeeds, the bad one fails.
	appendChannelFitness(t, store, "tool-good", toolCallEvidenceSource, good.ID, 1.0)
	appendChannelFitness(t, store, "tool-bad", toolCallEvidenceSource, bad.ID, 0.0)

	cfg := DefaultAggregatorConfig()
	cfg.MinSamplesBeforeJudge = 1
	// Arm the tool channel: it ships at weight 0 (opt-in), which is correct
	// as a default but means the tool dimension is INERT until an operator
	// turns it on. This line is what the Y.1 tool dimension depends on.
	cfg.Weights.ToolCall = 0.3
	agg := NewRuntimeFitnessAggregator(store, cfg)

	goodWin := agg.Window(context.Background(), good.ID)
	badWin := agg.Window(context.Background(), bad.ID)

	if !goodWin.Ok || !badWin.Ok {
		t.Fatalf("both windows must pass the judging gate: good.Ok=%v bad.Ok=%v", goodWin.Ok, badWin.Ok)
	}
	if _, ok := goodWin.PerSource[toolCallEvidenceSource]; !ok {
		t.Fatalf("armed tool_call channel missing from PerSource: the evidence never reached the aggregate")
	}
	if goodWin.Mean == badWin.Mean {
		t.Fatalf("tool dimension does not transmit: identical fitness %v for whitelists %q vs %q — GA cannot select on tool choice", goodWin.Mean, goodTools, badTools)
	}
	if goodWin.Mean <= badWin.Mean {
		t.Errorf("the succeeding tool set must score higher: good=%v bad=%v", goodWin.Mean, badWin.Mean)
	}
}

// TestToolDimension_UnarmedChannelIsInert states the flip side explicitly, so
// the zero default is understood as a deliberate gate rather than a bug: with
// Weights.ToolCall at its default 0, the very same evidence moves nothing. This
// is why the acceptance signal above has to arm the channel — and why shipping
// the tool dimension requires an operator-visible weight decision.
func TestToolDimension_UnarmedChannelIsInert(t *testing.T) {
	good := toolVariant("inert-good", "web_search")
	bad := toolVariant("inert-bad", "http_request")

	store := evidence.NewMemoryStore()
	appendChannelFitness(t, store, "outcome-good", observerEvidenceSource, good.ID, 1.0)
	appendChannelFitness(t, store, "outcome-bad", observerEvidenceSource, bad.ID, 1.0)
	appendChannelFitness(t, store, "tool-good", toolCallEvidenceSource, good.ID, 1.0)
	appendChannelFitness(t, store, "tool-bad", toolCallEvidenceSource, bad.ID, 0.0)

	cfg := DefaultAggregatorConfig()
	cfg.MinSamplesBeforeJudge = 1
	if cfg.Weights.ToolCall != 0 {
		t.Fatalf("tool_call weight must default to 0 (opt-in), got %v", cfg.Weights.ToolCall)
	}
	agg := NewRuntimeFitnessAggregator(store, cfg)

	goodWin := agg.Window(context.Background(), good.ID)
	badWin := agg.Window(context.Background(), bad.ID)
	if goodWin.Mean != badWin.Mean {
		t.Errorf("with the channel unarmed the tool difference must be invisible: good=%v bad=%v", goodWin.Mean, badWin.Mean)
	}
}

// TestToolStepMetadata_EndToEndClosedLoop is the C7 acceptance of the Y.1 plan:
// a metadata-only gene difference — the C4 作动面 (an evolved ToolStep node's
// budget/prior/enabled attribute) — must transmit all the way to a different
// selection signal, via the tool_step_id process attribution that the C1 event
// contract feeds and the C2 projection aggregates.
//
// The plan (§11 C7) phrases it as: two genes that differ only in ONE node's
// ToolStep.Metadata must split the transmission links AND let the GA promote
// the higher-success-rate side (needs tool_weight>0). Here we assert the
// observable backbone of that loop: distinct tool_step_ids from a differing
// metadata-driven tool set produce a distinct aggregated window (the G2 judge
// reads this), so evolution can select for the better process.
func TestToolStepMetadata_EndToEndClosedLoop(t *testing.T) {
	// REAL chain (C7 accepted as an integration test, not a hand-built one):
	// 1. C1 contract events (exactly what the three executors emit) land in the
	//    event store.
	// 2. Projector.Run (C2) reads them, projects ToolSteps, and writes each
	//    step's success rate as tool_call fitness evidence carrying tool_step_id.
	// 3. RuntimeFitnessAggregator.WindowToolStep (C3) reads the evidence and
	//    reports a HIGHER window for the higher-success-rate step.
	// The tool_step_id is produced by the projection from the event payload
	// (arg_shape), NOT hand-written into evidence — so a broken C1→C2→C3 link
	// fails here instead of being papered over.

	// C1 events: two strategies call the same tool with different argument
	// shapes. "web_search#q,k" (key set q,k) mostly succeeds; "web_search#k"
	// (key set k) always fails.
	es := ares_events.NewMemoryEventStore()
	events := []*ares_events.Event{
		toolCompletedEvent("agent-a", "web_search", `{"q":"a","k":1}`, true),
		toolCompletedEvent("agent-a", "web_search", `{"q":"b","k":2}`, true),
		toolCompletedEvent("agent-a", "web_search", `{"q":"c","k":3}`, false), // 2/3 success
		toolCompletedEvent("agent-b", "web_search", `{"k":9}`, false),
		toolCompletedEvent("agent-b", "web_search", `{"k":8}`, false),
	}
	for _, ev := range events {
		if err := es.Append(context.Background(), ev.StreamID, []*ares_events.Event{ev}, ev.Version); err != nil {
			t.Fatalf("append event: %v", err)
		}
	}

	// C2: project → write tool_call fitness evidence.
	evStore := evidence.NewMemoryStore()
	proj, err := toolprojection.NewProjector(es, evStore)
	if err != nil {
		t.Fatalf("NewProjector: %v", err)
	}
	written, err := proj.Run(context.Background(), toolprojection.Options{})
	if err != nil {
		t.Fatalf("Projector.Run: %v", err)
	}
	// Two distinct steps: web_search#q,k and web_search#k.
	if written != 2 {
		t.Fatalf("Projector wrote %d tool-step evidence records, want 2", written)
	}

	// C3: aggregate per tool_step_id and assert the higher-success step wins.
	// The projector assigns by agent/session (the C1 events carry agent_id, not
	// strategy_id), so the tool-step audit read is not strategy-scoped — pass an
	// empty strategyID and rely on the projection-level agent attribution.
	// NOTE: the toolStepID must be computed via ToolArgShape (keys are SORTED),
	// so {"q","k"} → "k,q"; hardcoding "q,k" would query a shape that the
	// projection never produced and vacuously pass.
	goodID := toolprojection.ToolStepID("web_search", ares_events.ToolArgShape(`{"q":"a","k":1}`))
	badID := toolprojection.ToolStepID("web_search", ares_events.ToolArgShape(`{"k":9}`))

	cfg := DefaultAggregatorConfig()
	cfg.MinSamplesBeforeJudge = 1
	cfg.Weights.ToolCall = 0.3 // GA must be armed to select on the tool dimension
	agg := NewRuntimeFitnessAggregator(evStore, cfg)

	goodWin := agg.WindowToolStep(context.Background(), "", goodID)
	badWin := agg.WindowToolStep(context.Background(), "", badID)

	if !goodWin.Ok || !badWin.Ok {
		t.Fatalf("both tool-step windows must pass the judge gate: good.Ok=%v bad.Ok=%v", goodWin.Ok, badWin.Ok)
	}
	if goodWin.Mean <= badWin.Mean {
		t.Fatalf("the higher-success-rate tool step must win selection: good=%v bad=%v (gene differs only in ToolStep.Metadata)", goodWin.Mean, badWin.Mean)
	}
}

// toolCompletedEvent builds a C1-contract EventToolCallCompleted the way the
// executors emit it (ares_events.ToolCompletedPayload.AsMap), so the projection
// reads the real key set rather than a test-only shape.
func toolCompletedEvent(agentID, tool, argsJSON string, ok bool) *ares_events.Event {
	shape := ares_events.ToolArgShape(argsJSON)
	return &ares_events.Event{
		Type:     ares_events.EventToolCallCompleted,
		StreamID: agentID,
		Payload: ares_events.ToolCompletedPayload{
			AgentID:     agentID,
			ToolName:    tool,
			ToolCallID:  "call-" + tool + "-" + shape,
			Round:       0,
			Seq:         0,
			Success:     ok,
			Error:       toolErrorMessage(ok),
			ArgShape:    shape,
			ExtraResult: "",
		}.AsMap(),
		ModuleName: "agentfabric",
	}
}

// toolErrorMessage mirrors the executor-side normalization for the C1 contract.
func toolErrorMessage(ok bool) string {
	if ok {
		return ""
	}
	return "boom"
}
