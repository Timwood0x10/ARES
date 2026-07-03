// Package core provides core interfaces for the ARES system.
package core

import "context"

// Evaluator defines the interface for evaluating agent outputs.
type Evaluator interface {
	// Evaluate evaluates an agent's output against expected results.
	// Args:
	// ctx - operation context.
	// input - the agent input.
	// output - the agent output.
	// expected - the expected output.
	// Returns a score (0.0-1.0) and optional error.
	Evaluate(ctx context.Context, input, output, expected string) (float64, error)
}

// EvaluatorRegistry defines the interface for managing evaluators.
type EvaluatorRegistry interface {
	// Register registers an evaluator by name.
	// Args:
	// name - evaluator name.
	// evaluator - the evaluator to register.
	// Returns error if name already exists.
	Register(name string, evaluator Evaluator) error

	// Get returns an evaluator by name.
	// Args:
	// name - evaluator name.
	// Returns the evaluator or nil if not found.
	Get(name string) Evaluator
}

// LLMClient defines the LLM client interface used by evaluators.
type LLMClient interface {
	// Generate generates text from the given prompt.
	// Args:
	// ctx - operation context.
	// prompt - the prompt text.
	// Returns generated text or error.
	Generate(ctx context.Context, prompt string) (string, error)
}
