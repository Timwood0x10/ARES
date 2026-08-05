package main

import (
	"os"

	api_tools "github.com/Timwood0x10/ares/api/tools"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// newToolRegistry creates the public tool registry with built-in + custom tools.
// The file tool is sandboxed to ARES_WORKSPACE_DIR (or the current working
// directory if the env var is unset) to prevent path-traversal attacks.
func newToolRegistry() (*api_tools.Registry, error) {
	r := api_tools.NewRegistry()
	workspaceDir := os.Getenv("ARES_WORKSPACE_DIR")
	if workspaceDir == "" {
		workspaceDir, _ = os.Getwd()
	}
	if err := api_tools.RegisterBuiltinTools(r, api_tools.WithFileSandboxDir(workspaceDir)); err != nil {
		return nil, err
	}
	return r, nil
}

// newToolBinder creates a sub.ToolBinder bridged from the internal core.Registry.
// This enables GetToolSchemas() to return tool schemas for LLM Chat API tool calling.
func newToolBinder(internalReg *core.Registry) sub.ToolBinder {
	binder := sub.NewToolBinder()
	binder.BridgeFromRegistry(internalReg)
	return binder
}
