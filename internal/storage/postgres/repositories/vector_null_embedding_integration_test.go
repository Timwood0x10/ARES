//go:build integration
// +build integration

// Package repositories — integration coverage for the NULL-embedding contract
// of vector search, verified against a real PostgreSQL + pgvector instance.
//
// These tests exist because the DDL-string assertions in
// internal/storage/postgres/vector_index_predicate_test.go can only prove the
// migration *says* the right thing. The claims that actually needed a live
// planner are:
//
//  1. NULL-embedding rows sort LAST, so adding `embedding IS NOT NULL` never
//     increases the number of rows returned. (The earlier analysis claimed the
//     opposite — that NULL rows consumed LIMIT slots — which is wrong.)
//  2. Those rows, when not filtered, reach the scan loop with a NULL similarity
//     and get silently dropped.
//  3. A partial ivfflat index is only usable when the query carries the
//     matching predicate — this is the concrete meaning of "the planner gains a
//     usable filter".
package repositories

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/storage/postgres"
	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
)

// vectorITDatabase is the throwaway database these tests migrate from scratch.
// A dedicated database (rather than the shared test DB) is required because the
// point is to observe what a *fresh* MigrateStorage produces: `CREATE INDEX IF
// NOT EXISTS` will not replace a pre-existing non-partial index.
const vectorITDatabase = "ares_vecidx_it"

// vectorITEnv reads a connection setting, falling back to the local
// docker-compose pgvector container.
func vectorITEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// newVectorITConfig builds a Config for the given database name.
func newVectorITConfig(t *testing.T, database string) *postgres.Config {
	t.Helper()

	port := 5433
	if _, err := fmt.Sscanf(vectorITEnv("ARES_IT_PG_PORT", "5433"), "%d", &port); err != nil {
		t.Fatalf("invalid ARES_IT_PG_PORT: %v", err)
	}

	return &postgres.Config{
		Host:            vectorITEnv("ARES_IT_PG_HOST", "localhost"),
		Port:            port,
		User:            vectorITEnv("ARES_IT_PG_USER", "postgres"),
		Password:        vectorITEnv("ARES_IT_PG_PASSWORD", "postgres"),
		Database:        database,
		SSLMode:         "disable",
		MaxOpenConns:    4,
		MaxIdleConns:    2,
		ConnMaxLifetime: time.Minute,
		ConnMaxIdleTime: 30 * time.Second,
		QueryTimeout:    30 * time.Second,
	}
}

// setupVectorITDB drops and recreates vectorITDatabase, enables pgvector, runs
// MigrateStorage, and returns a pool for it. Skips (never fails) when no
// PostgreSQL is reachable, so `go test -tags=integration ./...` stays usable
// without Docker.
func setupVectorITDB(t *testing.T) *postgres.Pool {
	t.Helper()

	adminCfg := newVectorITConfig(t, vectorITEnv("ARES_IT_PG_ADMIN_DB", "postgres"))
	admin, err := sql.Open("pgx", adminCfg.DSN())
	if err != nil {
		t.Skipf("skipping: cannot open admin connection: %v", err)
		return nil
	}
	defer func() { _ = admin.Close() }()

	ctx := context.Background()
	if err := admin.PingContext(ctx); err != nil {
		t.Skipf("skipping: no PostgreSQL at %s:%d: %v", adminCfg.Host, adminCfg.Port, err)
		return nil
	}

	// DROP/CREATE DATABASE cannot run inside a transaction, hence plain Exec.
	if _, err := admin.ExecContext(ctx, "DROP DATABASE IF EXISTS "+vectorITDatabase); err != nil {
		t.Skipf("skipping: cannot drop %s: %v", vectorITDatabase, err)
		return nil
	}
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+vectorITDatabase); err != nil {
		t.Skipf("skipping: cannot create %s: %v", vectorITDatabase, err)
		return nil
	}

	// Force sequential scans by default for this database. The semantics
	// subtests need exact, complete results, and an ivfflat index scan is
	// approximate (ivfflat.probes defaults to 1), which would make row counts
	// non-deterministic. The planner subtests re-enable index scans per session.
	if _, err := admin.ExecContext(ctx,
		"ALTER DATABASE "+vectorITDatabase+" SET enable_indexscan = off"); err != nil {
		t.Fatalf("cannot pin enable_indexscan for %s: %v", vectorITDatabase, err)
	}

	cfg := newVectorITConfig(t, vectorITDatabase)
	bootstrap, err := sql.Open("pgx", cfg.DSN())
	require.NoError(t, err, "open bootstrap connection")
	_, err = bootstrap.ExecContext(ctx, "CREATE EXTENSION IF NOT EXISTS vector")
	require.NoError(t, err, "pgvector extension must be installable")
	require.NoError(t, bootstrap.Close())

	pool, err := postgres.NewPool(cfg)
	require.NoError(t, err, "connect to freshly created %s", vectorITDatabase)
	require.NoError(t, postgres.MigrateStorage(ctx, pool), "MigrateStorage on a fresh database")

	t.Cleanup(func() {
		_ = pool.Close()
		// Best-effort teardown: a leftover database would only affect the next
		// run's DROP, which tolerates it.
		cleanupAdmin, err := sql.Open("pgx", adminCfg.DSN())
		if err != nil {
			return
		}
		defer func() { _ = cleanupAdmin.Close() }()
		_, _ = cleanupAdmin.ExecContext(context.Background(),
			"DROP DATABASE IF EXISTS "+vectorITDatabase+" WITH (FORCE)")
	})

	return pool
}

