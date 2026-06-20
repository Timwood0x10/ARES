package mutation

import (
	"fmt"
	"math/rand"
)

// MutatorOption configures a Mutator instance.
type MutatorOption func(*Mutator) error

// WithParamRanges sets custom parameter ranges for mutation.
// Replaces the default parameter ranges entirely.
//
// Args:
//
//	ranges - the custom parameter range map to use.
//
// Returns:
//
//	MutatorOption - the configuration function.
func WithParamRanges(ranges map[string]ParamRange) MutatorOption {
	return func(m *Mutator) error {
		if len(ranges) == 0 {
			return fmt.Errorf("param ranges must not be empty")
		}
		m.paramRanges = ranges
		return nil
	}
}

// WithPromptPool sets the available prompt templates for prompt mutation.
//
// Args:
//
//	pool - the list of prompt template strings.
//
// Returns:
//
//	MutatorOption - the configuration function.
func WithPromptPool(pool []string) MutatorOption {
	return func(m *Mutator) error {
		if len(pool) == 0 {
			return fmt.Errorf("prompt pool must not be empty")
		}
		copied := make([]string, len(pool))
		copy(copied, pool)
		m.promptPool = copied
		return nil
	}
}

// WithSeed sets a deterministic seed for the random number generator.
// Using the same seed produces reproducible mutation results.
//
// Args:
//
//	seed - the random seed value.
//
// Returns:
//
//	MutatorOption - the configuration function.
func WithSeed(seed int64) MutatorOption {
	return func(m *Mutator) error {
		m.rng = rand.New(rand.NewSource(seed)) // #nosec G404 — strategy mutation doesn't need crypto rand
		return nil
	}
}
