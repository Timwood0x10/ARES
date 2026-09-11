// agent — the control-plane actionHandler skeleton: the endpoint registry
// (M-S2) and its dispatch/authz core, plus the panel/cost read adapters.
// The route domains live in the agent_routes_*.go files; the peer-mode
// kernel assembly lives in agent_kernel.go — all split from the former
// merged agent.go (M-C2), which in turn came from actions.go, peer_mode.go,
// peer_agents.go, tools.go, mcp.go, mcp_null.go, dag_execution.go,
// session_admission.go, collab_graph.go.
//
// actions.go's file-level //nolint:errcheck was intentionally not carried
// over: errcheck reports no hits here today — response writes are either
// checked (see writeJSON) or explicit `_, _ =` best-effort bodies.
package main

import (
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strings"

	api_tools "github.com/Timwood0x10/ares/internal/apitools"
	"github.com/Timwood0x10/ares/internal/ares_security"
	"github.com/Timwood0x10/ares/internal/introspect"
	"github.com/Timwood0x10/ares/internal/runtime"
	evolution "github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
	"github.com/Timwood0x10/ares/internal/runtime/observability"
)

// writeJSON encodes v to w. HTTP handlers cannot recover a failed response
// write (the status line and headers are already sent), so the error is only
// logged — the client sees a truncated body, the log is the trace.
func writeJSON(w http.ResponseWriter, v any) {
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("actions: encode response failed", "error", err)
	}
}

// actionHandler wraps the monitoring HTTP handler with:
//   - Agent lifecycle (kill/resume/retry)
//   - Chaos engineering (random-kill/kill-all/recover)
//   - Tool API (list/call)
//
// All destructive endpoints (agents, chaos, tools/call) require authentication:
// either the legacy API key (Authorization: Bearer <key>) or a valid JWT with
// write permission (admin/operator) when JWT auth is configured. When neither
// credential is available, all destructive requests are denied
// (deny-by-default). Every destructive action is recorded on the modular audit
// sink (these paths were previously API-key-only and un-audited because
// actionHandler intercepted the gin routes).
type actionHandler struct {
	inner  http.Handler
	mgr    *runtime.Manager
	tools  *api_tools.Registry
	apiKey string                        // legacy credential (nil/empty = disabled)
	auth   *ares_security.AuthMiddleware // JWT credential (nil = disabled)
	audit  *ares_security.AuditLogger    // modular audit sink (nil = disabled)
	// kernel is the peer-runtime kernel handle (Leader OFF mode). It powers
	// POST /api/tasks (submitPeerTask); nil on the legacy leader path makes
	// that endpoint report 503 "peer runtime not active".
	kernel *kernelHandle
	// chaosStopToken guards the chaos emergency-stop endpoint: requests must
	// carry a matching X-Chaos-Token header. Empty disables the endpoint.
	chaosStopToken string
	// intro serves the runtime introspection panel (monitoring.md): embedded
	// UI at GET /introspect and the JSON read API at
	// /api/v1/introspect/*. Nil (panel not wired) yields 404, matching any
	// other unknown path.
	intro *introspect.Handler
	// lifecycle is the evolution StrategyLifecycle. It powers
	// POST /api/evolution/approve (manual gate release); nil disables the
	// endpoint with 503 "evolution lifecycle not active".
	lifecycle *evolution.StrategyLifecycle
	// cost serves the LLM cost dashboard API: /api/v1/observability/cost*
	// and the HTML dashboard. Nil disables the routes (404).
	cost *observability.CostDashboard
	// costMux routes the cost endpoints; built once at handler construction
	// via buildCostMux. Non-nil iff cost is non-nil — serveIntrospect
	// dereferences it whenever cost is set, so a nil here panics on the
	// first dashboard request.
	costMux *http.ServeMux
	// readAuth verifies JWTs at READ permission for the JSON read surfaces
	// (/api/v1/introspect/*, /api/tools, /api/mcp/tools, cost API). Nil when
	// auth is not configured: those surfaces then stay unauthenticated, which
	// is safe only because serve defaults to a loopback bind. The panel
	// HTML UI (/introspect) and /metrics stay open regardless — the UI
	// carries no data itself, and metrics follow the scraper convention.
	readAuth *ares_security.AuthMiddleware
	// introspectToken is the static bearer token for the introspect read
	// side (config introspect.token, M-S1). Empty: the read side is open,
	// protected only by the loopback default bind. Non-empty: non-loopback
	// clients must present "Authorization: Bearer <token>" (constant-time
	// compare) — a lightweight credential for deployments that expose the
	// panel without wiring full JWT auth. It composes with readAuth and
	// the legacy API key: any one of the three passes.
	introspectToken string
}

