package postgres

import (
	"context"
	"database/sql"
	stderrors "errors"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"

	"github.com/Timwood0x10/ares/internal/errors"
)

// tableRowEstimateSQL reads Postgres's own row estimate. reltuples is maintained by
// ANALYZE/VACUUM and costs nothing to read, unlike count(*).
const tableRowEstimateSQL = `
	SELECT c.reltuples::bigint
	FROM pg_class c
	JOIN pg_namespace n ON n.oid = c.relnamespace
	WHERE c.relname = $1 AND n.nspname = current_schema()
`

// countTableRows returns a table's row count, preferring Postgres's estimate and
// falling back to an exact count only when the estimate is unusable.
//
// The estimate is enough because of what it feeds: shouldRebuildIVFFlat triggers on
// a 2x outgrowth, so being off by tens of percent cannot flip the decision — whereas
// count(*) on a ten-million-row table is a full scan paid at EVERY startup, which is
// a much worse trade than a necessary rebuild happening one growth step late (normal
// maintenance refreshes the estimate).
//
// The table name is validated before interpolation because SQL identifiers cannot be
// bound as parameters — the same rule the search paths follow.
func countTableRows(ctx context.Context, pool *Pool, table string) (int64, error) {
	safe, err := validateTable(table)
	if err != nil {
		return 0, errors.Wrap(err, "count rows")
	}
	var estimate int64
	if err := pool.QueryRow(ctx, tableRowEstimateSQL, safe).Scan(&estimate); err != nil && !stderrors.Is(err, sql.ErrNoRows) {
		return 0, errors.Wrap(err, "read table row estimate")
	}
	if estimate > 0 {
		return estimate, nil
	}
	// A non-positive estimate means "never analyzed" or a freshly created table.
	// An exact count is cheap exactly there, and the decision needs a number.
	var rows int64
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+safe).Scan(&rows); err != nil {
		return 0, errors.Wrap(err, "count table rows")
	}
	return rows, nil
}

// Index methods pgvector supports for the cosine operator the search paths use.
const (
	// VectorIndexIVFFlat is the default: a partitioned index whose recall depends
	// on lists (build time) and probes (search time).
	VectorIndexIVFFlat = "ivfflat"
	// VectorIndexHNSW is the graph index: higher recall at any data size, paid
	// for in memory and build time. It takes no lists; its knobs are m and
	// ef_construction (the defaults below) and, at search time, hnsw.ef_search.
	VectorIndexHNSW = "hnsw"
)

const (
	// defaultIVFFlatLists is the lists value used when nothing is known about the
	// table (a fresh table, or a caller that did not measure one). It is the
	// historical value, kept so an unconfigured deployment builds exactly the
	// index it built before.
	defaultIVFFlatLists = 100
	// maxIVFFlatLists bounds the auto-derived value: pgvector accepts up to
	// 32768, and a larger number costs build time for no recall benefit.
	maxIVFFlatLists = 32768
	// ivfflatAutoListsRowsPerList is the documented rule of thumb for tables up
	// to a million rows: lists ≈ rows / 1000.
	ivfflatAutoListsRowsPerList = 1000
	// hnswDefaultM and hnswDefaultEFConstruction are pgvector's own defaults,
	// named here so the DDL below reads as a decision rather than a magic number.
	hnswDefaultM              = 16
	hnswDefaultEFConstruction = 64
)

