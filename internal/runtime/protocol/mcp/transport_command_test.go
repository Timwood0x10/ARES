package ares_mcp

import (
	"context"
	"strings"
	"testing"
)

// TestStdioTransport_RejectsRelativeCommand is the production half of the consistency fix: the SDK
// path (mcpclient.ConnectStdio) refused relative commands while this transport accepted them, so the
// same ares.yaml behaved differently depending on which code path opened the server.
func TestStdioTransport_RejectsRelativeCommand(t *testing.T) {
	tr := NewStdioTransport(StdioConfig{Command: "./relative-server"})

	err := tr.Start(context.Background())

	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("a relative command was accepted: %v", err)
	}
}

// TestStdioTransport_RejectsEmptyCommand keeps the message the caller already depended on: an empty
// command is still reported as "required", not as a path problem.
func TestStdioTransport_RejectsEmptyCommand(t *testing.T) {
	tr := NewStdioTransport(StdioConfig{})

	err := tr.Start(context.Background())

	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("want a 'required' error, got %v", err)
	}
}
