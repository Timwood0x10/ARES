package ares_security

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// AuthMiddleware enforces Bearer-token JWT authentication on the wrapped
// handler. It is the single enforcement point for all protected routes:
//
//   - extracts the token from the Authorization: Bearer header;
//   - verifies signature and expiry (VerifyJWT);
//   - parses the role (ParseRole, default deny on unknown);
//   - rejects the request unless the role holds the required permission;
//   - injects the authenticated principal into the request context.
//
// When the secret is nil, every request is denied with 401 — the same
// deny-by-default posture the existing API-key middleware uses, so enabling
// JWT cannot accidentally open a destructive endpoint.
type AuthMiddleware struct {
	// secret is the HS256 signing key. nil disables auth entirely (deny all).
	secret []byte
	// require is the minimum permission for the wrapped route.
	require Permission
	// audit is the modular audit sink; nil disables audit logging.
	audit *AuditLogger
	// now is the clock used for expiry checks (injectable for tests).
	now func() time.Time
}

// AuthOption configures an AuthMiddleware.
type AuthOption func(*AuthMiddleware)

// WithAudit attaches the modular audit sink (see audit.go). It records auth
// decisions and, when passed to the monitoring HTTP server, destructive
// actions too.
func WithAudit(a *AuditLogger) AuthOption {
	return func(m *AuthMiddleware) {
		m.audit = a
	}
}

// NewAuthMiddleware builds an AuthMiddleware. When secret is nil the
// middleware is in "deny all" mode until a secret is provided.
func NewAuthMiddleware(secret []byte, require Permission, opts ...AuthOption) *AuthMiddleware {
	m := &AuthMiddleware{
		secret:  secret,
		require: require,
		now:     time.Now,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// Principal is the authenticated identity extracted from a valid token. It is
// stored in the request context so handlers can act on the caller's role
// (e.g. gate destructive actions that need more than PermWrite).
type Principal struct {
	Subject string
	Role    Role
}

// principalKey is the context key for the authenticated Principal.
type principalKey struct{}

// FromContext returns the authenticated principal, or nil when the request
// was not authenticated. Callers on unprotected routes receive nil and must
// treat that as "no identity", not "admin".
func FromContext(ctx context.Context) *Principal {
	if p, ok := ctx.Value(principalKey{}).(*Principal); ok {
		return p
	}
	return nil
}

// Verify performs the JWT verification + role check for a request without
// requiring a wrapped handler. It returns the principal and an HTTP status; a
// non-200 status means the request must be rejected. Callers that need to
// compose JWT with another credential (e.g. the monitoring server's API key)
// use this instead of Wrap/WrapGin.
func (m *AuthMiddleware) Verify(r *http.Request) (*Principal, int) {
	return m.authenticate(r)
}

// authenticate performs verification + role check for a request. It returns
// the principal and an HTTP status; a non-200 status means the request must
// be rejected. It audits every decision (allow and deny).
func (m *AuthMiddleware) authenticate(r *http.Request) (*Principal, int) {
	token := bearerToken(r.Header.Get("Authorization"))
	if token == "" {
		m.auditAuth(r, "missing bearer token", "", "", http.StatusUnauthorized)
		return nil, http.StatusUnauthorized
	}
	sub, roleStr, err := VerifyJWT(m.secret, token, m.now())
	if err != nil {
		m.auditAuth(r, "invalid token", sub, roleStr, http.StatusUnauthorized)
		return nil, http.StatusUnauthorized
	}
	role, err := ParseRole(roleStr)
	if err != nil {
		m.auditAuth(r, "unknown role in token", sub, roleStr, http.StatusForbidden)
		return nil, http.StatusForbidden
	}
	if !AllowRole(role, m.require) {
		m.auditAuth(r, "insufficient role", sub, roleStr, http.StatusForbidden)
		return nil, http.StatusForbidden
	}
	m.auditAuth(r, "allowed", sub, roleStr, http.StatusOK)
	return &Principal{Subject: sub, Role: role}, http.StatusOK
}

// auditAuth delegates to the modular AuditLogger. The token itself is never
// logged; only the decoded identity and the decision are.
func (m *AuthMiddleware) auditAuth(r *http.Request, decision, subject, role string, status int) {
	if m.audit == nil {
		return
	}
	m.audit.Auth(decision, subject, role, r.Method, r.URL.Path, status)
}

// bearerToken extracts the token from an Authorization header. Only the
// "Bearer " scheme is accepted; any other scheme is treated as missing so a
// token cannot be smuggled in via a different scheme.
func bearerToken(authHeader string) string {
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
}
