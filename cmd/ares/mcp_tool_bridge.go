// mcp_tool_bridge — mirrors MCP tools from the manager's protocol registry into
// the two registries the agent plane reads: the kernel registry (the planner's
// toolBinder) and the public /api/tools registry (the dashboard). The MCP
// manager owns the protocol-level registry; mirroring instead of sharing it
// preserves the startup order (servers start before builtin dependencies exist)
// and the builtin-wins collision policy. This is not a second advertisement
// path: the planner reads only the kernel registry, and the bridge adds/removes
// only the names it registered itself.
package main

import (
	"context"
	"errors"
	"reflect"
	"sync"

	api_tools "github.com/Timwood0x10/ares/internal/apitools"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// mcpToolSource is the read side of the MCP manager: the tools currently
// registered by connected servers. *ares_mcp.MCPManager implements it.
type mcpToolSource interface {
	RegisteredTools() []core.Tool
}

// mcpToolChangeNotifier is the write side of the MCP manager's change hook.
// *ares_mcp.MCPManager implements it; tests use a fake to prove the handler is
// actually installed, not just that Sync works when called by hand.
type mcpToolChangeNotifier interface {
	SetToolChangeHandler(func())
}

// installMCPToolBridge subscribes the bridge to manager changes and performs
// the initial mirror. Subscribe first, then Sync: a tools/listChanged that
// lands between the two calls triggers another Sync instead of being missed.
// Returning the bridge keeps the wiring in one place and lets tests exercise
// the subscription without a live server.
func installMCPToolBridge(source mcpToolSource, notifier mcpToolChangeNotifier, internal *core.Registry, public *api_tools.Registry) *mcpToolBridge {
	bridge := newMCPToolBridge(source, internal, public)
	if notifier != nil {
		notifier.SetToolChangeHandler(bridge.Sync)
	}
	bridge.Sync()
	return bridge
}

// mcpToolBridge mirrors the source's current tool set into two registries and
// removes tools it previously added. Sync is the handler MCPManager invokes
// after every tools/listChanged and health flip.
//
// Ownership is tracked per registry: a name the bridge could not register
// (collision with a builtin) is never removed by the bridge, so builtins keep
// winning exactly as they did when setupMCP copied once at startup.
type mcpToolBridge struct {
	source   mcpToolSource
	internal *core.Registry
	public   *api_tools.Registry

	mu            sync.Mutex // serializes Sync and guards owned*
	ownedInternal map[string]core.Tool
	ownedPublic   map[string]core.Tool
}

// newMCPToolBridge creates a bridge from the MCP manager's registry into the
// kernel and public registries.
func newMCPToolBridge(source mcpToolSource, internal *core.Registry, public *api_tools.Registry) *mcpToolBridge {
	return &mcpToolBridge{
		source:        source,
		internal:      internal,
		public:        public,
		ownedInternal: make(map[string]core.Tool),
		ownedPublic:   make(map[string]core.Tool),
	}
}

// Sync makes both registries match the source's current tool set. It is safe to
// call concurrently and from the MCP manager's change handler.
func (b *mcpToolBridge) Sync() {
	if b == nil || b.source == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	current := make(map[string]core.Tool)
	for _, tool := range b.source.RegisteredTools() {
		if tool == nil {
			continue
		}
		current[tool.Name()] = tool
	}

	// Removals first: an owned name that is gone from the source must leave
	// both registries, or the planner keeps advertising a tool that no longer
	// exists.
	for name := range b.ownedInternal {
		if _, ok := current[name]; !ok {
			b.unregisterInternal(name)
		}
	}
	for name := range b.ownedPublic {
		if _, ok := current[name]; !ok {
			b.unregisterPublic(name)
		}
	}

	// Additions and updates. Every tool is independent, so map order does not
	// matter; each registry serializes its own writes.
	for name, tool := range current {
		b.syncTool(name, tool)
	}
}

// syncTool mirrors one tool into both registries. The public mirror runs only
// when the kernel mirror holds the tool: a tool the kernel refused (name
// collision with a builtin) must not appear on the dashboard either, or the
// two surfaces would disagree about which tool answers the name.
func (b *mcpToolBridge) syncTool(name string, tool core.Tool) {
	// A reconnect can change the server's description, which in turn changes
	// the derived cap_* tags. Log old/new tags so a tool that loses a
	// capability (and is therefore narrowed out by the kernel filter) is an
	// observable transition rather than a silent disappearance.
	if old, owned := b.ownedInternal[name]; owned && toolChanged(old, tool) {
		log.Info(context.Background(), "mcpToolBridge.syncTool",
			"mcp bridge: tool metadata changed; re-mirroring",
			"tool", name,
			"old_tags", toolTags(old),
			"new_tags", toolTags(tool))
	}
	if !b.syncInternal(name, tool) {
		return
	}
	b.syncPublic(name, tool)
}

// syncInternal returns true when the kernel registry holds the current version
// of the tool after the call.
func (b *mcpToolBridge) syncInternal(name string, tool core.Tool) bool {
	old, owned := b.ownedInternal[name]
	if owned && !toolChanged(old, tool) {
		return true
	}
	if owned {
		if err := b.internal.Unregister(name); err != nil && !errors.Is(err, core.ErrToolNotFound) {
			log.Warn(context.Background(), "mcpToolBridge.Sync",
				"mcp bridge: cannot replace kernel tool", "tool", name, "error", err)
			return false
		}
		delete(b.ownedInternal, name)
	}
	if err := b.internal.Register(tool); err != nil {
		// A builtin with the same name wins; the MCP tool stays out of the
		// kernel surface, which is the documented collision policy.
		if !errors.Is(err, core.ErrToolAlreadyRegistered) {
			log.Warn(context.Background(), "mcpToolBridge.Sync",
				"mcp bridge: cannot mirror tool into kernel registry", "tool", name, "error", err)
		}
		return false
	}
	b.ownedInternal[name] = tool
	return true
}

// syncPublic mirrors the tool into the public registry. The public registry
// accepts overwrites silently, so an existing name is checked explicitly: the
// pre-existing tool (a builtin) wins.
func (b *mcpToolBridge) syncPublic(name string, tool core.Tool) {
	old, owned := b.ownedPublic[name]
	if owned && !toolChanged(old, tool) {
		return
	}
	if owned {
		if err := b.public.Unregister(name); err != nil {
			log.Warn(context.Background(), "mcpToolBridge.Sync",
				"mcp bridge: cannot replace public tool", "tool", name, "error", err)
			return
		}
		delete(b.ownedPublic, name)
	} else if _, exists := b.public.Get(name); exists {
		return
	}
	if err := b.public.Register(publicToolFunc(tool)); err != nil {
		log.Warn(context.Background(), "mcpToolBridge.Sync",
			"mcp bridge: cannot mirror tool into public registry", "tool", name, "error", err)
		return
	}
	b.ownedPublic[name] = tool
}

func (b *mcpToolBridge) unregisterInternal(name string) {
	delete(b.ownedInternal, name)
	if err := b.internal.Unregister(name); err != nil && !errors.Is(err, core.ErrToolNotFound) {
		log.Warn(context.Background(), "mcpToolBridge.Sync",
			"mcp bridge: cannot remove kernel tool", "tool", name, "error", err)
	}
}

func (b *mcpToolBridge) unregisterPublic(name string) {
	delete(b.ownedPublic, name)
	if err := b.public.Unregister(name); err != nil {
		log.Warn(context.Background(), "mcpToolBridge.Sync",
			"mcp bridge: cannot remove public tool", "tool", name, "error", err)
	}
}

// toolChanged reports whether a tool's routing-relevant metadata changed since
// the previous mirror. Description changes matter because MCP appends an
// availability note; parameters and tags change when a server updates its
// definition on reconnect.
func toolChanged(old, current core.Tool) bool {
	if old == nil || current == nil {
		return old != current
	}
	if old.Description() != current.Description() ||
		old.Category() != current.Category() ||
		!reflect.DeepEqual(old.Parameters(), current.Parameters()) {
		return true
	}
	return !reflect.DeepEqual(toolTags(old), toolTags(current))
}

func toolTags(tool core.Tool) map[string]string {
	if taggable, ok := tool.(core.TaggableTool); ok {
		return taggable.Tags()
	}
	return nil
}

// publicToolFunc adapts a kernel tool to the public registry's function shape.
// The closure captures the tool value, so a replaced tool cannot leak into the
// previous wrapper.
func publicToolFunc(tool core.Tool) api_tools.ToolFunc {
	return api_tools.ToolFunc{
		ToolName: tool.Name(),
		ToolDesc: tool.Description(),
		Fn: func(ctx context.Context, params map[string]any) (any, error) {
			res, err := tool.Execute(ctx, params)
			if err != nil {
				return nil, err
			}
			return res.Data, nil
		},
	}
}
