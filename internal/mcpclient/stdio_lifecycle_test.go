package mcpclient

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

// echoMCPServer is a minimal real subprocess speaking enough JSON-RPC for
// initialize + tools/list: one line in, one response out. Built with /bin/sh
// so the test exercises the REAL exec.Command lifecycle (a pipe-based fake
// cannot — the bug is about process ownership). The response id ECHOES the
// request's id so repeated calls (the liveness poll) each get a match;
// hardcoded ids would only answer the first request per method.
const echoMCPServer = `while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
  case "$line" in
    *initialize*) printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"1","capabilities":{},"serverInfo":{"name":"t","version":"1"}}}\n' "$id" ;;
    *tools/list*) printf '{"jsonrpc":"2.0","id":%s,"result":{"tools":[]}}\n' "$id" ;;
  esac
done`

// TestConnectStdioSurvivesCallerContextCancel locks F-02: the SDK connects
// with a 30s timeout context and cancels it immediately after ConnectStdio
// returns (sdk/sdk.go wireMCPClients). The child process's lifetime must be
// owned by Client.Close, NOT by the connect-timeout context —
// exec.CommandContext(ctx) bound the process to that ctx, so the cancel
// killed the server and every subsequent ListTools/CallTool failed with
// "write |1: broken pipe". WithMCP was unusable end to end.
func TestConnectStdioSurvivesCallerContextCancel(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no sh on PATH: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := ConnectStdio(ctx, "cancel-probe", sh, []string{"-c", echoMCPServer})
	if err != nil {
		t.Fatalf("ConnectStdio: %v", err)
	}
	defer func() { _ = c.Close() }()

	// Reproduce the SDK's exact wiring: cancel the connect context right
	// after the client is built.
	cancel()

	// Liveness window instead of a single post-sleep sample: probe the
	// server repeatedly for ~300ms so a cancel-triggered kill (the F-02
	// bug) lands INSIDE the window and is observed, rather than racing a
	// one-shot check. Each probe is a real request round-trip.
	deadline := time.Now().Add(300 * time.Millisecond)
	for {
		tools, err := c.ListTools(context.Background())
		if err != nil {
			t.Fatalf("ListTools after connect-context cancel: %v (process was killed by the canceled ctx — F-02)", err)
		}
		if len(tools) != 0 {
			t.Fatalf("tools = %v, want empty", tools)
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond) //nolint:gomnd // poll interval inside the liveness window
	}
}

// TestConnectStdioConnectTimeoutStillBounded locks the other half of the
// contract: the connect context must still bound the INITIALIZE handshake,
// so a server that never answers fails at connect time instead of hanging.
func TestConnectStdioConnectTimeoutStillBounded(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no sh on PATH: %v", err)
	}

	// A server that reads but never responds.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err = ConnectStdio(ctx, "hung-probe", sh, []string{"-c", `while IFS= read -r line; do :; done`})
	if err == nil {
		t.Fatal("a hung server must fail the connect deadline")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("connect timeout not honored: %v", elapsed)
	}
}
