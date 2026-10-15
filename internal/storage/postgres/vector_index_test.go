package postgres

import (
	stderrors "errors"
	"strings"
	"testing"

	"github.com/Timwood0x10/ares/internal/errors"
)

// TestIVFFlatListsForRows locks the adaptivity rule that replaces the frozen
// lists=100 (defect R1): a value correct for a few thousand rows is a recall
// ceiling for a few hundred thousand, because ivfflat only probes a few clusters
// per search.
func TestIVFFlatListsForRows(t *testing.T) {
	tests := []struct {
		name string
		rows int64
		want int
	}{
		{"unmeasured keeps the historical default", 0, defaultIVFFlatLists},
		{"negative is unmeasured too", -5, defaultIVFFlatLists},
		// sqrt(rows) bounds one probe's scan share on small tables: rows/1000 alone
		// would give a 500-row table a single list and scan everything.
		{"tiny table is bounded by sqrt", 500, 22},
		{"small table is bounded by sqrt", 10_000, 100},
		{"mid table is bounded by sqrt", 100_000, 316},
		{"grown table is bounded by sqrt", 500_000, 707},
		{"large table takes the larger of the two rules", 2_000_000, 2000},
		{"huge table is clamped", 4_000_000_000, maxIVFFlatLists},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ivfflatListsForRows(tc.rows); got != tc.want {
				t.Errorf("ivfflatListsForRows(%d) = %d, want %d", tc.rows, got, tc.want)
			}
		})
	}
}

// TestVectorIndexDDL_BuildsBothMethods pins the two shapes the one builder must
// produce, including the HNSW option the defect named as missing.
func TestVectorIndexDDL_BuildsBothMethods(t *testing.T) {
	ivf, err := vectorIndexDDL("knowledge_chunks_1024", VectorIndexIVFFlat, 500_000)
	if err != nil {
		t.Fatalf("ivfflat ddl: %v", err)
	}
	// Identifiers are quoted (the rebuild path passes catalog names), and the lists
	// value follows the bounded rule: 707 for 500k rows.
	for _, want := range []string{`CREATE INDEX IF NOT EXISTS "knowledge_chunks_1024_embedding_idx"`,
		`ON "knowledge_chunks_1024"`, "USING ivfflat", "vector_cosine_ops", "WITH (lists = 707)"} {
		if !strings.Contains(ivf, want) {
			t.Errorf("ivfflat ddl missing %q:\n%s", want, ivf)
		}
	}

	hnsw, err := vectorIndexDDL("knowledge_chunks_1024", VectorIndexHNSW, 500_000)
	if err != nil {
		t.Fatalf("hnsw ddl: %v", err)
	}
	for _, want := range []string{"USING hnsw", "vector_cosine_ops",
		"m = " + itoa(hnswDefaultM), "ef_construction = " + itoa(hnswDefaultEFConstruction)} {
		if !strings.Contains(hnsw, want) {
			t.Errorf("hnsw ddl missing %q:\n%s", want, hnsw)
		}
	}
	// lists is an ivfflat-only parameter: emitting it for HNSW would fail at
	// execution time, which is how a "supported option" turns into a broken one.
	if strings.Contains(hnsw, "lists") {
		t.Errorf("hnsw ddl must not carry lists:\n%s", hnsw)
	}
}

// TestVectorIndexSpec_PreservesNameAndPredicate is the rebuild contract, and it is
// a regression guard: the first version of the review rebuilt `<table>_embedding_idx`
// while the migrations create `idx_<table>_embedding`, so the lookup never matched
// and the whole capability was dead. A rebuild must reuse the index it found AND
// its partial predicate.
func TestVectorIndexSpec_PreservesNameAndPredicate(t *testing.T) {
	ddl, err := vectorIndexSpec{
		Table:     "knowledge_chunks_1024",
		IndexName: "idx_knowledge_1024_embedding",
		Method:    VectorIndexIVFFlat,
		Rows:      500_000,
		Predicate: "embedding IS NOT NULL",
	}.ddl()
	if err != nil {
		t.Fatalf("ddl: %v", err)
	}
	if !strings.Contains(ddl, `"idx_knowledge_1024_embedding"`) {
		t.Errorf("rebuild must reuse the found index name, got:\n%s", ddl)
	}
	if strings.Contains(ddl, "knowledge_chunks_1024_embedding_idx") {
		t.Errorf("rebuild must not invent the builders' name, got:\n%s", ddl)
	}
	if !strings.Contains(ddl, "WHERE embedding IS NOT NULL") {
		t.Errorf("rebuild must preserve the partial predicate, got:\n%s", ddl)
	}

	// An empty name still means the builders' convention, and no predicate means a
	// full index.
	plain, err := vectorIndexSpec{Table: "t", Method: VectorIndexIVFFlat, Rows: 0}.ddl()
	if err != nil {
		t.Fatalf("ddl: %v", err)
	}
	if !strings.Contains(plain, `"t_embedding_idx"`) || strings.Contains(plain, "WHERE") {
		t.Errorf("default spec = %s, want the conventional name and no predicate", plain)
	}
}

// TestTableRowEstimate_PrefersTheCatalogEstimate locks the startup-cost contract:
// the review runs at EVERY boot, so it must read Postgres's own estimate rather than
// counting a possibly huge table. The 2x outgrowth trigger is what makes an estimate
// sufficient.
func TestTableRowEstimate_PrefersTheCatalogEstimate(t *testing.T) {
	if !strings.Contains(tableRowEstimateSQL, "reltuples") {
		t.Errorf("the row estimate must come from reltuples, got:\n%s", tableRowEstimateSQL)
	}
	if strings.Contains(tableRowEstimateSQL, "count(*)") {
		t.Errorf("the estimate query must not count rows, got:\n%s", tableRowEstimateSQL)
	}
}

