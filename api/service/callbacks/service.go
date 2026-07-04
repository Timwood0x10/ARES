// Package callbacks provides the public API for event callback registration.
package callbacks

import (
	internal "github.com/Timwood0x10/ares/internal/ares_callbacks"
)

// Registry wraps internal ares_callbacks.Registry for public consumption.
type Registry struct {
	inner *internal.Registry
}

// Event is a lifecycle event type.
type Event = internal.Event

// Context carries metadata for a lifecycle event.
type Context = internal.Context

// Handler processes a lifecycle event.
type Handler = internal.Handler

// Standard event types.
const (
	EventLLMStart   Event = internal.EventLLMStart
	EventLLMEnd     Event = internal.EventLLMEnd
	EventLLMError   Event = internal.EventLLMError
	EventLLMToken   Event = internal.EventLLMToken
	EventAgentStart Event = internal.EventAgentStart
	EventAgentEnd   Event = internal.EventAgentEnd
	EventAgentError Event = internal.EventAgentError
	EventToolStart  Event = internal.EventToolStart
	EventToolEnd    Event = internal.EventToolEnd
	EventToolError  Event = internal.EventToolError
)

// New creates a new callback registry.
func New() *Registry {
	return &Registry{inner: internal.NewRegistry()}
}

// On registers a handler for the given event type.
func (r *Registry) On(event Event, handler Handler) {
	r.inner.On(event, handler)
}

// Emit dispatches a lifecycle event to all registered handlers.
func (r *Registry) Emit(ctx *Context) {
	r.inner.Emit(ctx)
}

// Clear removes all registered handlers.
func (r *Registry) Clear() {
	r.inner.Clear()
}

// Count returns the number of registered handlers for the given event type.
func (r *Registry) Count(event Event) int {
	return r.inner.Count(event)
}
