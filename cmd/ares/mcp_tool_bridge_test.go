package main

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	api_tools "github.com/Timwood0x10/ares/internal/apitools"
	"github.com/Timwood0x10/ares/internal/logger"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// bridgeTestTool is the minimal core.Tool the bridge needs; BaseTool is
// abstract and does not implement Execute.
type bridgeTestTool struct {
	name   string
	desc   string
	tags   map[string]string
	params *core.ParameterSchema
}

func (t *bridgeTestTool) Name() string                      { return t.name }
func (t *bridgeTestTool) Description() string               { return t.desc }
func (t *bridgeTestTool) Category() core.ToolCategory       { return core.CategoryCore }
func (t *bridgeTestTool) Capabilities() []core.Capability   { return nil }
func (t *bridgeTestTool) Parameters() *core.ParameterSchema { return t.params }
func (t *bridgeTestTool) Tags() map[string]string           { return t.tags }
func (t *bridgeTestTool) Execute(context.Context, map[string]interface{}) (core.Result, error) {
	return core.Result{}, nil
}

func testTool(name, desc string) *bridgeTestTool {
	return &bridgeTestTool{name: name, desc: desc}
}

// fakeMCPToolSource is the read side of the MCP manager for bridge tests.
type fakeMCPToolSource struct {
	mu    sync.Mutex
	tools []core.Tool
}

func (f *fakeMCPToolSource) RegisteredTools() []core.Tool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]core.Tool(nil), f.tools...)
}

func (f *fakeMCPToolSource) set(tools ...core.Tool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tools = tools
}

type fakeMCPNotifier struct {
	mu      sync.Mutex
	handler func()
}

func (f *fakeMCPNotifier) SetToolChangeHandler(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handler = fn
}

func (f *fakeMCPNotifier) fire() bool {
	f.mu.Lock()
	fn := f.handler
	f.mu.Unlock()
	if fn == nil {
		return false
	}
	fn()
	return true
}

func newBridgeFixture(t *testing.T) (*core.Registry, *api_tools.Registry) {
	t.Helper()
	internal := core.NewRegistry()
	public := api_tools.NewEmptyRegistry()
	builtin := testTool("grep", "builtin grep")
	require.NoError(t, internal.Register(builtin))
	require.NoError(t, public.Register(publicToolFunc(builtin)))
	return internal, public
}

func requireToolDescription(t *testing.T, reg *core.Registry, name, want string) {
	t.Helper()
	tool, ok := reg.Get(name)
	require.True(t, ok, "tool %s must exist", name)
	assert.Equal(t, want, tool.Description())
}

func requirePublicDescription(t *testing.T, reg *api_tools.Registry, name, want string) {
	t.Helper()
	tool, ok := reg.Get(name)
	require.True(t, ok, "public tool %s must exist", name)
	assert.Equal(t, want, tool.Description())
}

// TestMCPToolBridge_MirrorsAddsUpdatesAndRemoves is the contract the old
// one-shot copy missed: after the initial bridge, an MCP server can add, change
// or remove a tool, and both the kernel and public surfaces must follow.
func TestMCPToolBridge_MirrorsAddsUpdatesAndRemoves(t *testing.T) {
	internal, public := newBridgeFixture(t)
	weather := testTool("mcp.weather", "Fetch weather over HTTP")
	echo := testTool("mcp.echo", "Echoes its input back")
	source := &fakeMCPToolSource{tools: []core.Tool{weather, echo}}
	bridge := newMCPToolBridge(source, internal, public)

	bridge.Sync()
	requireToolDescription(t, internal, "mcp.weather", "Fetch weather over HTTP")
	requireToolDescription(t, internal, "mcp.echo", "Echoes its input back")
	requirePublicDescription(t, public, "mcp.weather", "Fetch weather over HTTP")
	requirePublicDescription(t, public, "mcp.echo", "Echoes its input back")

	// The server reconnects with an updated weather description, drops echo and
	// adds exec.
	weatherUpdated := testTool("mcp.weather", "Fetch weather over HTTPS")
	exec := testTool("mcp.exec", "Run a shell command")
	source.set(weatherUpdated, exec)
	bridge.Sync()

	requireToolDescription(t, internal, "mcp.weather", "Fetch weather over HTTPS")
	requirePublicDescription(t, public, "mcp.weather", "Fetch weather over HTTPS")
	requireToolDescription(t, internal, "mcp.exec", "Run a shell command")
	requirePublicDescription(t, public, "mcp.exec", "Run a shell command")

	_, ok := internal.Get("mcp.echo")
	assert.False(t, ok, "a removed MCP tool must leave the kernel registry")
	_, ok = public.Get("mcp.echo")
	assert.False(t, ok, "a removed MCP tool must leave the public registry")

	// Idempotence: a health flip may notify without a tool-set change.
	bridge.Sync()
	requireToolDescription(t, internal, "mcp.weather", "Fetch weather over HTTPS")
}