// itVector builds a deterministic unit-ish 1024-dim vector whose direction is
// controlled by seed, so distance ordering between seeds is well defined.
func itVector(seed int) []float64 {
	v := make([]float64, 1024)
	for i := range v {
		v[i] = float64((i+seed)%97) / 97.0
	}
	return v
}

// vectorITSeedRows inserts withVec experiences carrying embeddings and nullVec
// experiences whose embedding column is SQL NULL (the state the async embedding
// worker later backfills). Returns the IDs of the rows that have vectors.
func vectorITSeedRows(t *testing.T, pool *postgres.Pool, tenantID string, withVec, nullVec int) []string {
	t.Helper()

	ctx := context.Background()
	db := pool.GetDB()
	ids := make([]string, 0, withVec)

	for i := 0; i < withVec; i++ {
		var id string
		err := db.QueryRowContext(ctx, `
			INSERT INTO `+storage_models.ExperiencesTable+`
			(tenant_id, type, input, output, embedding, embedding_model, embedding_version,
			 score, success, agent_id, metadata)
			VALUES ($1, 'distilled', $2, 'sol', $3::vector, 'e5-large', 1, 0.5, true, 'a', '{}'::jsonb)
			RETURNING id`,
			tenantID, fmt.Sprintf("with-vec-%02d", i),
			postgres.FormatVector(itVector(i+1)),
		).Scan(&id)
		require.NoError(t, err, "insert experience with embedding")
		ids = append(ids, id)
	}

	for i := 0; i < nullVec; i++ {
		_, err := db.ExecContext(ctx, `
			INSERT INTO `+storage_models.ExperiencesTable+`
			(tenant_id, type, input, output, embedding, embedding_model, embedding_version,
			 score, success, agent_id, metadata)
			VALUES ($1, 'distilled', $2, 'sol', NULL, 'e5-large', 1, 0.5, true, 'a', '{}'::jsonb)`,
			tenantID, fmt.Sprintf("null-vec-%02d", i))
		require.NoError(t, err, "insert experience awaiting embedding backfill")
	}

	return ids
}

// vectorITSeedBulk inserts n rows with 1024-dim vectors (plus nullVec
// vector-less rows) using a single server-side INSERT ... SELECT, then ANALYZEs.
//
// Bulk + ANALYZE matters for the planner tests: with only a few dozen rows the
// planner correctly prefers the tenant btree index and a sort, so an ivfflat
// plan would never appear and the test would be measuring table size rather
// than index usability.
func vectorITSeedBulk(t *testing.T, pool *postgres.Pool, table, tenantID string, n, nullVec int) {
	t.Helper()

	ctx := context.Background()
	db := pool.GetDB()

	// Vector literal is built in SQL to avoid n round trips carrying 1024
	// floats each.
	//nolint:gosec // G201: table is a package constant, tenant/counts are bound parameters.
	_, err := db.ExecContext(ctx, `
		INSERT INTO `+table+`
		(tenant_id, type, input, output, embedding, embedding_model, embedding_version,
		 score, success, agent_id, metadata)
		SELECT $1, 'distilled', 'bulk-' || i, 'sol',
		       (SELECT ('[' || string_agg((((i * 7 + j) % 97)::float / 97)::text, ',') || ']')::vector
		        FROM generate_series(1, 1024) j),
		       'e5-large', 1, 0.5, true, 'a', '{}'::jsonb
		FROM generate_series(1, $2) i`, tenantID, n)
	require.NoError(t, err, "bulk insert rows with embeddings")

	if nullVec > 0 {
		//nolint:gosec // G201: table is a package constant.
		_, err = db.ExecContext(ctx, `
			INSERT INTO `+table+`
			(tenant_id, type, input, output, embedding, embedding_model, embedding_version,
			 score, success, agent_id, metadata)
			SELECT $1, 'distilled', 'bulk-null-' || i, 'sol', NULL,
			       'e5-large', 1, 0.5, true, 'a', '{}'::jsonb
			FROM generate_series(1, $2) i`, tenantID, nullVec)
		require.NoError(t, err, "bulk insert rows awaiting backfill")
	}

	//nolint:gosec // G201: table is a package constant.
	_, err = db.ExecContext(ctx, "ANALYZE "+table)
	require.NoError(t, err, "ANALYZE so the planner uses real statistics")
}

