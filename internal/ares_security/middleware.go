package ares_security

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
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

// WithAuditLogger attaches an audit logger that records every auth decision
// (allow/deny) with subject, role and path. Kept for compatibility; the sink
// is an AuditLogger internally.
func WithAuditLogger(l *slog.Logger) AuthOption {
	return WithAudit(NewAuditLogger(l))
}

// WithAudit attaches the modular audit sink (see audit.go). It records auth
// decisions and, when passed to the monitoring HTTP server, destructive
// actions too.
func WithAudit(a *AuditLogger) AuthOption {
	return func(m *AuthMiddleware) {
		m.audit = a
	}
}

// WithClock overrides the time source (test use only).
func WithClock(f func() time.Time) AuthOption {
	return func(m *AuthMiddleware) {
		m.now = f
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

// Wrap returns an http.Handler that authenticates requests before delegating
// to next. Suitable for net/http ServeMux wrapping and for gin via
// gin.WrapH.
func (m *AuthMiddleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		princ, status := m.authenticate(r)
		if status != http.StatusOK {
			http.Error(w, http.StatusText(status), status)
			return
		}
		ctx := context.WithValue(r.Context(), principalKey{}, princ)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// WrapGin returns a gin.HandlerFunc enforcing the same policy. It aborts with
// 401 when authentication fails and 403 when the role lacks the required
// permission. gin is a first-party dependency of the repository, so importing
// it here keeps the two adapters sharing one enforcement core.
func (m *AuthMiddleware) WrapGin() gin.HandlerFunc {
	return func(c *gin.Context) {
		princ, status := m.authenticate(c.Request)
		if status != http.StatusOK {
			c.AbortWithStatusJSON(status, gin.H{"error": http.StatusText(status)})
			return
		}
		c.Set("ares.principal", princ)
		c.Next()
	}
}

// PrincipalFromGin reads the principal stored by WrapGin. Returns nil on
// routes that did not run WrapGin (e.g. public routes).
func PrincipalFromGin(c *gin.Context) *Principal {
	if v, ok := c.Get("ares.principal"); ok {
		if p, ok := v.(*Principal); ok {
			return p
		}
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
