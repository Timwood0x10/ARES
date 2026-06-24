// Package agents provides prompt building, agent interfaces, and execution
// infrastructure for the quantitative research pipeline.
package agents

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/internal/quant/research"
)

// ResearchAgent is the interface that every research pipeline agent must implement.
// It defines the contract for name, type classification, prompt building, execution,
// and output parsing.
type ResearchAgent interface {
	// Name returns the human-readable name of this agent (e.g., "Market Analyst").
	Name() string

	// Type returns the agent category: analyst, debater, manager, or trader.
	Type() string

	// BuildPrompt constructs the LLM prompt from the current research state.
	// This must be a pure function with no side effects.
	BuildPrompt(state *research.ResearchState) string

	// Execute runs the agent against the LLM and parses the result into the state.
	Execute(ctx context.Context, state *research.ResearchState) error
}

// LLMExecutor is the interface for executing LLM completions.
// Implementations can be real LLM APIs or mock executors for testing.
type LLMExecutor interface {
	// Complete sends messages to the LLM and returns the raw text response.
	Complete(ctx context.Context, messages []Message) (string, error)

	// CompleteStructured sends messages to the LLM with a schema hint for
	// structured output. Returns the parsed result or an error if unsupported.
	CompleteStructured(ctx context.Context, messages []Message, schema any) (any, error)
}

// Message represents a single conversation message in the LLM chat format.
type Message struct {
	// Role is one of: system, user, assistant.
	Role string

	// Content is the message body text.
	Content string
}

// OutputParser defines the contract for parsing LLM raw output into typed results.
type OutputParser interface {
	// Parse extracts structured data from raw LLM output and writes it into target.
	Parse(raw string, target interface{}) error
}

// ErrNoResponse is returned when the LLM produces an empty response.
var ErrNoResponse = fmt.Errorf("llm returned empty response")

// ErrParseFailed is returned when output parsing fails after all strategies.
var ErrParseFailed = fmt.Errorf("output parsing failed")
