package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestEmbedBatchRejectsTruncatedResponse locks S-13: the batch service may
// legally return fewer embeddings than inputs (per-item failures are dropped
// upstream). Indexing by position used to panic mid-assignment, leaving the
// caller with a crash instead of an actionable error; the count is verified
// up front now.
func TestEmbedBatchRejectsTruncatedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embed_batch" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// One embedding for two inputs: the truncation case.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embeddings": [][]float64{{0.1}},
			"dimension":  1,
		})
	}))
	defer srv.Close()

	c := NewEmbeddingClient(srv.URL, "test-model", nil, 2*time.Second)
	result, err := c.EmbedBatch(context.Background(), []string{"first", "second"})
	if err == nil {
		t.Fatal("EmbedBatch must reject a truncated response, got nil error")
	}
	if !strings.Contains(err.Error(), "1 embeddings for 2") {
		t.Fatalf("error must report the mismatch counts, got: %v", err)
	}
	if result != nil {
		t.Fatalf("no partial result on truncated response, got %v", result)
	}
}

// TestEmbedBatchAssignsByPosition locks the happy path the count check
// guards: batch embeddings must land on the right input positions.
func TestEmbedBatchAssignsByPosition(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embeddings": [][]float64{{0.1}, {0.2}},
			"dimension":  1,
		})
	}))
	defer srv.Close()

	c := NewEmbeddingClient(srv.URL, "test-model", nil, 2*time.Second)
	got, err := c.EmbedBatch(context.Background(), []string{"first", "second"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(got) != 2 || got[0][0] != 0.1 || got[1][0] != 0.2 {
		t.Fatalf("embeddings must map positionally, got %v", got)
	}
}
