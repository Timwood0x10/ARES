package ares_security

import (
	"log/slog"
	"net"
	"net/http"
)

// AuditLogger is the modular audit sink for security-relevant events: auth
// decisions (allow/deny on protected endpoints) and destructive actions
// (kill/resume/retry an agent, call an MCP tool). It wraps an *slog.Logger
// with a fixed set of structured fields so every audit record has the same
// shape, independent of which component wrote it.
//
// The token itself is never logged — only the decoded identity (subject,
// role) and the decision. This is the "complete, modular audit logging":
// previously the auth decision was the only audited event,
// and it was hard-wired inside AuthMiddleware.
type AuditLogger struct {
	l *slog.Logger
}

// NewAuditLogger builds an audit sink. A nil logger is allowed and disables
// audit output (callers may pass slog.Default() when they have no logger).
func NewAuditLogger(l *slog.Logger) *AuditLogger {
	return &AuditLogger{l: l}
}

// RequestDetails carries the per-request attribution every audit entry must
// include (C-8): where the request came from and which request it was, so an
// audit trail can be correlated with access logs and client reports.
// RemoteIP is the TCP peer (RemoteAddr host part) — deliberately NOT
// X-Forwarded-For, which a client can spoof and must only be trusted behind
// a known proxy.
type RequestDetails struct {
	RemoteIP  string
	UserAgent string
	RequestID string
}

// RequestDetailsFrom builds the audit attribution for one request. The
// request ID is read from the canonicalized X-Request-Id header (the console
// dispatcher sets it for every request; callers may supply their own).
func RequestDetailsFrom(r *http.Request) RequestDetails {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	return RequestDetails{
		RemoteIP:  ip,
		UserAgent: r.UserAgent(),
		RequestID: r.Header.Get("X-Request-Id"),
	}
}

// Auth records an authentication decision on a protected endpoint. decision
// is one of "allowed", "missing bearer token", "invalid token",
// "unknown role in token", "insufficient role"; status is the HTTP status
// that was (or would be) returned. d carries the request attribution (C-8).
func (a *AuditLogger) Auth(decision, subject, role, method, path string, status int, d RequestDetails) {
	if a == nil || a.l == nil {
		return
	}
	a.l.Info("auth",
		"decision", decision,
		"subject", subject,
		"role", role,
		"method", method,
		"path", path,
		"status", status,
		"remote_ip", d.RemoteIP,
		"user_agent", d.UserAgent,
		"request_id", d.RequestID,
	)
}

// Action records a destructive (or privileged) action executed on behalf of
// an authenticated principal: which action, against which target, who did it,
// and whether it succeeded. This is the audit trail for "who changed what".
// d carries the request attribution (C-8).
func (a *AuditLogger) Action(action, subject, target string, ok bool, d RequestDetails) {
	if a == nil || a.l == nil {
		return
	}
	a.l.Info("action",
		"action", action,
		"subject", subject,
		"target", target,
		"ok", ok,
		"remote_ip", d.RemoteIP,
		"user_agent", d.UserAgent,
		"request_id", d.RequestID,
	)
}
