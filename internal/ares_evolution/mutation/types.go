// Package mutation provides strategy mutation engine for Dream Mode evolution.
// It generates mutated child strategies from parent strategies by varying
// parameters, prompt templates, or tool configurations.
package mutation

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
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

	// MutationRoot indicates a root/initial strategy that was not created via
	// mutation or crossover. It represents the baseline strategy from which
	// evolution begins.
	MutationRoot
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
	case MutationRoot:
		return "root"
	default:
		return "unknown"
	}
}

// ParseMutationType converts a string to a MutationType.
// Empty strings are treated as root (default for initial strategies).
// Unknown non-empty strings are logged as a warning and return MutationRoot
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
	case "root", "":
		// Empty string is treated as root (default for initial strategies).
		return MutationRoot
	default:
		slog.Warn("unknown mutation type string, falling back to MutationRoot",
			"input", s,
		)
		return MutationRoot
	}
}

// Strategy represents an agent's execution strategy configuration.
type Strategy struct {
	// ID is the unique strategy identifier.
	ID string `json:"id"`

	// ParentID is the parent strategy ID (empty for root strategies).
	ParentID string `json:"parent_id,omitempty"`

	// EvidenceKey is a stable key derived from behaviorally relevant fields
	// (prompt template + normalized numeric params). It enables evidence
	// lookup by phenotype across different strategy IDs.
	EvidenceKey string `json:"evidence_key,omitempty"`

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
	// In single-objective mode, this holds the aggregate score.
	// In multi-objective mode, DimensionScores holds per-dimension values
	// and Score is AggregateDimensions(DimensionScores, weights).
	Score float64 `json:"score"`

	// DimensionScores holds per-objective scores for multi-objective evaluation.
	// Keys are dimension names (e.g. "success_rate", "cost", "latency", "quality").
	// Nil means single-objective mode (backward compatible).
	// When non-nil, Score should be the aggregate of these values.
	DimensionScores map[string]float64 `json:"dimension_scores,omitempty"`

	// CreatedAt is the timestamp when this strategy was created.
	CreatedAt time.Time `json:"created_at"`

	// hashCache caches the StrategyHash result. Set hashValid=false on mutation.
	hashCache  uint64
	hashCached bool
}

// Clone returns a deep copy of the strategy.
// Both Params map and nested slices are copied to avoid shared state.
func (s *Strategy) Clone() *Strategy {
	if s == nil {
		return nil
	}

	clone := &Strategy{
		ID:                   s.ID,
		ParentID:             s.ParentID,
		EvidenceKey:          s.EvidenceKey,
		Version:              s.Version,
		Name:                 s.Name,
		Params:               CloneParams(s.Params),
		PromptTemplate:       s.PromptTemplate,
		StrategyMutationType: s.StrategyMutationType,
		MutationDesc:         s.MutationDesc,
		Score:                s.Score,
		CreatedAt:            s.CreatedAt,
		hashCache:            s.hashCache,
		hashCached:           s.hashCached,
	}
	if s.DimensionScores != nil {
		clone.DimensionScores = make(map[string]float64, len(s.DimensionScores))
		for k, v := range s.DimensionScores {
			clone.DimensionScores[k] = v
		}
	}
	return clone
}

// HashCached returns true if the StrategyHash has been cached on this object.
func (s *Strategy) HashCached() bool { return s != nil && s.hashCached }

// HashValue returns the cached hash value. Only valid if HashCached() == true.
func (s *Strategy) HashValue() uint64 { return s.hashCache }

// SetHash caches the given hash value on this strategy object.
func (s *Strategy) SetHash(h uint64) {
	if s == nil {
		return
	}
	s.hashCache = h
	s.hashCached = true
}

// ComputeEvidenceKey derives a stable evidence key from behaviorally relevant
// fields: prompt template and sorted numeric params. The key format is:
// "promptTemplate|key1=value1,key2=value2". Only numeric values (float64)
// in Params are included, sorted by key for determinism.
func (s *Strategy) ComputeEvidenceKey() string {
	if s == nil {
		return ""
	}

	prompt := s.PromptTemplate
	if prompt == "" {
		prompt = "default"
	}

	var pairs []string
	keys := make([]string, 0, len(s.Params))
	for k := range s.Params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		v, ok := s.Params[k].(float64)
		if !ok {
			continue
		}
		pairs = append(pairs, fmt.Sprintf("%s=%.2f", k, v))
	}

	evidenceKey := prompt
	if len(pairs) > 0 {
		evidenceKey = prompt + "|" + strings.Join(pairs, ",")
	}

	s.EvidenceKey = evidenceKey
	return evidenceKey
}

// ParamRange defines the allowed range for a mutable parameter.
type ParamRange struct {
	// Name is the parameter name (e.g., "temperature").
	Name string

	// Values contains candidate values for this parameter.
	Values []any
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
