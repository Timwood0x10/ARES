package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_security"
	"github.com/Timwood0x10/ares/internal/introspect"
)

// ── M-S1: introspect read-side token ─────────────────────

// okInner is a stub control server that answers 200, so "reached the
// inner tail" is observable in tests.
func okInner() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
}

// newTokenTestHandler builds an actionHandler with ONLY the introspect token
// configured (no JWT, no API key) and a stub inner server, so read-side
// verdicts are attributable to the token layer alone.
func newTokenTestHandler(token string) *actionHandler {
	return &actionHandler{
		inner:           okInner(),
		intro:           introspect.NewHandler(&introspect.Store{}),
		introspectToken: token,
	}
}

// remoteAddr overrides the httptest default (which is a non-loopback
// example address) so tests can simulate local vs network clients.
func remoteAddr(r *http.Request, addr string) *http.Request {
	r.RemoteAddr = addr
	return r
}

// TestIntrospectTokenGatesNonLoopbackReads is the M-S1 acceptance matrix:
// with introspect.token set, a non-loopback client without the token is
// rejected (401); the token (constant-time compare) or a loopback source
// address passes. Under the default 127.0.0.1 bind only loopback clients
// can connect at all, so the token is what closes the exposure when an
// operator widens the bind.
func TestIntrospectTokenGatesNonLoopbackReads(t *testing.T) {
	h := newTokenTestHandler("panel-secret")

	// Non-loopback, no token → 401 on the introspect feed and the
	// control-server read tail alike (one read-side policy).
	for _, ep := range []string{"/api/v1/introspect/snapshot", "/api/insights", "/api/agents"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, remoteAddr(httptest.NewRequest(http.MethodGet, ep, nil), "192.0.2.1:4242"))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("GET %s non-loopback without token = %d, want 401", ep, rec.Code)
		}
	}

	// Non-loopback, wrong token → 401.
	req := remoteAddr(httptest.NewRequest(http.MethodGet, "/api/v1/introspect/snapshot", nil), "192.0.2.1:4242")
	req.Header.Set("Authorization", "Bearer not-the-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET with wrong token = %d, want 401", rec.Code)
	}

	// Non-loopback, valid token → reaches the handler (not 401).
	req = remoteAddr(httptest.NewRequest(http.MethodGet, "/api/v1/introspect/snapshot", nil), "192.0.2.1:4242")
	req.Header.Set("Authorization", "Bearer panel-secret")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized {
		t.Fatal("GET with valid introspect token must pass the read gate")
	}

	// Loopback, no token → still open (local operator, M-S1 posture).
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, remoteAddr(httptest.NewRequest(http.MethodGet, "/api/v1/introspect/snapshot", nil), "127.0.0.1:4242"))
	if rec.Code == http.StatusUnauthorized {
		t.Fatal("GET from loopback without token must stay open")
	}

	// IPv6 loopback counts as loopback too.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, remoteAddr(httptest.NewRequest(http.MethodGet, "/api/insights", nil), "[::1]:4242"))
	if rec.Code == http.StatusUnauthorized {
		t.Fatal("GET from IPv6 loopback without token must stay open")
	}
}

// TestIntrospectTokenDoesNotWeakenConfiguredAuth pins the composition rule:
// setting introspect.token never WEAKENS an auth-enabled deployment — a
// loopback client without any credential is still rejected when the JWT
// layer is configured, and a read-permission JWT passes without the token.
func TestIntrospectTokenDoesNotWeakenConfiguredAuth(t *testing.T) {
	secret := []byte(testActionJWTSecret)
	audit := ares_security.NewAuditLogger(nil)
	h := &actionHandler{
		inner: okInner(),
		intro: introspect.NewHandler(&introspect.Store{}),
		auth:  ares_security.NewAuthMiddleware(secret, ares_security.PermWrite, ares_security.WithAudit(audit)),
		readAuth: ares_security.NewAuthMiddleware(secret, ares_security.PermRead,
			ares_security.WithAudit(audit)),
		introspectToken: "panel-secret",
	}

	// Loopback without any credential: the JWT layer still denies —
	// the token's loopback bypass applies only when it is the sole
	// credential layer.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, remoteAddr(httptest.NewRequest(http.MethodGet, "/api/v1/introspect/snapshot", nil), "127.0.0.1:4242"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("loopback without credentials under configured auth = %d, want 401", rec.Code)
	}

	// A read-permission JWT passes without the introspect token.
	token, err := ares_security.SignJWT(secret, "reader", string(ares_security.RoleAgent), time.Hour, time.Now())
	if err != nil {
		t.Fatalf("issue jwt: %v", err)
	}
	req := remoteAddr(httptest.NewRequest(http.MethodGet, "/api/v1/introspect/snapshot", nil), "192.0.2.1:4242")
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized {
		t.Fatal("read JWT must pass the read gate without the introspect token")
	}
}

