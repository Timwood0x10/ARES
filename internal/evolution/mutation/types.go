// Package mutation provides strategy mutation engine for Dream Mode evolution.
// It generates mutated child strategies from parent strategies by varying
// parameters, prompt templates, or tool configurations.
package mutation

import (
	"log/slog"
	"time"
)

// MutationType represents the type of strategy mutation applied.
type MutationType int

const (
	// MutationParameter indicates a parameter value mutation (e.g., temperature change).
	MutationParameter MutationType = iota + 1

	// MutationPrompt indicates a prompt template mutation.
	MutationPrompt

	// MutationTool indicates a tool configuration mutation.
	// The tool configuration (stored in Params["tools"]) is replaced with
	// a different configuration from the tool pool.
	MutationTool

	// MutationCrossover indicates a strategy created via crossover (genetic algorithm).
	// Two parent strategies are combined to produce a child strategy.
	MutationCrossover
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
	case MutationCrossover:
		return "crossover"
	default:
		return "unknown"
	}
}

// ParseMutationType converts a string to a MutationType.
// Unknown strings are logged as a warning and return MutationParameter
// as a safe default, avoiding silent degradation.
func ParseMutationType(s string) MutationType {
	switch s {
	case "parameter":
		return MutationParameter
	case "prompt":
		return MutationPrompt
	case "tool":
		return MutationTool
	case "crossover":
		return MutationCrossover
	default:
		slog.Warn("unknown mutation type string, falling back to MutationParameter",
			"input", s,
		)
		return MutationParameter
	}
}

// Strategy represents an agent's execution strategy configuration.
type Strategy struct {
	// ID is the unique strategy identifier.
	ID string `json:"id"`

	// ParentID is the parent strategy ID (empty for root strategies).
	ParentID string `json:"parent_id,omitempty"`

	// Version is the monotonically increasing version number.
	Version int `json:"version"`

	// Name is the human-readable name of the strategy.
	Name string `json:"name,omitempty"`

	// Params holds mutable parameters (temperature, top_k, etc.).
	Params map[string]any `json:"params,omitempty"`

	// PromptTemplate is the behavior prompt template.
	PromptTemplate string `json:"prompt_template,omitempty"`

	// StrategyMutationType records how this strategy was created.
	StrategyMutationType MutationType `json:"strategy_mutation_type"`

	// MutationDesc is a human-readable description of the mutation.
	MutationDesc string `json:"mutation_desc,omitempty"`

	// Score is the current evaluation score (-1 = unevaluated).
	Score float64 `json:"score"`

	// CreatedAt is the timestamp when this strategy was created.
	CreatedAt time.Time `json:"created_at"`
}

// Clone returns a deep copy of the strategy.
// Both Params map and nested slices are copied to avoid shared state.
func (s *Strategy) Clone() *Strategy {
	if s == nil {
		return nil
	}

	return &Strategy{
		ID:                   s.ID,
		ParentID:             s.ParentID,
		Version:              s.Version,
		Name:                 s.Name,
		Params:               CloneParams(s.Params),
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

// CloneParams creates a shallow copy of a params map to avoid shared state.
func CloneParams(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = cloneValue(v)
	}
	return dst
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
	case []int:
		copied := make([]int, len(val))
		copy(copied, val)
		return copied
	case []int64:
		copied := make([]int64, len(val))
		copy(copied, val)
		return copied
	case []float64:
		copied := make([]float64, len(val))
		copy(copied, val)
		return copied
	case []bool:
		copied := make([]bool, len(val))
		copy(copied, val)
		return copied
	case map[string]any:
		copied := make(map[string]any, len(val))
		for k, vv := range val {
			copied[k] = cloneValue(vv)
		}
		return copied
	default:
		return v
	}
}
