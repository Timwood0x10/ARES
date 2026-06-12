// Package experience provides experience conflict resolution service.
// This package re-exports types and services from internal/experience for backward compatibility.
package experience

import (
	"goagentx/internal/experience"
)

// ConflictResolver re-exports from internal/experience.
type ConflictResolver = experience.ConflictResolver

// NewConflictResolver re-exports from internal/experience.
func NewConflictResolver() *ConflictResolver {
	return experience.NewConflictResolver()
}
