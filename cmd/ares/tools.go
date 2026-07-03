package main

import (
	api_tools "github.com/Timwood0x10/ares/api/tools"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// newToolRegistry creates the public tool registry with built-in + custom tools.
func newToolRegistry() (*api_tools.Registry, error) {
	r := api_tools.NewRegistry()
	if err := api_tools.RegisterBuiltinTools(r); err != nil {
		return nil, err
	}
	return r, nil
}

// newToolBinder creates a sub.ToolBinder bridged from the internal core.Registry.
func newToolBinder(internalReg *core.Registry) sub.ToolBinder {
	binder := sub.NewToolBinder()
	binder.BridgeFromRegistry(internalReg)
	return binder
}
