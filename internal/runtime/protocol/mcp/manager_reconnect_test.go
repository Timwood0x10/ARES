package ares_mcp

import (
	"context"
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
