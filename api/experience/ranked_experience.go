// Package experience provides experience ranking data structures.
// This package re-exports types from internal/experience for backward compatibility.
package experience

import (
	"goagentx/internal/experience"
)

// RankedExperience re-exports from internal/experience.
type RankedExperience = experience.RankedExperience

// Experience re-exports from internal/experience.
type Experience = experience.Experience

// ExperienceType constants re-exported from internal/experience.
const (
	ExperienceTypeSuccess = experience.ExperienceTypeSuccess
	ExperienceTypeFailure = experience.ExperienceTypeFailure
)
