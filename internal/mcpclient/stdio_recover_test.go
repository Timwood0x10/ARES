package mcpclient

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestStdio_TimeoutDoesNotKillTheConnection is the regression this transport was rebuilt for.
//
// The old timeout path closed stdin to unblock its per-call scanner. stdin is the child's input
// pipe: closing it is terminal — every later write fails, and there is no reconnect, so ONE slow
// response retired that MCP server for the life of the process.
//
// The assertion is behavioural: the first call must time out, and the SECOND call on the same
// transport must still succeed.
func TestStdio_TimeoutDoesNotKillTheConnection(t *testing.T) {
	serverIn, clientIn := io.Pipe()   // transport writes into clientIn; the server reads serverIn
	clientOut, serverOut := io.Pipe() // the server writes serverOut; the transport scans clientOut

	tr := &stdioTransport{
		stdin:  clientIn,
		stdout: newStdioScanner(clientOut),
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(serverIn)
		for sc.Scan() {
			var req struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
			}
			if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
				continue
			}
			if req.ID == 1 {
				continue // deliberately never answered: this is the slow response
			}
			_, _ = fmt.Fprintf(serverOut, `{"jsonrpc":"2.0","id":%d,"result":{"ok":true}}`+"\n", req.ID)
		}
	}()
	t.Cleanup(func() {
		_ = serverIn.Close()
		_ = serverOut.Close()
		_ = clientIn.Close()
		_ = clientOut.Close()
		wg.Wait()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := tr.roundTrip(ctx, jsonrpcRequest{ID: 1, Method: "tools/call"})
	if err == nil {
		t.Fatal("the first call should have timed out")
	}

	// The connection must still be usable: same transport, same pipes.
	resp, err := tr.roundTrip(context.Background(), jsonrpcRequest{ID: 2, Method: "tools/call"})
	if err != nil {
		t.Fatalf("a second call on the same transport failed after one timeout: %v", err)
	}
	if resp == nil || resp.Error != nil {
		t.Fatalf("unexpected response: %+v", resp)
	}

	// And the failure mode that made this fatal: the write half must still be open.
	if err := tr.notify(context.Background(), jsonrpcNotification{Method: "notifications/initialized"}); err != nil {
		t.Fatalf("stdin was closed by the timeout; a notification no longer writes: %v", err)
	}
}

// TestStdio_LateResponseIsDropped: after a timeout the late frame arrives with nobody waiting. The
// reader must discard it by id instead of blocking or delivering it to the next call.
func TestStdio_LateResponseIsDropped(t *testing.T) {
	serverIn, clientIn := io.Pipe()
	clientOut, serverOut := io.Pipe()

	tr := &stdioTransport{stdin: clientIn, stdout: newStdioScanner(clientOut)}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(serverIn)
		for sc.Scan() {
			var req struct {
				ID int `json:"id"`
			}
			if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
				continue
			}
			if req.ID == 1 {
				// Answer the timed-out call LATE, after the second call is already in flight.
				time.Sleep(120 * time.Millisecond)
				_, _ = fmt.Fprintf(serverOut, `{"jsonrpc":"2.0","id":1,"result":{"late":true}}`+"\n")
				continue
			}
			_, _ = fmt.Fprintf(serverOut, `{"jsonrpc":"2.0","id":%d,"result":{"ok":true}}`+"\n", req.ID)
		}
	}()
	t.Cleanup(func() {
		_ = serverIn.Close()
		_ = serverOut.Close()
		_ = clientIn.Close()
		_ = clientOut.Close()
		wg.Wait()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, _ = tr.roundTrip(ctx, jsonrpcRequest{ID: 1, Method: "tools/call"})

	resp, err := tr.roundTrip(context.Background(), jsonrpcRequest{ID: 2, Method: "tools/call"})
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	// The late id=1 response must not be delivered to id=2.
	if resp == nil || resp.Result == nil || strings.Contains(string(resp.Result), "late") {
		t.Fatalf("the late response for id=1 was delivered to id=2: %+v", resp)
	}
}
