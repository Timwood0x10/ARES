// Package agent provides error definitions for agent operations.
package agentapi

import (
	"fmt"

	apperrors "github.com/Timwood0x10/ares/internal/errors"
)

var (
	// ErrInvalidAgentID is returned when agent ID is empty or invalid.
	ErrInvalidAgentID = fmt.Errorf("invalid agent ID")

	// ErrAgentNotFound is returned when agent does not exist.
	// Wraps apperrors.ErrNotFound for generic checks via errors.Is(err, apperrors.ErrNotFound).
	ErrAgentNotFound = fmt.Errorf("agent not found: %w", apperrors.ErrNotFound)

	// ErrAgentAlreadyExists is returned when trying to create duplicate agent.
	ErrAgentAlreadyExists = fmt.Errorf("agent already exists")
)
