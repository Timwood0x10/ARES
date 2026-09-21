//go:build e2e

// Serve production E2E — boots the real `ares serve` binary in the Peer
// runtime (Leader OFF, the default), submits tasks over HTTP, and asserts
// kernel scheduling evidence plus the external one-interface golden path.
// Requires a working ./ares.yaml with real LLM credentials; skipped when
// the config is absent.
//
// Build tag: e2e — these tests need a REAL LLM key and are intended to run
// locally only (go test -tags=e2e ./cmd/ares/). They are deliberately NOT
// part of the `integration` tag that CI runs, so CI stays hermetic.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// findRootConfig locates the repo-root ares.yaml from wherever the test runs.
// Integration tests execute with cmd/ares as the working directory, so the
// real LLM config (which lives at the repo root) is found by probing the
// relative paths that cover both run locations. Returns "" when no root
// ares.yaml exists (tests then skip). Shared by the e2e-tagged suites.
func findRootConfig(t *testing.T) string {
	t.Helper()
	for _, pth := range []string{"ares.yaml", "../ares.yaml", "../../ares.yaml"} {
		if _, err := os.Stat(pth); err == nil {
			return pth
		}
	}
	return ""
}

// serveE2EEnv is one booted `ares serve` process under test: the built
// binary, its working directory (repo root), the generated per-test config
// (a copy of the repo ares.yaml with server.port set to a private free port
// — two e2e cases must never fight over the yaml's shared port), and the
// derived client-side connection facts.
type serveE2EEnv struct {
	bin      string
	repoRoot string
	cfgPath  string
	logPath  string
	port     int
	bearer   string
	logFile  *os.File
	cmd      *exec.Cmd
}

// freeTCPPort reserves an ephemeral port and returns it for later use.
func freeTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if closeErr := l.Close(); closeErr != nil {
		t.Fatalf("release reserved port: %v", closeErr)
	}
	return port
}

// serveHTTPPort reads server.port from the given ares.yaml path, defaulting
// to 8080 when unparseable.
func serveHTTPPort(cfgPath string) int {
	port := 8080
	if data, err := os.ReadFile(cfgPath); err == nil {
		var cfg struct {
			Server struct {
				Port int `yaml:"port"`
			} `yaml:"server"`
		}
		if err := yaml.Unmarshal(data, &cfg); err == nil && cfg.Server.Port > 0 {
			port = cfg.Server.Port
		}
	}
	return port
}

// serveHTTPBearer returns the HTTP bearer credential external clients
// present to the serve write/read gates. The gates compare against
// security.api_key when set, else llm.api_key (resolveServeAPIKey) — both
// live in ares.yaml, the single config entry point; the env-var override
// layer was removed. The e2e client presents the same value an external
// user would copy from their ares.yaml.
func serveHTTPBearer(cfgPath string) string {
	if data, err := os.ReadFile(cfgPath); err == nil {
		var cfg struct {
			Security struct {
				APIKey string `yaml:"api_key"`
			} `yaml:"security"`
			LLM struct {
				APIKey string `yaml:"api_key"`
			} `yaml:"llm"`
		}
		if err := yaml.Unmarshal(data, &cfg); err == nil {
			if cfg.Security.APIKey != "" {
				return cfg.Security.APIKey
			}
			if cfg.LLM.APIKey != "" {
				return cfg.LLM.APIKey
			}
		}
	}
	return "test-e2e-key"
}

// writeServeTestConfig copies the repo-root ares.yaml to a temp directory
// with server.port overridden to a fresh free port, and returns the new
// path. Configuration still travels exclusively through ares.yaml — the
// test simply writes its own instance of that file (no flags, no env).
func writeServeTestConfig(t *testing.T, repoRoot string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot, "ares.yaml"))
	if err != nil {
		t.Fatalf("read repo ares.yaml: %v", err)
	}
	var cfg map[string]any
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse repo ares.yaml: %v", err)
	}
	server, _ := cfg["server"].(map[string]any)
	if server == nil {
		server = map[string]any{}
		cfg["server"] = server
	}
	server["port"] = freeTCPPort(t)
	out, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal test ares.yaml: %v", err)
	}
	path := filepath.Join(t.TempDir(), "ares.yaml")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatalf("write test ares.yaml: %v", err)
	}
	return path
}