// explainVectorOrderBy returns the EXPLAIN output for a vector-ordered query
// against table, with nullFilter ("" or "AND embedding IS NOT NULL") spliced in.
//
// Index scans are re-enabled per session because the test database pins
// enable_indexscan = off for the deterministic-semantics tests. Sequential
// scans stay ENABLED (default costs), so a chosen ivfflat plan means the
// planner actually preferred it, not that it had no alternative.
func explainVectorOrderBy(t *testing.T, pool *postgres.Pool, table, tenantID, nullFilter string) string {
	t.Helper()

	ctx := context.Background()
	conn, err := pool.GetDB().Conn(ctx)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	_, err = conn.ExecContext(ctx, "SET enable_indexscan = on")
	require.NoError(t, err)

	//nolint:gosec // G202: nullFilter is one of two test-local literals; tenant and probe are bound.
	q := `EXPLAIN (COSTS OFF) SELECT id
		FROM ` + table + `
		WHERE tenant_id = $1
		  AND (decay_at IS NULL OR decay_at > NOW())
		  ` + nullFilter + `
		ORDER BY embedding <=> $2::vector
		LIMIT 5`

	rows, err := conn.QueryContext(ctx, q, tenantID, postgres.FormatVector(itVector(1)))
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	var plan strings.Builder
	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		plan.WriteString(line)
		plan.WriteString("\n")
	}
	require.NoError(t, rows.Err())
	return plan.String()
}

// TestVectorIndex_FreshMigrationCreatesPartialIndex verifies against a real
// server that MigrateStorage produces partial ivfflat indexes matching the
// readers' `embedding IS NOT NULL` predicate. The unit test can only check the
// DDL string; this checks what PostgreSQL actually recorded, including that the
// WITH/WHERE clause order is accepted.
func TestVectorIndex_FreshMigrationCreatesPartialIndex(t *testing.T) {
	pool := setupVectorITDB(t)
	if pool == nil {
		return
	}

	ctx := context.Background()
	for _, tc := range []struct{ index, table string }{
		{"idx_experiences_1024_embedding", storage_models.ExperiencesTable},
		{"idx_knowledge_1024_embedding", storage_models.KnowledgeChunksTable},
	} {
		t.Run(tc.index, func(t *testing.T) {
			var def string
			err := pool.GetDB().QueryRowContext(ctx,
				"SELECT indexdef FROM pg_indexes WHERE indexname = $1", tc.index).Scan(&def)
			require.NoError(t, err, "index %s must exist after MigrateStorage", tc.index)

			assert.Contains(t, def, "USING ivfflat", "must stay an ivfflat index")
			assert.Contains(t, def, "WHERE (embedding IS NOT NULL)",
				"index must be partial so it matches the reader's predicate; got: %s", def)
		})
	}
}

