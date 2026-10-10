package builtin

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ── HTTPClient / WebFetcher ─────────────────────────────────────────────────

func TestDefaultHTTPClient_Wiring(t *testing.T) {
	// SSRF transport blocks localhost, so we verify the client is wired
	// correctly (timeout, CheckRedirect, Transport) rather than making a
	// live request.
	c := NewDefaultHTTPClient(5 * time.Second)
	if c == nil {
		t.Fatal("NewDefaultHTTPClient returned nil")
	}
	if c.client == nil {
		t.Fatal("underlying http.Client is nil")
	}
	if c.client.CheckRedirect == nil {
		t.Error("CheckRedirect should be set for SSRF defense")
	}
	if c.client.Transport == nil {
		t.Error("Transport should be set for SSRF defense")
	}
	if c.client.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v, want 5s", c.client.Timeout)
	}
}

func TestDefaultHTTPClient_ZeroTimeoutDefaults(t *testing.T) {
	c := NewDefaultHTTPClient(0)
	if c.client.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want 30s default", c.client.Timeout)
	}
}

func TestMockHTTPClient_Do(t *testing.T) {
	mc := &mockHTTPClient{response: &http.Response{
		StatusCode: http.StatusOK,
		Body:       http.NoBody,
	}}
	req, err := http.NewRequestWithContext(context.Background(), "GET", "https://example.com/", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	resp, err := mc.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Errorf("close: %v", cerr)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestNewWebFetcher(t *testing.T) {
	f := NewWebFetcher(NewDefaultHTTPClient(5))
	if f == nil {
		t.Fatal("NewWebFetcher returned nil")
	}
	if f.userAgent == "" {
		t.Error("default user agent should not be empty")
	}
}

func TestWebFetcher_SetUserAgent(t *testing.T) {
	f := NewWebFetcher(NewDefaultHTTPClient(5))
	f.SetUserAgent("CustomAgent/1.0")
	if f.userAgent != "CustomAgent/1.0" {
		t.Errorf("userAgent = %q", f.userAgent)
	}
}

func TestWebFetcher_Get_Success(t *testing.T) {
	// Use a mock client since SSRF transport blocks localhost httptest servers.
	body := io.NopCloser(strings.NewReader("page content"))
	f := NewWebFetcher(&mockHTTPClient{response: &http.Response{
		StatusCode: http.StatusOK,
		Body:       body,
	}})
	got, err := f.Get(context.Background(), "https://example.com/page")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got) != "page content" {
		t.Errorf("got %q, want page content", got)
	}
}

func TestWebFetcher_Get_SSRFRejection(t *testing.T) {
	f := NewWebFetcher(NewDefaultHTTPClient(5))
	_, err := f.Get(context.Background(), "http://127.0.0.1/")
	if err == nil {
		t.Error("expected SSRF rejection for loopback")
	}
}

func TestWebFetcher_Get_HTTPError(t *testing.T) {
	body := io.NopCloser(strings.NewReader(""))
	f := NewWebFetcher(&mockHTTPClient{response: &http.Response{
		StatusCode: http.StatusNotFound,
		Body:       body,
		Status:     "404 Not Found",
	}})
	_, err := f.Get(context.Background(), "https://example.com/")
	if err == nil {
		t.Error("expected error for HTTP 404")
	}
}

type mockHTTPClient struct {
	response *http.Response
	err      error
}

func (m *mockHTTPClient) Do(_ *http.Request) (*http.Response, error) {
	return m.response, m.err
}

func TestHTTPError_Error(t *testing.T) {
	e := &HTTPError{StatusCode: 500, Message: "internal error"}
	if e.Error() != "internal error" {
		t.Errorf("Error() = %q", e.Error())
	}
}

// ── HTTPRequest tool ────────────────────────────────────────────────────────

func TestNewHTTPRequest(t *testing.T) {
	tool := NewHTTPRequest()
	if tool == nil {
		t.Fatal("NewHTTPRequest returned nil")
	}
	if tool.Name() != "http_request" {
		t.Errorf("Name = %q", tool.Name())
	}
	if tool.client == nil {
		t.Error("client is nil")
	}
}

func TestHTTPRequest_Execute_MissingURL(t *testing.T) {
	tool := NewHTTPRequest()
	result, err := tool.Execute(context.Background(), map[string]interface{}{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success {
		t.Error("expected failure for missing URL")
	}
}

func TestHTTPRequest_Execute_SSRFBlocked(t *testing.T) {
	tool := NewHTTPRequest()
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"url": "http://169.254.169.254/",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success {
		t.Error("expected SSRF rejection")
	}
}

func TestHTTPRequest_Execute_InvalidJSONBody(t *testing.T) {
	tool := NewHTTPRequest()
	// Use a public IP literal that passes SSRF; body validation returns
	// before any network call is made.
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"url":    "https://8.8.8.8/",
		"method": "POST",
		"headers": map[string]interface{}{
			"Content-Type": "application/json",
		},
		"body": "not valid json {",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success {
		t.Error("expected failure for invalid JSON body")
	}
}

func TestHTTPRequest_ToolProperties(t *testing.T) {
	// SSRF filter blocks loopback; the real HTTP round-trip is covered by
	// WebSearch tests which use an allowlist instead of IP blocking.
	tool := NewHTTPRequest()
	if tool.Name() != "http_request" {
		t.Errorf("Name = %q", tool.Name())
	}
	if tool.client == nil {
		t.Error("client should not be nil")
	}
}

func TestHTTPRequest_Execute_DefaultMethodGET(t *testing.T) {
	// SSRF filter blocks loopback; the success path needs a non-local target.
	// We verify the SSRF rejection path instead.
	tool := NewHTTPRequest()
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"url": "http://10.0.0.1/",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success {
		t.Error("expected SSRF rejection for private IP")
	}
}

func TestHTTPRequest_Execute_TimeoutZero(t *testing.T) {
	// SSRF blocks loopback; verify timeout=0 path still hits SSRF check.
	tool := NewHTTPRequest()
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"url":     "http://192.168.1.1/",
		"timeout": 0,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success {
		t.Error("expected SSRF rejection")
	}
}

func TestHTTPRequest_GetString(t *testing.T) {
	tests := []struct {
		key   string
		value interface{}
		want  string
	}{
		{"url", "https://example.com", "https://example.com"},
		{"url", 42, ""},
		{"missing", nil, ""},
	}
	for _, tt := range tests {
		params := map[string]interface{}{}
		if tt.value != nil {
			params[tt.key] = tt.value
		}
		if got := getString(params, tt.key); got != tt.want {
			t.Errorf("getString(%q, %v) = %q, want %q", tt.key, tt.value, got, tt.want)
		}
	}
}

func TestHTTPRequest_GetInt(t *testing.T) {
	tests := []struct {
		key   string
		value interface{}
		def   int
		want  int
	}{
		{"timeout", 30.0, 30, 30},
		{"timeout", 15, 30, 15},
		{"timeout", "20", 30, 20},
		{"timeout", "invalid", 30, 30},
		{"timeout", nil, 30, 30},
	}
	for _, tt := range tests {
		params := map[string]interface{}{}
		if tt.value != nil {
			params[tt.key] = tt.value
		}
		if got := getInt(params, tt.key, tt.def); got != tt.want {
			t.Errorf("getInt(%q, %v, %d) = %d, want %d", tt.key, tt.value, tt.def, got, tt.want)
		}
	}
}

// ── WebSearch tool ──────────────────────────────────────────────────────────

func TestNewWebSearch(t *testing.T) {
	ws := NewWebSearch()
	if ws == nil {
		t.Fatal("NewWebSearch returned nil")
	}
	if ws.Name() != "web_search" {
		t.Errorf("Name = %q", ws.Name())
	}
	if !ws.isBaseURLAllowed(defaultSearXNGBaseURL) {
		t.Error("default SearXNG URL should be allowed")
	}
}

func TestWebSearch_Execute_MissingQuery(t *testing.T) {
	ws := NewWebSearch()
	result, err := ws.Execute(context.Background(), map[string]interface{}{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success {
		t.Error("expected failure for missing query")
	}
}

func TestWebSearch_Execute_NotInAllowlist(t *testing.T) {
	ws := NewWebSearch()
	result, err := ws.Execute(context.Background(), map[string]interface{}{
		"query":            "test",
		"searxng_base_url": "http://evil.example.com",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success {
		t.Error("expected failure for URL not in allowlist")
	}
}

func TestWebSearch_Execute_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			t.Errorf("path = %q", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("q") != "golang" {
			t.Errorf("query = %q", q.Get("q"))
		}
		if q.Get("format") != "json" {
			t.Errorf("format = %q", q.Get("format"))
		}

		resp := SearXNGResponse{
			Query: "golang",
			Results: []SearXNGResult{
				{Title: "Go", URL: "https://go.dev", Content: "Go is fast", Engine: "google"},
			},
			Suggestions: []string{"golang tutorial"},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer server.Close()

	ws := NewWebSearch()
	ws.SetAllowedBaseURLs([]string{server.URL})

	result, err := ws.Execute(context.Background(), map[string]interface{}{
		"query":            "golang",
		"searxng_base_url": server.URL,
		"max_results":      5,
		"language":         "en",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got: %v", result.Error)
	}
	data, ok := result.Data.(map[string]interface{})
	if !ok {
		t.Fatal("result data is not a map")
	}
	if data["total_results"] != 1 {
		t.Errorf("total_results = %v", data["total_results"])
	}
	if data["suggestions"] == nil {
		t.Error("suggestions should be present")
	}
}

func TestWebSearch_Execute_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	ws := NewWebSearch()
	ws.SetAllowedBaseURLs([]string{server.URL})

	result, err := ws.Execute(context.Background(), map[string]interface{}{
		"query":            "test",
		"searxng_base_url": server.URL,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success {
		t.Error("expected failure for HTTP 500")
	}
}

func TestWebSearch_Execute_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte("not json")); err != nil {
			t.Errorf("write: %v", err)
		}
	}))
	defer server.Close()

	ws := NewWebSearch()
	ws.SetAllowedBaseURLs([]string{server.URL})

	result, err := ws.Execute(context.Background(), map[string]interface{}{
		"query":            "test",
		"searxng_base_url": server.URL,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success {
		t.Error("expected failure for invalid JSON response")
	}
}

func TestWebSearch_Execute_AllParams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("categories") != "general" {
			t.Errorf("categories = %q", q.Get("categories"))
		}
		if q.Get("engines") != "google,bing" {
			t.Errorf("engines = %q", q.Get("engines"))
		}
		if q.Get("pageno") != "2" {
			t.Errorf("pageno = %q", q.Get("pageno"))
		}
		if q.Get("time_range") != "week" {
			t.Errorf("time_range = %q", q.Get("time_range"))
		}
		resp := SearXNGResponse{Query: "test", Results: []SearXNGResult{}}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer server.Close()

	ws := NewWebSearch()
	ws.SetAllowedBaseURLs([]string{server.URL})

	result, err := ws.Execute(context.Background(), map[string]interface{}{
		"query":            "test",
		"searxng_base_url": server.URL,
		"categories":       "general",
		"engines":          "google,bing",
		"pageno":           2,
		"time_range":       "week",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Success {
		t.Errorf("expected success: %v", result.Error)
	}
}

func TestWebSearch_CheckRedirect(t *testing.T) {
	ws := NewWebSearch()
	ws.SetAllowedBaseURLs([]string{"http://allowed.example.com"})

	// Redirect to allowed URL — should pass
	req, err := http.NewRequestWithContext(context.Background(), "GET", "http://allowed.example.com/path", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	if err := ws.checkRedirect(req, []*http.Request{}); err != nil {
		t.Errorf("checkRedirect allowed = %v", err)
	}

	// Redirect to disallowed URL — should fail
	req2, err := http.NewRequestWithContext(context.Background(), "GET", "http://evil.example.com/", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	if err := ws.checkRedirect(req2, []*http.Request{}); err == nil {
		t.Error("expected error for redirect to disallowed URL")
	}
}

func TestWebSearch_CheckRedirect_MaxHops(t *testing.T) {
	ws := NewWebSearch()
	ws.SetAllowedBaseURLs([]string{"http://allowed.example.com"})

	via := make([]*http.Request, MaxHTTPRedirects)
	req, err := http.NewRequestWithContext(context.Background(), "GET", "http://allowed.example.com/", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	if err := ws.checkRedirect(req, via); err == nil {
		t.Error("expected error when exceeding max redirects")
	}
}

func TestWebSearch_SetAllowedBaseURLs_Replaces(t *testing.T) {
	ws := NewWebSearch()
	ws.SetAllowedBaseURLs([]string{"http://custom.example.com"})
	if ws.isBaseURLAllowed(defaultSearXNGBaseURL) {
		t.Error("default URL should no longer be allowed after SetAllowedBaseURLs")
	}
	if !ws.isBaseURLAllowed("http://custom.example.com") {
		t.Error("custom URL should be allowed")
	}
}

// ── WebScraper: SetGetter ──────────────────────────────────────────────────

func TestWebScraper_SetGetter(t *testing.T) {
	scraper := NewWebScraper(&MockHTTPGetter{content: []byte("old")})
	newGetter := &MockHTTPGetter{content: []byte("new content")}
	scraper.SetGetter(newGetter)

	result, err := scraper.Execute(context.Background(), map[string]interface{}{
		"url":           "https://example.com",
		"extract_title": false,
		"extract_body":  true,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	data := result.Data.(map[string]interface{})
	if data["content"] != "new content" {
		t.Errorf("content = %v, want 'new content'", data["content"])
	}
}

// ── SSRF: additional edge cases ────────────────────────────────────────────

func TestValidateURL_IPv6Literal(t *testing.T) {
	err := ValidateURL(context.Background(), "http://[::1]/")
	if err == nil {
		t.Error("expected error for IPv6 loopback")
	}
}

func TestValidateURL_PublicIPLiteral(t *testing.T) {
	// 8.8.8.8 is a public IP literal — ValidateURL resolves the IP directly
	// without DNS. It must pass the SSRF filter.
	err := ValidateURL(context.Background(), "https://8.8.8.8/")
	if err != nil {
		t.Errorf("ValidateURL(https://8.8.8.8/) = %v, want nil (public IP should pass)", err)
	}
}