// TestIntrospectReadSideLoopbackOnlyWithoutCredentials pins the fail-closed
// posture (#P0-9): with no token, no JWT, and no API key, the read side is
// open ONLY to loopback clients. A non-loopback client gets 401 — the old
// unconditional allow exposed the raw event stream under `--host 0.0.0.0`
// with only a startup warning as the guard.
func TestIntrospectReadSideOpenWithNoCredentials(t *testing.T) {
	h := newTokenTestHandler("")
	// Loopback: still open (local-dev contract).
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, remoteAddr(httptest.NewRequest(http.MethodGet, "/api/v1/introspect/snapshot", nil), "127.0.0.1:4242"))
	if rec.Code == http.StatusUnauthorized {
		t.Fatal("loopback with no credentials configured must stay open (local-dev contract)")
	}
	// Non-loopback: denied.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, remoteAddr(httptest.NewRequest(http.MethodGet, "/api/v1/introspect/snapshot", nil), "192.0.2.1:4242"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatal("non-loopback with no credentials configured must be denied (fail-closed read side)")
	}
}

// ── M-S2: endpoint registry ──────────────────────────────

// TestActionRoutesRegistry is the registry diff against the pre-M-S2
// hand-written switch: every branch of the old ServeHTTP has exactly one
// entry here (zero loss, zero invention), with the auth level the old
// branch enforced. Order matters — it is the dispatch priority the old
// switch checked.
func TestActionRoutesRegistry(t *testing.T) {
	want := []struct {
		method string
		path   string
		auth   authLevel
	}{
		{"POST", "/api/agents/...", authWrite},               // agent lifecycle kill/resume/retry
		{"GET", "/introspect", authNone},                     // panel UI shell
		{"GET", "/introspect/...", authNone},                 // panel UI assets
		{"GET", "/api/v1/introspect/...", authRead},          // introspect JSON feed
		{"GET", "/metrics", authNone},                        // Prometheus scrape
		{"GET", "/api/v1/observability/cost...", authRead},   // LLM cost API
		{"GET", "/api/v1/observability/dashboard", authRead}, // cost dashboard
		{"GET", "/", authNone},                               // root redirect
		{"POST", "/api/chaos/...", authWrite},                // chaos (admin RBAC in handler)
		{"POST", "/api/evolution/approve", authWrite},        // manual gate release
		{"POST", "/api/tools/call", authWrite},               // tool invocation
		{"GET", "/api/tools", authRead},                      // tool inventory
		{"GET", "/api/mcp/tools", authRead},                  // MCP tool inventory
		{"POST", "/api/mcp/tools/{name}/call", authWrite},    // MCP tool invocation
		{"POST", "/api/tasks", authWrite},                    // peer task submission
		{"POST", "/api/graphs", authWrite},                   // collaboration graph
		{"*", "/api/...", authRead},                          // control-server read tail
		{"*", "/...", authNone},                              // non-API 404 tail
	}
	if len(actionRoutes) != len(want) {
		for i, spec := range actionRoutes {
			t.Logf("registry[%d] = %s %s auth=%s (%s)", i, spec.Method, spec.Path, spec.Auth, spec.Desc)
		}
		t.Fatalf("registry has %d entries, want %d", len(actionRoutes), len(want))
	}
	seen := map[string]bool{}
	for i, spec := range actionRoutes {
		w := want[i]
		if spec.Method != w.method || spec.Path != w.path {
			t.Errorf("registry[%d] = %s %s, want %s %s", i, spec.Method, spec.Path, w.method, w.path)
		}
		if spec.Auth != w.auth {
			t.Errorf("registry[%d] (%s %s) auth = %s, want %s", i, spec.Method, spec.Path, spec.Auth, w.auth)
		}
		key := spec.Method + " " + spec.Path
		if seen[key] {
			t.Errorf("duplicate registry entry %q", key)
		}
		seen[key] = true
	}
}

