// Package memory provides error definitions for memory service.
package memoryservice

import "errors"

var (
	// ErrInvalidUserID is returned when user ID is empty.
	ErrInvalidUserID = errors.New("invalid user ID")

	// ErrInvalidSessionID is returned when session ID is empty.
	ErrInvalidSessionID = errors.New("invalid session ID")

	// ErrInvalidRole is returned when role is empty.
	ErrInvalidRole = errors.New("invalid role")

	// ErrInvalidContent is returned when content is empty.
	ErrInvalidContent = errors.New("invalid content")

	// ErrInvalidTaskID is returned when task ID is empty.
	ErrInvalidTaskID = errors.New("invalid task ID")

	// ErrInvalidQuery is returned when query is empty.
	ErrInvalidQuery = errors.New("invalid query")

	// ErrInvalidConfig is returned when configuration is invalid.
	ErrInvalidConfig = errors.New("invalid configuration")

	// ErrSessionNotFound is returned when session does not exist.
	ErrSessionNotFound = errors.New("session not found")

	// ErrTaskNotFound is returned when task does not exist.
	ErrTaskNotFound = errors.New("task not found")
)
