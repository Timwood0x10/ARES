package discovery

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// fakeLookup implements LookupFunc with an install map: a command name is
// "installed" iff it appears in the map (value is its resolved path).
func fakeLookup(installed map[string]string) LookupFunc {
	return func(name string) (string, error) {
		path, ok := installed[name]
		if !ok {
			return "", errors.New("command not found")
		}
		return path, nil
	}
}

// fakeExec implements ExecFunc: returns "usage: <name> <args>" for --help and
// "<name>:<joined-args>" for executions.
func fakeExec(_ context.Context, name string, args []string) ([]byte, error) {
	if len(args) == 1 && args[0] == "--help" {
		return []byte("\nusage: " + name + " [options]\n"), nil
	}
	joined := ""
	for i, a := range args {
		if i > 0 {
			joined += ","
		}
		joined += a
	}
	return []byte(name + ":" + joined), nil
}

func TestDiscoverer_DiscoverFindsInstalledCommands(t *testing.T) {
	d := NewDiscoverer(
		[]string{"git", "missing-cmd"},
		WithLookup(fakeLookup(map[string]string{"git": "/usr/bin/git"})),
		WithExec(fakeExec),
	)

	tools, err := d.Discover(context.Background())
	require.NoError(t, err)
	require.Len(t, tools, 1, "only installed commands should be discovered")

	git := tools[0]
	assert.Equal(t, "git", git.Name())
	assert.Contains(t, git.Description(), "usage: git", "description should come from --help first line")
}

func TestDiscoverer_DiscoverEmptyAllowlist(t *testing.T) {
	d := NewDiscoverer(nil, WithLookup(fakeLookup(nil)), WithExec(fakeExec))
	tools, err := d.Discover(context.Background())
	require.NoError(t, err)
	assert.Empty(t, tools)
}

func TestCommandTool_ExecuteRunsArguments(t *testing.T) {
	tool := NewCommandTool("git", "usage: git", fakeExec, func(name string) bool { return name == "git" })

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"args": []interface{}{"status", "--short"},
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	data, ok := result.Data.(map[string]interface{})
	require.True(t, ok, "Data should be a map")
	stdout, ok := data["stdout"].(string)
	require.True(t, ok, "stdout should be a string")
	assert.Equal(t, "git:status,--short", stdout)
}

func TestCommandTool_ExecuteRejectsNonAllowlistedName(t *testing.T) {
	tool := NewCommandTool("rm", "usage: rm", fakeExec, func(name string) bool { return name == "git" })

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"args": []interface{}{"-rf", "/"},
	})
	require.NoError(t, err)
	assert.False(t, result.Success, "non-allowlisted command must be rejected")
	assert.Contains(t, result.Error, "allowlist")
}

func TestCommandTool_ExecutePropagatesCommandError(t *testing.T) {
	failExec := func(_ context.Context, _ string, _ []string) ([]byte, error) {
		return nil, errors.New("exit status 1")
	}
	tool := NewCommandTool("git", "usage: git", failExec, func(string) bool { return true })

	result, err := tool.Execute(context.Background(), nil)
	require.NoError(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, result.Error, "exit status 1")
}

func TestCommandTool_ImplementsToolInterface(t *testing.T) {
	tool := NewCommandTool("git", "usage: git", fakeExec, func(string) bool { return true })
	var _ core.Tool = tool
	assert.Equal(t, core.CategorySystem, tool.Category())
	assert.Empty(t, tool.Capabilities())
	params := tool.Parameters()
	require.NotNil(t, params)
	require.NotNil(t, params.Properties["args"], "args parameter must be declared")
}

// TestCommandTool_ExecuteAcceptsStringSlice verifies the []string parameter
// shape is accepted (previously only []interface{} worked, and a []string
// would silently run the command with no arguments).
func TestCommandTool_ExecuteAcceptsStringSlice(t *testing.T) {
	tool := NewCommandTool("git", "usage: git", fakeExec, func(string) bool { return true })

	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"args": []string{"status", "--short"},
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	data, ok := result.Data.(map[string]interface{})
	require.True(t, ok)
	stdout, ok := data["stdout"].(string)
	require.True(t, ok)
	assert.Equal(t, "git:status,--short", stdout)
}

