package postgres

import "testing"

// TestShouldRebuildIndexMethod pins the decision that drives the one-time, blocking
// index rebuild — and, more importantly, the case where there is NO decision to make:
// an unknown expectation must never trigger a rebuild of somebody's index.
func TestShouldRebuildIndexMethod(t *testing.T) {
	tests := []struct {
		name        string
		haveMethod  string
		want        string
		wantRebuild bool
	}{
		{"configured hnsw but the index is ivfflat", VectorIndexIVFFlat, VectorIndexHNSW, true},
		{"configured ivfflat but the index is hnsw", VectorIndexHNSW, VectorIndexIVFFlat, true},
		{"a pre-pgvector btree index is rebuilt too", "btree", VectorIndexHNSW, true},
		{"matching methods are left alone", VectorIndexHNSW, VectorIndexHNSW, false},
		// The migration command passes no expectation: it must not rebuild.
		{"an unknown expectation is not judged", VectorIndexIVFFlat, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldRebuildIndexMethod(tc.haveMethod, tc.want); got != tc.wantRebuild {
				t.Errorf("shouldRebuildIndexMethod(%q, %q) = %v, want %v",
					tc.haveMethod, tc.want, got, tc.wantRebuild)
			}
		})
	}
}

// TestSearchEFSearchStatement pins the HNSW half of the query tuning: it is the same
// lever as ivfflat.probes for the other index method, and a non-positive value must
// emit nothing so an unconfigured deployment opens no transaction.
func TestSearchEFSearchStatement(t *testing.T) {
	if got := searchEFSearchStatement(0); got != "" {
		t.Errorf("unset ef_search must emit nothing, got %q", got)
	}
	if got := searchEFSearchStatement(-1); got != "" {
		t.Errorf("negative ef_search must emit nothing, got %q", got)
	}
	if got := searchEFSearchStatement(40); got != "SET LOCAL hnsw.ef_search = 40" {
		t.Errorf("ef_search statement = %q", got)
	}
}

// TestVectorSearchTuning_StatementsRendersWhatIsConfigured: both knobs belong to one
// decision, so a tuning carries whichever are set — and none at all when unconfigured,
// which is what keeps the no-op path free of transactions.
func TestVectorSearchTuning_StatementsRendersWhatIsConfigured(t *testing.T) {
	if got := (VectorSearchTuning{}).statements(); len(got) != 0 {
		t.Errorf("an empty tuning must render nothing, got %v", got)
	}
	probesOnly := VectorSearchTuning{Probes: 10}
	if got := probesOnly.statements(); len(got) != 1 || got[0] != "SET LOCAL ivfflat.probes = 10" {
		t.Errorf("probes-only tuning = %v", got)
	}
	both := VectorSearchTuning{Probes: 10, EFSearch: 40}
	got := both.statements()
	if len(got) != 2 {
		t.Fatalf("both knobs must be rendered, got %v", got)
	}
	// Stable order: the transaction applies them in the same sequence every time, so a
	// failure log names the same statement for the same configuration.
	if got[0] != "SET LOCAL ivfflat.probes = 10" || got[1] != "SET LOCAL hnsw.ef_search = 40" {
		t.Errorf("statements are not in a stable order: %v", got)
	}
}

// TestVectorSearchTuningFromEmbedding resolves the config, nil-safely.
func TestVectorSearchTuningFromEmbedding(t *testing.T) {
	if got := vectorSearchTuningFromEmbedding(nil); got != (VectorSearchTuning{}) {
		t.Errorf("nil config = %+v, want an empty tuning", got)
	}
	cfg := &EmbeddingConfig{SearchProbes: 12, SearchEFSearch: 64}
	got := vectorSearchTuningFromEmbedding(cfg)
	if got.Probes != 12 || got.EFSearch != 64 {
		t.Errorf("tuning = %+v, want both knobs carried", got)
	}
}
