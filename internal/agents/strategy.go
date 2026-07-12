// Package agents defines shared runtime contracts for live agents.
package agents

import "context"

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