// TestCommandTool_ExecuteOversizedOutput verifies an excessive command output
// is rejected instead of exhausting memory.
func TestCommandTool_ExecuteOversizedOutput(t *testing.T) {
	bigOutput := make([]byte, maxCommandOutputBytes+1)
	for i := range bigOutput {
		bigOutput[i] = 'x'
	}
	// the exec closure itself rejects over-cap output with an
	// error (single-point enforcement) — it never returns partial bytes.
	bigExec := func(_ context.Context, name string, _ []string) ([]byte, error) {
		return nil, fmt.Errorf("command %q output exceeds %d bytes; refusing to return partial output", name, maxCommandOutputBytes)
	}
	tool := NewCommandTool("git", "usage: git", bigExec, func(string) bool { return true })

	result, err := tool.Execute(context.Background(), map[string]interface{}{"args": []string{}})
	require.NoError(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, result.Error, "output exceeds")
}

// TestLimitedBuffer_CapsDuringRun is the #49 regression: the output cap must
// be enforced WHILE the command runs (the writer discards past the limit),
// not after cmd.Run returns. A chatty command (e.g. `yes`) would otherwise
// exhaust memory before a post-run size check ever fires.
func TestLimitedBuffer_CapsDuringRun(t *testing.T) {
	var kills int32
	lb := &limitedBuffer{limit: 1024, onExceed: func() { atomic.AddInt32(&kills, 1) }}
	chunk := make([]byte, 512)
	for i := range chunk {
		chunk[i] = 'x'
	}
	// Write 512 KiB in 512-byte chunks — far past the 1 KiB limit.
	for i := 0; i < 1024; i++ {
		n, err := lb.Write(chunk)
		require.NoError(t, err, "write %d", i)
		require.Equal(t, len(chunk), n, "Write must report full acceptance (it discards silently)")
	}
	assert.True(t, lb.exceeded.Load(), "exceeded flag must be set once the cap is hit")
	assert.LessOrEqual(t, lb.buf.Len(), 1024, "buffered bytes must never exceed the limit")
	assert.Equal(t, 1024, lb.buf.Len(), "first limit bytes are retained")

	// Writes after the cap are discarded without growing the buffer, and the
	// kill hook fired exactly once.
	more := make([]byte, 4096)
	n, err := lb.Write(more)
	require.NoError(t, err)
	assert.Equal(t, len(more), n)
	assert.Equal(t, 1024, lb.buf.Len())
	assert.Equal(t, int32(1), atomic.LoadInt32(&kills), "onExceed must fire exactly once")
}

// TestLimitedBuffer_ExactLimitHitNotExceeded: writing exactly up to (but not
// past) the limit must not set exceeded.
func TestLimitedBuffer_ExactLimitHitNotExceeded(t *testing.T) {
	lb := &limitedBuffer{limit: 100}
	n, err := lb.Write(make([]byte, 60))
	require.NoError(t, err)
	assert.Equal(t, 60, n)
	n, err = lb.Write(make([]byte, 40))
	require.NoError(t, err)
	assert.Equal(t, 40, n)
	assert.False(t, lb.exceeded.Load(), "exactly reaching the limit is not an overflow")
	assert.Equal(t, 100, lb.buf.Len())
}

// TestDiscoverer_ExecClosureRejectsOverCapOutput runs the REAL exec closure
// (not a mock) against an infinite producer — the `yes` scenario — and
// verifies it terminates promptly with the exceeds error instead of hanging
// until the context deadline or ballooning memory.
func TestDiscoverer_ExecClosureRejectsOverCapOutput(t *testing.T) {
	d := NewDiscoverer([]string{"yes"})
	// Find the yes binary; skip if absent (CI minimal images).
	if _, err := d.lookup("yes"); err != nil {
		t.Skip("yes(1) not available on this host")
	}

	// The cap-kill must terminate `yes` far before this deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := time.Now()
	out, err := d.exec(ctx, "yes", nil)
	elapsed := time.Since(start)
	require.Error(t, err, "infinite output must be rejected, got %q", out)
	assert.Contains(t, err.Error(), "output exceeds")
	assert.Less(t, elapsed, 20*time.Second, "cap-kill must terminate the producer promptly (got %v)", elapsed)
}