// TestVectorSearch_NullEmbeddingsSortLastAndNeverDisplace is the direct
// refutation of the earlier wrong analysis. `ORDER BY embedding <=> $1` is
// ascending and PostgreSQL sorts NULLs last, so vector-less rows can only ever
// occupy the tail. Consequence: adding `embedding IS NOT NULL` does not raise
// the number of usable rows returned — it only stops the useless tail from
// being read and then discarded.
func TestVectorSearch_NullEmbeddingsSortLastAndNeverDisplace(t *testing.T) {
	pool := setupVectorITDB(t)
	if pool == nil {
		return
	}

	const tenantID = "it-null-embed"
	const withVec, nullVec = 12, 3
	vectorITSeedRows(t, pool, tenantID, withVec, nullVec)

	ctx := context.Background()
	probe := postgres.FormatVector(itVector(1))

	// selectIDs runs the reader's ORDER BY with or without the NULL filter and
	// returns (ids, count of NULL similarities).
	selectIDs := func(t *testing.T, filterNull bool, limit int) ([]string, int) {
		t.Helper()
		nullFilter := ""
		if filterNull {
			nullFilter = "AND embedding IS NOT NULL"
		}
		//nolint:gosec // G202: nullFilter is one of two literals above, not user input.
		q := `
			SELECT id, 1 - (embedding <=> $1::vector) AS similarity
			FROM ` + storage_models.ExperiencesTable + `
			WHERE tenant_id = $2
			  AND (decay_at IS NULL OR decay_at > NOW())
			  ` + nullFilter + `
			ORDER BY embedding <=> $1::vector
			LIMIT $3`

		rows, err := pool.GetDB().QueryContext(ctx, q, probe, tenantID, limit)
		require.NoError(t, err)
		defer func() { _ = rows.Close() }()

		var ids []string
		nullSimilarities := 0
		for rows.Next() {
			var id string
			var similarity sql.NullFloat64
			require.NoError(t, rows.Scan(&id, &similarity))
			if !similarity.Valid {
				nullSimilarities++
			}
			ids = append(ids, id)
		}
		require.NoError(t, rows.Err())
		return ids, nullSimilarities
	}

	t.Run("limit_equal_to_vector_rows_returns_identical_ranking", func(t *testing.T) {
		filtered, filteredNulls := selectIDs(t, true, withVec)
		unfiltered, unfilteredNulls := selectIDs(t, false, withVec)

		require.Len(t, filtered, withVec)
		assert.Equal(t, filtered, unfiltered,
			"the NULL filter must not change which rows win, nor their order")
		assert.Zero(t, filteredNulls)
		assert.Zero(t, unfilteredNulls,
			"NULL-embedding rows must not reach a LIMIT that the vector rows already fill")
	})

	t.Run("limit_covering_all_rows_puts_nulls_in_the_tail", func(t *testing.T) {
		all, nullSimilarities := selectIDs(t, false, withVec+nullVec)
		require.Len(t, all, withVec+nullVec)
		assert.Equal(t, nullVec, nullSimilarities,
			"vector-less rows are reachable only past the real results (NULLS LAST)")

		// The NULL-similarity rows are the LAST nullVec entries, never earlier.
		ranked, _ := selectIDs(t, true, withVec)
		assert.Equal(t, ranked, all[:withVec],
			"the leading rows are exactly the filtered result: NULLs never displace")
	})

	t.Run("filter_never_increases_row_count", func(t *testing.T) {
		for _, limit := range []int{1, 5, withVec, withVec + nullVec, withVec + nullVec + 10} {
			filtered, _ := selectIDs(t, true, limit)
			unfiltered, _ := selectIDs(t, false, limit)
			assert.LessOrEqual(t, len(filtered), len(unfiltered),
				"limit=%d: filtering can only remove tail rows, never add any", limit)
			assert.LessOrEqual(t, len(filtered), withVec,
				"limit=%d: at most the rows that actually have vectors", limit)
		}
	})
}

// TestVectorSearch_RepositoryDropsNoUsableRows exercises the shipped
// SearchByVector against real data: with the predicate in place, every row that
// has a vector comes back and no vector-less row reaches the scan loop (where a
// NULL similarity would fail Scan and be swallowed by `continue`).
func TestVectorSearch_RepositoryDropsNoUsableRows(t *testing.T) {
	pool := setupVectorITDB(t)
	if pool == nil {
		return
	}

	const tenantID = "it-repo-null-embed"
	const withVec, nullVec = 12, 3
	wantIDs := vectorITSeedRows(t, pool, tenantID, withVec, nullVec)

	repo := NewExperienceRepository(pool.GetDB())
	// Ask for more than exists so the limit cannot be what truncates the result.
	got, err := repo.SearchByVector(context.Background(), itVector(1), tenantID, withVec+nullVec+5)
	require.NoError(t, err)

	require.Len(t, got, withVec,
		"every row with a vector must be returned; vector-less rows must be filtered in SQL")
	gotIDs := make(map[string]bool, len(got))
	for _, exp := range got {
		assert.Len(t, exp.Embedding, 1024, "returned rows must carry a parsed embedding")
		assert.InDelta(t, 1.0, exp.Metadata["similarity"].(float64), 1.0,
			"similarity is 1 - cosine_distance, so it lies in [-1,1]")
		gotIDs[exp.ID] = true
	}
	for _, id := range wantIDs {
		assert.True(t, gotIDs[id], "experience %s has a vector but was dropped", id)
	}
}

