package agentfabric

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Timwood0x10/ares/internal/causalchain"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
	llmcore "github.com/Timwood0x10/ares/internal/llmcore"
	"github.com/Timwood0x10/ares/internal/truncate"
)

// historyMode selects how much of each observation the rebuilt history carries.
//
// The two callers need different halves of the same chain, so they get
// different budgets rather than one compromised middle:
//
//   - the PLANNER decides the next action, so it needs the causal chain
//     (why → did → saw) at a cost it can afford on every quantum: observations
//     over the verbatim budget (defaultObservationRawBudgetRunes unless
//     ObservationReplayPolicy says otherwise) are normalized to a replayable
//     minimum.
//   - the ANSWER synthesizer writes the answer FROM the observations, so it
//     keeps them verbatim — normalizing there would be compression that
//     destroys its own input.
type historyMode int

const (
	// historyPlanning is the planner's causal view.
	historyPlanning historyMode = iota
	// historyEvidence is the answer synthesizer's verbatim view.
	historyEvidence
)

// defaultObservationRawBudgetRunes is how much of a tool output the planner's
// history keeps verbatim before switching to the normalized form. Chosen as a
// product decision: large enough that ordinary results (a grep hit, a short
// file, an exit code block) arrive intact, small enough that reading a whole
// source file cannot dominate ten quanta of history.
const defaultObservationRawBudgetRunes = 2000

// observationBudgetDefault is the PlannerDeps spelling of "use
// defaultObservationRawBudgetRunes". It is the ONLY place the deps-side zero
// needs a name: the renderer's "never normalize" case is
// causalchain.ObservationBudgetVerbatim and the evidence view uses that same
// constant, so no bare 0 stands for two meanings any more.
const observationBudgetDefault = 0

// ObservationReplayPolicy says how much of each observation the planner's
// rebuilt history replays verbatim.
//
// It is a struct rather than one int because the knob has two independent
// meanings — "how big is the verbatim window" and "is there a window at all" —
// and encoding the second as a negative number made a mistyped budget flip
// compression off silently. Here each meaning has its own field, and the zero
// value stays the useful default.
type ObservationReplayPolicy struct {
	// RawRunes is the verbatim budget per observation, in runes. 0 = the
	// package default (defaultObservationRawBudgetRunes). Only positive values
	// are meaningful: there is no negative sentinel — use Verbatim.
	RawRunes int
	// Verbatim disables normalization entirely: every output is replayed
	// exactly as it came back. Explicit, so no policy is selected by the value
	// of a number.
	Verbatim bool
}

// BudgetRunes resolves the policy into the budget causalchain.RenderObservation
// expects: a positive rune count, or ObservationBudgetVerbatim.
func (p ObservationReplayPolicy) BudgetRunes() int {
	if p.Verbatim {
		return causalchain.ObservationBudgetVerbatim
	}
	if p.RawRunes > observationBudgetDefault {
		return p.RawRunes
	}
	// Zero AND negatives land here: a negative budget is a wiring mistake, and
	// silently reading it as "verbatim" is how a typo turned compression off.
	// The explicit switch above is the only way to ask for verbatim.
	return defaultObservationRawBudgetRunes
}

