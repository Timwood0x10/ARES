package mcpclient

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestSSETransport_DrainsStreamAfterEndpointEvent is the #48 regression:
// after reading the initial "endpoint" event the transport never read the
// SSE body again. A server that keeps sending events (keepalives,
// notifications) eventually filled the TCP receive window and stalled the
// connection. The transport must keep a read loop draining the stream.
//
// The fake server writes far more SSE data than any socket buffer holds and
// only answers message-endpoint POSTs after the SSE writer finished. Without
// a drain the POST never gets answered and the client times out.
func TestSSETransport_DrainsStreamAfterEndpointEvent(t *testing.T) {
	sseDone := make(chan struct{})
	var sseBytesWritten atomic.Int64
	const floodChunks = 2000
	const floodChunkBytes = 32 * 1024

	mux := http.NewServeMux()
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		// endpoint event first — the client needs it to connect. Real MCP
		// servers send the absolute message URL.
		_, _ = fmt.Fprintf(w, "event: endpoint\ndata: http://%s/messages\n\n", r.Host)
		flusher.Flush()
		// Then flood: 2000 * 32 KiB ≈ 62.5 MiB, far beyond any TCP window.
		chunk := strings.Repeat("x", floodChunkBytes)
		for i := 0; i < floodChunks; i++ {
			_, _ = fmt.Fprintf(w, "event: progress\ndata: %s\n\n", chunk)
			flusher.Flush()
			sseBytesWritten.Add(int64(len(chunk)))
		}
		close(sseDone)
	})
	mux.HandleFunc("/messages", func(w http.ResponseWriter, r *http.Request) {
		// Only answer once the SSE writer completed: if the client stopped
		// draining, the writer is stuck and this handler never fires.
		select {
		case <-sseDone:
		case <-r.Context().Done():
			return
		}
		var req jsonrpcRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jsonrpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  json.RawMessage(`{}`),
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	client, err := ConnectSSE(ctx, "flood-server", srv.URL+"/sse")
	if err != nil {
		t.Fatalf("ConnectSSE stalled or failed (SSE stream not drained?): %v", err)
	}
	defer func() { _ = client.Close() }()

	// The connection must stay usable after the flood: a tools/list round
	// trip goes through the message endpoint.
	toolsCtx, toolsCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer toolsCancel()
	if _, err := client.ListTools(toolsCtx); err != nil {
		t.Fatalf("ListTools after SSE flood: %v", err)
	}
	if sseBytesWritten.Load() < floodChunks*floodChunkBytes {
		t.Fatalf("server wrote only %d bytes; flood did not complete", sseBytesWritten.Load())
	}
}

// TestSSETransport_ConcurrentReadsDuringRoundTrip is the -race companion:
// the drain goroutine reads the SSE body while roundTrips POST to the message
// endpoint; both must be race-free.
func TestSSETransport_ConcurrentReadsDuringRoundTrip(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = fmt.Fprintf(w, "event: endpoint\ndata: http://%s/messages\n\n", r.Host)
		flusher.Flush()
		// Periodic keepalives for the lifetime of the connection.
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				_, _ = fmt.Fprint(w, "event: keepalive\ndata: {}\n\n")
				flusher.Flush()
			}
		}
	})
	mux.HandleFunc("/messages", func(w http.ResponseWriter, r *http.Request) {
		var req jsonrpcRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jsonrpcResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  json.RawMessage(`{"tools":[]}`),
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	client, err := ConnectSSE(ctx, "keepalive-server", srv.URL+"/sse")
	if err != nil {
		t.Fatalf("ConnectSSE: %v", err)
	}
	defer func() { _ = client.Close() }()

	for i := 0; i < 20; i++ {
		if _, err := client.ListTools(ctx); err != nil {
			t.Fatalf("ListTools #%d: %v", i, err)
		}
	}
}

// TestSSETransport_ReadEndpointEventSingleScanner pins the internal
// invariant behind #48: the endpoint event and the subsequent drain MUST use
// the same bufio.Scanner. A fresh scanner per read silently discards data the
// previous scanner had buffered past the endpoint event.
func TestSSETransport_ReadEndpointEventSingleScanner(t *testing.T) {
	// Two events in one write: the endpoint event plus a follow-up the
	// scanner may buffer together with it.
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() {
		_ = pr.Close()
		_ = pw.Close()
	})
	_, _ = pw.Write([]byte("event: endpoint\ndata: /messages\n\nevent: progress\ndata: {}\n\n"))

	sc := bufio.NewScanner(pr)
	tr := &sseTransport{}
	endpoint, err := tr.readEndpointEvent(sc)
	if err != nil {
		t.Fatalf("readEndpointEvent: %v", err)
	}
	if endpoint != "/messages" {
		t.Fatalf("endpoint = %q, want /messages", endpoint)
	}
	// The follow-up event must still be readable through the SAME scanner.
	if !sc.Scan() {
		t.Fatal("second event lost: scanner must be reused after the endpoint event")
	}
	if !strings.Contains(sc.Text(), "event: progress") {
		t.Fatalf("next frame = %q, want the buffered progress event", sc.Text())
	}
}
