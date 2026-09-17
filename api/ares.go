// Package ares is the public API for the ARES agent runtime.
//
// Import this package to access all functionality:
//
//	import "github.com/Timwood0x10/ares/api"
//
//	rt, err := api.New(api.WithOpenAI("gpt-4"), api.WithAPIKey("sk-..."))
//	defer rt.Close()
//
//	agent := rt.NewAgent("bot", api.WithInstruction("You are helpful"))
//	result, err := agent.Run(ctx, "hello")
package ares

import "github.com/Timwood0x10/ares/sdk"

// ---------------------------------------------------------------------------
// Runtime lifecycle
// ---------------------------------------------------------------------------

// Runtime is the top-level container that owns all components (LLM, memory,
// knowledge, evolution, tools, MCP). Create one with New or NewRuntime.
type Runtime = sdk.Runtime

// Option configures a Runtime at construction time.
type Option = sdk.Option

// ConfigOption is an alias for Option, used by config-file helpers.
type ConfigOption = sdk.ConfigOption

// New creates a Runtime with the given options. Returns an error if any
// component fails to initialize.
func New(opts ...Option) (*Runtime, error) {
	return sdk.New(opts...)
}

// NewRuntime creates a Runtime, panicking on initialization failure.
// Prefer New in production code.
func NewRuntime(opts ...Option) *Runtime {
	return sdk.NewRuntime(opts...)
}

// MustNew is a convenience wrapper around New that panics on error.
func MustNew() *Runtime {
	return sdk.MustNew()
}
