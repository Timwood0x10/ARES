package builtin

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveFileToolsAllowedDir pins E-3/E-4: the configured dir
// (tools.file_sandbox_dir in ares.yaml) is the single knob for the file-tool
// sandbox, and the empty fallback is a process-PRIVATE directory — neither
// the working directory (privilege escalation into the source tree) nor the
// shared world-writable temp dir itself (any local user could pre-plant or
// read the agent's files).
func TestResolveFileToolsAllowedDir(t *testing.T) {
	t.Run("configured dir wins", func(t *testing.T) {
		dir, err := ResolveFileToolsAllowedDir("/custom/sandbox")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if dir != "/custom/sandbox" {
			t.Fatalf("expected the configured dir, got %q", dir)
		}
	})

	t.Run("empty config falls back to a private dir", func(t *testing.T) {
		dir, err := ResolveFileToolsAllowedDir("")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		// Clean both sides: os.TempDir() may carry a trailing separator.
		tempRoot := filepath.Clean(os.TempDir())
		if filepath.Clean(dir) == tempRoot {
			t.Fatal("fallback must not be the shared world-writable temp dir itself")
		}
		if filepath.Clean(filepath.Dir(dir)) != tempRoot {
			t.Fatalf("fallback must live under the temp dir, got %q", dir)
		}
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("fallback dir must exist: %v", err)
		}
		if !info.IsDir() {
			t.Fatalf("fallback must be a directory, got %v", info.Mode())
		}
	})
}
