package sdk

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// importsPath reports whether the file's IMPORT BLOCK (comments and string
// literals excluded — only the parsed AST counts) imports the given package
// path. Scanning raw bytes would false-trigger on documentation that names
// the retired package.
func importsPath(data []byte, pkgPath string) bool {
	f, err := parser.ParseFile(token.NewFileSet(), "", data, parser.ImportsOnly)
	if err != nil {
		return false
	}
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, `"`) == pkgPath {
			return true
		}
	}
	return false
}

// repoRoot resolves the repository root from this test file's location
// (sdk/arch_test.go → repo root), so the architecture scans do not depend on
// the test's working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(filepath.Dir(file))
}

// walkGoFiles invokes fn for every .go file under root, skipping VCS and
// dependency directories. Unreadable entries are skipped, not fatal.
func walkGoFiles(t *testing.T, root string, fn func(path string, data []byte)) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "testdata", "generated-images":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		fn(path, data)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}

// TestNoInternalAgentloopImports locks the B4 deletion: no Go file anywhere
// in the repository may reference the retired internal/agentloop package.
// The ReAct engine was localized into the sdk (FriendlyErr, discovery
// adapters) and then deleted; a reintroduced import means a second
// execution loop outside the L2 session core.
func TestNoInternalAgentloopImports(t *testing.T) {
	root := repoRoot(t)
	pkg := "internal/" + "agentloop" // split so this scanner file cannot match itself
	var offenders []string
	walkGoFiles(t, root, func(path string, data []byte) {
		if importsPath(data, pkg) {
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				rel = path
			}
			offenders = append(offenders, rel)
		}
	})
	if len(offenders) > 0 {
		t.Fatalf("retired package %s imported in: %v", pkg, offenders)
	}
}

// TestL2ExecutionCoreConstructionLocked locks the single-construction-point
// contract: agentruntime.NewExecution may only be built at the two known
// assembly sites — sdk/l2.go (ensureL2, the SDK's core) and
// cmd/ares/agent_kernel.go (serve peer mode). A third construction site
// means a second execution core with its own session registry, planner and
// reaper — exactly the split the B3 convergence removed. Comment lines are
// ignored so documentation that names the constructor does not count.
func TestL2ExecutionCoreConstructionLocked(t *testing.T) {
	root := repoRoot(t)
	allowed := map[string]bool{
		filepath.Join("sdk", "l2.go"):                  true,
		filepath.Join("cmd", "ares", "agent_kernel.go"): true,
	}
	var offenders []string
	walkGoFiles(t, root, func(path string, data []byte) {
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			rel = path
		}
		if allowed[rel] {
			return
		}
		// Split the needle so this scanner file cannot match itself.
		needle := "agentruntime." + "NewExecution("
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if strings.Contains(line, needle) {
				offenders = append(offenders, rel)
				return
			}
		}
	})
	if len(offenders) > 0 {
		t.Fatalf("agentruntime.NewExecution constructed outside the locked assembly sites (sdk/l2.go, cmd/ares/agent_kernel.go): %v", offenders)
	}
}
