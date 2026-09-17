//go:build integration
// +build integration

// Package repositories — vector-search behaviour measured on REAL data: real
// text taken from this repository's docs/ tree, embedded by a real
// qwen3-embedding model (1024 dimensions, matching the schema), stored in real
// PostgreSQL + pgvector.
//
// The companion file vector_null_embedding_integration_test.go uses synthetic
// vectors because its claims are about SQL semantics (NULLS LAST, scan-loop
// drops, planner predicate proofs), which do not depend on what the numbers
// mean. Two claims cannot be checked that way:
//
//  1. ivfflat recall. ivfflat is an APPROXIMATE index; how much it misses
//     depends on the real distribution of the vectors, so recall has to be
//     measured against real embeddings, comparing the index result with the
//     exact sequential-scan answer.
//  2. The aggregate "dropped rows" warning. The previous round only proved the
//     drop happens; nothing asserted an operator can actually see it. Here the
//     emitted log line is captured and its text and attributes are asserted.
package repositories

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	golog "log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/storage/postgres"
	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
)

// ---------------------------------------------------------------------------
// Real corpus: docs/*.md paragraphs + real e5-large-v2 embeddings
// ---------------------------------------------------------------------------

// corpusDoc is one real paragraph and its real embedding.
type corpusDoc struct {
	Content   string
	Embedding []float64
}

// realCorpus is embedded once per `go test` process and shared by the tests
// below, because embedding ~1k paragraphs costs tens of seconds.
var (
	realCorpusOnce sync.Once
	realCorpusDocs []corpusDoc
	realCorpusErr  string
)

const (
	// corpusEmbedDim must match the VECTOR(1024) columns in the schema.
	corpusEmbedDim = 1024
	// corpusMaxDocs caps how much of docs/ is embedded. 900 rows is enough for
	// an ivfflat index with lists = 100 to have populated, non-degenerate
	// centroids while keeping the test around half a minute.
	corpusMaxDocs = 900
)

// codeFenceRE strips fenced code blocks so the corpus is prose, not YAML/Go.
var codeFenceRE = regexp.MustCompile("(?s)```.*?```")

// embedHost returns the Ollama base URL.
func embedHost() string {
	if v := os.Getenv("OLLAMA_HOST"); v != "" {
		return strings.TrimSuffix(v, "/")
	}
	return "http://localhost:11434"
}

// embedModel returns the embedding model tag. qwen3-embedding:0.6b is the
// default because it produces 1024 dimensions, matching the VECTOR(1024)
// columns in the schema, so its vectors can be stored and compared exactly the
// way production vectors are.
func embedModel() string {
	if v := os.Getenv("ARES_IT_EMBED_MODEL"); v != "" {
		return v
	}
	return "qwen3-embedding:0.6b"
}

// embedBatchSize bounds how many texts go into one /api/embed call. Large
// batches make the local runner allocate one big tokenizer request and it can
// drop the connection; a few hundred per call is both reliable and fast.
const embedBatchSize = 200

// embedTexts calls the real embedding model, in batches. Returns an error
// string (not an error) so callers can Skip with a reason.
func embedTexts(texts []string) ([][]float64, string) {
	all := make([][]float64, 0, len(texts))
	for start := 0; start < len(texts); start += embedBatchSize {
		end := start + embedBatchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch, errMsg := embedBatch(texts[start:end])
		if errMsg != "" {
			return nil, errMsg
		}
		all = append(all, batch...)
	}
	return all, ""
}