// bootServeE2E builds the serve binary from the repo root that owns the
// found ares.yaml, writes a per-test config on a private port, starts
// `ares serve -c <config>` and returns the handle. The caller must call
// stop (registered via t.Cleanup).
func bootServeE2E(t *testing.T, ctx context.Context) *serveE2EEnv {
	t.Helper()
	cfgPath := findRootConfig(t)
	if cfgPath == "" {
		t.Skip("no root ares.yaml (real LLM credentials) — skipping serve E2E")
	}
	repoRoot := filepath.Dir(cfgPath)
	if repoRoot == "" || repoRoot == "." {
		repoRoot = ".."
	}

	bin := filepath.Join(t.TempDir(), "ares-serve")
	build := exec.CommandContext(ctx, "go", "build", "-o", bin, filepath.Join(repoRoot, "cmd", "ares"))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build serve: %v\n%s", err, out)
	}

	testCfg := writeServeTestConfig(t, repoRoot)
	logPath := filepath.Join(t.TempDir(), "serve-e2e.log")
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create log: %v", err)
	}

	// Peer runtime (default, Leader OFF). The HTTP write/read gates compare
	// the bearer against security.api_key / llm.api_key from ares.yaml — the
	// env-var override layer was removed (yaml is the single config entry
	// point), so e2e clients present the yaml key via serveHTTPBearer, like
	// any external user.
	//
	// exec.Command, NOT CommandContext: the test-scoped ctx fires (defer
	// cancel) BEFORE t.Cleanup runs, so binding the process to ctx would
	// SIGKILL serve before stop() could send its graceful SIGTERM — turning
	// every run's shutdown into a hard kill. stop() owns the lifetime.
	cmd := exec.Command(bin, "serve", "-c", testCfg)
	cmd.Dir = repoRoot
	cmd.Stdout = f
	cmd.Stderr = f
	if err := cmd.Start(); err != nil {
		_ = f.Close()
		t.Fatalf("start serve: %v", err)
	}
	env := &serveE2EEnv{
		bin:      bin,
		repoRoot: repoRoot,
		cfgPath:  testCfg,
		logPath:  logPath,
		port:     serveHTTPPort(testCfg),
		bearer:   serveHTTPBearer(testCfg),
		logFile:  f,
		cmd:      cmd,
	}
	t.Cleanup(env.stop)
	return env
}

// stop terminates the serve process gracefully (SIGTERM), escalating to
// SIGKILL if it does not exit within the grace period, and closes the log
// file. The bounded wait keeps a SIGTERM-ignoring serve from hanging the
// test run forever.
func (e *serveE2EEnv) stop() {
	if e.cmd != nil && e.cmd.Process != nil {
		_ = e.cmd.Process.Signal(syscall.SIGTERM)
		waited := make(chan error, 1)
		go func() { waited <- e.cmd.Wait() }()
		select {
		case <-waited:
		case <-time.After(10 * time.Second):
			_ = e.cmd.Process.Kill()
			<-waited
		}
	}
	if e.logFile != nil {
		_ = e.logFile.Close()
	}
}

// waitBootEvidence polls the serve log until every evidence string appears
// or the deadline expires; missing evidence is reported as a test error.
//
// time.Sleep is the correct synchronization here (code_rules_v2 §7.3
// exemption): the unit under test is a SEPARATE OS process — there is no
// in-process channel/WaitGroup to join — so bounded polling with a deadline
// is the only applicable primitive.
func (e *serveE2EEnv) waitBootEvidence(t *testing.T, ctx context.Context, evidence []string) map[string]bool {
	t.Helper()
	seen := map[string]bool{}
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			t.Fatalf("timeout: %v", ctx.Err())
		default:
		}
		data, err := os.ReadFile(e.logPath)
		if err == nil {
			text := string(data)
			for _, ev := range evidence {
				if strings.Contains(text, ev) {
					seen[ev] = true
				}
			}
			if len(seen) == len(evidence) {
				return seen
			}
		}
		time.Sleep(2 * time.Second)
	}
	return seen
}

// TestServeProductionE2E boots the real serve binary in the Peer runtime and
// watches its log for the full task lifecycle: peer agents registered → kernel
// taskfabric scheduler → sub-agent execution, with a task submitted over HTTP
// (POST /api/tasks). It is the production E2E proof that serve wires the whole
// peer runtime — real LLM + agents + kernel scheduler — not just the SDK path.
func TestServeProductionE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	env := bootServeE2E(t, ctx)

	// Wait for the peer runtime to come up: leader-OFF registration + kernel
	// scheduler evidence. These appear shortly after boot.
	evidence := []string{
		"serve: Leader OFF mode",
		"kernel scheduler:",
		"kernel: live flip to policy=taskfabric",
	}
	seen := env.waitBootEvidence(t, ctx, evidence)

	// Submit a task over HTTP (POST /api/tasks) — the peer-runtime user entry.
	submitErr := submitTaskViaHTTP(env.port, env.bearer, "coder", "fix the bug")
	if submitErr != nil {
		t.Fatalf("submit task over HTTP: %v", submitErr)
	}

	// The scheduler must show execution activity after the submission. The
	// peer path has no "task N completed" leader log; instead we wait for the
	// scheduler to attempt execution (its "kernel scheduler:" activity) — the
	// task itself requires the real LLM and completes asynchronously.
	sawSchedulerActivity := false
	activityDeadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(activityDeadline) {
		data, _ := os.ReadFile(env.logPath)
		if strings.Contains(string(data), "kernel scheduler: execute") ||
			strings.Contains(string(data), "no capable candidate") {
			sawSchedulerActivity = true
			break
		}
		time.Sleep(2 * time.Second)
	}

	logText, _ := os.ReadFile(env.logPath)
	t.Logf("serve E2E log:\n%s", string(logText))

	for _, e := range evidence {
		if !seen[e] {
			t.Errorf("missing scheduling evidence %q in serve log", e)
		}
	}
	if !sawSchedulerActivity {
		t.Errorf("no scheduler activity observed after HTTP task submission")
	}
	t.Logf("serve production E2E OK: peer runtime up, task submitted, scheduler evidence present")
}

