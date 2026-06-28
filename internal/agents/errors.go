// Package agent provides error definitions for agent service.
package agents

import "errors"

var (
	// ErrInvalidAgentID is returned when agent ID is empty or invalid.
	ErrInvalidAgentID = errors.New("invalid agent ID")

	// ErrAgentNotFound is returned when agent does not exist.
	ErrAgentNotFound = errors.New("agent not found")

	// ErrAgentAlreadyExists is returned when trying to create duplicate agent.
	ErrAgentAlreadyExists = errors.New("agent already exists")

	// ErrInvalidTaskID is returned when task ID is empty or invalid.
	ErrInvalidTaskID = errors.New("invalid task ID")

	// ErrTaskNotFound is returned when task does not exist.
	ErrTaskNotFound = errors.New("task not found")

	// ErrInvalidConfig is returned when configuration is invalid.
	ErrInvalidConfig = errors.New("invalid configuration")
)
