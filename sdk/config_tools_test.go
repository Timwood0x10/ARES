package sdk

import (
	"strings"
	"testing"
)

// TestToOptions_ToolsMCPWired locks E-9: ConfigFile.Tools.MCP was parsed from
// YAML but ToOptions never read it, so an `ares init`-generated config with a
// `tools.mcp` list connected nothing — the keys existed, validated, and were
// silently dropped. MCP is the one field in that block with a real SDK
// capability behind it (WithMCP), so it must be wired.
func TestToOptions_ToolsMCPWired(t *testing.T) {
	cfg := &ConfigFile{
		LLM: LLMFileConfig{Provider: "ollama"},
	}
	cfg.Tools.MCP = []string{"npx @modelcontextprotocol/server-filesystem ./data"}

	opts, err := cfg.ToOptions()
	if err != nil {
		t.Fatalf("ToOptions error: %v", err)
	}

	var applied = defaultConfig()
	for _, opt := range opts {
		if err := opt(applied); err != nil {
			t.Fatalf("apply option: %v", err)
		}
	}

	if len(applied.mcpConns) != 1 {
		t.Fatalf("expected 1 mcp connection from tools.mcp, got %d", len(applied.mcpConns))
	}
	conn := applied.mcpConns[0]
	if conn.Command != "npx" {
		t.Errorf("command = %q, want %q", conn.Command, "npx")
	}
	wantArgs := []string{"@modelcontextprotocol/server-filesystem", "./data"}
	if strings.Join(conn.Args, " ") != strings.Join(wantArgs, " ") {
		t.Errorf("args = %v, want %v", conn.Args, wantArgs)
	}
}

// TestToOptions_ReflectionUnsupported locks the other half of E-9:
// ConfigFile.Reflection.Enabled has no SDK implementation (there is no
// WithReflection, and no consumer of the flag anywhere in the tree). Wiring it
// to nothing would be a fake implementation, so enabling it must fail loudly
// instead of drifting into a silent no-op. The zero value (false) is the
// documented default and must keep working.
func TestToOptions_ReflectionUnsupported(t *testing.T) {
	cfg := &ConfigFile{
		LLM: LLMFileConfig{Provider: "ollama"},
	}
	cfg.Reflection.Enabled = true

	if _, err := cfg.ToOptions(); err == nil {
		t.Fatal("reflection.enabled=true must be rejected: the sdk has no reflection implementation")
	} else if !strings.Contains(err.Error(), "reflection") {
		t.Errorf("error should name the unsupported key, got: %v", err)
	}

	// Default (disabled) must still work.
	cfg.Reflection.Enabled = false
	if _, err := cfg.ToOptions(); err != nil {
		t.Fatalf("reflection disabled (default) must not error: %v", err)
	}
}
