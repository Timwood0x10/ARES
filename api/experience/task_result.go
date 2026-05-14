// Package experience provides task result data structures for experience distillation.
// This package re-exports types from internal/experience for backward compatibility.
package experience

import (
	internalexperience "github.com/Timwood0x10/goagent/internal/experience"
)

// TaskResult re-exports from internal/experience.
type TaskResult = internalexperience.TaskResult
