package ares_events

import (
	"encoding/json"
	"sort"
	"strings"
)

// Tool-event payload keys shared by the three ReAct executors
// (agentfabric/chat_cognition.go, agents/sub/executor.go, agentloop/engine.go)
// and the workflow graph ToolNode. Hoisting the keys here satisfies the
// goconst "string repeated >= 3 times" rule and gives the projection layer
// (Y1 方案C C2) a single, stable contract to read — the acceptance for C1 is
// that all executors emit the SAME key set.
const (
	EventKeyToolName   = "tool_name"
	EventKeyToolCallID = "tool_call_id"
	EventKeyRound      = "round"
	EventKeySeq        = "seq"
	EventKeySuccess    = "success"
	EventKeyError      = "error"
	EventKeyArgShape   = "arg_shape"
	EventKeyAgentID    = "agent_id"
	EventKeyTool       = "tool"
)

// ToolCompletedPayload records the result of one tool-call completion with a
// UNIFIED key set across executors (Y1 C1). round/seq give the execution order
// within a session (edge source for the trajectory projection); success/error
// carry the outcome that was previously dropped by the peer executors (Y1
// §9.3); arg_shape is the normalized argument-key set that aggregates
// "the same tool used the same way" into one ToolStep (Y1 §11.0). It embeds
// no values — only key names — so identical method shapes collapse to one key.
type ToolCompletedPayload struct {
	AgentID     string
	ToolName    string
	ToolCallID  string
	Round       int
	Seq         int
	Success     bool
	Error       string
	ArgShape    string
	ExtraResult string
}

// AsMap renders the payload as an event probe map using the unified keys.
func (p ToolCompletedPayload) AsMap() map[string]any {
	m := map[string]any{
		EventKeyAgentID:    p.AgentID,
		EventKeyToolName:   p.ToolName,
		EventKeyToolCallID: p.ToolCallID,
		EventKeyRound:      p.Round,
		EventKeySeq:        p.Seq,
		EventKeySuccess:    p.Success,
	}
	if p.Error != "" {
		m[EventKeyError] = p.Error
	}
	if p.ArgShape != "" {
		m[EventKeyArgShape] = p.ArgShape
	}
	// result is advisory (only set by executors that retain the output text);
	// key is not part of the C1 identity set.
	if p.ExtraResult != "" {
		m[EventKeyResult] = p.ExtraResult
	}
	return m
}

// ToolArgShape computes the normalized argument shape for a tool-call
// arguments JSON blob: the sorted set of top-level argument key names joined
// by ",". Two calls of the same tool with the same argument KEY SET produce
// the same shape regardless of the values, so "search(q=foo)" and
// "search(q=bar)" collapse into a single ToolStep (Y1 §11.0). Malformed or
// empty JSON yields "".
func ToolArgShape(argsJSON string) string {
	if argsJSON == "" {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &m); err != nil {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}
