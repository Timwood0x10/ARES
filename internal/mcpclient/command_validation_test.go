package mcpclient

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveStdioCommand pins the rule both stdio paths share, including the half that would have
// broken existing configurations if it had been enforced as "absolute path or nothing".
func TestResolveStdioCommand(t *testing.T) {
	t.Run("empty is refused", func(t *testing.T) {
		if _, err := ResolveStdioCommand(""); err == nil || !strings.Contains(err.Error(), "required") {
			t.Fatalf("want a 'required' error, got %v", err)
		}
	})

	// A path relative to the working directory is the case that silently means a different binary
	// depending on where the process was started.
	for _, command := range []string{"./evil", "../bin/x", "bin/x", "sub/dir/tool"} {
		t.Run("relative path "+command, func(t *testing.T) {
			if _, err := ResolveStdioCommand(command); err == nil || !strings.Contains(err.Error(), "absolute") {
				t.Fatalf("%q was accepted: %v", command, err)
			}
		})
	}

	t.Run("absolute is passed through", func(t *testing.T) {
		got, err := ResolveStdioCommand("/usr/local/bin/tool")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "/usr/local/bin/tool" {
			t.Fatalf("absolute path was rewritten: %q", got)
		}
	})

	t.Run("missing bare name is reported", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir()) // empty directory: nothing to find
		if _, err := ResolveStdioCommand("ares-command-that-does-not-exist"); err == nil ||
			!strings.Contains(err.Error(), "PATH") {
			t.Fatalf("want a PATH error, got %v", err)
		}
	})

	// The compatibility case: the repository's own configuration and documentation name commands
	// without a path (configs/api_impl.yaml: "command: codegraph"). Refusing those would break
	// deployments that work today, so a bare name resolves through PATH — and the resolved path is
	// what gets executed, which pins the binary at start.
	t.Run("bare name on PATH resolves to an absolute path", func(t *testing.T) {
		dir := t.TempDir()
		name := "ares-stdio-probe"
		target := filepath.Join(dir, name)

		exe, err := os.Executable()
		if err != nil {
			t.Fatalf("resolve test binary: %v", err)
		}
		copyExecutable(t, exe, target)

		t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

		got, err := ResolveStdioCommand(name)
		if err != nil {
			t.Fatalf("bare name was refused: %v", err)
		}
		if !filepath.IsAbs(got) {
			t.Fatalf("resolved command is not absolute: %q", got)
		}
		if filepath.Base(got) != name {
			t.Fatalf("resolved to %q, want a path ending in %q", got, name)
		}
	})
}

// copyExecutable copies the test binary so the PATH lookup has something real to find on any
// platform, without depending on a host utility being installed.
func copyExecutable(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatalf("open %s: %v", src, err)
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatalf("create %s: %v", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		t.Fatalf("copy: %v", err)
	}
	if err := out.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}
