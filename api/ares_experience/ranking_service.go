// Package experience provides experience ranking service.
// This package re-exports types and services from internal/experience for backward compatibility.
package experience

import (
	experience "github.com/Timwood0x10/ares/internal/ares_experience"
)

// RankingService re-exports from internal/experience.
type RankingService = experience.RankingService

// RankingWeights re-exports from internal/experience.
type RankingWeights = experience.RankingWeights

// NewRankingService re-exports from internal/experience.
func NewRankingService() *RankingService {
	return experience.NewRankingService()
}

// DefaultRankingWeights re-exports from internal/experience.
func DefaultRankingWeights() *RankingWeights {
	return experience.DefaultRankingWeights()
}
