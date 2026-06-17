package agents

import (
	"context"
	"fmt"

	"goagentx/internal/quant/research"
)

// PromptFunc is a function type for building prompts from research state.
// This allows BaseAgent to use any prompt builder via dependency injection.
type PromptFunc func(state *research.ResearchState) string

// BaseAgent provides common execution logic for all research agents.
// It implements the template method pattern: BuildPrompt -> Execute LLM -> Parse Output.
type BaseAgent struct {
	name          string
	agentType     string
	executor      LLMExecutor
	parser        OutputParser
	promptBuilder PromptFunc
}

// NewBaseAgent creates a new BaseAgent with the given configuration.
//
// Args:
//   - name: human-readable agent name (e.g., "Market Analyst").
//   - agentType: category of this agent (analyst/debater/manager/trader).
//   - executor: LLM execution backend (dependency-injected).
//   - parser: output parser for converting LLM response to typed data.
//   - promptFn: function that builds the prompt from research state.
//
// Returns:
//   - initialized BaseAgent ready for Execute calls.
func NewBaseAgent(name string, agentType string, executor LLMExecutor, parser OutputParser, promptFn PromptFunc) *BaseAgent {
	return &BaseAgent{
		name:          name,
		agentType:     agentType,
		executor:      executor,
		parser:        parser,
		promptBuilder: promptFn,
	}
}

// Name returns the human-readable name of this agent.
func (a *BaseAgent) Name() string {
	return a.name
}

// Type returns the agent category (analyst/debater/manager/trader).
func (a *BaseAgent) Type() string {
	return a.agentType
}

// BuildPrompt constructs the LLM prompt using the injected prompt builder function.
//
// Args:
//   - state: current research state containing accumulated analysis results.
//
// Returns:
//   - the constructed prompt string.
func (a *BaseAgent) BuildPrompt(state *research.ResearchState) string {
	if a.promptBuilder != nil {
		return a.promptBuilder(state)
	}
	return ""
}

// Execute runs the full agent pipeline against the LLM.
//
// The execution follows the template method pattern:
//  1. BuildPrompt(state) to construct the LLM prompt.
//  2. Call executor.Complete to get the raw LLM response.
//  3. Call parser.Parse to convert raw output into structured data.
//  4. Update state.CurrentStep and state.StepsCompleted.
//
// Args:
//   - ctx: context for cancellation and timeout.
//   - state: mutable research state that will be updated with results.
//
// Returns:
//   - error if LLM call or parsing fails.
func (a *BaseAgent) Execute(ctx context.Context, state *research.ResearchState) error {
	prompt := a.BuildPrompt(state)
	messages := []Message{
		{Role: "system", Content: prompt},
	}

	raw, err := a.executor.Complete(ctx, messages)
	if err != nil {
		return fmt.Errorf("%s execute llm call: %w", a.name, err)
	}

	if raw == "" {
		return fmt.Errorf("%s execute: %w", a.name, ErrNoResponse)
	}

	if err := a.parser.Parse(raw, state); err != nil {
		return fmt.Errorf("%s execute parse output: %w", a.name, err)
	}

	state.CurrentStep = a.name
	state.StepsCompleted = append(state.StepsCompleted, a.name)
	return nil
}

// ExecuteStructured attempts structured output first, falling back to text parsing.
//
// This is the recommended execution path when the LLM supports structured output.
// If CompleteStructured fails or is not supported, it falls back to the standard
// text-based Execute path.
//
// Args:
//   - ctx: context for cancellation and timeout.
//   - state: mutable research state that will be updated with results.
//   - schema: the target schema for structured output (e.g., &ResearchPlan{}).
//
// Returns:
//   - error if both structured and fallback paths fail.
func (a *BaseAgent) ExecuteStructured(ctx context.Context, state *research.ResearchState, schema any) error {
	prompt := a.BuildPrompt(state)
	messages := []Message{
		{Role: "system", Content: prompt},
	}

	// Try structured output first.
	result, err := a.executor.CompleteStructured(ctx, messages, schema)
	if err == nil && result != nil {
		state.CurrentStep = a.name
		state.StepsCompleted = append(state.StepsCompleted, a.name)
		return nil
	}

	// Fallback to text-based path.
	raw, err := a.executor.Complete(ctx, messages)
	if err != nil {
		return fmt.Errorf("%s execute structured fallback: %w", a.name, err)
	}

	if raw == "" {
		return fmt.Errorf("%s execute structured fallback: %w", a.name, ErrNoResponse)
	}

	if err := a.parser.Parse(raw, state); err != nil {
		return fmt.Errorf("%s execute structured fallback parse: %w", a.name, err)
	}

	state.CurrentStep = a.name
	state.StepsCompleted = append(state.StepsCompleted, a.name)
	return nil
}
