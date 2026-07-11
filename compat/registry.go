// Package compat is the ARES Compatibility Layer — the ecosystem entry point.
//
// ARES is "evolution included", not "batteries included". The compat layer
// provides the thin adapters that bind third-party ecosystem components
// (LLM providers, vector DBs, document loaders, wire protocols, tool shells)
// into ARES's internal runtime. ARES officially maintains only the 20% of
// components that 80% of users need (OpenAI, Ollama, pgvector, Markdown/PDF,
// MCP); everything else is a third-party plugin registered via the helpers
// in this package.
//
// Directory layout (per next_step.md):
//
//	compat/
//	    llm/        — LLM provider adapters (openai, ollama, anthropic, …)
//	    vector/     — Vector store adapters (pgvector, chroma, qdrant, …)
//	    loader/     — Document loaders (markdown, pdf, html, …)
//	    protocol/   — Wire protocol adapters (openai_api, mcp, http)
//	    tool/       — Tool registry and builtin tool adapters
//
// Registration entry points:
//
//	compat.RegisterLLM(name, factory)
//	compat.RegisterVector(name, factory)
//	compat.RegisterLoader(name, factory)
//	compat.RegisterProtocol(name, factory)
//	compat.RegisterTool(name, factory)
//
// Each subsystem keeps its own typed registry under its sub-package; the
// top-level helpers in compat.go delegate to the per-subsystem registries.
package compat

import "fmt"

// Sentinel errors for the compat layer.
var (
	// ErrNotFound is returned when a requested component is not registered.
	ErrNotFound = fmt.Errorf("compat: component not found")
	// ErrAlreadyRegistered is returned when registering a duplicate name.
	ErrAlreadyRegistered = fmt.Errorf("compat: component already registered")
)
