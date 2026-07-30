package toolsource

import (
	"context"
	"errors"
	"fmt"

	core "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// Sentinel errors returned by ToolSource implementations.
var (
	// ErrNilToolSource is returned when Tools is called on a source whose
	// underlying dependency (e.g. registry) is nil, or on a nil receiver.
	ErrNilToolSource = errors.New("toolsource: tool source is nil")
)

// ToolSource discovers available executable tools from one or more origins.
// Implementations must be safe for concurrent use; Tools returns a snapshot
// that callers must not mutate.
type ToolSource interface {
	// Tools returns a snapshot of currently-available tools. Callers must
	// not mutate the returned slice or any element.
	Tools(ctx context.Context) ([]core.Tool, error)
	// OnChange registers a best-effort callback invoked when the tool set may
	// have changed. Static sources may register nothing. Callbacks must not
	// block (registry fires them under its write lock).
	OnChange(func())
	// Source returns a stable identifier used for dedup priority logging
	// (e.g. "static", "registry", "mcp").
	Source() string
}

// RegistrySource adapts a *core.Registry (which already holds builtin +
// MCP-registered tools). The registry owns its own locking; this source only
// reads via the registry's exported, lock-protected methods.
type RegistrySource struct {
	reg *core.Registry
}

// NewRegistrySource builds a RegistrySource over reg. If reg is nil, Tools
// returns ErrNilToolSource.
func NewRegistrySource(reg *core.Registry) *RegistrySource {
	return &RegistrySource{reg: reg}
}

// Tools returns a snapshot of all tools currently in the registry. The
// snapshot is eventually consistent: List and Get each take the registry
// RLock independently, so a tool unregistered between the two calls is
// simply skipped (Get returns ok=false).
func (s *RegistrySource) Tools(_ context.Context) ([]core.Tool, error) {
	if s == nil || s.reg == nil {
		return nil, ErrNilToolSource
	}
	names := s.reg.List()
	tools := make([]core.Tool, 0, len(names))
	for _, name := range names {
		if tool, ok := s.reg.Get(name); ok {
			tools = append(tools, tool)
		}
	}
	return tools, nil
}

// OnChange forwards fn to the registry's change notification. It is a no-op
// when reg is nil or fn is nil.
func (s *RegistrySource) OnChange(fn func()) {
	if s == nil || s.reg == nil || fn == nil {
		return
	}
	s.reg.OnChange(fn)
}

// Source returns the stable identifier "registry".
func (s *RegistrySource) Source() string { return "registry" }

// StaticSource wraps an explicit, immutable tool list (backing WithTools).
// The wrapped slice is copied at construction and never mutated thereafter,
// so concurrent reads are safe without locking.
type StaticSource struct {
	tools []core.Tool
}

// NewStaticSource builds a StaticSource from a defensive copy of tools.
func NewStaticSource(tools []core.Tool) *StaticSource {
	cp := make([]core.Tool, len(tools))
	copy(cp, tools)
	return &StaticSource{tools: cp}
}

// Tools returns a copy of the wrapped tool list so callers cannot mutate the
// source's internal state.
func (s *StaticSource) Tools(_ context.Context) ([]core.Tool, error) {
	if s == nil {
		return nil, ErrNilToolSource
	}
	cp := make([]core.Tool, len(s.tools))
	copy(cp, s.tools)
	return cp, nil
}

// OnChange is a no-op: static sources do not change after construction.
func (s *StaticSource) OnChange(func()) {}

// Source returns the stable identifier "static".
func (s *StaticSource) Source() string { return "static" }

// MultiSource merges multiple sources. On tool-name collision the FIRST source
// in the ordered list wins (priority: Static > Registry > MCP). The sources
// slice is set at construction and never mutated, so concurrent reads of the
// slice header are safe; per-source Tools calls use each source's own locking.
type MultiSource struct {
	sources []ToolSource
}

// NewMultiSource builds a MultiSource from the given sources, skipping nil
// entries. Sources are merged in order with first-wins name dedup.
func NewMultiSource(sources ...ToolSource) *MultiSource {
	filtered := make([]ToolSource, 0, len(sources))
	for _, s := range sources {
		if s != nil {
			filtered = append(filtered, s)
		}
	}
	return &MultiSource{sources: filtered}
}

// Tools merges each source's tools in order, deduplicating by tool.Name().
// The first occurrence of a name wins. A source error is wrapped with the
// source identifier and propagated.
func (m *MultiSource) Tools(ctx context.Context) ([]core.Tool, error) {
	if m == nil {
		return nil, ErrNilToolSource
	}
	seen := make(map[string]bool)
	merged := make([]core.Tool, 0)
	for _, src := range m.sources {
		tools, err := src.Tools(ctx)
		if err != nil {
			return nil, fmt.Errorf("toolsource: source %q: %w", src.Source(), err)
		}
		for _, t := range tools {
			if t == nil {
				continue
			}
			name := t.Name()
			if seen[name] {
				continue
			}
			seen[name] = true
			merged = append(merged, t)
		}
	}
	return merged, nil
}

// OnChange fans out fn to every constituent source's OnChange.
func (m *MultiSource) OnChange(fn func()) {
	if m == nil || fn == nil {
		return
	}
	for _, src := range m.sources {
		src.OnChange(fn)
	}
}

// Source returns the stable identifier "multi".
func (m *MultiSource) Source() string { return "multi" }