// buildCostMux registers the cost dashboard routes on a dedicated mux, once
// at handler construction. The result is assigned to actionHandler.costMux
// in the same literal that sets cost, so the two can never drift apart
// (an earlier per-request rebuild hid exactly that drift until it panicked).
func buildCostMux(dash *observability.CostDashboard) *http.ServeMux {
	if dash == nil {
		return nil
	}
	mux := http.NewServeMux()
	dash.RegisterCostRoutes(mux)
	return mux
}

// checkAuth enforces authentication on destructive endpoints: the legacy API
// key OR a valid JWT with write permission. Returns true if authorized.
// When neither credential is configured, all requests are denied
// (deny-by-default). A valid JWT that lacks write permission is rejected with
// 403 Forbidden (not 401), so the "unauthenticated vs forbidden" distinction
// matches the gin middleware.
func (h *actionHandler) checkAuth(w http.ResponseWriter, r *http.Request) *ares_security.Principal {
	// JWT path first: a valid token with write permission.
	jwtForbidden := false
	if h.auth != nil {
		if princ, status := h.auth.Verify(r); status == http.StatusOK {
			return princ
		} else if status == http.StatusForbidden {
			jwtForbidden = true
		}
	}
	// Legacy API key path.
	if h.apiKey != "" {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if strings.HasPrefix(auth, prefix) {
			token := strings.TrimPrefix(auth, prefix)
			if token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(h.apiKey)) == 1 {
				return &ares_security.Principal{Subject: "api-key", Role: ares_security.RoleOperator}
			}
		}
	}
	if h.apiKey == "" && h.auth == nil {
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(w, map[string]any{"error": "auth not configured"})
		return nil
	}
	// A well-formed JWT was presented but the role lacks write permission.
	// Report Forbidden (authenticated, not authorized) rather than the
	// misleading Unauthorized the generic path would give.
	if jwtForbidden {
		w.WriteHeader(http.StatusForbidden)
		writeJSON(w, map[string]any{"error": "insufficient role: token is valid but lacks write permission"})
		return nil
	}
	w.WriteHeader(http.StatusUnauthorized)
	writeJSON(w, map[string]any{"error": "invalid credentials"})
	return nil
}

// auditAction records a destructive action on the modular audit sink.
func (h *actionHandler) auditAction(action, target string, princ *ares_security.Principal, ok bool) {
	if h.audit == nil {
		return
	}
	subject := "unauthenticated"
	if princ != nil {
		subject = princ.Subject
	}
	h.audit.Action(action, subject, target, ok)
}

// checkAuthRead gates the JSON read surfaces at READ permission: a valid
// JWT with read permission, the legacy API key (a write key may read), or
// the introspect read token. When no credential at all is configured it
// allows the request — the same policy the introspect surface documented
// before, safe only under the loopback default bind. Returns false after
// writing the 401/403 response.
func (h *actionHandler) checkAuthRead(w http.ResponseWriter, r *http.Request) bool {
	// JWT path first: a valid token with READ permission (agent role qualifies).
	if h.readAuth != nil {
		if _, status := h.readAuth.Verify(r); status == http.StatusOK {
			return true
		}
	}
	// Legacy API key path: a write credential may also read.
	if h.apiKey != "" {
		if token := bearerToken(r); token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(h.apiKey)) == 1 {
			return true
		}
	}
	// Introspect read token (M-S1): a static bearer credential for the read
	// side. Non-loopback clients must present it (or one of the credentials
	// above); loopback stays open — but only while no stronger credential
	// layer is configured, so an auth-enabled deployment is never weakened
	// because an operator also set introspect.token.
	if h.introspectToken != "" {
		if token := bearerToken(r); token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(h.introspectToken)) == 1 {
			return true
		}
		if h.readAuth == nil && h.apiKey == "" && isLoopbackRequest(r) {
			return true
		}
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(w, map[string]any{"error": "introspect read side requires a bearer token (introspect.token)"})
		return false
	}
	// Auth not configured at all: loopback requests only. The previous
	// unconditional allow was fail-open — `--host 0.0.0.0` with no
	// credentials exposed the raw event stream (task inputs, checkpoints)
	// to the network while serve.go only logged a warning. Non-loopback
	// clients must configure one of the credential layers above.
	if h.readAuth == nil && h.apiKey == "" {
		if isLoopbackRequest(r) {
			return true
		}
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(w, map[string]any{"error": "read API requires a credential for non-loopback clients (configure auth, api key, or introspect.token)"})
		return false
	}
	w.WriteHeader(http.StatusUnauthorized)
	writeJSON(w, map[string]any{"error": "invalid credentials"})
	return false
}