// TestExternalSimpleQueryE2E is the stranger-view golden path: with only a
// working ares.yaml, an external client POSTs {"query": "..."} (no
// capability — the yaml default applies), polls GET /api/tasks/{id}, and
// receives a terminal task view. It is the end-to-end proof of the
// one-interface contract (plan/external-simple-api-plan.md §Phase 4).
//
// The assertion targets the SCHEDULING chain (submission admitted, L2
// capability stamped, terminal state reachable, GET readable) — an LLM
// failure still terminates the task (FAILED) and that is intentionally
// accepted here; response-quality checks belong to deployments with known
// good credentials.
func TestExternalSimpleQueryE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	env := bootServeE2E(t, ctx)
	evidence := []string{
		"serve: Leader OFF mode",
		"kernel scheduler:",
	}
	seen := env.waitBootEvidence(t, ctx, evidence)
	// Report missing evidence immediately so a boot failure surfaces as
	// such, not buried under the cascading POST failures below.
	for _, ev := range evidence {
		if !seen[ev] {
			t.Errorf("missing boot evidence %q in serve log", ev)
		}
	}

	taskID := postExternalQuery(t, env.port, env.bearer, "reply with the single word pong")
	view := pollExternalTask(t, env.port, env.bearer, taskID, 90*time.Second)

	if view.State != string(taskfabric.StateCompleted) && view.State != string(taskfabric.StateFailed) {
		t.Fatalf("terminal state = %q, want COMPLETED or FAILED", view.State)
	}
	if view.Capability == "" {
		t.Error("terminal view must carry a capability")
	}
	t.Logf("external golden path OK: task=%s state=%s quantum=%d capability=%s",
		view.TaskID, view.State, view.Quantum, view.Capability)
}

// postExternalQuery submits the minimal external body {"query": "..."} — no
// capability — and returns the accepted task id.
func postExternalQuery(t *testing.T, port int, bearer, query string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"query": query})
	if err != nil {
		t.Fatalf("marshal query body: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("http://localhost:%d/api/tasks", port), bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /api/tasks: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusAccepted {
		var detail map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&detail)
		t.Fatalf("POST /api/tasks = %d, want 202 (body: %v)", resp.StatusCode, detail)
	}
	var accepted struct {
		TaskID string `json:"task_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode submit response: %v", err)
	}
	if accepted.TaskID == "" {
		t.Fatal("submit response missing task_id")
	}
	return accepted.TaskID
}

// pollExternalTask polls GET /api/tasks/{id} until the task view reports a
// terminal state or the wait expires.
//
// time.Sleep polling is the §7.3 exemption for external-process state: the
// serve process is a separate OS process with no in-process sync primitive
// to join; bounded deadline polling is the applicable primitive.
func pollExternalTask(t *testing.T, port int, bearer, taskID string, wait time.Duration) taskStatusResponse {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		req, err := http.NewRequest(http.MethodGet,
			fmt.Sprintf("http://localhost:%d/api/tasks/%s", port, taskID), nil)
		if err != nil {
			t.Fatalf("build GET request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+bearer)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET /api/tasks/%s: %v", taskID, err)
		}
		var view taskStatusResponse
		decodeErr := json.NewDecoder(resp.Body).Decode(&view)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /api/tasks/%s = %d, want 200", taskID, resp.StatusCode)
		}
		if decodeErr != nil {
			t.Fatalf("decode task view: %v", decodeErr)
		}
		if view.State == string(taskfabric.StateCompleted) || view.State == string(taskfabric.StateFailed) {
			return view
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("task %s did not reach a terminal state within %s", taskID, wait)
	return taskStatusResponse{}
}

// submitTaskViaHTTP POSTs an explicit-capability task to the serve
// /api/tasks endpoint using the given port and bearer.
func submitTaskViaHTTP(port int, bearer, capability, input string) error {
	body, _ := json.Marshal(map[string]any{
		"capability": capability,
		"payload":    map[string]any{"task_desc": input},
	})
	req, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("http://localhost:%d/api/tasks", port), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("POST /api/tasks: status %d", resp.StatusCode)
	}
	return nil
}
