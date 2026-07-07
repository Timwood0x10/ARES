// Package flight provides the public API for flight recording and replay.
package flight

import (
	"context"

	"github.com/Timwood0x10/ares/internal/ares_events"
	internal "github.com/Timwood0x10/ares/internal/ares_flight"
)

// Config re-exports internal's flight recorder config.
type Config = internal.FlightRecorderConfig

// Recorder wraps internal/ares_flight.FlightRecorder for public consumption.
type Recorder struct {
	inner *internal.FlightRecorder
}

// New creates a new flight recorder.
func New(eventStore ares_events.EventStore) *Recorder {
	inner := internal.NewFlightRecorder(internal.FlightRecorderConfig{
		EventStore: eventStore,
	})
	return &Recorder{inner: inner}
}

// Start starts the flight recorder.
func (r *Recorder) Start(ctx context.Context) error {
	return r.inner.Start(ctx)
}

// Stop stops the flight recorder.
func (r *Recorder) Stop() {
	r.inner.Stop()
}

// Replay creates a replay session for a task.
func (r *Recorder) Replay(ctx context.Context, taskID string) (*internal.ReplaySession, error) {
	return r.inner.Replay(ctx, taskID)
}

// Timeline returns the flight timeline.
func (r *Recorder) Timeline() *internal.Timeline {
	return r.inner.Timeline()
}

// Graph returns the flight graph.
func (r *Recorder) Graph() *internal.Graph {
	return r.inner.Graph()
}

// Diagnostics returns the diagnostics engine.
func (r *Recorder) Diagnostics() *internal.DiagnosticsEngine {
	return r.inner.Diagnostics()
}