// ivfflatListsForRows derives the ivfflat `lists` parameter from the table's row
// count.
//
// This is the fix for the silent ceiling (defect R1 / P1-3): a fixed lists=100 is
// correct for a few thousand rows and wrong for a few hundred thousand — ivfflat
// probes only `probes` clusters per search, so when rows are spread over far more
// clusters than lists, genuinely relevant rows sit in clusters nobody looks at.
// The recall loss is silent: the query succeeds and returns plausible neighbours
// that are not the nearest ones.
//
// Two rules, and the LARGER wins, because lists alone decides the share of the
// table one probe scans (probes/lists):
//
//   - pgvector's rows/1000, which assumes probes are tuned upward with the data;
//   - sqrt(rows), the floor that keeps a single probe — the default probes is 1 —
//     from reading a large share of the table.
//
// Together they bound one probe at roughly sqrt(rows) rows: ~22 rows for a 500-row
// table, ~700 for 500k, ~2000 for 2M. The pre-fix fixed 100 scanned 1% regardless,
// i.e. 5,000 rows per probe on a 500k-row table, with none of the recall that
// fewer-but-larger clusters would have bought. Using rows/1000 alone would have
// gone the other way on small tables (a 500-row table would get 1 list and scan
// everything).
//
// rows <= 0 means "not measured": the historical default is kept, because guessing
// small from an unmeasured count would build a one-cluster index on a large table
// (the same defect, mirrored).
func ivfflatListsForRows(rows int64) int {
	if rows <= 0 {
		return defaultIVFFlatLists
	}
	byRatio := rows / ivfflatAutoListsRowsPerList
	bySqrt := int64(math.Sqrt(float64(rows)))
	lists := byRatio
	if bySqrt > lists {
		lists = bySqrt
	}
	if lists < 1 {
		lists = 1
	}
	if lists > maxIVFFlatLists {
		lists = maxIVFFlatLists
	}
	return int(lists)
}

// vectorIndexDDL builds the CREATE INDEX statement for a table's embedding
// column.
//
// It is the single source of the index shape on purpose: the statement used to
// be written out in three places (the migration plus two Go builders) and two of
// them omitted the lists parameter entirely, so "what index do we actually run"
// had no single answer. Callers pass the row count they measured (0 when unknown)
// so the lists value adapts to the data instead of being frozen at 100.
//
// Args:
//   - table: already-validated table name.
//   - method: VectorIndexIVFFlat or VectorIndexHNSW.
//   - rows: the table's current row count, or 0 when unknown.
//
// Returns ErrInvalidArgument for an unknown method.
func vectorIndexDDL(table, method string, rows int64) (string, error) {
	return vectorIndexSpec{Table: table, Method: method, Rows: rows}.ddl()
}

// vectorIndexSpec is everything one CREATE INDEX statement needs.
//
// The name and the predicate are carried explicitly because neither is derivable
// in the rebuild path: the migrations name their indexes idx_<table>_embedding and
// make the knowledge one PARTIAL (`WHERE embedding IS NOT NULL`), while the Go
// builders use <table>_embedding_idx with no predicate. A rebuild that guessed
// either would silently replace a partial index with a plain one — a bigger index,
// and the planner loses the proof that the search predicate is implied.
type vectorIndexSpec struct {
	// Table is the source table (already validated by the caller).
	Table string
	// IndexName is the index to create; empty means the builders' convention.
	IndexName string
	// Method is VectorIndexIVFFlat or VectorIndexHNSW.
	Method string
	// Rows is the table's row count, or 0 when unknown (see ivfflatListsForRows).
	Rows int64
	// Predicate is the optional partial-index WHERE clause, without the keyword.
	Predicate string
}

// ddl renders the statement. Identifiers are quoted: the rebuild path passes a
// name read from the catalog, and an unquoted identifier that happens to be a
// keyword or to differ in case would break the statement.
func (s vectorIndexSpec) ddl() (string, error) {
	name := s.IndexName
	if name == "" {
		name = s.Table + "_embedding_idx"
	}
	var body string
	switch s.Method {
	case VectorIndexIVFFlat:
		body = fmt.Sprintf("USING ivfflat (embedding vector_cosine_ops) WITH (lists = %d)", ivfflatListsForRows(s.Rows))
	case VectorIndexHNSW:
		body = fmt.Sprintf("USING hnsw (embedding vector_cosine_ops) WITH (m = %d, ef_construction = %d)", hnswDefaultM, hnswDefaultEFConstruction)
	default:
		return "", fmt.Errorf("unknown vector index method %q: %w", s.Method, errors.ErrInvalidArgument)
	}
	stmt := fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s %s",
		quoteIdentifier(name), quoteIdentifier(s.Table), body)
	if s.Predicate != "" {
		stmt += " WHERE " + s.Predicate
	}
	return stmt, nil
}

// Identifiers are quoted with the package's existing quoteIdentifier
// (base_repository.go), not a second copy: one rule for one job.

