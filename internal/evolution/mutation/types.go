// Package mutation provides strategy mutation engine for Dream Mode evolution.
// It generates mutated child strategies from parent strategies by varying
// parameters, prompt templates, or tool configurations.
package mutation

import "time"

// MutationType represents the type of strategy mutation applied.
type MutationType int

const (
	// MutationParameter indicates a parameter value mutation (e.g., temperature change).
	MutationParameter MutationType = iota + 1

	// MutationPrompt indicates a prompt template mutation.
	MutationPrompt

	// MutationTool indicates a tool generation mutation.
	// TODO: reserved for future use in Iteration 3 - tool configuration mutations.
	// Currently no code path generates this mutation type.
	MutationTool
)

// String returns the human-readable name of the mutation type.
func (mt MutationType) String() string {
	switch mt {
	case MutationParameter:
		return "parameter"
	case MutationPrompt:
		return "prompt"
	case MutationTool:
		return "tool"
	default:
		return "unknown"
	}
}

// Strategy represents an agent's execution strategy configuration.
type Strategy struct {
	// ID is the unique strategy identifier.
	ID string

	// ParentID is the parent strategy ID (empty for root strategies).
	ParentID string

	// Version is the monotonically increasing version number.
	Version int

	// Params holds mutable parameters (temperature, top_k, etc.).
	Params map[string]any

	// PromptTemplate is the behavior prompt template.
	PromptTemplate string

	// StrategyMutationType records how this strategy was created.
	StrategyMutationType MutationType

	// MutationDesc is a human-readable description of the mutation.
	MutationDesc string

	// Score is the current evaluation score (-1 = unevaluated).
	Score float64

	// CreatedAt is the timestamp when this strategy was created.
	CreatedAt time.Time
}

// Clone returns a deep copy of the strategy.
// Both Params map and nested slices are copied to avoid shared state.
func (s *Strategy) Clone() *Strategy {
	if s == nil {
		return nil
	}

	clonedParams := make(map[string]any, len(s.Params))
	for k, v := range s.Params {
		clonedParams[k] = cloneValue(v)
	}

	return &Strategy{
		ID:                   s.ID,
		ParentID:             s.ParentID,
		Version:              s.Version,
		Params:               clonedParams,
		PromptTemplate:       s.PromptTemplate,
		StrategyMutationType: s.StrategyMutationType,
		MutationDesc:         s.MutationDesc,
		Score:                s.Score,
		CreatedAt:            s.CreatedAt,
	}
}

// ParamRange defines the allowed range for a mutable parameter.
type ParamRange struct {
	// Name is the parameter name (e.g., "temperature").
	Name string

	// Values contains candidate values for this parameter.
	Values []any

	// Current is the current value of this parameter.
	Current any
}

// DefaultParamRanges provides sensible default parameter ranges for LLM agents.
var DefaultParamRanges = map[string]ParamRange{
	"temperature":        {Name: "temperature", Values: []any{0.1, 0.3, 0.5, 0.7, 0.9}},
	"top_k":              {Name: "top_k", Values: []any{10, 20, 40, 80}},
	"max_steps":          {Name: "max_steps", Values: []any{5, 10, 15, 20}},
	"memory_limit":       {Name: "memory_limit", Values: []any{3, 5, 10}},
	"conflict_threshold": {Name: "conflict_threshold", Values: []any{0.85, 0.90, 0.95}},
}

// cloneValue performs a shallow-to-moderate copy of a value.
// For slices, it creates a new slice with copied elements.
// For other types, it returns the value as-is (safe for primitives and strings).
func cloneValue(v any) any {
	if v == nil {
		return nil
	}
	switch val := v.(type) {
	case []any:
		copied := make([]any, len(val))
		copy(copied, val)
		return copied
	case []string:
		copied := make([]string, len(val))
		copy(copied, val)
		return copied
	case map[string]any:
		copied := make(map[string]any, len(val))
		for k, vv := range val {
			copied[k] = cloneValue(vv)
		}
		return copied
	default:
		// Primitives (int, float64, string, bool) are safe to assign directly.
		return v
	}
}