// TestMCPToolBridge_LogsTagNarrowingOnChange pins the observability gap: a
// reconnect that removes a capability keyword changes the derived tags, and
// the kernel filter may then hide the tool. The bridge must leave an explicit
// old/new-tags line so that disappearance is attributable.
func TestMCPToolBridge_LogsTagNarrowingOnChange(t *testing.T) {
	oldLog := log
	var buf strings.Builder
	log = logger.NewWithBase("ares", slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { log = oldLog })

	internal, public := newBridgeFixture(t)
	old := testTool("mcp.weather", "Fetch weather over HTTP")
	old.tags = map[string]string{"cap_network": "http"}
	source := &fakeMCPToolSource{tools: []core.Tool{old}}
	bridge := newMCPToolBridge(source, internal, public)
	bridge.Sync()
	buf.Reset()

	narrowed := testTool("mcp.weather", "Fetch weather")
	source.set(narrowed)
	bridge.Sync()

	out := buf.String()
	assert.Contains(t, out, "mcp bridge: tool metadata changed; re-mirroring")
	assert.Contains(t, out, "tool=mcp.weather")
	assert.Contains(t, out, "old_tags=")
	assert.Contains(t, out, "cap_network")
}

// TestInstallMCPToolBridge_SubscribesToManagerChanges is the wiring half of the
// fix: the initial Sync alone was the old behavior. This test proves the
// manager's change handler is installed and that firing it (what a reconnect /
// tools-list change does in production) reaches both registries.
func TestInstallMCPToolBridge_SubscribesToManagerChanges(t *testing.T) {
	internal, public := newBridgeFixture(t)
	source := &fakeMCPToolSource{tools: []core.Tool{testTool("mcp.echo", "Echoes input")}}
	notifier := &fakeMCPNotifier{}

	installMCPToolBridge(source, notifier, internal, public)
	requireToolDescription(t, internal, "mcp.echo", "Echoes input")

	source.set(testTool("mcp.echo", "Echoes input"), testTool("mcp.exec", "Run a command"))
	require.True(t, notifier.fire(), "install must subscribe the bridge to manager changes")
	requireToolDescription(t, internal, "mcp.exec", "Run a command")
	requirePublicDescription(t, public, "mcp.exec", "Run a command")
}

// TestMCPToolBridge_KernelCollisionKeepsBuiltin is the collision policy: an MCP
// tool that shares a name with a builtin must not replace it in either surface.
func TestMCPToolBridge_KernelCollisionKeepsBuiltin(t *testing.T) {
	internal, public := newBridgeFixture(t)
	source := &fakeMCPToolSource{tools: []core.Tool{
		testTool("grep", "mcp grep should lose"),
	}}
	bridge := newMCPToolBridge(source, internal, public)

	bridge.Sync()
	requireToolDescription(t, internal, "grep", "builtin grep")
	requirePublicDescription(t, public, "grep", "builtin grep")

	// Removing the MCP tool must not remove the builtin it never owned.
	source.set()
	bridge.Sync()
	requireToolDescription(t, internal, "grep", "builtin grep")
	requirePublicDescription(t, public, "grep", "builtin grep")
}

// TestMCPToolBridge_PublicOnlyCollisionIsNotOverwritten covers a name that
// exists only in the public registry: the API surface keeps its tool, while the
// kernel may still hold the MCP tool because there was no kernel collision.
func TestMCPToolBridge_PublicOnlyCollisionIsNotOverwritten(t *testing.T) {
	internal := core.NewRegistry()
	public := api_tools.NewEmptyRegistry()
	require.NoError(t, public.Register(publicToolFunc(testTool("dashboard.only", "dashboard builtin"))))

	source := &fakeMCPToolSource{tools: []core.Tool{
		testTool("dashboard.only", "mcp version must not overwrite"),
	}}
	bridge := newMCPToolBridge(source, internal, public)
	bridge.Sync()

	requireToolDescription(t, internal, "dashboard.only", "mcp version must not overwrite")
	requirePublicDescription(t, public, "dashboard.only", "dashboard builtin")

	source.set()
	bridge.Sync()
	_, ok := internal.Get("dashboard.only")
	assert.False(t, ok, "the bridge must remove the MCP tool it owned in the kernel")
	requirePublicDescription(t, public, "dashboard.only", "dashboard builtin")
}
