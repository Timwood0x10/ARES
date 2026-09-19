package builtin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ── NewEmbeddingTool ────────────────────────────────────────────────────────

func TestNewEmbeddingTool_DefaultURL(t *testing.T) {
	tool := NewEmbeddingTool("")
	if tool == nil {
		t.Fatal("NewEmbeddingTool returned nil")
	}
	if tool.baseURL != defaultEmbeddingBaseURL {
		t.Errorf("baseURL = %q, want %q", tool.baseURL, defaultEmbeddingBaseURL)
	}
	if tool.Name() != "embedding" {
		t.Errorf("Name = %q", tool.Name())
	}
}

func TestNewEmbeddingTool_CustomURL(t *testing.T) {
	tool := NewEmbeddingTool("http://custom:9000")
	if tool.baseURL != "http://custom:9000" {
		t.Errorf("baseURL = %q", tool.baseURL)
	}
}

// ── readEmbeddingBody ───────────────────────────────────────────────────────

func TestReadEmbeddingBody_Success(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       http.NoBody,
	}
	body, err := readEmbeddingBody(resp)
	if err != nil {
		t.Fatalf("readEmbeddingBody: %v", err)
	}
	_ = body
}

func TestReadEmbeddingBody_ErrorStatus(t *testing.T) {
	statuses := []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusInternalServerError, http.StatusNotFound}
	for _, status := range statuses {
		resp := &http.Response{
			StatusCode: status,
			Status:     http.StatusText(status),
			Body:       http.NoBody,
		}
		_, err := readEmbeddingBody(resp)
		if err == nil {
			t.Errorf("expected error for status %d", status)
		}
	}
}

// ── Execute: unknown action ─────────────────────────────────────────────────

func TestExecute_UnknownAction(t *testing.T) {
	tool := NewEmbeddingTool("")
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "bogus",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success {
		t.Error("expected failure for unknown action")
	}
}

func TestExecute_EmptyAction(t *testing.T) {
	tool := NewEmbeddingTool("")
	result, err := tool.Execute(context.Background(), map[string]interface{}{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success {
		t.Error("expected failure for empty action")
	}
}

// ── checkHealth via httptest ────────────────────────────────────────────────

func TestCheckHealth_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"status": "ok", "model": "e5-large"}); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer server.Close()

	tool := NewEmbeddingTool(server.URL)
	result, err := tool.Execute(context.Background(), map[string]interface{}{"action": "health"})
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
	if data["status"] != "ok" {
		t.Errorf("status = %v", data["status"])
	}
}

func TestCheckHealth_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	tool := NewEmbeddingTool(server.URL)
	result, err := tool.Execute(context.Background(), map[string]interface{}{"action": "health"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success {
		t.Error("expected failure for HTTP 500")
	}
}

func TestCheckHealth_Unreachable(t *testing.T) {
	tool := NewEmbeddingTool("http://127.0.0.1:1")
	result, err := tool.Execute(context.Background(), map[string]interface{}{"action": "health"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success {
		t.Error("expected failure for unreachable service")
	}
}

// ── embedText via httptest ──────────────────────────────────────────────────

func TestEmbedText_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embed" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var req EmbeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
		}
		if req.Text == "" {
			t.Error("text should not be empty")
		}
		resp := EmbeddingResponse{
			Embedding: []float64{0.1, 0.2, 0.3},
			Dimension: 3,
			Cached:    false,
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer server.Close()

	tool := NewEmbeddingTool(server.URL)
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "embed",
		"text":   "hello world",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got: %v", result.Error)
	}
	data := result.Data.(map[string]interface{})
	if data["dimension"] != 3 {
		t.Errorf("dimension = %v", data["dimension"])
	}
}

func TestEmbedText_MissingText(t *testing.T) {
	tool := NewEmbeddingTool("")
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "embed",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success {
		t.Error("expected failure for missing text")
	}
}

func TestEmbedText_EmptyText(t *testing.T) {
	tool := NewEmbeddingTool("")
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "embed",
		"text":   "",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success {
		t.Error("expected failure for empty text")
	}
}

func TestEmbedText_DefaultPrefix(t *testing.T) {
	var capturedPrefix string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req EmbeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
		}
		capturedPrefix = req.Prefix
		resp := EmbeddingResponse{Embedding: []float64{0.1}, Dimension: 1}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer server.Close()

	tool := NewEmbeddingTool(server.URL)
	_, _ = tool.Execute(context.Background(), map[string]interface{}{
		"action": "embed",
		"text":   "test",
	})
	if capturedPrefix != "query:" {
		t.Errorf("prefix = %q, want query:", capturedPrefix)
	}
}

// ── embedBatch via httptest ─────────────────────────────────────────────────

func TestEmbedBatch_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embed_batch" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var req BatchEmbeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
		}
		if len(req.Texts) != 2 {
			t.Errorf("texts len = %d", len(req.Texts))
		}
		resp := BatchEmbeddingResponse{
			Embeddings:  [][]float64{{0.1, 0.2}, {0.3, 0.4}},
			Dimension:   2,
			CachedCount: 0,
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer server.Close()

	tool := NewEmbeddingTool(server.URL)
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "embed_batch",
		"texts":  []interface{}{"text one", "text two"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Success {
		t.Fatalf("expected success, got: %v", result.Error)
	}
	data := result.Data.(map[string]interface{})
	if data["count"] != 2 {
		t.Errorf("count = %v", data["count"])
	}
}

func TestEmbedBatch_MissingTexts(t *testing.T) {
	tool := NewEmbeddingTool("")
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "embed_batch",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success {
		t.Error("expected failure for missing texts")
	}
}

func TestEmbedBatch_EmptyTexts(t *testing.T) {
	tool := NewEmbeddingTool("")
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "embed_batch",
		"texts":  []interface{}{},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success {
		t.Error("expected failure for empty texts array")
	}
}

func TestEmbedBatch_NonStringElement(t *testing.T) {
	tool := NewEmbeddingTool("")
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"action": "embed_batch",
		"texts":  []interface{}{"ok", 42, "also ok"},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Success {
		t.Error("expected failure for non-string element in texts")
	}
}

func TestEmbedBatch_DefaultPrefix(t *testing.T) {
	var capturedPrefix string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req BatchEmbeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode: %v", err)
		}
		capturedPrefix = req.Prefix
		resp := BatchEmbeddingResponse{Embeddings: [][]float64{{0.1}}, Dimension: 1}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer server.Close()

	tool := NewEmbeddingTool(server.URL)
	_, _ = tool.Execute(context.Background(), map[string]interface{}{
		"action": "embed_batch",
		"texts":  []interface{}{"test"},
	})
	if capturedPrefix != "passage:" {
		t.Errorf("prefix = %q, want passage:", capturedPrefix)
	}
}

// ── newEmbeddingHTTPClient redirect limit ───────────────────────────────────

func TestNewEmbeddingHTTPClient_RedirectLimit(t *testing.T) {
	client := newEmbeddingHTTPClient(0)
	if client == nil {
		t.Fatal("newEmbeddingHTTPClient returned nil")
	}
	if client.CheckRedirect == nil {
		t.Error("CheckRedirect should be set")
	}
}

// ── Capabilities ────────────────────────────────────────────────────────────

func TestEmbeddingTool_Capabilities(t *testing.T) {
	tool := NewEmbeddingTool("")
	caps := tool.Capabilities()
	if len(caps) == 0 {
		t.Error("capabilities should not be empty")
	}
}
