// Package genome provides genetic algorithm crossover (recombination) operators
// for combining two parent strategies into a child strategy. It implements
// uniform crossover and multi-point crossover variants with configurable
// randomness sources for deterministic reproduction in evolutionary algorithms.
package genome

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"goagentx/internal/evolution/mutation"
)

// ErrNilParent is returned when a nil parent strategy is passed to Crossover.
var ErrNilParent = fmt.Errorf("parent strategy must not be nil")

// ErrInvalidCrossoverPoints is returned when the number of crossover points is invalid.
var ErrInvalidCrossoverPoints = fmt.Errorf("crossover points must be non-negative")

// Crossover combines two parent strategies into a child strategy using
// uniform crossover by default. Each parameter is independently selected
// from either parent A or parent B with equal probability.
type Crossover struct {
	rng *rand.Rand // Deterministic randomness source.
}

// NewCrossover creates a new crossover operator with default configuration.
//
// Default configuration:
//   - rng: seeded with current time (non-deterministic).
//
// Args:
//
//	opts - optional configuration functions (WithSeed).
//
// Returns:
//
//	*Crossover - the configured crossover instance.
//	error - non-nil if any option fails validation.
func NewCrossover(opts ...CrossoverOption) (*Crossover, error) {
	c := &Crossover{
		rng: rand.New(rand.NewSource(rand.Int63())), // #nosec G404 — strategy crossover doesn't need crypto rand
	}

	for _, opt := range opts {
		if err := opt(c); err != nil {
			return nil, fmt.Errorf("apply crossover option: %w", err)
		}
	}

	return c, nil
}

// CrossoverOption configures a Crossover instance.
type CrossoverOption func(*Crossover) error

// WithSeed sets the random seed for deterministic crossover results.
// Using the same seed produces reproducible child strategies.
//
// Args:
//
//	seed - the random seed value.
//
// Returns:
//
//	CrossoverOption - the configuration function.
func WithSeed(seed int64) CrossoverOption {
	return func(c *Crossover) error {
		c.rng = rand.New(rand.NewSource(seed)) // #nosec G404 — strategy crossover doesn't need crypto rand
		return nil
	}
}

// Crossover performs uniform crossover on two parent strategies.
// For each parameter key present in either parent, the child inherits
// from parent A or B with 50% probability each. The PromptTemplate
// is inherited from the higher-scoring parent (parent A on tie).
//
// Args:
//
//	ctx - operation context (used for cancellation).
//	a - first parent strategy (must not be nil).
//	b - second parent strategy (must not be nil).
//
// Returns:
//
//	*mutation.Strategy - the generated child strategy.
//	error - ErrNilParent if either parent is nil, ctx.Err() if cancelled.
func (c *Crossover) Crossover(ctx context.Context, a, b *mutation.Strategy) (*mutation.Strategy, error) {
	if a == nil || b == nil {
		return nil, fmt.Errorf("%w: both parents must be non-nil", ErrNilParent)
	}

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("crossover cancelled: %w", ctx.Err())
	default:
	}

	childParams, desc := c.uniformCrossParams(a.Params, b.Params)
	promptTemplate := c.selectPromptTemplate(a, b)

	child := &mutation.Strategy{
		ID:                   uuid.New().String(),
		ParentID:             formatParentIDs(a.ID, b.ID),
		Version:              maxVersion(a.Version, b.Version) + 1,
		Params:               childParams,
		PromptTemplate:       promptTemplate,
		StrategyMutationType: mutation.MutationParameter, // Reuse existing type; described as crossover in MutationDesc.
		MutationDesc:         desc,
		Score:                -1, // Unevaluated.
		CreatedAt:            time.Now(),
	}

	slog.Debug("crossover completed",
		"child_id", child.ID,
		"parent_a", a.ID,
		"parent_b", b.ID,
		"version", child.Version,
	)

	return child, nil
}

// MultiPointCrossover performs k-point crossover on two parent strategies.
// Instead of per-parameter coin flip, it splits the sorted parameter list
// at k points and alternates between parents in contiguous segments.
// This produces more contiguous inheritance patterns than uniform crossover.
//
// Args:
//
//	ctx - operation context (used for cancellation).
//	a - first parent strategy (must not be nil).
//	b - second parent strategy (must not be nil).
//	k - number of crossover points (must be >= 0).
//
// Returns:
//
//	*mutation.Strategy - the generated child strategy.
//	error - ErrNilParent if either parent is nil, ErrInvalidCrossoverPoints if k < 0.
func (c *Crossover) MultiPointCrossover(ctx context.Context, a, b *mutation.Strategy, k int) (*mutation.Strategy, error) {
	if a == nil || b == nil {
		return nil, fmt.Errorf("%w: both parents must be non-nil", ErrNilParent)
	}
	if k < 0 {
		return nil, fmt.Errorf("%w: got %d, want >= 0", ErrInvalidCrossoverPoints, k)
	}

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("multi-point crossover cancelled: %w", ctx.Err())
	default:
	}

	allKeys := collectParamKeys(a.Params, b.Params)
	sort.Strings(allKeys)

	childParams, desc := c.multiPointSelect(allKeys, a.Params, b.Params, k)
	promptTemplate := c.selectPromptTemplate(a, b)

	child := &mutation.Strategy{
		ID:                   uuid.New().String(),
		ParentID:             formatParentIDs(a.ID, b.ID),
		Version:              maxVersion(a.Version, b.Version) + 1,
		Params:               childParams,
		PromptTemplate:       promptTemplate,
		StrategyMutationType: mutation.MutationParameter,
		MutationDesc:         desc,
		Score:                -1,
		CreatedAt:            time.Now(),
	}

	slog.Debug("multi-point crossover completed",
		"child_id", child.ID,
		"parent_a", a.ID,
		"parent_b", b.ID,
		"crossover_points", k,
		"version", child.Version,
	)

	return child, nil
}