// searchProbesStatement renders the statement that raises the ivfflat probe
// count for the CURRENT transaction, or "" when probes are not configured.
//
// Search-time probes are the cheap half of R1: raising lists requires rebuilding
// the index, while probes can be raised per query to look at more clusters —
// recall goes up, latency with it. SET LOCAL is used rather than SET because the
// connection is pooled: a session-level setting would leak into the next tenant's
// query. Postgres ignores SET LOCAL outside a transaction block, so the caller
// must run the search in one for the lever to take effect.
func searchProbesStatement(probes int) string {
	if probes <= 0 {
		return ""
	}
	return fmt.Sprintf("SET LOCAL ivfflat.probes = %d", probes)
}

// searchEFSearchStatement renders the HNSW half of the same lever: ef_search is the
// number of candidates the graph search keeps, and ivfflat ignores it exactly as HNSW
// ignores probes.
func searchEFSearchStatement(efSearch int) string {
	if efSearch <= 0 {
		return ""
	}
	return fmt.Sprintf("SET LOCAL hnsw.ef_search = %d", efSearch)
}

// vectorSearchTuningFromEmbedding resolves the per-query tuning for a search.
//
// It is separate from vectorIndexConfigFromEmbedding (which answers "how is the index
// BUILT") because the two questions have different lifetimes: the build-time method and
// lists are fixed when the index is created, while these knobs are applied to each
// query.
func vectorSearchTuningFromEmbedding(cfg *EmbeddingConfig) VectorSearchTuning {
	if cfg == nil {
		return VectorSearchTuning{}
	}
	return VectorSearchTuning{Probes: cfg.SearchProbes, EFSearch: cfg.SearchEFSearch}
}

// embeddingIndexSQL finds a table's embedding index from the CATALOG, by
// (table, column) rather than by a guessed name.
//
// Guessing is how the first version of this review was dead on arrival: it looked
// for <table>_embedding_idx while the migrations create idx_<table>_embedding, so
// the lookup missed every time, each table was skipped, and the review the defect
// asked for never ran. The catalog also returns the index's own parameters (lists)
// and its partial predicate — both of which a rebuild must preserve.
const embeddingIndexSQL = `
	SELECT ic.relname,
	       COALESCE(am.amname, ''),
	       COALESCE(array_to_string(ic.reloptions, ','), ''),
	       COALESCE(pg_get_expr(i.indpred, i.indrelid), '')
	FROM pg_index i
	JOIN pg_class ic ON ic.oid = i.indexrelid
	JOIN pg_class tc ON tc.oid = i.indrelid
	JOIN pg_am am ON am.oid = ic.relam
	JOIN pg_namespace n ON n.oid = tc.relnamespace
	JOIN pg_attribute a ON a.attrelid = tc.oid AND a.attname = 'embedding' AND a.attnum = ANY (i.indkey)
	WHERE tc.relname = $1 AND n.nspname = current_schema()
	ORDER BY ic.relname
	LIMIT 1
`

// embeddingIndex is what the catalog reports about a table's embedding index.
type embeddingIndex struct {
	// Name is the index's actual name, which a rebuild must reuse.
	Name string
	// Method is the access method the index was built with (pg_am: "ivfflat",
	// "hnsw", or "btree" for a vector index that predates pgvector).
	Method string
	// Lists is its ivfflat lists value; HasLists is false for an index that does
	// not carry one (HNSW, or one built before lists was set), which this code
	// must not judge.
	Lists    int
	HasLists bool
	// Predicate is the index's partial WHERE clause, or "" for a full index.
	Predicate string
}

// readEmbeddingIndex reports a table's embedding index. found=false means the
// table has none yet — an absence, not an error, so a caller can skip it.
func readEmbeddingIndex(ctx context.Context, pool *Pool, table string) (embeddingIndex, bool, error) {
	safe, err := validateTable(table)
	if err != nil {
		return embeddingIndex{}, false, errors.Wrap(err, "read embedding index")
	}
	var name, method, reloptions, predicate string
	if err := pool.QueryRow(ctx, embeddingIndexSQL, safe).Scan(&name, &method, &reloptions, &predicate); err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return embeddingIndex{}, false, nil
		}
		return embeddingIndex{}, false, errors.Wrap(err, "read embedding index")
	}
	lists, hasLists := parseIVFFlatLists(reloptions)
	return embeddingIndex{Name: name, Method: method, Lists: lists, HasLists: hasLists, Predicate: predicate}, true, nil
}

