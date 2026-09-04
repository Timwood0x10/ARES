// Package agents defines shared runtime contracts for live agents.
package agents

import (
	"context"
	"strings"
)

// ActiveStrategy is the runtime view of the evolution strategy currently
// deployed to live agents. It carries an optional prompt-template override
// and per-call LLM parameter overrides (temperature, max_tokens, top_k).
type ActiveStrategy struct {
	// ID identifies the source strategy (for tracing/logging).
	ID string
	// Prompt optionally overrides the agent's default prompt template.
	Prompt string
	// Params carries LLM parameter overrides applied on each LLM call.
	Params map[string]any
}

// StrategySource yields the currently-active strategy so live agents can be
// steered at runtime (prompt + LLM params). It is intentionally decoupled
// from the evolution engine internals; adapters in the wiring layer convert
// engine-specific stores into this interface.
type StrategySource interface {
	// GetActiveStrategy returns the active strategy, or nil if none is set.
	GetActiveStrategy(ctx context.Context) (*ActiveStrategy, error)
}

// ParamKeyTools is the Params key that carries the tool whitelist. The value
// is a comma-separated string of tool names (e.g. "web_search,calculator").
// An empty or missing value means "no filter" — all registered tools are
// advertised to the LLM (zero-value usable, code_rules_v2 §5.4).
const ParamKeyTools = "tools"

// ToolWhitelistFromParams extracts the tool whitelist from a strategy's
// Params map. Returns nil when no whitelist is configured (meaning "all tools
// allowed"). The whitelist is a set of tool names with whitespace trimmed;
// empty entries are skipped.
//
// This is the Y.3-ACT wiring point: both execution bodies (sub executor and
// agentfabric chatCognition) call this on the params they received from
// renderPromptAndParams to filter which ToolSchemas reach the LLM. Filtering
// happens BEFORE the LLM sees the tool list, not at CallTool time — letting
// the model see a tool and then rejecting the call wastes a round and
// pollutes the not_found metric (code_rules_v2 §5.3).
func ToolWhitelistFromParams(params map[string]any) map[string]bool {
	if params == nil {
		return nil
	}
	raw, ok := params[ParamKeyTools].(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return nil
	}
	whitelist := make(map[string]bool)
	for _, name := range strings.Split(raw, ",") {
		name = strings.TrimSpace(name)
		if name != "" {
			whitelist[name] = true
		}
	}
	if len(whitelist) == 0 {
		return nil
	}
	return whitelist
}
