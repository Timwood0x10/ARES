package mcpclient

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"testing"
	"time"
)

// pipeTransport builds a stdioTransport wired to an in-process fake server:
// the server reads requests from one pipe and writes frames to the other.
type pipeTransport struct {
	tr        *stdioTransport
	serverIn  io.ReadCloser  // server reads requests here
	serverOut io.WriteCloser // server writes frames here
}

func newPipeTransport(t *testing.T) *pipeTransport {
	t.Helper()
	reqR, reqW, err := os.Pipe()
	if err != nil {
		t.Fatalf("request pipe: %v", err)
	}
	respR, respW, err := os.Pipe()
	if err != nil {
		t.Fatalf("response pipe: %v", err)
	}
	t.Cleanup(func() {
		_ = reqR.Close()
		_ = reqW.Close()
		_ = respR.Close()
		_ = respW.Close()
	})
	return &pipeTransport{
		tr: &stdioTransport{
			stdin:  reqW,
			stdout: newStdioScanner(respR),
		},
		serverIn:  reqR,
		serverOut: respW,
	}
}

// TestStdioRoundTrip_SkipsNotificationsAndForeignIDs is the #47 regression:
// roundTrip treated the FIRST line on stdout as the response to the pending
// request. A server notification (no id) or a server-initiated request
// (foreign id) arriving before the actual response was returned as if it
// were the response. The read loop must keep scanning until a frame whose
// JSON-RPC id matches the request id.
func TestStdioRoundTrip_SkipsNotificationsAndForeignIDs(t *testing.T) {
	pt := newPipeTransport(t)

	frames := []string{
		// notification: no id at all
		`{"jsonrpc":"2.0","method":"notifications/progress","params":{"progress":50}}`,
		// server-initiated request: has an id, but not ours
		`{"jsonrpc":"2.0","id":77,"method":"sampling/create","params":{}}`,
		// response to a DIFFERENT (stale) request id
		`{"jsonrpc":"2.0","id":999,"result":{"stale":true}}`,
		// the actual response to id 1
		`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"t1"}]}}`,
	}
	go func() {
		// Consume the request line first so the write side never blocks.
		sc := bufio.NewScanner(pt.serverIn)
		if !sc.Scan() {
			return
		}
		for _, f := range frames {
			if _, err := pt.serverOut.Write([]byte(f + "\n")); err != nil {
				return
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := pt.tr.roundTrip(ctx, jsonrpcRequest{JSONRPC: "2.0", ID: 1, Method: "tools/list"})
	if err != nil {
		t.Fatalf("roundTrip: %v", err)
	}
	if resp.ID != 1 {
		t.Fatalf("response id = %d, want 1 (got a notification/foreign frame as the response)", resp.ID)
	}
	var payload struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &payload); err != nil {
		t.Fatalf("unmarshal result: %v (result=%s)", err, resp.Result)
	}
	if len(payload.Tools) != 1 || payload.Tools[0].Name != "t1" {
		t.Errorf("result = %s, want the id=1 response payload", resp.Result)
	}
}

// TestStdioRoundTrip_MultipleSequentialRequests verifies the id matching
// survives consecutive requests on one connection (each response must match
// its own request id, with interleaved notifications).
func TestStdioRoundTrip_MultipleSequentialRequests(t *testing.T) {
	pt := newPipeTransport(t)

	go func() {
		sc := bufio.NewScanner(pt.serverIn)
		for sc.Scan() {
			var req jsonrpcRequest
			if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
				return
			}
			// notification noise before every response
			_, _ = pt.serverOut.Write([]byte(`{"jsonrpc":"2.0","method":"notifications/progress"}` + "\n"))
			resp := map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{"echo": req.ID},
			}
			data, _ := json.Marshal(resp)
			_, _ = pt.serverOut.Write(append(data, '\n'))
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for wantID := 1; wantID <= 3; wantID++ {
		resp, err := pt.tr.roundTrip(ctx, jsonrpcRequest{JSONRPC: "2.0", ID: wantID, Method: "tools/list"})
		if err != nil {
			t.Fatalf("roundTrip #%d: %v", wantID, err)
		}
		if resp.ID != wantID {
			t.Fatalf("roundTrip #%d: response id = %d, want %d", wantID, resp.ID, wantID)
		}
		var payload struct {
			Echo int `json:"echo"`
		}
		if err := json.Unmarshal(resp.Result, &payload); err != nil || payload.Echo != wantID {
			t.Fatalf("roundTrip #%d: result = %s, want echo=%d", wantID, resp.Result, wantID)
		}
	}
}

// TestStdioRoundTrip_ConcurrentCloseAndRead is a -race companion: Close
// during an in-flight roundTrip must not corrupt shared state.
func TestStdioRoundTrip_ConcurrentCloseAndRead(t *testing.T) {
	pt := newPipeTransport(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(pt.serverIn)
		for sc.Scan() {
			time.Sleep(time.Millisecond)
			_, _ = pt.serverOut.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}` + "\n"))
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = pt.tr.roundTrip(ctx, jsonrpcRequest{JSONRPC: "2.0", ID: 1, Method: "tools/list"})
	// Unblock the fake server's read loop (EOF) so the test cannot hang on
	// a goroutine stuck in Scan.
	_ = pt.tr.stdin.Close()
	<-done
}

// TestStdioRoundTrip_LargeResponseIsNotTruncated locks the framing bound.
//
// Regression: the transport used a bare bufio.NewScanner, whose default
// MaxScanTokenSize is 64KB. MCP frames are single-line JSON, so any response
// larger than that made Scan() fail — and roundTrip reported a misleading
// "connection closed" while the child process was perfectly healthy. Every
// subsequent call on that client then failed too. sse.go even documented a
// "64MB stdio buffer" guard that did not exist.
func TestStdioRoundTrip_LargeResponseIsNotTruncated(t *testing.T) {
	pt := newPipeTransport(t)
	defer func() { _ = pt.tr.stdin.Close() }()

	// A response comfortably past the 64KB default token limit.
	bigContent := make([]byte, 256*1024)
	for i := range bigContent {
		bigContent[i] = 'a' + byte(i%26)
	}

	go func() {
		// Read the request so the client is parked in roundTrip, then answer
		// with an oversized frame.
		br := bufio.NewReader(pt.serverIn)
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		var req jsonrpcRequest
		if json.Unmarshal([]byte(line), &req) != nil {
			return
		}
		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  map[string]any{"content": string(bigContent)},
		}
		frame, _ := json.Marshal(resp)
		_, _ = pt.serverOut.Write(append(frame, '\n'))
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := pt.tr.roundTrip(ctx, jsonrpcRequest{JSONRPC: "2.0", ID: 1, Method: "big"})
	if err != nil {
		t.Fatalf("large response must round-trip, got %v — a 64KB scanner limit silently kills the transport", err)
	}
	if resp == nil {
		t.Fatal("expected a response")
	}
}
