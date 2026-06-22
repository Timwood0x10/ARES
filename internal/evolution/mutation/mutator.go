package mutation

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"sort"
	"sync/atomic"

	"github.com/google/uuid"
)

// ErrNilParent is returned when a nil parent strategy is passed to Mutate.
var ErrNilParent = fmt.Errorf("parent strategy must not be nil")

// ErrInvalidCount is returned when the requested mutation count is invalid.
var ErrInvalidCount = fmt.Errorf("mutation count must be positive")

// Mutator generates mutated strategies from a parent strategy.
// It supports parameter value mutations, prompt template mutations, and tool
// configuration mutations with configurable ranges, pools, and randomness sources.
type Mutator struct {
	paramRanges      map[string]ParamRange // Configurable parameter ranges.
	promptPool       []string              // Available prompt templates for mutation.
	toolPool         []string              // Available tool configurations for mutation.
	rng              *rand.Rand            // Deterministic randomness source.
	deterministicIDs bool         // When true, use counter-based IDs instead of UUID.
	idCounter        atomic.Int64 // Monotonic counter for deterministic ID generation (thread-safe).
}

// NewMutator creates a new strategy mutator with default configuration.
//
// Default configuration:
//   - paramRanges: DefaultParamRanges (temperature, top_k, max_steps, etc.)
//   - promptPool: empty (prompt mutation disabled unless configured)
//   - toolPool: empty (tool mutation disabled unless configured)
//   - rng: seeded with current time (non-deterministic)
//
// Args:
//
//	opts - optional configuration functions (WithParamRanges, WithPromptPool, WithToolPool, WithSeed).
//
// Returns:
//
//	*Mutator - the configured mutator instance.
//	error - non-nil if any option fails validation.
func NewMutator(opts ...MutatorOption) (*Mutator, error) {
	m := &Mutator{
		paramRanges: deepCopyParamRanges(DefaultParamRanges),
		promptPool:  []string{},
		toolPool:    []string{},
		rng:         rand.New(rand.NewSource(rand.Int63())), // #nosec G404 — strategy mutation doesn't need crypto rand
	}

	for _, opt := range opts {
		if err := opt(m); err != nil {
			return nil, fmt.Errorf("apply mutator option: %w", err)
		}
	}

	return m, nil
}

// Mutate generates n mutated child strategies from the given parent.
// Each child is guaranteed to differ from parent in at least one parameter,
// or be a deep copy if no valid mutation is possible.
//
// Mutation distribution per child (when pools are non-empty):
//   - 70% probability: parameter value mutation
//   - 15% probability: prompt template mutation (requires non-empty promptPool)
//   - 15% probability: tool configuration mutation (requires non-empty toolPool)
//
// If a pool is empty, its probability is redistributed among the remaining
// available mutation types.
//
// Args:
//
//	ctx - operation context (used for cancellation).
//	parent - the parent strategy to mutate (must not be nil).
//	n - number of child strategies to generate (must be > 0).
//
// Returns:
//
//	[]*Strategy - the generated child strategies.
//	error - ErrNilParent if parent is nil, ErrInvalidCount if n <= 0.
func (m *Mutator) Mutate(ctx context.Context, parent *Strategy, n int) ([]*Strategy, error) {
	if parent == nil {
		return nil, ErrNilParent
	}
	if n <= 0 {
		return nil, ErrInvalidCount
	}

	children := make([]*Strategy, 0, n)
	for i := 0; i < n; i++ {
		select {
		case <-ctx.Done():
			return children, ctx.Err()
		default:
		}

		child, err := m.mutateOne(parent, i)
		if err != nil {
			return nil, fmt.Errorf("mutate child %d: %w", i, err)
		}
		children = append(children, child)
	}

	return children, nil
}