// assembleContext builds the LLM message list from the predecessor path:
// the session prompt (root output) + the outputs of every tool node on the
// path from root to this plan node. Outputs are read from the fabric task
// envelopes by node ID = task ID join (nodes carry no Output of
// their own).
func (c *plannerCognition) assembleContext(ctx context.Context, task *models.Task, g *L2Graph, mode historyMode) ([]*llmcore.LLMMessage, error) {
	// Read the session prompt from the root task's envelope.
	rootID := g.Root()
	rootPrompt, err := c.readNodeOutput(rootID)
	if err != nil || strings.TrimSpace(rootPrompt) == "" {
		// The root may not have completed yet (readNodeOutput reports an
		// incomplete task as empty, not as an error), or it completed
		// empty. Either way the payload's "input" is the prompt the
		// admission path stamped — never send an empty user message, real
		// providers reject it.
		//
		// The payload's "input" is asserted strictly: the admission path
		// (submitPeerTask → ensureSessionAdmission) always stamps a string
		// input, so a missing or non-string value is a wiring bug, not a
		// data condition — surface it as an error (the fabric retries the
		// task) instead of silently planning on an empty prompt.
		fallback, ok := task.Payload["input"].(string)
		if !ok {
			return nil, fmt.Errorf("agentfabric: planner cognition: task %q payload has no string %q (root %q unreadable: %v)",
				task.TaskID, "input", rootID, err)
		}
		rootPrompt = fallback
	}

	messages := []*llmcore.LLMMessage{
		{Role: roleUser, Content: rootPrompt},
	}

	// Collect this plan node's full ancestor set (BFS over ALL DependsOn
	// edges, nearest-first). The walk is nearest-first, so the collected
	// nodes are REVERSED before being appended: the LLM must observe tool
	// results in execution order, the same order ReAct's Messages[] presented
	// them. Appending in walk order would invert the history and change what
	// the model concludes.
	chain := g.Ancestors(task.TaskID)
	steps := g.DAG().StepIndex()
	// The evidence view uses ObservationBudgetVerbatim: the answer is written
	// FROM the outputs, so normalizing them would compress the answer's own
	// input. Named, not a literal 0 — see observationRawBudget for why the same
	// digit means something different in PlannerDeps.
	observationBudget := causalchain.ObservationBudgetVerbatim
	if mode == historyPlanning {
		observationBudget = c.observationRawBudget()
	}
	// lastIntent collapses the repeats: one response's rationale is stamped on
	// every action it grew (each node must be self-describing), but replaying
	// the same why before each of its three calls would pay for it three times.
	// The FIRST intent of a run survives; the repeats render as empty content,
	// which is exactly what the provider saw on the original turn.
	var lastIntent causalchain.Intent
	for i := len(chain) - 1; i >= 0; i-- {
		nodeID := chain[i]
		step, ok := steps[nodeID]
		if !ok || !strings.HasPrefix(step.AgentType, "tool/") {
			continue
		}
		output, err := c.readNodeOutput(nodeID)
		switch {
		case err != nil:
			// A predecessor whose task is gone is a HOLE in the history, not
			// an empty step: the envelope is the only place output lives
			// (decision C), so losing it silently would let the model plan on
			// a truncated past. Surface it and keep walking.
			c.logger.Warn("planner: predecessor output unreadable; context has a hole",
				"node", nodeID, "error", err)
		case output != "":
			unit := historyUnit(step, nodeID)
			unit.Observation = causalchain.Observe(unit.Action.Tool, unit.Action.Args, output, nodeID)
			// The intent is replayed even when the observation is large: the
			// why is cheap and irreplaceable, the observation is neither.
			if unit.Intent != "" && unit.Intent == lastIntent {
				unit.Intent = ""
			} else if unit.Intent != "" {
				lastIntent = unit.Intent
			}
			messages = append(messages, toolHistoryPair(unit, unit.Observation.RenderObservation(output, observationBudget))...)
		}
	}

	// The previous quantum's rationale (cognition persistence, A6 follow-up):
	// the tool history above says WHAT ran, in order; this says WHY the previous
	// quantum chose that order — the one thing a REPLACEMENT agent, the default
	// outcome of an agent death, cannot reconstruct from the graph.
	//
	// It is read from THIS task's own args, not from an ancestor's checkpoint:
	// the quantum that decided it stamped it onto the plan node it grew for the
	// next round, and a replacement re-running this same task therefore finds it
	// right here (see growToolNodes). Absent — the common case for providers that
	// send no content alongside tool calls — means no message is added at all:
	// no placeholder, and no invented rationale.
	if r := rationaleFromPayload(task.Payload); r != "" {
		messages = append(messages, &llmcore.LLMMessage{
			Role:    roleSystem,
			Content: predecessorRationaleHeader + r,
		})
	}

	// M4.3 read side: the executing agent's distilled experience prior rides
	// as the FIRST message, ahead of the root prompt and the strategy
	// template appended by ExecuteStep — early grounding, late steering.
	if sys := c.experiencePriorSystemMessage(task); sys != nil {
		messages = append([]*llmcore.LLMMessage{sys}, messages...)
	}

	// Degraded-inputs disclosure (closure of the AllowPartial contract): when
	// an AllowPartial task proceeds without a permanently-failed predecessor,
	// the fabric mirrors that predecessor's ID into the task payload under
	// degraded_inputs. Surface it to the model so it reasons WITH the gap
	// ("input X is missing") instead of silently assuming every input is
	// present — otherwise degraded_inputs is a write-only field and the task
	// plans on a truncated reality.
	//
	// Role: system, appended after the tool history. This matches the planner's
	// existing convention for trailing system hints (the L1 priors and the
	// evolution-strategy template that ExecuteStep appends next), so the
	// prefix stays cache-stable. If a strict system-must-be-first provider is
	// targeted, fold these trailing hints into the leading system block
	// together — do not special-case this one message.
	if gaps := degradedInputsFromPayload(task.Payload); len(gaps) > 0 {
		messages = append(messages, &llmcore.LLMMessage{
			Role: roleSystem,
			Content: "degraded inputs — the following prerequisites failed permanently and are MISSING; " +
				"reason about their absence, do not assume their output exists:\n- " + strings.Join(gaps, "\n- "),
		})
	}

	return messages, nil
}