// embedBatch performs a single /api/embed round trip.
func embedBatch(texts []string) ([][]float64, string) {
	body, err := json.Marshal(map[string]any{
		"model": embedModel(),
		"input": texts,
	})
	if err != nil {
		return nil, "marshal embed request: " + err.Error()
	}

	// Generous timeout: the first call may have to load the model into memory.
	client := &http.Client{Timeout: 15 * time.Minute}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		embedHost()+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, "build embed request: " + err.Error()
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, "no embedding service at " + embedHost() + ": " + err.Error()
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, "embedding service returned " + resp.Status + ": " + string(detail)
	}

	var out struct {
		Embeddings [][]float64 `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, "decode embed response: " + err.Error()
	}
	if len(out.Embeddings) != len(texts) {
		return nil, "embedding service returned a different number of vectors"
	}
	for _, e := range out.Embeddings {
		if len(e) != corpusEmbedDim {
			return nil, "embedding model produces " + strconv.Itoa(len(e)) +
				" dims, schema needs 1024 — set ARES_IT_EMBED_MODEL"
		}
	}
	return out.Embeddings, ""
}

// collectCorpusParagraphs walks the repository's docs/ tree and returns
// deduplicated prose paragraphs of a useful length.
func collectCorpusParagraphs(docsDir string, limit int) []string {
	var files []string
	_ = filepath.WalkDir(docsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(path, ".md") {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files) // deterministic corpus across runs

	seen := make(map[string]bool)
	var paragraphs []string
	for _, f := range files {
		raw, err := os.ReadFile(f) //nolint:gosec // paths come from walking a repo dir
		if err != nil {
			continue
		}
		text := codeFenceRE.ReplaceAllString(string(raw), "")
		for _, p := range strings.Split(text, "\n\n") {
			p = strings.Join(strings.Fields(p), " ")
			// Skip tables and headings: they are boilerplate-heavy and produce
			// near-duplicate vectors, which would make recall meaningless.
			if len(p) < 200 || len(p) > 800 ||
				strings.HasPrefix(p, "|") || strings.HasPrefix(p, "#") {
				continue
			}
			if seen[p] {
				continue
			}
			seen[p] = true
			paragraphs = append(paragraphs, p)
			if len(paragraphs) >= limit {
				return paragraphs
			}
		}
	}
	return paragraphs
}

// loadRealCorpus returns real paragraphs with real embeddings, or skips the
// test when docs/ or the embedding service is unavailable.
func loadRealCorpus(t *testing.T) []corpusDoc {
	t.Helper()

	realCorpusOnce.Do(func() {
		// The test file lives at internal/storage/postgres/repositories/.
		docsDir := filepath.Join("..", "..", "..", "..", "docs")
		if _, err := os.Stat(docsDir); err != nil {
			realCorpusErr = "docs/ not found: " + err.Error()
			return
		}

		paragraphs := collectCorpusParagraphs(docsDir, corpusMaxDocs)
		if len(paragraphs) < 200 {
			realCorpusErr = "docs/ yielded too few paragraphs for a recall measurement"
			return
		}

		// Documents get the "passage:" prefix and queries get "query:", which
		// is the asymmetric prefix protocol the production retrieval path uses
		// (SimpleRetrievalConfig.QueryPrefix / EmbedWithPrefix). Mixing the two
		// up degrades similarity, so the test mirrors production.
		inputs := make([]string, len(paragraphs))
		for i, p := range paragraphs {
			inputs[i] = "passage: " + p
		}

		vectors, errMsg := embedTexts(inputs)
		if errMsg != "" {
			realCorpusErr = errMsg
			return
		}

		realCorpusDocs = make([]corpusDoc, len(paragraphs))
		for i := range paragraphs {
			realCorpusDocs[i] = corpusDoc{Content: paragraphs[i], Embedding: vectors[i]}
		}
	})

	if realCorpusErr != "" {
		t.Skipf("skipping real-corpus test: %s", realCorpusErr)
	}
	return realCorpusDocs
}

// embedQuery embeds a single search query with the "query:" prefix.
func embedQuery(t *testing.T, text string) []float64 {
	t.Helper()
	vectors, errMsg := embedTexts([]string{"query: " + text})
	if errMsg != "" {
		t.Skipf("skipping: cannot embed query: %s", errMsg)
	}
	return vectors[0]
}

// seedRealCorpus inserts the corpus into knowledge_chunks_1024 in batches.
func seedRealCorpus(t *testing.T, pool *postgres.Pool, tenantID string, docs []corpusDoc) {
	t.Helper()

	ctx := context.Background()
	repo := NewKnowledgeRepository(pool.GetDB(), pool.GetDB())

	const batch = 100
	for start := 0; start < len(docs); start += batch {
		end := start + batch
		if end > len(docs) {
			end = len(docs)
		}
		chunks := make([]*storage_models.KnowledgeChunk, 0, end-start)
		for i := start; i < end; i++ {
			chunks = append(chunks, &storage_models.KnowledgeChunk{
				TenantID:         tenantID,
				Content:          docs[i].Content,
				Embedding:        docs[i].Embedding,
				EmbeddingModel:   embedModel(),
				EmbeddingVersion: 1,
				EmbeddingStatus:  storage_models.EmbeddingStatusCompleted,
				SourceType:       "document",
				Source:           "docs",
				ChunkIndex:       i,
				ContentHash:      "it-corpus-" + strconv.Itoa(i),
			})
		}
		require.NoError(t, repo.CreateBatch(ctx, chunks), "seed corpus batch %d", start)
	}

	_, err := pool.GetDB().ExecContext(ctx, "ANALYZE "+storage_models.KnowledgeChunksTable)
	require.NoError(t, err)
}

// searchIDs runs the reader's vector query on a dedicated session, applying the
// given per-session settings, and returns the ordered chunk IDs.
func searchIDs(t *testing.T, pool *postgres.Pool, tenantID string,
	query []float64, limit int, settings ...string) []string {
	t.Helper()

	ctx := context.Background()
	conn, err := pool.GetDB().Conn(ctx)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	for _, s := range settings {
		_, err = conn.ExecContext(ctx, s)
		require.NoError(t, err, "apply session setting %q", s)
	}

	rows, err := conn.QueryContext(ctx, `
		SELECT id
		FROM `+storage_models.KnowledgeChunksTable+`
		WHERE tenant_id = $2
		  AND embedding_status = 'completed'
		  AND embedding IS NOT NULL
		ORDER BY embedding <=> $1::vector
		LIMIT $3`,
		postgres.FormatVector(query), tenantID, limit)
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		require.NoError(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	require.NoError(t, rows.Err())
	return ids
}

// recallAt compares an approximate result with the exact one.
func recallAt(exact, approx []string) float64 {
	if len(exact) == 0 {
		return 1
	}
	want := make(map[string]bool, len(exact))
	for _, id := range exact {
		want[id] = true
	}
	hits := 0
	for _, id := range approx {
		if want[id] {
			hits++
		}
	}
	return float64(hits) / float64(len(exact))
}

// realCorpusQueries are natural questions about this repository, so the query
// vectors sit in the same semantic space as the indexed paragraphs.
var realCorpusQueries = []string{
	"how does the agent evolution loop work",
	"what is the vector index used for knowledge retrieval",
	"how are experiences distilled and stored",
	"multi tenant isolation in the storage layer",
	"how does the scheduler assign tasks to agents",
	"what happens when an embedding fails to generate",
	"configuration options for the llm provider",
	"how is the knowledge base imported from documents",
	"security hardening and audit logging",
	"how does peer mode communication work",
	"retry and timeout behaviour for tool calls",
	"what metrics are exposed for observability",
}

// ---------------------------------------------------------------------------
// 1. ivfflat recall, measured on real embeddings
// ---------------------------------------------------------------------------

// TestIvfflatRecall_OnRealCorpus measures what the approximate index actually
// costs in recall, against the exact answer produced by a sequential scan.
//
// This is the gap the previous round left open. ivfflat partitions vectors into
// `lists` cells and, at query time, only visits `ivfflat.probes` of them, so it
// can miss true nearest neighbours. How badly is a property of the real vector
// distribution, hence a real corpus and a real embedding model.
//
// The index is REINDEXed after loading: MigrateStorage necessarily creates it on
// an empty table, where k-means has nothing to cluster. The separate subtest
// below quantifies that.
func TestIvfflatRecall_OnRealCorpus(t *testing.T) {
	docs := loadRealCorpus(t)
	pool := setupVectorITDB(t)
	if pool == nil {
		return
	}

	const tenantID = "it-recall"
	seedRealCorpus(t, pool, tenantID, docs)

	ctx := context.Background()
	// Rebuild the index now that the table holds data, so the centroids
	// describe the real distribution.
	_, err := pool.GetDB().ExecContext(ctx, "REINDEX INDEX idx_knowledge_1024_embedding")
	require.NoError(t, err)

	const k = 10
	// Exact ground truth: index scans are off at database level, so this is a
	// sequential scan over every vector.
	exact := make([][]string, len(realCorpusQueries))
	queries := make([][]float64, len(realCorpusQueries))
	for i, q := range realCorpusQueries {
		queries[i] = embedQuery(t, q)
		exact[i] = searchIDs(t, pool, tenantID, queries[i], k)
		require.Len(t, exact[i], k, "corpus must be large enough for k=%d", k)
	}

	// meanRecall runs every query with the given probes setting.
	meanRecall := func(probes int) float64 {
		total := 0.0
		for i := range queries {
			approx := searchIDs(t, pool, tenantID, queries[i], k,
				"SET enable_indexscan = on",
				"SET enable_seqscan = off",
				"SET ivfflat.probes = "+strconv.Itoa(probes))
			total += recallAt(exact[i], approx)
		}
		return total / float64(len(queries))
	}

	r1 := meanRecall(1)
	r10 := meanRecall(10)
	r100 := meanRecall(100)
	t.Logf("ivfflat recall@%d on %d real paragraphs, %d real queries: "+
		"probes=1 %.3f | probes=10 %.3f | probes=100 (=lists) %.3f",
		k, len(docs), len(realCorpusQueries), r1, r10, r100)

	// probes = lists visits every cell, so the index result must match the
	// exact one. This is the invariant that holds regardless of distribution.
	assert.Equal(t, 1.0, r100,
		"probing all %d lists must be exhaustive and therefore exact", 100)

	// More probes cannot see fewer cells, so recall must not regress.
	assert.GreaterOrEqual(t, r10, r1, "recall must not decrease as probes grows")
	assert.GreaterOrEqual(t, r100, r10, "recall must not decrease as probes grows")

	// The default (probes = 1) is lossy. Pinning a loose lower bound documents
	// that the default is usable while making a collapse to near-zero fail.
	assert.Greater(t, r1, 0.15,
		"default probes=1 recall collapsed (%.3f) — the index or the corpus is degenerate", r1)
	assert.Less(t, r1, 1.0,
		"probes=1 returning the exact answer means this is not measuring an "+
			"approximate scan; check that the index is really being used")

	t.Run("null_embedding_rows_do_not_affect_recall", func(t *testing.T) {
		// Add vector-less rows: the partial index cannot contain them, and the
		// reader filters them, so the ranked result must be unchanged.
		_, err := pool.GetDB().ExecContext(ctx, `
			INSERT INTO `+storage_models.KnowledgeChunksTable+`
			(tenant_id, content, embedding, embedding_model, embedding_version,
			 embedding_status, content_hash)
			SELECT $1, 'awaiting backfill ' || i, NULL, $2, 1, 'completed',
			       'it-corpus-null-' || i
			FROM generate_series(1, 50) i`, tenantID, embedModel())
		require.NoError(t, err)

		for i := range queries {
			after := searchIDs(t, pool, tenantID, queries[i], k)
			assert.Equal(t, exact[i], after,
				"query %d: vector-less rows must not perturb the exact ranking", i)
		}
	})
}

// TestIvfflatRecall_IndexBuiltOnEmptyTable quantifies a pre-existing property of
// the migration, surfaced while measuring recall: MigrateStorage creates the
// ivfflat index before any rows exist, so its k-means centroids are built from
// nothing. Rows inserted afterwards are assigned to those degenerate lists.
//
// This is not caused by making the index partial — it is inherent to creating an
// ivfflat index at migration time — but it directly limits real recall, so the
// numbers belong next to the recall test. The assertion is deliberately weak
// (only that REINDEX does not make recall worse); the value is the logged
// comparison.
func TestIvfflatRecall_IndexBuiltOnEmptyTable(t *testing.T) {
	docs := loadRealCorpus(t)
	pool := setupVectorITDB(t)
	if pool == nil {
		return
	}

	const tenantID = "it-recall-empty-build"
	seedRealCorpus(t, pool, tenantID, docs)

	const k = 10
	exact := make([][]string, len(realCorpusQueries))
	queries := make([][]float64, len(realCorpusQueries))
	for i, q := range realCorpusQueries {
		queries[i] = embedQuery(t, q)
		exact[i] = searchIDs(t, pool, tenantID, queries[i], k)
	}

	meanRecall := func() float64 {
		total := 0.0
		for i := range queries {
			approx := searchIDs(t, pool, tenantID, queries[i], k,
				"SET enable_indexscan = on",
				"SET enable_seqscan = off",
				"SET ivfflat.probes = 1")
			total += recallAt(exact[i], approx)
		}
		return total / float64(len(queries))
	}

	asMigrated := meanRecall()

	_, err := pool.GetDB().ExecContext(context.Background(),
		"REINDEX INDEX idx_knowledge_1024_embedding")
	require.NoError(t, err)
	afterReindex := meanRecall()

	t.Logf("recall@%d with probes=1: index as built by MigrateStorage (empty table) %.3f "+
		"| after REINDEX on loaded data %.3f", k, asMigrated, afterReindex)

	assert.GreaterOrEqual(t, afterReindex, asMigrated,
		"rebuilding ivfflat on real data must not reduce recall")
}

// ---------------------------------------------------------------------------
// 2. The aggregate warning is actually emitted, with usable attributes
// ---------------------------------------------------------------------------

// captureLogs redirects the standard library log output, which is where slog's
// default handler writes, and returns everything written while fn runs.
//
// Redirecting the log package (rather than calling slog.SetDefault) is what
// works here: the package logger is built once at init from slog.Default(), so
// swapping the default afterwards would not affect it.
func captureLogs(t *testing.T, fn func()) string {
	t.Helper()

	var buf bytes.Buffer
	oldWriter := golog.Writer()
	oldFlags := golog.Flags()
	golog.SetOutput(&buf)
	golog.SetFlags(0)
	defer func() {
		golog.SetOutput(oldWriter)
		golog.SetFlags(oldFlags)
	}()

	fn()
	return buf.String()
}

// TestExperienceSearch_DroppedRowsAreReported asserts the operator-visible
// output, not just the behaviour: a row that has a real embedding but an
// unscannable column is skipped, and the aggregate WARN naming the tenant, the
// skipped count and the returned count is emitted.
//
// The broken column is a NULL `score`: it is nullable in the schema, passes the
// CHECK constraint, and fails Scan into float64 — a realistic "one bad row"
// case that the `embedding IS NOT NULL` predicate cannot filter.
func TestExperienceSearch_DroppedRowsAreReported(t *testing.T) {
	docs := loadRealCorpus(t)
	pool := setupVectorITDB(t)
	if pool == nil {
		return
	}

	ctx := context.Background()
	const tenantID = "it-warn-experiences"
	const good, broken = 6, 3
	require.GreaterOrEqual(t, len(docs), good+broken)

	insert := func(doc corpusDoc, idx int, scoreNull bool) {
		score := any(0.5)
		if scoreNull {
			score = nil
		}
		_, err := pool.GetDB().ExecContext(ctx, `
			INSERT INTO `+storage_models.ExperiencesTable+`
			(tenant_id, type, input, output, embedding, embedding_model,
			 embedding_version, score, success, agent_id, metadata)
			VALUES ($1, 'distilled', $2, 'solution', $3::vector, $4, 1, $5, true, 'a', '{}'::jsonb)`,
			tenantID, doc.Content, postgres.FormatVector(doc.Embedding),
			embedModel(), score)
		require.NoError(t, err, "insert experience %d", idx)
	}
	for i := 0; i < good; i++ {
		insert(docs[i], i, false)
	}
	for i := good; i < good+broken; i++ {
		insert(docs[i], i, true) // real embedding, NULL score
	}

	repo := NewExperienceRepository(pool.GetDB())
	query := embedQuery(t, "how are experiences distilled and stored")

	var got []*storage_models.Experience
	logs := captureLogs(t, func() {
		var err error
		got, err = repo.SearchByVector(ctx, query, tenantID, good+broken)
		require.NoError(t, err)
	})

	require.Len(t, got, good,
		"the unscannable rows must be skipped, not fail the whole search")

	// Per-row warning: identifies what went wrong.
	assert.Contains(t, logs, "Skipping experience row in vector search",
		"each bad row must be logged individually; logs:\n%s", logs)

	// Aggregate warning: the line an operator can alert on.
	require.Contains(t, logs, "Vector search dropped experience rows",
		"the aggregate warning must be emitted; logs:\n%s", logs)

	aggregate := ""
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, "Vector search dropped experience rows") {
			aggregate = line
			break
		}
	}
	require.NotEmpty(t, aggregate)

	assert.Contains(t, aggregate, "WARN",
		"parity at INFO would hide a shrinking result set; line: %s", aggregate)
	assert.Contains(t, aggregate, "module=repositories", "line: %s", aggregate)
	assert.Contains(t, aggregate, "tenant_id="+tenantID,
		"the tenant is needed to tell one broken dataset from a quiet one; line: %s", aggregate)
	assert.Contains(t, aggregate, "skipped="+strconv.Itoa(broken),
		"the skipped count must be exact; line: %s", aggregate)
	assert.Contains(t, aggregate, "returned="+strconv.Itoa(good),
		"the ratio is what shows the result set was truncated by data problems; line: %s", aggregate)
}

// TestExperienceSearch_HealthySearchIsQuiet is the other half of the contract:
// the warning must not fire when nothing was dropped, or it would be noise that
// operators learn to ignore.
func TestExperienceSearch_HealthySearchIsQuiet(t *testing.T) {
	docs := loadRealCorpus(t)
	pool := setupVectorITDB(t)
	if pool == nil {
		return
	}

	ctx := context.Background()
	const tenantID = "it-warn-quiet"
	const good = 8
	require.GreaterOrEqual(t, len(docs), good)

	for i := 0; i < good; i++ {
		_, err := pool.GetDB().ExecContext(ctx, `
			INSERT INTO `+storage_models.ExperiencesTable+`
			(tenant_id, type, input, output, embedding, embedding_model,
			 embedding_version, score, success, agent_id, metadata)
			VALUES ($1, 'distilled', $2, 'solution', $3::vector, $4, 1, 0.5, true, 'a', '{}'::jsonb)`,
			tenantID, docs[i].Content, postgres.FormatVector(docs[i].Embedding), embedModel())
		require.NoError(t, err)
	}

	repo := NewExperienceRepository(pool.GetDB())
	query := embedQuery(t, "what is the vector index used for knowledge retrieval")

	var got []*storage_models.Experience
	logs := captureLogs(t, func() {
		var err error
		got, err = repo.SearchByVector(ctx, query, tenantID, good)
		require.NoError(t, err)
	})

	require.Len(t, got, good)
	assert.NotContains(t, logs, "Vector search dropped experience rows",
		"a healthy search must stay silent; logs:\n%s", logs)
}

// TestKnowledgeSearch_DroppedRowsAreReported is the knowledge-side equivalent.
// Its reader counts rows_scanned against chunks_returned instead of keeping a
// skip counter, so this pins that the gap is detected and reported at WARN.
//
// The broken column here is a NULL `chunk_index`, scanned into an int.
func TestKnowledgeSearch_DroppedRowsAreReported(t *testing.T) {
	docs := loadRealCorpus(t)
	pool := setupVectorITDB(t)
	if pool == nil {
		return
	}

	ctx := context.Background()
	const tenantID = "it-warn-knowledge"
	const good, broken = 5, 2
	require.GreaterOrEqual(t, len(docs), good+broken)

	insert := func(doc corpusDoc, idx int, chunkIndexNull bool) {
		chunkIndex := any(idx)
		if chunkIndexNull {
			chunkIndex = nil
		}
		_, err := pool.GetDB().ExecContext(ctx, `
			INSERT INTO `+storage_models.KnowledgeChunksTable+`
			(tenant_id, content, embedding, embedding_model, embedding_version,
			 embedding_status, source_type, source, chunk_index, content_hash)
			VALUES ($1, $2, $3::vector, $4, 1, 'completed', 'document', 'docs', $5, $6)`,
			tenantID, doc.Content, postgres.FormatVector(doc.Embedding),
			embedModel(), chunkIndex, "it-warn-knowledge-"+strconv.Itoa(idx))
		require.NoError(t, err, "insert chunk %d", idx)
	}
	for i := 0; i < good; i++ {
		insert(docs[i], i, false)
	}
	for i := good; i < good+broken; i++ {
		insert(docs[i], i, true) // real embedding, NULL chunk_index
	}

	repo := NewKnowledgeRepository(pool.GetDB(), pool.GetDB())
	query := embedQuery(t, "how is the knowledge base imported from documents")

	var got []*storage_models.KnowledgeChunk
	logs := captureLogs(t, func() {
		var err error
		got, err = repo.SearchByVector(ctx, query, tenantID, good+broken)
		require.NoError(t, err)
	})

	require.Len(t, got, good, "unscannable chunks must be skipped, not returned")

	require.Contains(t, logs, "Vector search dropped knowledge rows",
		"the knowledge reader must report the gap too; logs:\n%s", logs)

	aggregate := ""
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, "Vector search dropped knowledge rows") {
			aggregate = line
			break
		}
	}
	require.NotEmpty(t, aggregate)

	assert.Contains(t, aggregate, "WARN", "line: %s", aggregate)
	assert.Contains(t, aggregate, "tenant_id="+tenantID, "line: %s", aggregate)
	assert.Contains(t, aggregate, "skipped="+strconv.Itoa(broken), "line: %s", aggregate)
	assert.Contains(t, aggregate, "returned="+strconv.Itoa(good), "line: %s", aggregate)
}
