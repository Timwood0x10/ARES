// Package scoring provides cost-controlled scoring infrastructure for the
// evolution system, including strategy hashing, score caching, and tiered
// scorer pipelines.
package scoring

import (
	"fmt"
	"hash/fnv"
	"sort"

	"goagentx/internal/evolution/mutation"
)

// ErrNilStrategy is returned when a nil strategy is passed to StrategyHash.
var ErrNilStrategy = fmt.Errorf("strategy must not be nil")

// StrategyHash computes a stable 64-bit hash for a strategy.
// Two strategies with the same params, prompt template, tool config,
// and model config will produce the same hash regardless of creation time or ID.
//
// Hash components (order matters for stability):
//   - Sorted params (key-value pairs, values converted to string)
//   - PromptTemplate
//   - Tools from Params["tools"] (if present)
//   - Model from Params["model"] (if present)
//
// Metadata fields that are excluded from the hash:
//   - Score, ID, ParentID, Version, CreatedAt, MutationDesc,
//     Name, StrategyMutationType — these are metadata, not identity.
//
// Args:
//
//	s - the strategy to hash (must not be nil).
//
// Returns:
//
//	uint64 - the computed hash value.
//	error - non-nil if s is nil.
func StrategyHash(s *mutation.Strategy) (uint64, error) {
	if s == nil {
		return 0, ErrNilStrategy
	}

	h := fnv.New64a()

	// Hash sorted params for order-independence.
	keys := make([]string, 0, len(s.Params))
	for k := range s.Params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		v := s.Params[k]
		_, _ = fmt.Fprintf(h, "%s=%v|", k, v)
	}

	// Hash prompt template.
	_, _ = fmt.Fprintf(h, "prompt=%s|", s.PromptTemplate)

	return h.Sum64(), nil
}
