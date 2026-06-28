// Package agent provides error definitions for agent operations.
package agentapi

import "errors"

var (
	// ErrInvalidAgentID is returned when agent ID is empty or invalid.
	ErrInvalidAgentID = errors.New("invalid agent ID")

	// ErrAgentNotFound is returned when agent does not exist.
	ErrAgentNotFound = errors.New("agent not found")

	// ErrAgentAlreadyExists is returned when trying to create duplicate agent.
	ErrAgentAlreadyExists = errors.New("agent already exists")
)