// bearerToken extracts the bearer token from the Authorization header
// ("" when the header is absent or not Bearer). Shared by the API-key and
// introspect-token comparisons.
func bearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return ""
	}
	return strings.TrimPrefix(auth, prefix)
}

// isLoopbackRequest reports whether the request's TCP peer is a loopback
// address — the "local operator" the introspect read side trusts when no
// token is required. RemoteAddr is always host:port on server-side
// requests; a parse failure fails closed (not loopback).
func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ── Endpoint registry (M-S2) ─────────────────────────────
//
// The control plane's HTTP surface used to be a hand-written routing switch:
// every endpoint's auth level lived in an if-branch, and a new endpoint
// without a branch silently inherited whatever fell through to it. The
// registry below declares every route with its method, path pattern, and
// auth level, and the dispatcher enforces the declared level before the
// handler runs — an endpoint cannot exist in the table without stating its
// authentication requirement.

// authLevel classifies a route's credential requirement.
type authLevel int

const (
	// authNone: public — the panel HTML shell (no data), the Prometheus
	// scrape, and the non-API 404 tail.
	authNone authLevel = iota
	// authRead: the JSON read surfaces (checkAuthRead) — read-permission
	// JWT, legacy API key, or the introspect read token for non-loopback
	// clients.
	authRead
	// authWrite: the destructive endpoints (checkAuth) — write-permission
	// JWT or legacy API key, deny-by-default. Finer RBAC (e.g. chaos
	// requires admin) is enforced inside the handlers on the verified
	// principal.
	authWrite
	// authLocal: loopback-only (no route uses it today; declared so the
	// level exists for endpoints that should never see a network client).
	authLocal
)

// String renders the level for startup/audit logging.
func (a authLevel) String() string {
	switch a {
	case authNone:
		return "none"
	case authRead:
		return "read"
	case authWrite:
		return "write"
	case authLocal:
		return "local"
	default:
		return "unknown"
	}
}

// routeSpec is one entry in the control-plane endpoint registry.
type routeSpec struct {
	// Method is the HTTP method ("GET", "POST", or "*" for any).
	Method string
	// Path is the route pattern, in one of three forms:
	//   "/api/tasks"                  exact match
	//   "/api/v1/introspect/..."      prefix match (trailing "..." is the marker)
	//   "/api/mcp/tools/{name}/call"  segment match ("{name}" matches one segment)
	Path string
	// Auth is the credential level the dispatcher enforces before Handler.
	Auth authLevel
	// Available reports whether the route participates in dispatch at all
	// (nil = always). Wiring-dependent surfaces use it: the panel routes
	// exist only when the introspect handler is wired, the cost routes only
	// when the dashboard is — mirroring the pre-registry serveIntrospect
	// early-returns.
	Available func(*actionHandler) bool
	// Handler serves the request; princ is the verified principal on
	// authWrite routes (nil otherwise). These are dispatch glue only — the
	// handler bodies are the pre-registry functions, untouched.
	Handler func(*actionHandler, http.ResponseWriter, *http.Request, *ares_security.Principal)
	// Desc documents the route (one line, for the registry audit in
	// docs/reviews/ARCHITECTURE_REVIEW_MERMAID.md §6 / M-S3).
	Desc string
}

// panelAvailable gates the introspect-handler surfaces: the panel UI, its
// JSON feed, /metrics and the root redirect all route through h.intro; when
// it is not wired the whole family falls to the control-server tail.
func panelAvailable(h *actionHandler) bool { return h.intro != nil }

// costAvailable gates the cost dashboard routes: they additionally require
// the dashboard to be wired (cost != nil implies costMux != nil — the pair
// is constructed atomically, see buildCostMux).
func costAvailable(h *actionHandler) bool { return h.intro != nil && h.cost != nil }

