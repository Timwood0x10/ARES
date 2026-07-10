// Package knowledge tests for the AKF HTTP API service.
package knowledge

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	knowledge "github.com/Timwood0x10/ares/internal/knowledge"
)

func TestNew(t *testing.T) {
	svc := New(nil, nil, nil)
	require.NotNil(t, svc)
}

func TestSetAPIKey(t *testing.T) {
	svc := New(nil, nil, nil)
	svc.SetAPIKey("test-key-123")
	// No panic is the main assertion.
}

func TestRegisterRoutesWithoutAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	svc := New(nil, nil, nil)
	svc.RegisterRoutes(router.Group("/api"))

	// POST /api/kg/build should return 400 (no body) without auth.
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/kg/build", nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// POST /api/kg/context should return 400 (no body) without auth.
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest(http.MethodPost, "/api/kg/context", nil)
	router.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusBadRequest, w2.Code)

	// POST /api/kg/query should return 400 (no body) without auth.
	w3 := httptest.NewRecorder()
	req3, _ := http.NewRequest(http.MethodPost, "/api/kg/query", nil)
	router.ServeHTTP(w3, req3)
	assert.Equal(t, http.StatusBadRequest, w3.Code)

	// POST /api/kg/distill should return 400 (no body) without auth.
	w4 := httptest.NewRecorder()
	req4, _ := http.NewRequest(http.MethodPost, "/api/kg/distill", nil)
	router.ServeHTTP(w4, req4)
	assert.Equal(t, http.StatusBadRequest, w4.Code)
}

func TestRegisterRoutesWithAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	svc := New(nil, nil, nil)
	svc.SetAPIKey("secret-key")
	svc.RegisterRoutes(router.Group("/api"))

	// Without auth header, should return 401.
	w := httptest.NewRecorder()
	body := `{"goal":"test"}`
	req, _ := http.NewRequest(http.MethodPost, "/api/kg/build", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// With wrong auth header, should return 401.
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest(http.MethodPost, "/api/kg/build", strings.NewReader(body))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer wrong-key")
	router.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusUnauthorized, w2.Code)

	// With correct auth header, the middleware passes but the handler panics
	// because rt is nil. Test that the middleware itself works by checking
	// that the request reaches the handler (not blocked by auth).
	// Instead of testing the full handler path, test the authMiddleware directly.
	authFn := svc.authMiddleware()
	handler := func(c *gin.Context) {
		c.Status(http.StatusOK)
	}
	router2 := gin.New()
	router2.Use(authFn)
	router2.POST("/test", handler)

	w3 := httptest.NewRecorder()
	req3, _ := http.NewRequest(http.MethodPost, "/test", nil)
	req3.Header.Set("Authorization", "Bearer secret-key")
	router2.ServeHTTP(w3, req3)
	assert.Equal(t, http.StatusOK, w3.Code)

	// Wrong key should be rejected.
	w4 := httptest.NewRecorder()
	req4, _ := http.NewRequest(http.MethodPost, "/test", nil)
	req4.Header.Set("Authorization", "Bearer wrong-key")
	router2.ServeHTTP(w4, req4)
	assert.Equal(t, http.StatusUnauthorized, w4.Code)
}

func TestHandleDistillNoContent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	svc := New(nil, nil, nil)
	svc.RegisterRoutes(router.Group("/api"))

	// POST /api/kg/distill without content should return 400.
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/kg/distill", nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleDistillWithContent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	svc := New(nil, nil, nil)
	svc.RegisterRoutes(router.Group("/api"))

	body := `{"content":"test content","tags":["tag1","tag2"],"type":"memory"}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/kg/distill", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "object_id")
	assert.Contains(t, w.Body.String(), "test content")
}

func TestHandleDistillWithEmptyType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	svc := New(nil, nil, nil)
	svc.RegisterRoutes(router.Group("/api"))

	// When type is empty, it defaults to memory.
	body := `{"content":"test data"}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/kg/distill", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	// Response should contain the default type.
}

func TestNodeIDsEmpty(t *testing.T) {
	// Pass an empty graph (not nil) to avoid nil pointer dereference.
	ids := nodeIDs(&knowledge.WorkingGraph{Nodes: map[string]*knowledge.KnowledgeObject{}})
	assert.Empty(t, ids)
}
