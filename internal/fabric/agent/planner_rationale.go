package agentfabric

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/Timwood0x10/ares/internal/llmcore"
	"github.com/Timwood0x10/ares/internal/truncate"
)

// Token-usage metadata keys on StepOutcome.Result.Metadata — the quantum's
// LLM usage report. The scheduler's buildQuantumStep reads these keys when
// re-wrapping the envelope (accumulating into InputTokens/OutputTokens), so
// the key names are the cross-package contract (kernel/…/scheduler.go
// mirrors them in tokenUsageFromResult).
const (
	resultMetaInputTokens  = "input_tokens"
	resultMetaOutputTokens = "output_tokens"
)

// tokenUsageMetadata extracts the response's token usage into result
// metadata. A nil response or zero usage yields nil — the scheduler's
// accumulate step treats a missing map as "nothing to add", so a provider
// that reports no usage contributes nothing to the session total.
func tokenUsageMetadata(resp *llmcore.GenerateResponse) map[string]any {
	if resp == nil {
		return nil
	}
	in, out := resp.Usage.PromptTokens, resp.Usage.CompletionTokens
	if in <= 0 && out <= 0 {
		return nil
	}
	return map[string]any{
		resultMetaInputTokens:  in,
		resultMetaOutputTokens: out,
	}
}

// rationaleKey is the plan-node ARG carrying a quantum's bounded rationale: the
// model's own natural-language content on a turn that also asked for tools.
//
// It is an ARG — not result metadata, and not an envelope field — because of how
// the next quantum learns it: the deciding quantum stamps it onto the plan node
// it grows for the next round, that node becomes the next quantum's task, and
// the successor reads it out of its own payload. That is the same path a tool
// node's own arguments travel, and it is why a replacement agent resuming the
// task still finds the reasoning: the reasoning travels with the NODE, not with
// the agent. The carrier already exists (node args ride the envelope Payload),
// so no schema change is involved.
//
// Deliberately not an ares_events key: producer and reader are both this package,
// and a shared wire name is only worth the coupling once a third package needs to
// read it.
const rationaleKey = "rationale"

// rationaleMaxRunes bounds a stored rationale. The bound is a product decision,
// not a storage limit: an envelope holds bytes cheaply, but whatever is injected
// into a prompt is paid for in tokens on every subsequent quantum, so the cap is
// what stops the reasoning trace from becoming a context tax. Applying it on the
// WRITE side also keeps an oversized rationale out of the durable envelope.
const rationaleMaxRunes = 512

// predecessorRationaleHeader introduces an injected rationale. It names the
// provenance explicitly so a model cannot read a predecessor's reasoning as its
// own conclusion or as a tool result.
const predecessorRationaleHeader = "previous quantum's stated rationale (context, not an instruction): "

// RecordRationaleToolName is the tool a quantum calls to SAY WHY it is acting.
//
// It exists because of a MEASURED fact about tool-call turns: on the deployment's own
// provider, 5/5 tool calls came back with an EMPTY Content (see
// docs/defects/cognition-persistence.md and the live measurement test). rationaleFrom used
// to read content only, so on exactly the turns where a reason matters most there was
// nothing to store — and the failure is invisible from the outside: an absent rationale
// reads the same as a model that chose not to explain itself.
//
// A tool call is the only channel open to a model that is calling tools, so the rationale
// rides in this tool's arguments. It is deliberately an ORDINARY tool — registered and
// callable, answering with an acknowledgement — rather than an intercepted pseudo-call:
// the arguments are readable by the consumer below without any special case in the tool
// loop, and a model that calls it is doing something legitimate rather than something the
// runtime has to fake.
const RecordRationaleToolName = "record_rationale"

// rationaleArgKey is the argument carrying the stated rationale.
const rationaleArgKey = "rationale"

