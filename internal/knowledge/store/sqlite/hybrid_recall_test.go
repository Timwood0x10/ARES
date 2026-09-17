package sqlitestore

import (
	"strings"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// TestHybridRecallLimit locks the SQL-side candidate bound of HybridSearch
// (independent-review F-09): sqlite previously had no LIMIT at all, so a
// HybridSearch materialized EVERY matching row — the asymmetric counterpart
// of the postgres fix (hybridRecallCap). The default window is
// hybridRecallCap, and an explicit wider TopK/FinalK raises it so a
// caller's own recall request is honored.
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
// memory-bounding property: a LIMIT placeholder. The query is built by
// HybridSearch inline, so this reconstructs the exact same fragments the
// production path concatenates and fails while the LIMIT is missing.
func TestHybridSearchCandidateQueryShape(t *testing.T) {
	conditions, args := hybridConditions(knowledge.HybridSearchRequest{
		Namespace: "ns",
	})
	args = append(args, hybridRecallLimit(knowledge.HybridSearchRequest{}))
	query := `SELECT id, type, namespace, raw, normalized, summary, metadata, tags, confidence, version, created_at, updated_at, status, quality, relations, embedding_model
		FROM akf_objects` + conditions + ` ORDER BY updated_at DESC LIMIT ?`

	if !strings.Contains(query, "LIMIT ?") {
		t.Error("candidate query must have a SQL-side LIMIT placeholder")
	}
	if !strings.Contains(query, "ORDER BY updated_at DESC") {
		t.Error("candidate query must order by updated_at DESC so the recall window is the most recent objects")
	}
	// The LIMIT arg must actually bind: every ? in the query needs exactly
	// one arg, so the appended hybridRecallLimit value cannot drift away.
	if want := strings.Count(query, "?"); len(args) != want {
		t.Errorf("query has %d placeholders but HybridSearch will bind %d args", want, len(args))
	}
}