// TestRouteSpecMatch locks the three pattern forms: exact, prefix ("..."),
// and segment capture ("{param}"), including the empty-segment case that
// strings.Split semantics produce for a doubled slash.
func TestRouteSpecMatch(t *testing.T) {
	tests := []struct {
		name   string
		spec   routeSpec
		method string
		path   string
		want   bool
	}{
		{name: "exact match", spec: routeSpec{Method: "POST", Path: "/api/tasks"},
			method: "POST", path: "/api/tasks", want: true},
		{name: "exact wrong path", spec: routeSpec{Method: "POST", Path: "/api/tasks"},
			method: "POST", path: "/api/tasks/extra", want: false},
		{name: "method mismatch", spec: routeSpec{Method: "GET", Path: "/api/tasks"},
			method: "POST", path: "/api/tasks", want: false},
		{name: "wildcard method", spec: routeSpec{Method: "*", Path: "/api/..."},
			method: "DELETE", path: "/api/anything", want: true},
		{name: "prefix match", spec: routeSpec{Method: "GET", Path: "/api/v1/introspect/..."},
			method: "GET", path: "/api/v1/introspect/events", want: true},
		{name: "prefix matches bare prefix", spec: routeSpec{Method: "GET", Path: "/api/v1/observability/cost..."},
			method: "GET", path: "/api/v1/observability/cost", want: true},
		{name: "prefix too short", spec: routeSpec{Method: "GET", Path: "/api/v1/introspect/..."},
			method: "GET", path: "/api/v1/introspect", want: false},
		{name: "segment capture", spec: routeSpec{Method: "POST", Path: "/api/mcp/tools/{name}/call"},
			method: "POST", path: "/api/mcp/tools/web_search/call", want: true},
		{name: "segment wrong literal", spec: routeSpec{Method: "POST", Path: "/api/mcp/tools/{name}/call"},
			method: "POST", path: "/api/mcp/tools/web_search/invoke", want: false},
		{name: "segment extra depth", spec: routeSpec{Method: "POST", Path: "/api/mcp/tools/{name}/call"},
			method: "POST", path: "/api/mcp/tools/a/call/b", want: false},
		{name: "segment empty capture (Split semantics)", spec: routeSpec{Method: "POST", Path: "/api/mcp/tools/{name}/call"},
			method: "POST", path: "/api/mcp/tools//call", want: true},
		{name: "catch-all", spec: routeSpec{Method: "*", Path: "/..."},
			method: "GET", path: "/not-a-route", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.spec.match(tt.method, tt.path); got != tt.want {
				t.Errorf("match(%s, %s) = %v, want %v", tt.method, tt.path, got, tt.want)
			}
		})
	}
}

// TestRegistryAgentLifecycleAuthOrder pins the dispatch contract the old
// switch had: the write credential is checked BEFORE the path shape is
// parsed, and an unknown shape falls through to the control-server tail
// instead of a local 404.
func TestRegistryAgentLifecycleAuthOrder(t *testing.T) {
	// No credentials configured → deny-by-default even for an unknown
	// action shape (auth precedes parsing).
	h := &actionHandler{inner: okInner()}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/agents/a/not-an-action", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST unknown agent action without credentials = %d, want 401 (auth precedes shape parsing)", rec.Code)
	}

	// With a credential, an unknown shape reaches the control-server tail
	// (read-gate passes, inner answers) — not a local 404.
	h.apiKey = "test-key"
	req := httptest.NewRequest(http.MethodPost, "/api/agents/a/not-an-action", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST unknown agent action with credentials = %d, want fall-through to inner (200)", rec.Code)
	}

	// And a known shape is dispatched to the lifecycle handler: kill with
	// credentials against an unregistered agent → the handler's own
	// ErrAgentNotFound mapping (404), proving the route reached
	// handleAction (not the tail, which would answer 200 here).
	env := newActionTestEnv(t)
	req = httptest.NewRequest(http.MethodPost, "/api/agents/no-such-agent/kill", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	rec = httptest.NewRecorder()
	env.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("POST kill with credentials = %d, want 404 (reached handleAction, agent not found)", rec.Code)
	}
}

// TestRegistryChaosRequiresCredentials is the M-S3 spot check at the HTTP
// layer: chaos endpoints sit behind the write gate (deny-by-default), and
// the dispatcher enforces the registry's authWrite level before
// handleChaos runs.
func TestRegistryChaosRequiresCredentials(t *testing.T) {
	h := &actionHandler{inner: okInner()}
	for _, chaosType := range []string{"random-kill", "kill-all", "recover", "stop"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/chaos/"+chaosType, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("POST /api/chaos/%s without credentials = %d, want 401 (authWrite)", chaosType, rec.Code)
		}
	}
}