// actionRoutes is the endpoint registry, in dispatch priority order (the
// order the pre-registry switch checked its branches). The final two
// entries are the tail: every /api/* path not claimed above is read-gated
// and passed to the read-only control server; every non-API path passes
// straight through.
var actionRoutes = []routeSpec{
	{Method: "POST", Path: "/api/agents/...", Auth: authWrite,
		Desc:    "agent lifecycle: /api/agents/:id/{kill,resume,retry} (unknown shapes fall to the control-server tail)",
		Handler: (*actionHandler).routeAgentLifecycle},
	{Method: "GET", Path: "/introspect", Auth: authNone, Available: panelAvailable,
		Desc:    "introspect panel UI shell (no data)",
		Handler: (*actionHandler).routePanel},
	{Method: "GET", Path: "/introspect/...", Auth: authNone, Available: panelAvailable,
		Desc:    "introspect panel UI assets",
		Handler: (*actionHandler).routePanel},
	{Method: "GET", Path: "/api/v1/introspect/...", Auth: authRead, Available: panelAvailable,
		Desc:    "introspect JSON feed: task payloads, raw events, scheduler state",
		Handler: (*actionHandler).routePanel},
	{Method: "GET", Path: "/metrics", Auth: authNone, Available: panelAvailable,
		Desc:    "Prometheus scrape endpoint",
		Handler: (*actionHandler).routeMetrics},
	{Method: "GET", Path: "/api/v1/observability/cost...", Auth: authRead, Available: costAvailable,
		Desc:    "LLM cost API (read-only)",
		Handler: (*actionHandler).routeCost},
	{Method: "GET", Path: "/api/v1/observability/dashboard", Auth: authRead, Available: costAvailable,
		Desc:    "LLM cost dashboard HTML (read-only)",
		Handler: (*actionHandler).routeCost},
	{Method: "GET", Path: "/", Auth: authNone, Available: panelAvailable,
		Desc:    "root redirect to the panel",
		Handler: (*actionHandler).routeRoot},
	{Method: "POST", Path: "/api/chaos/...", Auth: authWrite,
		Desc:    "chaos: {random-kill,kill-all,recover} require admin role; stop additionally requires X-Chaos-Token",
		Handler: (*actionHandler).routeChaos},
	{Method: "POST", Path: "/api/evolution/approve", Auth: authWrite,
		Desc:    "evolution manual-approval gate release",
		Handler: (*actionHandler).routeEvolutionApprove},
	{Method: "POST", Path: "/api/tools/call", Auth: authWrite,
		Desc:    "tool invocation",
		Handler: (*actionHandler).routeCallTool},
	{Method: "GET", Path: "/api/tools", Auth: authRead,
		Desc:    "tool inventory (reconnaissance surface)",
		Handler: (*actionHandler).routeListTools},
	{Method: "GET", Path: "/api/mcp/tools", Auth: authRead,
		Desc:    "MCP tool inventory (reconnaissance surface)",
		Handler: (*actionHandler).routeListMCPTools},
	{Method: "POST", Path: "/api/mcp/tools/{name}/call", Auth: authWrite,
		Desc:    "MCP tool invocation",
		Handler: (*actionHandler).routeCallMCPTool},
	{Method: "POST", Path: "/api/tasks", Auth: authWrite,
		Desc:    "peer task submission (submitPeerTask)",
		Handler: (*actionHandler).routeSubmitTask},
	{Method: "POST", Path: "/api/graphs", Auth: authWrite,
		Desc:    "collaboration graph submission (DAG)",
		Handler: (*actionHandler).routeSubmitGraph},
	{Method: "*", Path: "/api/...", Auth: authRead,
		Desc:    "read-only control server: /api/agents, /api/health, /api/runtime/config, /api/flight/*, /api/observability/spans, /api/insights, /api/anomalies, /api/evolution/trajectory",
		Handler: (*actionHandler).routeInner},
	{Method: "*", Path: "/...", Auth: authNone,
		Desc:    "non-API tail (control-server 404), left ungated so probing a wrong URL needs no token",
		Handler: (*actionHandler).routeInner},
}

// match reports whether the request's method and path match the spec.
func (s routeSpec) match(method, path string) bool {
	if s.Method != "*" && s.Method != method {
		return false
	}
	// A trailing "..." marks a prefix pattern: the marker is appended
	// directly to the prefix, so "/api/agents/..." prefixes "/api/agents/"
	// while "/api/v1/observability/cost..." prefixes the bare
	// "/api/v1/observability/cost" (mirroring the pre-registry HasPrefix).
	if prefix, ok := strings.CutSuffix(s.Path, "..."); ok {
		return strings.HasPrefix(path, prefix)
	}
	if strings.Contains(s.Path, "{") {
		return matchSegments(s.Path, path)
	}
	return path == s.Path
}

