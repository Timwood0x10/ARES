package ares_mcp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// TestConnectTwiceRebindsToolsToNewClient is the #P1-18 regression: on
// reconnect, registerTools ran BEFORE the stale client's tools were
// unregistered. registerTools skips name conflicts (existing entry wins), so
// the registry kept the OLD client's tool — and after the old client was
// closed, the tool pointed at a dead connection forever. After a reconnect
// the registered tool must execute against the NEW transport.
func TestConnectTwiceRebindsToolsToNewClient(t *testing.T) {
	first := newTestServer(lifecycleTestTools, &ToolCallResult{
		Content: []ContentBlock{{Type: "text", Text: "from-first"}},
	})
	second := newTestServer(lifecycleTestTools, &ToolCallResult{
		Content: []ContentBlock{{Type: "text", Text: "from-second"}},
	})
	m := newTestManager(t, &MCPManagerConfig{}, core.NewRegistry())
	sc := &MCPServerConfig{Name: "mock", Enabled: true, Timeout: 2 * time.Second}
	ctx := context.Background()

	if err := m.connectWithTransport(ctx, "mock", sc, first); err != nil {
		t.Fatalf("first connect: %v", err)
	}
	if _, ok := m.registry.Get("mcp.mock.mock_tool"); !ok {
		t.Fatalf("tool must be registered after first connect; have %v", m.registry.List())
	}

	if err := m.connectWithTransport(ctx, "mock", sc, second); err != nil {
		t.Fatalf("second connect: %v", err)
	}

	// Re-fetch from the registry: the entry must have been replaced with the
	// SECOND client's tool. Under the bug registerTools skipped the name
	// conflict (existing entry wins), so the registry still held the first
	// client's tool — and after stale.Close() it pointed at a dead connection.
	tool, ok := m.registry.Get("mcp.mock.mock_tool")
	if !ok {
		t.Fatalf("tool must be registered after reconnect; have %v", m.registry.List())
	}

	// The registered tool must now be bound to the SECOND client: execute it
	// and check the response text. Under the bug the registry still held the
	// first client's tool, whose client is closed after the swap — the call
	// fails against the dead connection.
	callCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	res, err := tool.Execute(callCtx, map[string]interface{}{})
	if err != nil {
		t.Fatalf("tool execute after reconnect: %v (tool bound to the closed stale client?)", err)
	}
	data, _ := res.Data.(map[string]interface{})
	content, _ := data["content"].(string)
	if content != "from-second" {
		t.Fatalf("tool result = %q, want %q (registry kept the stale client's tool)", content, "from-second")
	}
}

// TestConnectTwice_RestoresStaleToolsWhenRegisterFails pins the reconnect
// rollback contract: the stale client's tools are unregistered BEFORE the new
// client's set is registered (see TestConnectTwiceRebindsToolsToNewClient), so
// a failure inside registerTools must put them back. The stale client is still
// the live m.clients entry at that point — without a restore the server stayed
// Connected with zero tools and no path could re-register them.
func TestConnectTwice_RestoresStaleToolsWhenRegisterFails(t *testing.T) {
	first := newTestServer(lifecycleTestTools, &ToolCallResult{
		Content: []ContentBlock{{Type: "text", Text: "from-first"}},
	})
	// A JSON array is valid JSON (so it survives the mock wire) but cannot be
	// unmarshalled into the jsonSchema struct, which makes NewMCPTool — and
	// therefore registerTools — fail.
	badSchema := []MCPToolDef{{
		Name:        "mock_tool",
		Description: "a tool with a schema that cannot convert",
		InputSchema: json.RawMessage(`[1,2,3]`),
	}}
	second := newTestServer(badSchema, nil)

	m := newTestManager(t, &MCPManagerConfig{}, core.NewRegistry())
	sc := &MCPServerConfig{Name: "mock", Enabled: true, Timeout: 2 * time.Second}
	ctx := context.Background()

	if err := m.connectWithTransport(ctx, "mock", sc, first); err != nil {
		t.Fatalf("first connect: %v", err)
	}
	if _, ok := m.registry.Get("mcp.mock.mock_tool"); !ok {
		t.Fatalf("tool must be registered after first connect; have %v", m.registry.List())
	}

	if err := m.connectWithTransport(ctx, "mock", sc, second); err == nil {
		t.Fatal("second connect must fail: its tool schema cannot be converted")
	}

	// The tool must still be registered — the unregister that preceded the
	// failed registerTools has to be rolled back.
	tool, ok := m.registry.Get("mcp.mock.mock_tool")
	if !ok {
		t.Fatalf("tool must survive a failed reconnect; have %v", m.registry.List())
	}

	// And it must still execute against the FIRST, still-live client. Under
	// the bug the registry entry was gone; had it survived without a restore
	// it would be bound to nothing.
	callCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	res, err := tool.Execute(callCtx, map[string]interface{}{})
	if err != nil {
		t.Fatalf("tool execute after failed reconnect: %v", err)
	}
	data, _ := res.Data.(map[string]interface{})
	content, _ := data["content"].(string)
	if content != "from-first" {
		t.Fatalf("tool result = %q, want %q (stale client was not restored)", content, "from-first")
	}
}