// TestVectorSearch_WithoutPredicateRowsAreSilentlyDropped demonstrates the harm
// the predicate actually prevents, using the repository's own scan shape.
//
// The reader scans `embedding::text` into a plain string. For a vector-less row
// that is a SQL NULL, which fails Scan — and the loop's `continue` swallows it.
// So without the predicate the row is read, fails, and disappears with no trace
// in the result: exactly the "pending backfill looks like an empty tenant"
// symptom. The count of such failures is the thing the aggregate warn now
// reports.
func TestVectorSearch_WithoutPredicateRowsAreSilentlyDropped(t *testing.T) {
	pool := setupVectorITDB(t)
	if pool == nil {
		return
	}

	const tenantID = "it-silent-drop"
	const withVec, nullVec = 8, 4
	vectorITSeedRows(t, pool, tenantID, withVec, nullVec)

	ctx := context.Background()
	// The reader query verbatim, minus `AND embedding IS NOT NULL`.
	rows, err := pool.GetDB().QueryContext(ctx, `
		SELECT id, embedding::text, 1 - (embedding <=> $1::vector) AS similarity
		FROM `+storage_models.ExperiencesTable+`
		WHERE tenant_id = $2
		  AND (decay_at IS NULL OR decay_at > NOW())
		ORDER BY embedding <=> $1::vector
		LIMIT $3`,
		postgres.FormatVector(itVector(1)), tenantID, withVec+nullVec)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	scanned, dropped := 0, 0
	for rows.Next() {
		var id, embeddingStr string
		var similarity float64
		// Same target types as ExperienceRepository.SearchByVector.
		if err := rows.Scan(&id, &embeddingStr, &similarity); err != nil {
			dropped++
			continue
		}
		scanned++
	}
	require.NoError(t, rows.Err())

	assert.Equal(t, withVec, scanned, "only rows with vectors survive the scan")
	assert.Equal(t, nullVec, dropped,
		"vector-less rows fail Scan and would vanish via `continue` — this is the "+
			"invisible loss the SQL predicate and the aggregate warn now address")
}

// TestKnowledgeSearch_StatusCompletedButNullEmbedding pins the claim in
// KnowledgeRepository.SearchByVector's doc comment: embedding_status and the
// embedding column can disagree, because UpdateEmbeddingStatus writes the status
// alone. A row marked 'completed' with a NULL vector passes the status filter, so
// only `embedding IS NOT NULL` keeps it out of the scan loop.
func TestKnowledgeSearch_StatusCompletedButNullEmbedding(t *testing.T) {
	pool := setupVectorITDB(t)
	if pool == nil {
		return
	}

	ctx := context.Background()
	const tenantID = "it-knowledge-status"
	repo := NewKnowledgeRepository(pool.GetDB(), pool.GetDB())

	// One healthy chunk.
	good := &storage_models.KnowledgeChunk{
		TenantID:         tenantID,
		Content:          "chunk with a vector",
		Embedding:        itVector(1),
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		EmbeddingStatus:  storage_models.EmbeddingStatusCompleted,
		ContentHash:      "it-hash-good",
	}
	require.NoError(t, repo.Create(ctx, good))

	// One chunk still awaiting backfill: NULL vector, status 'pending'.
	pending := &storage_models.KnowledgeChunk{
		TenantID:         tenantID,
		Content:          "chunk awaiting backfill",
		Embedding:        nil,
		EmbeddingModel:   "e5-large",
		EmbeddingVersion: 1,
		EmbeddingStatus:  storage_models.EmbeddingStatusPending,
		ContentHash:      "it-hash-pending",
	}
	require.NoError(t, repo.Create(ctx, pending))

	// Flip its status to 'completed' WITHOUT writing a vector — the exact
	// divergence UpdateEmbeddingStatus makes possible.
	require.NoError(t, repo.UpdateEmbeddingStatus(ctx, tenantID, pending.ID,
		storage_models.EmbeddingStatusCompleted, ""))

	var statusOnlyMatches int
	require.NoError(t, pool.GetDB().QueryRowContext(ctx, `
		SELECT count(*) FROM `+storage_models.KnowledgeChunksTable+`
		WHERE tenant_id = $1 AND embedding_status = 'completed'`, tenantID).Scan(&statusOnlyMatches))
	require.Equal(t, 2, statusOnlyMatches,
		"the status filter alone admits the vector-less row, so it is not a substitute")

	got, err := repo.SearchByVector(ctx, itVector(1), tenantID, 10)
	require.NoError(t, err)
	require.Len(t, got, 1, "only the chunk that actually has a vector may be returned")
	assert.Equal(t, good.ID, got[0].ID)
	assert.Len(t, got[0].Embedding, 1024)
}

