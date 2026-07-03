// Package core provides core interfaces for the ARES system.
package core

import "context"

// Event represents a callback event.
type CallbackEvent string

const (
	// CallbackEventLLMStart is emitted when LLM processing starts.
	CallbackEventLLMStart CallbackEvent = "llm.start"
	// CallbackEventLLMEnd is emitted when LLM processing ends.
	CallbackEventLLMEnd CallbackEvent = "llm.end"
	// CallbackEventToolCall is emitted when a tool is called.
	CallbackEventToolCall CallbackEvent = "tool.call"
	// CallbackEventToolResult is emitted when a tool returns.
	CallbackEventToolResult CallbackEvent = "tool.result"
	// CallbackEventAgentEnd is emitted when an agent finishes.
	CallbackEventAgentEnd CallbackEvent = "agent.end"
)

// CallbackHandler processes callback events.
type CallbackHandler interface {
	// Handle processes a callback event.
	// Args:
	// ctx - operation context.
	// event - the event name.
	// data - event payload.
	Handle(ctx context.Context, event CallbackEvent, data map[string]interface{})
}

// CallbackRegistry defines the interface for registering and emitting callbacks.
type CallbackRegistry interface {
	// On registers a handler for a specific event.
	// Args:
	// event - the event to listen for.
	// handler - the handler to invoke.
	On(event CallbackEvent, handler CallbackHandler)

	// Emit emits an event to all registered handlers.
	// Args:
	// ctx - operation context.
	// event - the event to emit.
	// data - event payload.
	Emit(ctx context.Context, event CallbackEvent, data map[string]interface{})
}
