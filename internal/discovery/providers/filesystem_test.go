package providers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Timwood0x10/ares/internal/discovery"
)

func TestClaudeProvider_Discover(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0750); err != nil {
		t.Fatal(err)
	}

	cfg := map[string]any{
		"mcpServers": map[string]any{
			"codegraph": map[string]any{
				"command": "/usr/local/bin/codegraph",
				"args":    []string{"serve", "--mcp"},
			},
		},
	}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	p := NewClaudeProvider(dir)
	if p.Name() != "claude" {
		t.Errorf("expected name 'claude', got %q", p.Name())
	}
	if p.Confidence() != discovery.ConfidenceHigh {
		t.Errorf("expected confidence %d, got %d", discovery.ConfidenceHigh, p.Confidence())
	}

	records, err := p.Discover(context.Background())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(records) < 1 {
		t.Fatalf("expected at least 1 record, got %d", len(records))
	}

	// Find the record from our test config.
	found := false
	for _, r := range records {
		if r.Endpoint == "/usr/local/bin/codegraph" {
			found = true
			if r.Metadata["config_name"] != "codegraph" {
				t.Errorf("expected config_name 'codegraph', got %q", r.Metadata["config_name"])
			}
		}
	}
	if !found {
		t.Error("expected to find record with endpoint '/usr/local/bin/codegraph'")
	}
}

func TestCursorProvider_Discover(t *testing.T) {
	home, _ := os.UserHomeDir()
	if home == "" {
		t.Skip("no home directory")
	}

	// Cursor config may or may not exist. Just verify provider creation.
	p := NewCursorProvider()
	if p.Name() != "cursor" {
		t.Errorf("expected name 'cursor', got %q", p.Name())
	}
}

func TestVSCodeProvider_Discover(t *testing.T) {
	dir := t.TempDir()
	vscodeDir := filepath.Join(dir, ".vscode")
	if err := os.MkdirAll(vscodeDir, 0750); err != nil {
		t.Fatal(err)
	}

	cfg := map[string]any{
		"servers": map[string]any{
			"my-server": map[string]any{
				"command": "node",
				"args":    []string{"server.js"},
			},
		},
	}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(filepath.Join(vscodeDir, "mcp.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	p := NewVSCodeProvider(dir)
	records, err := p.Discover(context.Background())
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Metadata["config_name"] != "my-server" {
		t.Errorf("expected config_name 'my-server', got %q", records[0].Metadata["config_name"])
	}
}

func TestARESProvider_Discover(t *testing.T) {
	home, _ := os.UserHomeDir()
	if home == "" {
		t.Skip("no home directory")
	}

	// ARES config may or may not exist. Just verify provider creation.
	p := NewARESProvider()
	if p.Name() != "ares" {
		t.Errorf("expected name 'ares', got %q", p.Name())
	}
	if p.Confidence() != discovery.ConfidenceMax {
		t.Errorf("expected confidence %d, got %d", discovery.ConfidenceMax, p.Confidence())
	}
}

func TestScanConfigFile_ClaudeFormat(t *testing.T) {
	dir := t.TempDir()
	cfg := map[string]any{
		"mcpServers": map[string]any{
			"server-a": map[string]any{
				"command": "/bin/server-a",
			},
		},
	}
	data, _ := json.Marshal(cfg)
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	records, err := scanConfigFile(path, "test", discovery.ConfidenceHigh)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Metadata["config_name"] != "server-a" {
		t.Errorf("expected config_name 'server-a', got %q", records[0].Metadata["config_name"])
	}
}

func TestScanConfigFile_ARESFormat(t *testing.T) {
	dir := t.TempDir()
	cfg := map[string]any{
		"servers": []map[string]any{
			{"name": "codegraph", "command": "/bin/codegraph", "args": []string{"serve"}},
		},
	}
	data, _ := json.Marshal(cfg)
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	records, err := scanConfigFile(path, "test", discovery.ConfidenceMax)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Metadata["config_name"] != "codegraph" {
		t.Errorf("expected config_name 'codegraph', got %q", records[0].Metadata["config_name"])
	}
	if records[0].Endpoint != "/bin/codegraph" {
		t.Errorf("expected endpoint '/bin/codegraph', got %q", records[0].Endpoint)
	}
}

func TestScanConfigFile_InvalidFile(t *testing.T) {
	_, err := scanConfigFile("/nonexistent/path", "test", discovery.ConfidenceHigh)
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestScanConfigFile_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	records, err := scanConfigFile(path, "test", discovery.ConfidenceHigh)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("expected 0 records for invalid JSON, got %d", len(records))
	}
}