// TestVectorSearch_PartialIndexRequiresPredicate pins the planner-side claim.
// A partial index is usable only when the query's WHERE implies the index
// predicate, so:
//   - reader query (with `embedding IS NOT NULL`) → planner chooses the partial
//     ivfflat index;
//   - same query without the predicate → the planner cannot prove the predicate
//     and falls back to a full scan plus sort, silently losing the vector index.
//
// This is what "the planner gains a usable filter" means concretely, and it also
// guards against someone dropping the predicate from the reader.
//
// Sequential scans are left ENABLED and the table is seeded to a realistic size
// and ANALYZEd, so a chosen index plan reflects a genuine planner preference
// rather than an artificially disabled alternative.
func TestVectorSearch_PartialIndexRequiresPredicate(t *testing.T) {
	pool := setupVectorITDB(t)
	if pool == nil {
		return
	}

	const tenantID = "it-planner"
	vectorITSeedBulk(t, pool, storage_models.ExperiencesTable, tenantID, 1000, 50)

	t.Run("reader_query_uses_partial_index", func(t *testing.T) {
		plan := explainVectorOrderBy(t, pool, storage_models.ExperiencesTable, tenantID,
			"AND embedding IS NOT NULL")
		assert.Contains(t, plan, "idx_experiences_1024_embedding",
			"the reader's predicate must let the planner reach the partial index; plan:\n%s", plan)
	})

	t.Run("query_without_predicate_loses_the_index", func(t *testing.T) {
		plan := explainVectorOrderBy(t, pool, storage_models.ExperiencesTable, tenantID, "")
		assert.NotContains(t, plan, "idx_experiences_1024_embedding",
			"without the predicate a partial index is unusable — dropping the filter "+
				"from the reader would silently give up the vector index; plan:\n%s", plan)
		assert.Contains(t, plan, "Seq Scan",
			"the fallback is a full scan plus sort; plan:\n%s", plan)
	})
}

// TestVectorSearch_LegacyNonPartialIndexStaysUsable checks the migration
// boundary claimed in migrate_storage.go: databases created before this change
// keep a non-partial ivfflat index, and the new reader query (which carries the
// predicate) can still use it. So the change needs no index rebuild to stay
// correct — there the predicate is simply an extra filter.
func TestVectorSearch_LegacyNonPartialIndexStaysUsable(t *testing.T) {
	pool := setupVectorITDB(t)
	if pool == nil {
		return
	}

	ctx := context.Background()
	db := pool.GetDB()

	// Reproduce the pre-change index shape on a scratch table that mirrors the
	// columns the reader query touches.
	for _, stmt := range []string{
		`CREATE TABLE legacy_vec (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id TEXT NOT NULL,
			type VARCHAR(50),
			input TEXT,
			output TEXT,
			embedding VECTOR(1024),
			embedding_model TEXT,
			embedding_version INT,
			score FLOAT,
			success BOOLEAN,
			agent_id VARCHAR(255),
			metadata JSONB DEFAULT '{}'::jsonb,
			decay_at TIMESTAMP DEFAULT NOW() + INTERVAL '30 days'
		)`,
		`CREATE INDEX idx_legacy_vec_embedding ON legacy_vec
			USING ivfflat (embedding vector_cosine_ops) WITH (lists = 100)`,
	} {
		_, err := db.ExecContext(ctx, stmt)
		require.NoError(t, err, "setup legacy index shape")
	}

	const tenantID = "legacy"
	vectorITSeedBulk(t, pool, "legacy_vec", tenantID, 1000, 50)

	var def string
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT indexdef FROM pg_indexes WHERE indexname = 'idx_legacy_vec_embedding'").Scan(&def))
	require.NotContains(t, def, "WHERE",
		"this fixture must model the NON-partial index shipped before the change")

	plan := explainVectorOrderBy(t, pool, "legacy_vec", tenantID, "AND embedding IS NOT NULL")
	assert.Contains(t, plan, "idx_legacy_vec_embedding",
		"a pre-existing non-partial index must remain usable by the new predicated "+
			"query, so no index rebuild is required; plan:\n%s", plan)
}
