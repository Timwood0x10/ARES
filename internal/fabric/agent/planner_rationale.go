package agentfabric

import (
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

// rationaleFrom extracts the bounded rationale from a quantum's response: the
// model's natural-language content, trimmed and cut to rationaleMaxRunes.
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
	return truncate.WithEllipsis(strings.TrimSpace(resp.Content), rationaleMaxRunes)
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
