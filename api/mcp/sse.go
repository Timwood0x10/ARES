package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// sseTransport implements JSON-RPC over MCP SSE transport.
type sseTransport struct {
	client     *http.Client
	messageURL string
	sseBody    io.ReadCloser
	sseCtx     context.Context
	sseCancel  context.CancelFunc
	closeMu    sync.Mutex
	closed     bool
}

// ConnectSSE connects to an MCP server via SSE transport.
func ConnectSSE(ctx context.Context, name, url string) (*Client, error) {
	tr := &sseTransport{
		client: &http.Client{Timeout: 0}, // SSE requires no timeout
	}

	sseReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("sse request: %w", err)
	}
	sseReq.Header.Set("Accept", "text/event-stream")

	sseResp, err := tr.client.Do(sseReq)
	if err != nil {
		return nil, fmt.Errorf("sse connect: %w", err)
	}
	if sseResp.StatusCode != http.StatusOK {
		sseResp.Body.Close()
		return nil, fmt.Errorf("sse connect: unexpected status %d", sseResp.StatusCode)
	}

	tr.sseBody = sseResp.Body
	tr.sseCtx, tr.sseCancel = context.WithCancel(ctx)

	// Read SSE events until we get the endpoint event.
	endpoint, err := tr.readEndpointEvent()
	if err != nil {
		tr.sseCancel()
		sseResp.Body.Close()
		return nil, fmt.Errorf("read endpoint: %w", err)
	}
	tr.messageURL = endpoint

	c := &Client{
		name:      name,
		transport: tr,
		idCounter: 1,
	}

	if err := c.initialize(ctx); err != nil {
		tr.close()
		return nil, fmt.Errorf("initialize: %w", err)
	}

	c.connected = true
	return c, nil
}

// readEndpointEvent reads SSE events until an "endpoint" event is received.
func (tr *sseTransport) readEndpointEvent() (string, error) {
	scanner := bufio.NewScanner(tr.sseBody)
	var eventType string
	var dataBuf strings.Builder

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			// Empty line = end of event.
			if eventType == "endpoint" {
				return strings.TrimSpace(dataBuf.String()), nil
			}
			eventType = ""
			dataBuf.Reset()
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			dataBuf.WriteString(strings.TrimPrefix(line, "data: "))
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("sse scan: %w", err)
	}
	return "", fmt.Errorf("sse stream ended without endpoint event")
}

func (tr *sseTransport) roundTrip(_ context.Context, req jsonrpcRequest) (*jsonrpcResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(tr.sseCtx, http.MethodPost, tr.messageURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("post request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	httpResp, err := tr.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("post: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("post: unexpected status %d", httpResp.StatusCode)
	}

	var resp jsonrpcResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &resp, nil
}

func (tr *sseTransport) close() error {
	tr.closeMu.Lock()
	defer tr.closeMu.Unlock()
	if tr.closed {
		return nil
	}
	tr.closed = true
	tr.sseCancel()
	if tr.sseBody != nil {
		return tr.sseBody.Close()
	}
	return nil
}