// uniformCrossParams applies uniform crossover to the parameter maps of two parents.
// For each key present in either parent, randomly selects from A or B.
// Returns the child params map and a description string of inheritance.
func (c *Crossover) uniformCrossParams(paramsA, paramsB map[string]any) (map[string]any, string) {
	allKeys := collectParamKeys(paramsA, paramsB)
	sort.Strings(allKeys)

	childParams := make(map[string]any, len(allKeys))
	var fromA, fromB []string

	for _, key := range allKeys {
		valA, existsA := paramsA[key]
		valB, existsB := paramsB[key]

		switch {
		case existsA && existsB:
			if c.rng.Float64() < 0.5 {
				childParams[key] = valA
				fromA = append(fromA, key)
			} else {
				childParams[key] = valB
				fromB = append(fromB, key)
			}
		case existsA:
			childParams[key] = valA
			fromA = append(fromA, key)
		default:
			childParams[key] = valB
			fromB = append(fromB, key)
		}
	}

	desc := buildInheritanceDesc(fromA, fromB, "uniform")
	return childParams, desc
}

// multiPointSelect selects parameters using k-point crossover on sorted keys.
// It splits the key list at k random positions and alternates source parent per segment.
func (c *Crossover) multiPointSelect(sortedKeys []string, paramsA, paramsB map[string]any, k int) (map[string]any, string) {
	n := len(sortedKeys)
	if n == 0 {
		return make(map[string]any), "multi-point crossover: no parameters"
	}

	// Generate k unique crossover point indices in [1, n-1].
	points := generateCrossoverPoints(c.rng, k, n)

	childParams := make(map[string]any, n)
	var fromA, fromB []string
	useA := true // Start with parent A for the first segment.

	prev := 0
	for _, pt := range points {
		for i := prev; i < pt; i++ {
			key := sortedKeys[i]
			if useA {
				if val, ok := paramsA[key]; ok {
					childParams[key] = val
				} else {
					childParams[key] = paramsB[key]
				}
				fromA = append(fromA, key)
			} else {
				if val, ok := paramsB[key]; ok {
					childParams[key] = val
				} else {
					childParams[key] = paramsA[key]
				}
				fromB = append(fromB, key)
			}
		}
		useA = !useA
		prev = pt
	}

	// Handle the last segment after the final crossover point.
	for i := prev; i < n; i++ {
		key := sortedKeys[i]
		if useA {
			if val, ok := paramsA[key]; ok {
				childParams[key] = val
			} else {
				childParams[key] = paramsB[key]
			}
			fromA = append(fromA, key)
		} else {
			if val, ok := paramsB[key]; ok {
				childParams[key] = val
			} else {
				childParams[key] = paramsA[key]
			}
			fromB = append(fromB, key)
		}
	}

	desc := buildInheritanceDesc(fromA, fromB, fmt.Sprintf("multi-point(k=%d)", k))
	return childParams, desc
}

// selectPromptTemplate returns the prompt template from the higher-scoring parent.
// If scores are equal, parent A's template is preferred.
func (c *Crossover) selectPromptTemplate(a, b *mutation.Strategy) string {
	if a.Score >= b.Score {
		return a.PromptTemplate
	}
	return b.PromptTemplate
}

// CrossoverInterface defines the contract for crossover operations.
// Implementations combine two parent strategies to produce a child strategy.
type CrossoverInterface interface {
	// Crossover performs crossover on two parent strategies and returns a child.
	Crossover(ctx context.Context, a, b *mutation.Strategy) (*mutation.Strategy, error)
}

// collectParamKeys returns the sorted union of all parameter keys from both parent maps.
func collectParamKeys(paramsA, paramsB map[string]any) []string {
	keySet := make(map[string]struct{}, len(paramsA)+len(paramsB))
	for k := range paramsA {
		keySet[k] = struct{}{}
	}
	for k := range paramsB {
		keySet[k] = struct{}{}
	}

	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// formatParentIDs combines two parent IDs into a single ParentID field.
func formatParentIDs(idA, idB string) string {
	return idA + "+" + idB
}

// maxVersion returns the larger of two version numbers.
func maxVersion(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// buildInheritanceDesc creates a human-readable description of which params came from which parent.
func buildInheritanceDesc(fromA, fromB []string, method string) string {
	parts := []string{fmt.Sprintf("crossover(%s): ", method)}
	if len(fromA) > 0 {
		parts = append(parts, fmt.Sprintf("from_A=[%s]", strings.Join(fromA, ",")))
	}
	if len(fromB) > 0 {
		parts = append(parts, fmt.Sprintf("from_B=[%s]", strings.Join(fromB, ",")))
	}
	if len(fromA) == 0 && len(fromB) == 0 {
		parts = append(parts, "no_parameters")
	}
	return strings.Join(parts, " ")
}

// generateCrossoverPoints generates k unique crossover point indices in range [1, n-1].
// If k >= n-1, returns all possible split positions.
func generateCrossoverPoints(rng *rand.Rand, k, n int) []int {
	maxPoints := n - 1
	if maxPoints <= 0 {
		return nil
	}
	if k >= maxPoints {
		k = maxPoints
	}

	// Use reservoir sampling via shuffle to pick k unique positions.
	positions := make([]int, maxPoints)
	for i := 0; i < maxPoints; i++ {
		positions[i] = i + 1
	}
	// Fisher-Yates shuffle first k elements.
	for i := 0; i < k; i++ {
		j := rng.Intn(maxPoints-i) + i
		positions[i], positions[j] = positions[j], positions[i]
	}

	result := positions[:k]
	sort.Ints(result) // Ensure points are in ascending order.
	return result
}
