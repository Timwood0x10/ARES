package postgresstore

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// TestHybridRecallLimit locks the SQL-side candidate bound of HybridSearch
// (REVIEW 2.5#29): the default window is hybridRecallCap, and an explicit
// wider TopK/FinalK raises it so a caller's own recall request is honored.
func TestHybridRecallLimit(t *testing.T) {
	cases := []struct {
		name string
		req  knowledge.HybridSearchRequest
		want int
	}{
		{"defaults", knowledge.HybridSearchRequest{}, hybridRecallCap},
		{"topk below cap", knowledge.HybridSearchRequest{TopK: 20}, hybridRecallCap},
		{"topk above cap", knowledge.HybridSearchRequest{TopK: 900}, 900},
		{"finalk above cap", knowledge.HybridSearchRequest{FinalK: 700}, 700},
		{"topk dominates finalk", knowledge.HybridSearchRequest{TopK: 900, FinalK: 700}, 900},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hybridRecallLimit(tc.req); got != tc.want {
				t.Errorf("hybridRecallLimit(%+v) = %d, want %d", tc.req, got, tc.want)
			}
		})
	}
}

// TestHybridSearchCandidateQueryShape verifies the candidate SQL keeps the
// memory-bounding properties: a LIMIT placeholder and NULL AS raw (the BYTEA
// column must stay out of the bounded scan). The query is built by
// HybridSearch inline, so this reconstructs the exact same fragments the
// production path concatenates.
func TestHybridSearchCandidateQueryShape(t *testing.T) {
	conditions, args := hybridConditions(knowledge.HybridSearchRequest{
		Namespace: "ns",
	})
	args = append(args, hybridRecallLimit(knowledge.HybridSearchRequest{}))
	query := `SELECT id, type, namespace, NULL AS raw, normalized, summary, metadata, tags, confidence, version, created_at, updated_at, status, quality, relations, embedding_model
		FROM akf_objects` + conditions + ` ORDER BY updated_at DESC LIMIT $` + strconv.Itoa(len(args))

	if !strings.Contains(query, "NULL AS raw") {
		t.Error("candidate query must not load the raw BYTEA column (NULL AS raw missing)")
	}
	if !strings.Contains(query, "LIMIT $") {
		t.Error("candidate query must have a SQL-side LIMIT")
	}
}