// matchSegments matches path against a pattern containing "{param}"
// segments: each "{param}" captures exactly one "/"-delimited segment
// (possibly empty, matching strings.Split semantics), literal segments
// compare exactly. Handlers re-derive their captured values from the path
// the same way the pre-registry switch did.
func matchSegments(pattern, path string) bool {
	p := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	s := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(p) != len(s) {
		return false
	}
	for i, seg := range p {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			continue
		}
		if seg != s[i] {
			return false
		}
	}
	return true
}

// authorize enforces the route's auth level. It returns the verified
// principal (non-nil only for authWrite, whose handlers audit on it) and
// whether the request may proceed; a denial has already been written to w.
func (h *actionHandler) authorize(level authLevel, w http.ResponseWriter, r *http.Request) (*ares_security.Principal, bool) {
	switch level {
	case authNone:
		return nil, true
	case authRead:
		if !h.checkAuthRead(w, r) {
			return nil, false
		}
		return nil, true
	case authWrite:
		princ := h.checkAuth(w, r)
		if princ == nil {
			return nil, false
		}
		return princ, true
	case authLocal:
		if !isLoopbackRequest(r) {
			w.WriteHeader(http.StatusForbidden)
			writeJSON(w, map[string]any{"error": "localhost only"})
			return nil, false
		}
		return nil, true
	default:
		// Unknown level: deny. The registry is the only source of levels,
		// so this is unreachable — but a future level without an enforcement
		// case must never become an implicit allow.
		w.WriteHeader(http.StatusForbidden)
		writeJSON(w, map[string]any{"error": "unhandled auth level"})
		return nil, false
	}
}

// ServeHTTP dispatches through the endpoint registry: the first available
// route matching the method+path gets its declared auth level enforced,
// then its handler runs. The registry replaced the hand-written routing
// switch one-for-one; the handler bodies are unchanged.
func (h *actionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Limit request body on all POST endpoints to 1MB to prevent
	// memory exhaustion from oversized payloads.
	if r.Method == http.MethodPost && r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB
	}
	for _, spec := range actionRoutes {
		if spec.Available != nil && !spec.Available(h) {
			continue
		}
		if !spec.match(r.Method, r.URL.Path) {
			continue
		}
		princ, ok := h.authorize(spec.Auth, w, r)
		if !ok {
			return
		}
		spec.Handler(h, w, r, princ)
		return
	}
	// Unreachable in practice — the registry's final entry matches every
	// path — but kept as the fail-safe: an exotic request target still
	// reaches the control server instead of a silent no-response.
	h.inner.ServeHTTP(w, r)
}

// ── Registry route adapters ──────────────────────────────
//
// One thin adapter per registry entry. They only translate (w, r, princ)
// into the pre-registry handler signatures; no business logic lives here.

// routePanel serves the introspect panel UI and its JSON feed (the feed's
// read gate is enforced by the dispatcher's authRead level).
func (h *actionHandler) routePanel(w http.ResponseWriter, r *http.Request, _ *ares_security.Principal) {
	h.intro.ServeHTTP(w, r)
}

// routeMetrics serves the Prometheus scrape endpoint (the old :8090
// dashboard server mounted /metrics; re-mounted here so scraping the ARES
// runtime survives the dashboard deletion).
func (h *actionHandler) routeMetrics(w http.ResponseWriter, r *http.Request, _ *ares_security.Principal) {
	observability.MetricsHTTPHandler().ServeHTTP(w, r)
}

// routeCost serves the LLM cost dashboard API/HTML through the mux built
// once at handler construction (buildCostMux); the read gate is enforced
// by the dispatcher.
func (h *actionHandler) routeCost(w http.ResponseWriter, r *http.Request, _ *ares_security.Principal) {
	h.costMux.ServeHTTP(w, r)
}

// routeRoot redirects the console root to the panel.
func (h *actionHandler) routeRoot(w http.ResponseWriter, r *http.Request, _ *ares_security.Principal) {
	http.Redirect(w, r, "/introspect", http.StatusFound)
}

// routeEvolutionApprove releases the evolution manual-approval gate.
func (h *actionHandler) routeEvolutionApprove(w http.ResponseWriter, r *http.Request, princ *ares_security.Principal) {
	h.handleEvolutionApprove(w, r, princ)
}

// routeInner passes through to the read-only control server. The /api/*
// tail entry already applied the read gate (authRead) at the dispatcher;
// the non-API tail entry is ungated by policy.
func (h *actionHandler) routeInner(w http.ResponseWriter, r *http.Request, _ *ares_security.Principal) {
	h.inner.ServeHTTP(w, r)
}