// mutateOne performs a single mutation on the parent strategy.
// It randomly selects between parameter, prompt, and tool mutation based on
// probability and pool availability, then applies the chosen mutation method.
//
// Probability distribution:
//   - All pools available: 70% parameter, 15% prompt, 15% tool
//   - Only prompt available: 80% parameter, 20% prompt
//   - Only tool available: 80% parameter, 20% tool
//   - No pools available: 100% parameter
func (m *Mutator) mutateOne(parent *Strategy, index int) (*Strategy, error) {
	hasPrompt := len(m.promptPool) > 0
	hasTool := len(m.toolPool) > 0
	r := m.rng.Float64()

	var child *Strategy
	var err error

	if hasPrompt && hasTool {
		if r < 0.70 {
			child, err = m.mutateParameter(parent)
		} else if r < 0.85 {
			child, err = m.mutatePrompt(parent)
		} else {
			child, err = m.mutateTool(parent)
		}
	} else if hasPrompt {
		if r < 0.80 {
			child, err = m.mutateParameter(parent)
		} else {
			child, err = m.mutatePrompt(parent)
		}
	} else if hasTool {
		if r < 0.80 {
			child, err = m.mutateParameter(parent)
		} else {
			child, err = m.mutateTool(parent)
		}
	} else {
		child, err = m.mutateParameter(parent)
	}

	if err != nil {
		return nil, err
	}

	// Fill in metadata for the new child strategy.
	now := parent.CreatedAt
	if m.deterministicIDs {
		counter := m.idCounter.Add(1)
		parentShort := parent.ID
		if len(parentShort) > 8 {
			parentShort = parentShort[:8]
		}
		child.ID = fmt.Sprintf("det-mut-%s-%d", parentShort, counter)
	} else {
		child.ID = uuid.New().String()
	}
	child.ParentID = parent.ID
	child.Version = parent.Version + 1
	child.Score = -1
	child.CreatedAt = now

	return child, nil
}

// mutateParameter changes one random parameter to a different value from its range.
// Selects a parameter uniformly at random, then picks a value != current.
// Returns a deep copy of parent if no valid mutation exists for any parameter.
func (m *Mutator) mutateParameter(parent *Strategy) (*Strategy, error) {
	child := parent.Clone()

	// Collect mutable parameter names that exist in both config and parent params.
	candidates := m.mutableParamNames(child.Params)
	if len(candidates) == 0 {
		// No mutable params found; return copy with description.
		child.MutationDesc = "no mutable parameters available"
		child.StrategyMutationType = MutationParameter
		return child, nil
	}

	// Shuffle candidates to pick a random one.
	m.shuffleStrings(candidates)
	paramName := candidates[0]
	rangeDef, ok := m.paramRanges[paramName]
	if !ok {
		child.MutationDesc = fmt.Sprintf("parameter %q has no range definition", paramName)
		child.StrategyMutationType = MutationParameter
		return child, nil
	}

	// Pick a different value from the range.
	newVal := m.pickDifferentValue(rangeDef.Values, child.Params[paramName])
	if newVal == nil {
		// All values are identical to current; return copy.
		child.MutationDesc = fmt.Sprintf("no alternative value for parameter %q", paramName)
		child.StrategyMutationType = MutationParameter
		return child, nil
	}

	child.Params[paramName] = newVal
	child.MutationDesc = fmt.Sprintf("parameter %q changed to %v", paramName, newVal)
	child.StrategyMutationType = MutationParameter

	return child, nil
}

// mutatePrompt replaces the prompt template with a different one from the pool.
// Returns a deep copy of parent if no alternative template is available.
func (m *Mutator) mutatePrompt(parent *Strategy) (*Strategy, error) {
	child := parent.Clone()

	if len(m.promptPool) <= 1 {
		child.MutationDesc = "insufficient prompt templates for mutation"
		child.StrategyMutationType = MutationPrompt
		return child, nil
	}

	newTemplate := m.pickDifferentString(m.promptPool, parent.PromptTemplate)
	if newTemplate == "" {
		child.MutationDesc = "no alternative prompt template available"
		child.StrategyMutationType = MutationPrompt
		return child, nil
	}

	child.PromptTemplate = newTemplate
	child.MutationDesc = "prompt template changed"
	child.StrategyMutationType = MutationPrompt

	return child, nil
}

