//go:build e2e

// Serve production E2E — boots the real `ares serve` binary, lets its
// built-in submitTasks loop push real tasks through the runtime, and asserts
// the kernel scheduling evidence appears in the log (leader split → kernel
// dispatch → sub-agent execution → completion). Requires a working ./ares.yaml
// with real LLM credentials (see examples/25-dual-endpoint-fallback for a
// template); skipped when the config is absent.
//
// Build tag: e2e — these tests need a REAL LLM key and are intended to run
// locally only (go test -tags=e2e ./cmd/ares/). They are deliberately NOT part
// of the `integration` tag that CI runs, so CI stays hermetic.
package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

// findRootConfig locates the repo-root ares.yaml from wherever the test runs.
// Integration tests execute with cmd/ares as the working directory, so the
// real LLM config (which lives at the repo root) is found by probing the
// relative paths that cover both run locations. Returns "" when no root
// ares.yaml exists (tests then skip).
func findRootConfig(t *testing.T) string {
	t.Helper()
	for _, p := range []string{"ares.yaml", "../ares.yaml", "../../ares.yaml"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// taskCompletedRe matches the serve log's completion line, which is printed
// as "task %d completed" (cmd/ares/agents.go:306) — the task number sits
// between the words, so a plain "task completed" substring never matches.
var taskCompletedRe = regexp.MustCompile(`task \d+ completed`)

// TestServeProductionE2E boots the real serve binary and watches its log for
// the full task lifecycle: leader planning (submitTasks), kernel taskfabric
// scheduling, sub-agent execution, and task completion. It is the production
// E2E proof that serve wires the whole runtime — real LLM + agents + kernel
// scheduler — not just the SDK path exercised by examples.
func TestServeProductionE2E(t *testing.T) {
	// The test runs with cmd/ares as the working directory; derive the repo
	// root from the found config path so the build target is always correct.
	cfgPath := findRootConfig(t)
	if cfgPath == "" {
		t.Skip("no root ares.yaml (real LLM credentials) — skipping serve production E2E")
	}
	repoRoot := filepath.Dir(cfgPath)
	if repoRoot == "" || repoRoot == "." {
		repoRoot = ".."
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	bin := filepath.Join(t.TempDir(), "ares-serve")
	build := exec.CommandContext(ctx, "go", "build", "-o", bin, filepath.Join(repoRoot, "cmd", "ares"))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build serve: %v\n%s", err, out)
	}

	logPath := filepath.Join(t.TempDir(), "serve-e2e.log")
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create log: %v", err)
	}
	defer func() { _ = f.Close() }()

	cmd := exec.CommandContext(ctx, bin, "serve", "--autopilot")
	// serve reads ares.yaml from its working directory; run it from the repo
	// root so the real LLM config is found.
	cmd.Dir = repoRoot
	cmd.Stdout = f
	cmd.Stderr = f
	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	defer func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_ = cmd.Wait()
	}()

	// Wait for the first task to complete plus the scheduling evidence. The
	// built-in submitTasks loop fires the first task ~3s after boot; a single
	// completed task proves the full path (submit → kernel flip → scheduler →
	// sub-agent execution → completion) without waiting for the slow 15s
	// cadence multiple times — keeps the test fast enough to rerun locally.
	deadline := time.Now().Add(90 * time.Second)
	completed := 0
	evidence := []string{
		"kernel: live flip to policy=taskfabric",
		"kernel scheduler:",
		"Leader dispatching tasks",
	}
	seen := map[string]bool{}
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			t.Fatalf("timeout: %v", ctx.Err())
		default:
		}
		data, err := os.ReadFile(logPath)
		if err == nil {
			text := string(data)
			completed = len(taskCompletedRe.FindAllString(text, -1))
			for _, e := range evidence {
				if strings.Contains(text, e) {
					seen[e] = true
				}
			}
			if completed >= 1 && len(seen) == len(evidence) {
				break
			}
		}
		time.Sleep(2 * time.Second)
	}

	logText, _ := os.ReadFile(logPath)
	t.Logf("serve E2E log:\n%s", string(logText))

	if completed < 1 {
		t.Fatalf("want >= 1 task completed, got %d", completed)
	}
	for _, e := range evidence {
		if !seen[e] {
			t.Errorf("missing scheduling evidence %q in serve log", e)
		}
	}
	t.Logf("serve production E2E OK: %d task completed, kernel scheduling evidence present", completed)
}