// parseIVFFlatLists extracts the lists value out of a reloptions string.
// ok=false means the index is not an ivfflat index (or predates any lists
// setting), which the caller must treat as "unknown", never as "zero".
func parseIVFFlatLists(reloptions string) (int, bool) {
	for _, opt := range strings.Split(reloptions, ",") {
		key, value, found := strings.Cut(strings.TrimSpace(opt), "=")
		if !found || strings.TrimSpace(key) != "lists" {
			continue
		}
		lists, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || lists <= 0 {
			return 0, false
		}
		return lists, true
	}
	return 0, false
}

// shouldRebuildIVFFlat reports whether a table's row count has outgrown its
// index's lists badly enough to be worth rebuilding.
//
// The 2x margin is deliberate: rebuilding is a blocking DROP + CREATE, and the
// recall curve is flat until lists is far below the rule of thumb. Rebuilding on
// every small growth would pay a real cost to fix nothing, while never
// rebuilding — the pre-fix behaviour — leaves a recall ceiling nobody can see.
func shouldRebuildIVFFlat(currentLists int, rows int64) bool {
	if currentLists <= 0 {
		return false // unknown shape: do not rebuild what we cannot compare
	}
	return ivfflatListsForRows(rows) > currentLists*2
}

// AdaptVectorIndexScale reviews the ivfflat index of each known hot table and
// rebuilds it when the table has outgrown its lists (defect R1 / P1-3).
//
// It exists because CREATE INDEX IF NOT EXISTS never revisits a decision: the
// index built when the table was empty keeps its lists forever, so recall
// degrades silently as the data grows. This is the one place that looks again.
//
// Rebuilding is a blocking DROP + CREATE — deliberately not CONCURRENTLY, which
// cannot run inside the migration's statement execution and would fail rather
// than degrade. The 2x trigger keeps it rare (a one-time event each time the data
// roughly doubles in order of magnitude), and the alternative is an index that
// quietly returns the wrong neighbours.
//
// Best-effort per table: a table that does not exist yet, or whose index shape
// cannot be read, is skipped rather than failing startup.
func AdaptVectorIndexScale(ctx context.Context, pool *Pool, tables []string, review VectorIndexReview, logger *slog.Logger) {
	if pool == nil {
		return
	}
	for _, table := range tables {
		if err := adaptVectorIndexScaleForTable(ctx, pool, table, review, logger); err != nil {
			logger.WarnContext(ctx, "vector index scale review failed",
				"table", table, "error", err)
		}
	}
}

// shouldRebuildIndexMethod reports whether an index built with haveMethod differs from
// the configured want.
//
// An EMPTY want means "not judged", not "no method": a caller that does not know the
// deployment's configuration (the migration command) must not rebuild anything, and a
// missing catalog entry must not read as a mismatch.
func shouldRebuildIndexMethod(haveMethod, want string) bool {
	return want != "" && haveMethod != want
}

// VectorIndexReview carries what a review needs beyond the catalogs.
type VectorIndexReview struct {
	// ExpectedMethod is the access method the deployment is CONFIGURED for
	// (EmbeddingConfig.IndexMethod).
	//
	// Empty means "unknown, do not judge the method": a caller that does not know the
	// configuration (the migration command) must neither rebuild an index to a default
	// nobody asked for nor raise an alarm about a difference it cannot explain.
	ExpectedMethod string
}