// degradedInputsFromPayload extracts the degraded_inputs id list the fabric
// mirrors into an AllowPartial task's payload. The checkpoint envelope round-
// trips through JSON, so the slice may arrive as []string (in-process) or
// []any of strings (after decode); both are handled. A missing key or wrong
// type yields nil; an empty list yields a zero-length slice, not nil — either
// way the caller's len>0 check injects nothing, so a task with no gap is never
// announced as degraded.
func degradedInputsFromPayload(payload map[string]any) []string {
	if payload == nil {
		return nil
	}
	raw, ok := payload[taskfabric.PayloadKeyDegradedInputs]
	if !ok {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		ids := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok && s != "" {
				ids = append(ids, s)
			}
		}
		return ids
	default:
		return nil
	}
}

// assembleAnswerMessages rebuilds the session's LLM context for the answer
// body's synthesis call (M4.2): the same message list the planner would send
// on its next quantum — root prompt, tool history (rebuilt as
// assistant+tool pairs from the fabric envelopes), experience prior —
// assembled from the ANSWER task's own predecessor chain. The answer node IS
// a graph node (node id = task id), so the same walk that feeds the planner
// feeds the synthesizer; non-tool nodes on the chain (plan nodes) are
// skipped by assembleContext already.
//
// It delegates to assembleContext wholesale so the synthesizer sees EXACTLY
// what the planner sees — a divergent context would make the synthesized
// answer inconsistent with the plan that produced it. The context is bounded
// by the same growth-depth guard that bounds every planner quantum (no
// extra truncation: parity with the planner's own view is the contract).
//
// The bool (not error) contract is deliberate: an assembly miss is a
// degraded condition (session already released, root prompt unreadable),
// not a retriable quantum failure — the answer body falls back to the gap
// body instead of letting the fabric burn retry budget on a failure no
// retry can fix.
func (c *plannerCognition) assembleAnswerMessages(ctx context.Context, task *models.Task) ([]*llmcore.LLMMessage, bool) {
	g, err := c.sessions.GetSession(task.SessionID)
	if err != nil {
		// Most commonly the session was already released (duplicate
		// answer execution): there is no history left to synthesize
		// from, which is a gap, not a failure.
		c.logger.Warn("answer synthesis: session graph unavailable",
			"session", task.SessionID, "task", task.TaskID, "error", err)
		return nil, false
	}
	msgs, err := c.assembleContext(ctx, task, g, historyEvidence)
	if err != nil {
		c.logger.Warn("answer synthesis: context assembly failed",
			"session", task.SessionID, "task", task.TaskID, "error", err)
		return nil, false
	}
	return msgs, true
}

// experiencePriorSystemMessage renders the EXECUTING agent's distilled
// experience prior as the leading system message. The join key is the
// executingAgentKey stamp Agent.ExecuteStep puts on the task payload (the
// planner is shared across agents; the task is the only carrier of the
// executor's identity). Every failure mode — no fabric wired, no stamp
// (planner invoked directly), unknown agent, empty/unrenderable prior —
// degrades to nil: a missing prior must change nothing (zero-value usable),
// never fail the quantum.
func (c *plannerCognition) experiencePriorSystemMessage(task *models.Task) *llmcore.LLMMessage {
	if c.agentFabric == nil {
		return nil
	}
	agentID, _ := task.Payload[executingAgentKey].(string)
	if agentID == "" {
		return nil
	}
	cs, err := c.agentFabric.CognitiveState(agentID)
	if err != nil {
		// The executing agent vanished from the fabric (killed mid-lease):
		// log and plan without a prior — the quantum is still runnable.
		c.logger.Warn("planner: executing agent unreadable; planning without experience prior",
			"agent", agentID, "error", err)
		return nil
	}
	prior := truncate.WithEllipsis(renderExperiencePrior(cs.Context), maxExperiencePriorRunes)
	if prior == "" {
		return nil
	}
	return &llmcore.LLMMessage{
		Role:    roleSystem,
		Content: experiencePriorPrefix + prior,
	}
}

// renderExperiencePrior renders a cognitive Context value into prompt text.
// The production write side (cmd/ares loadExperiencePrior) stores a
// {type, problem, solution, constraints} map, which is JSON-encoded for
// readability; a plain-string prior (tests, Recover-restored states) is used
// as-is. Unrenderable values yield "" — the prior is informative, never
// load-bearing.
func renderExperiencePrior(v any) string {
	switch p := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(p)
	default:
		buf, err := json.MarshalIndent(p, "", "  ")
		if err != nil {
			return ""
		}
		return string(buf)
	}
}

