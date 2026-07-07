// Package events — event store errors.
package events

import "errors"

// ErrInvalidEvent is returned when an event cannot be cast to the expected type.
var ErrInvalidEvent = errors.New("events: invalid event type")

// NewErrInvalidEvent creates a new ErrInvalidEvent.
func NewErrInvalidEvent() error {
	return ErrInvalidEvent
}
