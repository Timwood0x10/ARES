// Package callbacks provides lifecycle event hooks for LLM calls and agent execution.
package ares_callbacks

import (
	"sync"
	"time"
)

// Event represents a lifecycle event type.
type Event string

const (
	EventLLMStart   Event = "llm.start"
	EventLLMEnd     Event = "llm.end"
	EventLLMError   Event = "llm.error"
	EventLLMToken   Event = "llm.token"
	EventAgentStart Event = "agent.start"
	EventAgentEnd   Event = "agent.end"
	EventAgentError Event = "agent.error"
	EventToolStart  Event = "tool.start"
	EventToolEnd    Event = "tool.end"
	EventToolError  Event = "tool.error"
)

// Context carries metadata for a lifecycle event.
type Context struct {
	Event      Event
	AgentID    string
	ToolName   string
	Model      string
	Input      string
	Output     string
	Error      error
	Duration   time.Duration
	TokenCount int
	Extra      map[string]any
	// GoCtx is the standard Go context for trace propagation. When set, the
	// BridgeEventStore uses it instead of context.Background() so that event
	// emission participates in the caller's trace and cancellation chain.
	GoCtx context.Context
}

// Handler processes a lifecycle event.
type Handler func(ctx *Context)

// Emitter dispatches lifecycle events to registered handlers.
// Components should depend on this interface rather than the concrete Registry.
type Emitter interface {
	Emit(ctx *Context)
}

// CallbackRegistrar registers and queries callback event handlers.
// This interface decouples consumers from the concrete Registry implementation,
// following the Dependency Inversion Principle.
type CallbackRegistrar interface {
	// On registers a handler for the given event type.
	On(event Event, handler Handler)
	// Count returns the number of registered handlers for the given event type.
	Count(event Event) int
}

// Ensure Registry implements CallbackRegistrar at compile time.
var _ CallbackRegistrar = (*Registry)(nil)

// Ensure Registry implements Emitter at compile time.
var _ Emitter = (*Registry)(nil)

// Registry manages event handlers and dispatches events.
type Registry struct {
	handlers map[Event][]Handler
	mu       sync.RWMutex
}

// NewRegistry creates a new Registry instance.
// Returns:
//
//	*Registry - a new Registry instance.
func NewRegistry() *Registry {
	return &Registry{
		handlers: make(map[Event][]Handler),
	}
}

// On registers a handler for the given event type.
// Multiple handlers can be registered for the same event.
// Args:
//
//	event   - the event type to listen for.
//	handler - the handler function to invoke when the event is emitted.
func (r *Registry) On(event Event, handler Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.handlers[event] = append(r.handlers[event], handler)
}

// Emit dispatches an event to all registered handlers for the event type.
// Handlers are called sequentially in registration order.
// If no handlers are registered for the event, Emit is a no-op.
// Args:
//
//	ctx - the event context carrying metadata.
func (r *Registry) Emit(ctx *Context) {
	if ctx == nil {
		return
	}

	r.mu.RLock()
	handlers := make([]Handler, len(r.handlers[ctx.Event]))
	copy(handlers, r.handlers[ctx.Event])
	r.mu.RUnlock()

	for _, h := range handlers {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Error("handler panicked", "event", ctx.Event, "recover", r)
				}
			}()
			h(ctx)
		}()
	}
}

// Clear removes all registered handlers for all event types.
func (r *Registry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.handlers = make(map[Event][]Handler)
}

// Count returns the number of registered handlers for the given event type.
// Args:
//
//	event - the event type to count handlers for.
//
// Returns:
//
//	int - the number of handlers registered for the event.
func (r *Registry) Count(event Event) int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.handlers[event])
}