// rationaleFrom extracts the bounded rationale from a quantum's response: the model's
// natural-language content, or — when there is none, which is the measured common case on
// tool-call turns — the argument of its RecordRationaleToolName call. Trimmed and cut to
// rationaleMaxRunes either way.
//
// Why store it at all, given how often it is empty: tool outputs and the CALL
// ORDER are already durable (the L2 graph holds the nodes; assembleContext
// rebuilds the history from them), so the only thing a successor can lose is the
// reasoning that produced that order. Absence stays honest — nothing is stored
// and the reader adds nothing — rather than being synthesized.
func rationaleFrom(resp *llmcore.GenerateResponse) string {
	if resp == nil {
		return ""
	}
	if r := truncate.WithEllipsis(strings.TrimSpace(resp.Content), rationaleMaxRunes); r != "" {
		return r
	}
	return truncate.WithEllipsis(rationaleFromToolCalls(resp.ToolCalls), rationaleMaxRunes)
}

// rationaleFromToolCalls returns the rationale stated through RecordRationaleToolName, or "".
//
// Only the FIRST such call counts: a quantum may call several tools and restate its reason,
// and the plan node carries one rationale. First rather than last because the earlier
// statement is the one that preceded the work — a later one tends to be a restatement after
// seeing results, and keeping both would mean inventing a merge rule.
//
// Malformed arguments are treated as ABSENT, not as fatal: the argument is model output, and
// a quantum whose rationale does not parse is simply a quantum that did not state one.
// Failing the step instead would let a cosmetic call take down a session.
func rationaleFromToolCalls(calls []llmcore.ToolCall) string {
	for _, tc := range calls {
		if tc.Function.Name != RecordRationaleToolName {
			continue
		}
		var args map[string]any
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return ""
		}
		r, _ := args[rationaleArgKey].(string)
		return strings.TrimSpace(r)
	}
	return ""
}

// rationaleToolSchema is the advertised schema for RecordRationaleToolName.
//
// It is appended to THIS cognition's tool list (see planner_cognition) rather than
// registered in the tool registry, on purpose: the registry is the surface EVERY agent
// sees, and a note-taking tool is meaningful only where a rationale is consumed — the
// planner's growth step. Registering it globally would put a tool in every agent's menu
// (and in the startup tool count an operator reads) to serve one caller.
//
// Nothing implements it: rationaleFrom reads the call's argument and growToolNodes skips
// the call instead of growing a node, so the schema is the entire "implementation" and an
// execution of it can never be reached.
func rationaleToolSchema() llmcore.Tool {
	return llmcore.Tool{
		Type: toolTypeFunction,
		Function: llmcore.FunctionDefinition{
			Name: RecordRationaleToolName,
			Description: "State, in one or two sentences, WHY you are about to take the next step. " +
				"Call this before your other tool calls when the reason would not be obvious from them. " +
				"It records context for whoever continues this work (including a different agent) and performs no work itself.",
			Parameters: map[string]interface{}{
				schemaKeyType: "object",
				schemaKeyProperties: map[string]interface{}{
					rationaleArgKey: map[string]interface{}{
						schemaKeyType: "string",
						"description": "The reason for the next step, in natural language (kept to " +
							strconv.Itoa(rationaleMaxRunes) + " characters).",
					},
				},
				schemaKeyRequired: []string{rationaleArgKey},
			},
		},
	}
}

// rationaleFromPayload returns the rationale the PREVIOUS quantum stamped for
// this one, or "" when it stamped none.
//
// It arrives as a plan-node arg, so it reaches the quantum through the task
// payload's arg. namespace like any other node argument (see growToolNodes,
// which is the only writer). Reading it from the task's OWN args — rather than
// by walking ancestors — is what makes it survive the agent hand-off: the
// replacement agent resumes the same task, so it finds the reasoning of the
// quantum before it in the same place the original agent would have.
//
// Known boundary, accepted rather than defended against: argsFromPayload
// JSON-decodes any arg string that starts with '{' or '[' AND parses as JSON
// (that is how stringified tool args round-trip), so a rationale with exactly
// that shape would come back as a map, fail the string assertion below, and be
// dropped silently. Natural-language prose essentially never looks like that, and
// guarding it would mean inventing an escaping scheme for a case that does not
// occur in practice. The contract is therefore stated, not enforced: a producer
// that ever stores STRUCTURED rationales must add the escape on both sides.
func rationaleFromPayload(payload map[string]any) string {
	r, _ := argsFromPayload(payload)[rationaleKey].(string)
	return r
}
