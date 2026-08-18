package ares_security

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

const testSecret = "test-secret"

func testToken(t *testing.T, role, subject string) string {
	t.Helper()
	tok, err := SignJWT([]byte(testSecret), subject, role, time.Hour, time.Now())
	if err != nil {
		t.Fatalf("SignJWT: %v", err)
	}
	return tok
}

func TestWrapAllowsValidToken(t *testing.T) {
	mw := NewAuthMiddleware([]byte(testSecret), PermWrite)
	tok := testToken(t, "operator", "alice")

	gotPrincipal := false
	handler := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := FromContext(r.Context())
		if p == nil || p.Subject != "alice" || p.Role != RoleOperator {
			t.Fatalf("principal = %+v, want alice/operator", p)
		}
		gotPrincipal = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/agents/1/kill", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !gotPrincipal {
		t.Fatal("handler did not run")
	}
}

func TestWrapDeniesMissingToken(t *testing.T) {
	mw := NewAuthMiddleware([]byte(testSecret), PermWrite)
	handler := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not run")
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/agents/1/kill", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestWrapDeniesWrongScheme(t *testing.T) {
	mw := NewAuthMiddleware([]byte(testSecret), PermWrite)
	tok := testToken(t, "operator", "alice")
	handler := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not run")
	}))
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.Header.Set("Authorization", "Basic "+tok) // wrong scheme
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestWrapDeniesInsufficientRole(t *testing.T) {
	mw := NewAuthMiddleware([]byte(testSecret), PermWrite)
	tok := testToken(t, "agent", "bob") // read-only role
	handler := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not run")
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/agents/1/kill", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestWrapDeniesWhenSecretNil(t *testing.T) {
	// Nil secret = deny all (misconfig safety).
	mw := NewAuthMiddleware(nil, PermWrite)
	handler := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not run")
	}))
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestWrapGinAllowsAndDenies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mw := NewAuthMiddleware([]byte(testSecret), PermRead)

	engine := gin.New()
	engine.GET("/read", mw.WrapGin(), func(c *gin.Context) {
		p := PrincipalFromGin(c)
		if p == nil || p.Subject != "carol" {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "no principal"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	// Valid agent (read) token passes.
	req := httptest.NewRequest(http.MethodGet, "/read", nil)
	req.Header.Set("Authorization", "Bearer "+testToken(t, "agent", "carol"))
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid token status = %d, want 200", rec.Code)
	}

	// No token rejected.
	req = httptest.NewRequest(http.MethodGet, "/read", nil)
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d, want 401", rec.Code)
	}
}

func TestFromContextNilOnUnprotected(t *testing.T) {
	if p := FromContext(context.Background()); p != nil {
		t.Fatalf("FromContext on plain ctx = %+v, want nil", p)
	}
}

// TestWrapAuditsThroughModule verifies auth decisions reach the modular audit
// sink (WithAudit), both allow and deny paths.
func TestWrapAuditsThroughModule(t *testing.T) {
	audit, buf := newTestAuditLogger(t)
	mw := NewAuthMiddleware([]byte(testSecret), PermWrite, WithAudit(audit))
	tok := testToken(t, "operator", "alice")

	handler := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Allow path.
	req := httptest.NewRequest(http.MethodPost, "/api/agents/1/kill", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	// Deny path (missing token).
	req = httptest.NewRequest(http.MethodPost, "/api/agents/1/kill", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	out := buf.String()
	// slog's TextHandler quotes values containing spaces.
	if !strings.Contains(out, "decision=allowed") || !strings.Contains(out, `decision="missing bearer token"`) {
		t.Fatalf("audit sink must record both allow and deny decisions; got:\n%s", out)
	}
	if !strings.Contains(out, "subject=alice") {
		t.Fatalf("audit must carry the subject; got:\n%s", out)
	}
}