// historyUnit reads one executed tool node back into the causal atom it was:
// its stored intent (why), its call (what), and the edge to the node it
// depended on. The observation is filled by the caller, which is the only place
// holding the output bytes.
func historyUnit(step *engine.Step, nodeID string) causalchain.Unit {
	meta := make(map[string]any, len(step.Metadata))
	for k, v := range step.Metadata {
		meta[k] = v
	}
	unit := causalchain.Unit{
		NodeID: nodeID,
		Intent: causalchain.Intent(intentFromStep(step)),
		Action: causalchain.Action{
			Tool:   strings.TrimPrefix(step.AgentType, "tool/"),
			CallID: nodeID,
			Args:   argsFromPayload(meta),
		},
	}
	if len(step.DependsOn) > 0 {
		unit.Parent = step.DependsOn[0]
	}
	return unit
}

// toolHistoryPair renders one causal unit as the assistant+tool message pair the
// Chat API requires: the assistant message carries the original tool call
// (reconstructed from the node's arg.-namespaced Metadata) AND the intent that
// produced it, and the tool message carries the observation, linked by the node
// ID as the tool-call ID.
//
// Content is no longer hardcoded empty. That was the single point where the
// model's thinking was discarded: the L2 graph rebuild replays actions, so an
// assistant message with an empty body says "a call happened" and cannot say
// why — and a successor quantum reading ten such pairs sees a sequence of
// effects with no reasons, which is precisely the causal chain the system is
// required to keep. The intent now rides the message it belongs to, in
// execution order, instead of only arriving as one trailing system note about
// the immediately preceding quantum.
//
// A bare tool message with no preceding assistant tool_call violates the
// provider contract (OpenAI rejects orphan tool messages; lenient providers
// accept them but the model loses track of what it already did — observed
// live as the same grep call repeated every round until the depth cap).
// ReAct never has this problem because its history IS the live conversation
// that produced the calls; the planner rebuilds history from the graph, so
// it must rebuild the pairing too.
func toolHistoryPair(unit causalchain.Unit, observation string) []*llmcore.LLMMessage {
	// Args came out of a JSON decode, so a marshal failure here is theoretical
	// (map keys are strings, values are decoded JSON). The "{}" fallback is a
	// last resort, not a policy: an unrenderable argument set must not take the
	// whole pair down, but it does mean this call loses its detail.
	argsJSON, err := json.Marshal(unit.Action.Args)
	if err != nil || len(unit.Action.Args) == 0 {
		argsJSON = []byte("{}")
	}
	return []*llmcore.LLMMessage{
		{
			Role:    "assistant",
			Content: string(unit.Intent),
			ToolCalls: []llmcore.ToolCall{{
				ID:   unit.Action.CallID,
				Type: toolTypeFunction,
				Function: llmcore.FunctionCall{
					Name:      unit.Action.Tool,
					Arguments: string(argsJSON),
				},
			}},
		},
		{Role: roleTool, Content: observation, ToolCallID: unit.Action.CallID},
	}
}

// observationRawBudget resolves the planner's configured replay policy. A nil
// cognition (defensive: tests construct partial planners) falls back to the
// zero policy, i.e. the package default.
func (c *plannerCognition) observationRawBudget() int {
	if c == nil {
		return ObservationReplayPolicy{}.BudgetRunes()
	}
	return c.historyObservation.BudgetRunes()
}

// checkpointDecoder is the optional observed read-back surface. FabricReader
// deliberately stays minimal (tests use small fakes); the production fabric
// implements this method, so its reads count toward the consistency metric.
type checkpointDecoder interface {
	DecodeCheckpoint(cp any) (taskfabric.DecodedCheckpoint, error)
}

func decodeNodeCheckpoint(f FabricReader, cp any) (taskfabric.DecodedCheckpoint, error) {
	if decoder, ok := f.(checkpointDecoder); ok {
		return decoder.DecodeCheckpoint(cp)
	}
	return taskfabric.DecodeCheckpoint(cp)
}

// readNodeOutput reads one node's execution output from its fabric task
// envelope by node ID = task ID join. Returns "" when the task is not
// found or has no output yet (e.g. not yet completed).
func (c *plannerCognition) readNodeOutput(nodeID string) (string, error) {
	tk, err := c.fabric.Task(nodeID)
	if err != nil {
		return "", err
	}
	if tk.State != taskfabric.StateCompleted {
		return "", nil
	}
	dc, err := decodeNodeCheckpoint(c.fabric, tk.Checkpoint)
	if err != nil {
		return "", err
	}
	return extractOutputContent(dc.StepCheckpoint), nil
}