// adaptVectorIndexScaleForTable reviews one table, returning any database error.
func adaptVectorIndexScaleForTable(ctx context.Context, pool *Pool, table string, review VectorIndexReview, logger *slog.Logger) error {
	idx, found, err := readEmbeddingIndex(ctx, pool, table)
	if err != nil {
		return err
	}
	if !found {
		return nil // no embedding index yet: nothing to review
	}

	// The METHOD is reviewed first, because an index built with a different access
	// method than the deployment is configured for is not a tuning question — it is the
	// wrong index, and the lists question below would be meaningless.
	if shouldRebuildIndexMethod(idx.Method, review.ExpectedMethod) {
		return rebuildVectorIndexMethod(ctx, pool, table, idx, review.ExpectedMethod, logger)
	}
	if !idx.HasLists {
		// Not a lists-based index (HNSW, or one built before lists was set): its shape
		// is not this code's to judge.
		return nil
	}
	rows, err := countTableRows(ctx, pool, table)
	if err != nil {
		return err
	}
	if !shouldRebuildIVFFlat(idx.Lists, rows) {
		return nil
	}
	want := ivfflatListsForRows(rows)
	logger.WarnContext(ctx, "rebuilding vector index: the table outgrew its lists",
		"table", table, "index", idx.Name, "rows", rows,
		"current_lists", idx.Lists, "new_lists", want, "partial", idx.Predicate != "")
	if _, err := pool.Exec(ctx, "DROP INDEX IF EXISTS "+quoteIdentifier(idx.Name)); err != nil {
		return errors.Wrap(err, "drop index")
	}
	// The rebuild reuses the index's OWN name and predicate: replacing a partial
	// index with a full one would grow it and drop the planner's predicate proof.
	ddl, err := vectorIndexSpec{
		Table:     table,
		IndexName: idx.Name,
		Method:    VectorIndexIVFFlat,
		Rows:      rows,
		Predicate: idx.Predicate,
	}.ddl()
	if err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, ddl); err != nil {
		return errors.Wrap(err, "rebuild index")
	}
	logger.InfoContext(ctx, "vector index rebuilt for scale",
		"table", table, "index", idx.Name, "lists", want, "partial", idx.Predicate != "")
	return nil
}

// rebuildVectorIndexMethod replaces an index built with the wrong access method.
//
// It is ONE-TIME and LOUD rather than silent auto-tuning. One-time, because after the
// rebuild the catalog matches the configuration and the next start is a no-op — so
// "each boot rolls the dice" cannot happen. Loud, because this is a data-plane
// decision: the DROP + CREATE is BLOCKING, and on a large table it is exactly the kind
// of operation an operator wants to have happened in a maintenance window. The
// alternative the defect describes — quietly serving from the wrong index — is worse.
//
// The index keeps its own name and partial predicate, so nothing downstream has to
// learn a new name and the planner keeps its proof.
func rebuildVectorIndexMethod(ctx context.Context, pool *Pool, table string, idx embeddingIndex, method string, logger *slog.Logger) error {
	rows, err := countTableRows(ctx, pool, table)
	if err != nil {
		return err
	}
	logger.WarnContext(ctx, "vector index uses a different method than configured: rebuilding (blocking)",
		"table", table, "index", idx.Name, "index_method", idx.Method,
		"configured_method", method, "rows", rows, "partial", idx.Predicate != "")
	if _, err := pool.Exec(ctx, "DROP INDEX IF EXISTS "+quoteIdentifier(idx.Name)); err != nil {
		return errors.Wrap(err, "drop index for method change")
	}
	ddl, err := vectorIndexSpec{
		Table:     table,
		IndexName: idx.Name,
		Method:    method,
		Rows:      rows,
		Predicate: idx.Predicate,
	}.ddl()
	if err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, ddl); err != nil {
		return errors.Wrap(err, "rebuild index with the configured method")
	}
	logger.InfoContext(ctx, "vector index method applied",
		"table", table, "index", idx.Name, "method", method)
	return nil
}

// validateVectorIndexMethod reports whether method is one this package can build.
func validateVectorIndexMethod(method string) error {
	if method != VectorIndexIVFFlat && method != VectorIndexHNSW {
		return fmt.Errorf("unknown vector index method %q: %w", method, errors.ErrInvalidArgument)
	}
	return nil
}

// vectorIndexConfigFromEmbedding resolves the effective index settings, filling
// defaults so a nil or partially-set config never produces an index nobody
// intended.
func vectorIndexConfigFromEmbedding(cfg *EmbeddingConfig) (method string, probes int) {
	if cfg == nil {
		return VectorIndexIVFFlat, 0
	}
	method = strings.TrimSpace(cfg.IndexMethod)
	if method == "" {
		method = VectorIndexIVFFlat
	}
	return method, cfg.SearchProbes
}