// TestEmbeddingIndexSQL_LooksUpByColumnNotName locks the fix for the lookup itself:
// the index is found by (table, embedding column) from the catalog, never by a
// guessed name, and the query returns the two things a rebuild needs (its lists
// parameters and its partial predicate).
func TestEmbeddingIndexSQL_LooksUpByColumnNotName(t *testing.T) {
	for _, want := range []string{
		"a.attname = 'embedding'", // found by column
		"tc.relname = $1",         // scoped to the table
		"reloptions",              // carries lists
		"indpred",                 // carries the partial predicate
	} {
		if !strings.Contains(embeddingIndexSQL, want) {
			t.Errorf("embeddingIndexSQL missing %q:\n%s", want, embeddingIndexSQL)
		}
	}
	if strings.Contains(embeddingIndexSQL, "_embedding_idx") {
		t.Errorf("the lookup must not assume a name convention:\n%s", embeddingIndexSQL)
	}
}

func TestVectorIndexDDL_RejectsUnknownMethod(t *testing.T) {
	if _, err := vectorIndexDDL("t", "flat", 0); !stderrors.Is(err, errors.ErrInvalidArgument) {
		t.Fatalf("unknown method err = %v, want ErrInvalidArgument", err)
	}
	if err := validateVectorIndexMethod("hnsw"); err != nil {
		t.Errorf("hnsw must be a valid method: %v", err)
	}
}

// TestSearchProbesStatement pins the no-rebuild recall lever.
func TestSearchProbesStatement(t *testing.T) {
	if got := searchProbesStatement(0); got != "" {
		t.Errorf("unset probes must emit nothing, got %q", got)
	}
	if got := searchProbesStatement(-1); got != "" {
		t.Errorf("negative probes must emit nothing, got %q", got)
	}
	got := searchProbesStatement(10)
	if !strings.Contains(got, "SET LOCAL ivfflat.probes = 10") {
		t.Errorf("probes statement = %q, want a SET LOCAL for the current transaction", got)
	}
}

func TestParseIVFFlatLists(t *testing.T) {
	tests := []struct {
		name     string
		options  string
		wantList int
		wantOK   bool
	}{
		{"plain", "lists=100", 100, true},
		{"among others", "lists=500,foo=bar", 500, true},
		{"hnsw has none", "m=16,ef_construction=64", 0, false},
		{"empty", "", 0, false},
		{"garbage value", "lists=abc", 0, false},
		{"non-positive", "lists=0", 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lists, ok := parseIVFFlatLists(tc.options)
			if lists != tc.wantList || ok != tc.wantOK {
				t.Errorf("parseIVFFlatLists(%q) = (%d,%v), want (%d,%v)", tc.options, lists, ok, tc.wantList, tc.wantOK)
			}
		})
	}
}

// TestShouldRebuildIVFFlat pins the 2x margin: rebuilding is a blocking DROP +
// CREATE, so it must trigger on a real outgrowth — and never on an unknown shape,
// where "current" is not zero but unknown.
func TestShouldRebuildIVFFlat(t *testing.T) {
	tests := []struct {
		name         string
		currentLists int
		rows         int64
		want         bool
	}{
		{"index still fits the data", 100, 30_000, false},
		{"data outgrew the index", 100, 100_000, true},
		{"just under the margin stays put", 100, 40_000, false},
		{"unknown shape is never rebuilt blind", 0, 500_000, false},
		{"plenty of lists already", 5000, 500_000, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldRebuildIVFFlat(tc.currentLists, tc.rows); got != tc.want {
				t.Errorf("shouldRebuildIVFFlat(%d, %d) = %v, want %v", tc.currentLists, tc.rows, got, tc.want)
			}
		})
	}
}

// TestVectorIndexConfigFromEmbedding pins the nil-safe resolution: an unset
// config must mean "ivfflat, server default probes", never an empty method that
// would fail every index build.
func TestVectorIndexConfigFromEmbedding(t *testing.T) {
	method, probes := vectorIndexConfigFromEmbedding(nil)
	if method != VectorIndexIVFFlat || probes != 0 {
		t.Errorf("nil config = (%q,%d), want (ivfflat,0)", method, probes)
	}

	method, probes = vectorIndexConfigFromEmbedding(&EmbeddingConfig{SearchProbes: 25})
	if method != VectorIndexIVFFlat || probes != 25 {
		t.Errorf("partial config = (%q,%d), want (ivfflat,25)", method, probes)
	}
}

// TestEmbeddingConfigValidateRejectsBadIndexSettings keeps a misconfigured index
// from being discovered at index-build time in production.
func TestEmbeddingConfigValidateRejectsBadIndexSettings(t *testing.T) {
	cfg := DefaultEmbeddingConfig()
	cfg.IndexMethod = "bogus"
	if err := cfg.Validate(); err == nil {
		t.Error("an unknown index method must be rejected by Validate")
	}

	cfg = DefaultEmbeddingConfig()
	cfg.SearchProbes = -1
	if err := cfg.Validate(); err == nil {
		t.Error("a negative probe count must be rejected by Validate")
	}

	cfg = DefaultEmbeddingConfig()
	if err := cfg.Validate(); err != nil {
		t.Errorf("default config must validate: %v", err)
	}
	if cfg.IndexMethod != VectorIndexIVFFlat {
		t.Errorf("Validate must fill the default method, got %q", cfg.IndexMethod)
	}
}

// itoa avoids importing strconv into a test that only renders small integers.
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	digits := ""
	for v > 0 {
		digits = string(rune('0'+v%10)) + digits
		v /= 10
	}
	return digits
}
