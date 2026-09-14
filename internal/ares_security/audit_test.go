package ares_security

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// newTestAuditLogger builds an AuditLogger writing to an in-memory buffer so
// tests can assert on emitted structured fields.
func newTestAuditLogger(t *testing.T) (*AuditLogger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	l := slog.New(slog.NewTextHandler(&buf, nil))
	return NewAuditLogger(l), &buf
}

func TestAuditLoggerAuth(t *testing.T) {
	a, buf := newTestAuditLogger(t)
	a.Auth("allowed", "alice", "operator", "POST", "/api/agents/a1/kill", 200,
		RequestDetails{RemoteIP: "10.0.0.9", UserAgent: "test-agent", RequestID: "req-42"})

	out := buf.String()
	for _, want := range []string{"msg=auth", "decision=allowed", "subject=alice",
		"role=operator", "method=POST", "path=/api/agents/a1/kill", "status=200",
		"remote_ip=10.0.0.9", "user_agent=test-agent", "request_id=req-42"} {
		if !strings.Contains(out, want) {
			t.Errorf("auth log missing %q; got:\n%s", want, out)
		}
	}
}

func TestAuditLoggerAction(t *testing.T) {
	a, buf := newTestAuditLogger(t)
	a.Action("kill", "deploy-user", "worker-7", true,
		RequestDetails{RemoteIP: "10.0.0.9", UserAgent: "test-agent", RequestID: "req-42"})

	out := buf.String()
	for _, want := range []string{"msg=action", "action=kill", "subject=deploy-user",
		"target=worker-7", "ok=true",
		"remote_ip=10.0.0.9", "user_agent=test-agent", "request_id=req-42"} {
		if !strings.Contains(out, want) {
			t.Errorf("action log missing %q; got:\n%s", want, out)
		}
	}
}

func TestAuditLoggerNilNoPanic(t *testing.T) {
	var a *AuditLogger
	a.Auth("allowed", "s", "r", "GET", "/x", 200, RequestDetails{}) // must not panic
	a.Action("kill", "s", "t", true, RequestDetails{})              // must not panic
}

func TestAuditLoggerNilSinkNoPanic(t *testing.T) {
	a := NewAuditLogger(nil)
	a.Auth("allowed", "s", "r", "GET", "/x", 200, RequestDetails{})
	a.Action("kill", "s", "t", true, RequestDetails{})
}