// mutateTool replaces the tool configuration with a different one from the pool.
// The tool configuration is stored in Params["tools"] as a string.
//
// If the parent strategy does not have a "tools" key in Params and the toolPool
// is non-empty, this method initializes the tools field to the first pool entry
// and returns the deep copy (similar to mutateParameter's "no mutable params"
// handling). This prevents silently adding a tools configuration that did not
// exist in the parent.
//
// Returns a deep copy of parent if no alternative configuration is available.
func (m *Mutator) mutateTool(parent *Strategy) (*Strategy, error) {
	child := parent.Clone()

	if len(m.toolPool) <= 1 {
		child.MutationDesc = "insufficient tool configurations for mutation"
		child.StrategyMutationType = MutationTool
		return child, nil
	}

	currentTools, hasToolsKey := parent.Params["tools"].(string)
	if !hasToolsKey && len(m.toolPool) > 0 {
		// Parent has no "tools" config; initialize with first pool entry
		// instead of silently picking a random different value.
		child.Params["tools"] = m.toolPool[0]
		child.MutationDesc = fmt.Sprintf("tool configuration initialized to %q (parent had no tools key)", m.toolPool[0])
		child.StrategyMutationType = MutationTool
		return child, nil
	}

	newTools := m.pickDifferentString(m.toolPool, currentTools)
	if newTools == "" {
		child.MutationDesc = "no alternative tool configuration available"
		child.StrategyMutationType = MutationTool
		return child, nil
	}

	child.Params["tools"] = newTools
	child.MutationDesc = fmt.Sprintf("tool configuration changed to %q", newTools)
	child.StrategyMutationType = MutationTool

	return child, nil
}

// mutableParamNames returns sorted parameter names that exist in both
// the configured ranges and the parent strategy params.
// Sorting ensures deterministic iteration order for reproducible mutations.
func (m *Mutator) mutableParamNames(params map[string]any) []string {
	names := make([]string, 0, len(m.paramRanges))
	for name := range m.paramRanges {
		if _, exists := params[name]; exists {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// pickDifferentValue returns a random value from candidates that differs from current.
// Returns nil if all values are equal to current or candidates is empty.
func (m *Mutator) pickDifferentValue(candidates []any, current any) any {
	different := m.filterDifferent(candidates, current)
	if len(different) == 0 {
		return nil
	}
	return different[m.rng.Intn(len(different))]
}

// filterDifferent returns values from candidates that are not deeply equal to current.
func (m *Mutator) filterDifferent(candidates []any, current any) []any {
	var result []any
	for _, v := range candidates {
		if !valuesEqual(v, current) {
			result = append(result, v)
		}
	}
	return result
}

// pickDifferentString returns a random string from pool that differs from current.
// Returns empty string if no different string exists.
func (m *Mutator) pickDifferentString(pool []string, current string) string {
	var different []string
	for _, s := range pool {
		if s != current {
			different = append(different, s)
		}
	}
	if len(different) == 0 {
		return ""
	}
	return different[m.rng.Intn(len(different))]
}

// shuffleStrings performs an in-place Fisher-Yates shuffle of the string slice.
func (m *Mutator) shuffleStrings(s []string) {
	for i := len(s) - 1; i > 0; i-- {
		j := m.rng.Intn(i + 1)
		s[i], s[j] = s[j], s[i]
	}
}

// deepCopyParamRanges creates a full copy of the param ranges map.
func deepCopyParamRanges(src map[string]ParamRange) map[string]ParamRange {
	dst := make(map[string]ParamRange, len(src))
	for k, v := range src {
		clonedValues, ok := cloneValue(v.Values).([]any)
		if !ok {
			slog.Warn("deepCopyParamRanges: unexpected type from cloneValue, falling back to nil Values",
				"key", k,
				"type", fmt.Sprintf("%T", v.Values))
			clonedValues = nil
		}
		dst[k] = ParamRange{
			Name:    v.Name,
			Values:  clonedValues,
			Current: v.Current,
		}
	}
	return dst
}

// valuesEqual checks if two interface values are equal.
// Supports cross-type numeric comparison (int/float64/int64) and
// standard comparison of strings, booleans, and nil.
func valuesEqual(a, b any) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}

	switch va := a.(type) {
	case int:
		switch vb := b.(type) {
		case int:
			return va == vb
		case float64:
			return float64(va) == vb
		case int64:
			return int64(va) == vb
		}
		return false
	case int64:
		switch vb := b.(type) {
		case int64:
			return va == vb
		case int:
			return va == int64(vb)
		case float64:
			return float64(va) == vb
		}
		return false
	case float64:
		switch vb := b.(type) {
		case float64:
			return va == vb
		case int:
			return va == float64(vb)
		case int64:
			return va == float64(vb)
		}
		return false
	case string:
		vb, ok := b.(string)
		return ok && va == vb
	case bool:
		vb, ok := b.(bool)
		return ok && va == vb
	default:
		return a == b
	}
}
