package planner

import (
	"fmt"

	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// RegistryProvider adapts a core.Registry to the ToolProvider interface
// by extracting capability declarations from each registered tool.
//
// Usage:
//
//	reg := core.NewRegistry()
//	provider := planner.NewRegistryProvider(reg)
//	resolver, err := planner.NewToolResolver(provider)
type RegistryProvider struct {
	reg *core.Registry
}

// NewRegistryProvider creates a ToolProvider that reads tools and their
// capabilities directly from a core.Registry.
func NewRegistryProvider(reg *core.Registry) *RegistryProvider {
	return &RegistryProvider{reg: reg}
}

// ListTools returns all registered tool names.
func (p *RegistryProvider) ListTools() []string {
	return p.reg.List()
}

// GetToolCapabilities extracts capability names from the tool's Capabilities()
// method. This enables dynamic capability discovery: when a tool declares
// capabilities, the planner can resolve against them without static mapping.
//
// Returns nil (no error) if the tool is not found — the resolver treats this
// as "no dynamic capabilities" and falls back to the static capability mapping.
func (p *RegistryProvider) GetToolCapabilities(toolName string) ([]string, error) {
	tool, exists := p.reg.Get(toolName)
	if !exists {
		return nil, fmt.Errorf("tool %q not found", toolName)
	}

	caps := tool.Capabilities()
	if len(caps) == 0 {
		return nil, nil
	}

	names := make([]string, 0, len(caps))
	for _, c := range caps {
		if string(c) != "" {
			names = append(names, string(c))
		}
	}
	return names, nil
}
