package knowledge

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// newProbeRouter mounts the service's auth middleware on a probe route so
// tests can exercise authentication without touching the real handlers
// (whose runtime dependencies are nil in unit tests).
func newProbeRouter(svc *Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/probe", svc.authMiddleware(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	return router
}

// TestSetAPIKeyConcurrentWithRequests is the HIGH-4 regression: SetAPIKey
// (config hot-reload path) running concurrently with request handling must
// not data-race on the apiKey field.
//
// Bug scenario: before the fix apiKey was a plain field written by
// SetAPIKey and read by the auth middleware without any synchronization;
// under `go test -race` the concurrent write/read is reported as a data
// race and the middleware could observe a torn value.
//
// Fix contract: writes take the write lock, reads (apiKeyEnabled /
// currentAPIKey) take the read lock. Every response must be a valid auth
// outcome (200 or 401) regardless of interleaving.
func TestSetAPIKeyConcurrentWithRequests(t *testing.T) {
	svc := New(nil, nil, nil)
	svc.SetAPIKey("key-a")
	router := newProbeRouter(svc)

	doRequest := func(authHeader string) int {
		req := httptest.NewRequest(http.MethodPost, "/probe", nil)
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w.Code
	}

	const writers = 4
	const readers = 8
	const iterations = 50

	var wg sync.WaitGroup
	wg.Add(writers + readers)

	// Writers rotate the API key between two values and the empty string,
	// mirroring a config reload that enables, rotates, and disables auth.
	keys := []string{"key-a", "key-b", ""}
	for i := 0; i < writers; i++ {
		go func(i int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				svc.SetAPIKey(keys[(i+j)%len(keys)])
			}
		}(i)
	}

	// Readers issue requests with every header variant; each response must
	// be either authenticated (200) or rejected (401) — never a panic or an
	// unexpected status from a torn read.
	headers := []string{
		"Bearer key-a",
		"Bearer key-b",
		"Bearer wrong",
		"",
	}
	for i := 0; i < readers; i++ {
		go func(i int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				code := doRequest(headers[(i+j)%len(headers)])
				if code != http.StatusOK && code != http.StatusUnauthorized {
					assert.Failf(t, "unexpected status",
						"got %d for header %q during concurrent reload", code, headers[(i+j)%len(headers)])
					return
				}
			}
		}(i)
	}
	wg.Wait()
}

// TestAuthMiddlewareHeaderVariants covers the boundary shapes of the
// Authorization header accepted by the middleware.
//
// Bug scenarios: a scheme mismatch ("Basic"), a missing space after
// "Bearer", a case difference ("bearer"), and an empty token must all be
// rejected — only the exact "Bearer <key>" form authenticates.
func TestAuthMiddlewareHeaderVariants(t *testing.T) {
	svc := New(nil, nil, nil)
	svc.SetAPIKey("secret-key")
	router := newProbeRouter(svc)

	cases := []struct {
		name       string
		authHeader string
		wantCode   int
	}{
		{name: "missing header", authHeader: "", wantCode: http.StatusUnauthorized},
		{name: "wrong scheme", authHeader: "Basic secret-key", wantCode: http.StatusUnauthorized},
		{name: "bearer without space", authHeader: "Bearersecret-key", wantCode: http.StatusUnauthorized},
		{name: "lowercase scheme", authHeader: "bearer secret-key", wantCode: http.StatusUnauthorized},
		{name: "wrong key", authHeader: "Bearer wrong-key", wantCode: http.StatusUnauthorized},
		{name: "empty bearer token", authHeader: "Bearer ", wantCode: http.StatusUnauthorized},
		{name: "correct key", authHeader: "Bearer secret-key", wantCode: http.StatusOK},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/probe", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			assert.Equal(t, tc.wantCode, w.Code)
		})
	}
}

// TestSetAPIKeyAfterRegisterRoutesDoesNotProtect documents the current
// contract of route registration: RegisterRoutes evaluates apiKeyEnabled()
// ONCE at mount time, so calling SetAPIKey afterwards does not retroactively
// attach the auth middleware to already-registered routes.
//
// Production callers must therefore configure the key BEFORE RegisterRoutes
// (as cmd/ares does). If late configuration ever needs support, the
// middleware must be mounted unconditionally and consult apiKeyEnabled()
// per request instead.
func TestSetAPIKeyAfterRegisterRoutesDoesNotProtect(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	svc := New(nil, nil, nil)
	svc.RegisterRoutes(router.Group("/api"))

	// Auth configured AFTER registration: the /kg/build route was mounted
	// without middleware, so even a request carrying no credentials reaches
	// the handler (which rejects the nil body with 400, not 401).
	svc.SetAPIKey("late-key")

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/kg/build", nil)
	router.ServeHTTP(w, req)
	assert.NotEqual(t, http.StatusUnauthorized, w.Code,
		"late SetAPIKey must not retroactively protect registered routes")
}
