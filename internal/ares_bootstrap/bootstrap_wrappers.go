package ares_bootstrap

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/internal/ares_events"
	evolution "github.com/Timwood0x10/ares/internal/ares_evolution"
	flight "github.com/Timwood0x10/ares/internal/ares_flight"
)

type flightRecorderWrapper struct {
	recorder *flight.FlightRecorder
}

// Diagnostics returns access to diagnostic reports.
func (w *flightRecorderWrapper) Diagnostics() evolution.DiagnosticsAccessor {
	return &diagnosticsAccessorWrapper{engine: w.recorder.Diagnostics()}
}

// EventStore returns the event store subscriber.
func (w *flightRecorderWrapper) EventStore() evolution.EventStoreSubscriber {
	return &eventStoreSubscriberWrapper{store: w.recorder.EventStoreRef()}
}

// diagnosticsAccessorWrapper wraps flight.DiagnosticsEngine to implement evolution.DiagnosticsAccessor.
type diagnosticsAccessorWrapper struct {
	engine *flight.DiagnosticsEngine
}

// Get retrieves the diagnostic report for a specific agent.
func (w *diagnosticsAccessorWrapper) Get(agentID string) *evolution.DiagnosticsReport {
	if w.engine == nil {
		return nil
	}

	records := w.engine.FilterByAgent(agentID)
	if len(records) == 0 {
		return nil
	}

	diagRecords := make([]evolution.DiagnosticRecord, len(records))
	for i, r := range records {
		diagRecords[i] = evolution.DiagnosticRecord{
			ID:         r.ID,
			AgentID:    r.AgentID,
			TaskID:     r.TaskID,
			Category:   string(r.Category),
			RootCause:  r.RootCause,
			Suggestion: r.Suggestion,
			Severity:   categorizeSeverity(r.Category),
		}
	}

	return &evolution.DiagnosticsReport{
		AgentID:   agentID,
		Records:   diagRecords,
		HasIssues: true,
	}
}

// categorizeSeverity converts DiagnosticCategory to a severity score (1-10).
func categorizeSeverity(cat flight.DiagnosticCategory) int {
	switch cat {
	case flight.DiagToolTimeout:
		return 5
	case flight.DiagLLMError:
		return 7
	case flight.DiagParseError:
		return 4
	case flight.DiagMemoryError:
		return 6
	case flight.DiagNetworkError:
		return 6
	case flight.DiagConfigError:
		return 3
	case flight.DiagConcurrencyError:
		return 8
	default:
		return 5
	}
}

// eventStoreSubscriberWrapper wraps ares_events.EventStore to implement evolution.EventStoreSubscriber.
type eventStoreSubscriberWrapper struct {
	store ares_events.EventStore
}

// Subscribe subscribes to ares_events from the underlying event store.
func (w *eventStoreSubscriberWrapper) Subscribe(ctx context.Context, filter ares_events.EventFilter) (<-chan *ares_events.Event, error) {
	if w.store == nil {
		return nil, fmt.Errorf("event store is nil")
	}
	return w.store.Subscribe(ctx, filter)
}
