package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Timwood0x10/ares/internal/ares_config"
)

// TestAllowConfigDirForConfinesLoad pins C-3: allowConfigDirFor wires the
// ares_config path-traversal guard, which previously had ZERO production
// callers — the SECURITY.md-documented control was a runtime no-op (the
// default empty allow-list skipped the whole check). After wiring, Load
// accepts a config inside the allowed directory and rejects one escaping it.
func TestAllowConfigDirForConfinesLoad(t *testing.T) {
	dir := t.TempDir()
	inside := filepath.Join(dir, "ares.yaml")
	configContent := `
server:
  host: "localhost"
  port: 8080

llm:
  provider: "ollama"
  model: "llama3.2"
  timeout: 60
  max_tokens: 4096
`
	if err := os.WriteFile(inside, []byte(configContent), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	allowConfigDirFor(inside)
	t.Cleanup(func() { ares_config.SetAllowedConfigDir("") })

	if _, err := ares_config.Load(inside); err != nil {
		t.Fatalf("config inside the allowed dir must load: %v", err)
	}

	// A traversal that escapes the allowed directory must be rejected —
	// before the wiring this guard was skipped entirely.
	outside := filepath.Join(dir, "..", "escape.yaml")
	if _, err := ares_config.Load(outside); err == nil {
		t.Fatal("config outside the allowed dir must be rejected")
	}
}
