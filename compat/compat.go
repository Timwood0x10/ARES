// Package compat is the ARES Compatibility Layer — the ecosystem entry point.
//
// This file holds the top-level convenience registration helpers that bind
// the per-subsystem registries into a single façade. Application bootstrap
// code calls compat.RegisterLLM/RegisterVector/… instead of importing each
// subsystem registry directly.
package compat

import (
	"github.com/Timwood0x10/ares/compat/llm"
	"github.com/Timwood0x10/ares/compat/loader"
	"github.com/Timwood0x10/ares/compat/protocol"
	"github.com/Timwood0x10/ares/compat/tool"
	"github.com/Timwood0x10/ares/compat/vector"
)

// Default is the process-wide compat registry, pre-initialized with empty
// per-subsystem registries. Bootstrap code imports Default and calls the
// Register* helpers; runtime components look up via Default.LLM()/Vector()/….
var Default = &DefaultRegistries{
	llm:      llm.NewRegistry(),
	vector:   vector.NewRegistry(),
	loader:   loader.NewRegistry(),
	protocol: protocol.NewRegistry(),
	tool:     tool.NewRegistry(),
}

// DefaultRegistries bundles the per-subsystem registries into one holder.
// All fields are non-nil; access is safe without nil checks.
type DefaultRegistries struct {
	llm      *llm.Registry
	vector   *vector.Registry
	loader   *loader.Registry
	protocol *protocol.Registry
	tool     *tool.Registry
}

// LLM returns the LLM provider registry.
func (d *DefaultRegistries) LLM() *llm.Registry { return d.llm }

// Vector returns the vector store registry.
func (d *DefaultRegistries) Vector() *vector.Registry { return d.vector }

// Loader returns the document loader registry.
func (d *DefaultRegistries) Loader() *loader.Registry { return d.loader }

// Protocol returns the wire protocol registry.
func (d *DefaultRegistries) Protocol() *protocol.Registry { return d.protocol }

// Tool returns the tool registry.
func (d *DefaultRegistries) Tool() *tool.Registry { return d.tool }

// RegisterLLM registers an LLM provider factory into Default.
func RegisterLLM(name string, factory llm.Factory) error { return Default.llm.Register(name, factory) }

// RegisterVector registers a vector backend factory into Default.
func RegisterVector(name string, factory vector.Factory) error {
	return Default.vector.Register(name, factory)
}

// RegisterLoader registers a document loader factory into Default.
func RegisterLoader(name string, factory loader.Factory) error {
	return Default.loader.Register(name, factory)
}

// RegisterProtocol registers a protocol adapter factory into Default.
func RegisterProtocol(name string, factory protocol.Factory) error {
	return Default.protocol.Register(name, factory)
}

// RegisterTool registers a tool factory into Default.
func RegisterTool(name string, factory tool.Factory) error {
	return Default.tool.Register(name, factory)
}
