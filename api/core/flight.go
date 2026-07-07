// Package core provides core interfaces for the ARES system.
package core

import "context"

// FlightRecorder defines the interface for recording and replaying agent flights.
type FlightRecorder interface {
	// Record records an event in the flight.
	// Args:
	// ctx - operation context.
	// event - the event to record.
	// Returns error if recording fails.
	Record(ctx context.Context, event interface{}) error

	// Replay replays a flight by session ID.
	// Args:
	// sessionID - the session to replay.
	// Returns a replay iterator or error.
	Replay(ctx context.Context, sessionID string) (interface{}, error)

	// Stop stops the flight recorder.
	Stop()
}
