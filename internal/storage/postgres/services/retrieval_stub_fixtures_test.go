package services

import (
	"context"
	"database/sql"
	"sync/atomic"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	apperrors "github.com/Timwood0x10/ares/internal/errors"
	memembed "github.com/Timwood0x10/ares/internal/runtime/memory/embedding"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
	pgembed "github.com/Timwood0x10/ares/internal/storage/postgres/embedding"
	"github.com/Timwood0x10/ares/internal/storage/postgres/repositories"
)

// Shared fixture constants (goconst shield: repeated literals live here once).
const (
	stubTenant          = "tenant-1"
	stubModel           = "stub-model"
	stubEmbeddingText   = "[0.1,0.2]"
	stubEmptyMeta       = "{}"
	stubQueryLong       = "how does the hybrid retrieval pipeline rank knowledge chunks"
	stubQueryShort      = "go error"
	stubKBContent       = "knowledge chunk body"
	stubEXPInput        = "deploy the service"
	stubEXPOutput       = "use a rolling update strategy"
	stubToolName        = "stub-deploy-tool"
	stubToolDescription = "deploys the service with rolling updates"
)

// stubReKBVector matches KnowledgeRepository.SearchByVector (pgvector ORDER BY).
const stubReKBVector = `(?s)FROM knowledge_chunks_1024.*ORDER BY embedding <=>`

// stubReKBKeyword matches KnowledgeRepository.SearchByKeyword (ts_rank).
const stubReKBKeyword = `(?s)FROM knowledge_chunks_1024.*ts_rank`

// stubReKBSubstring matches KnowledgeRepository.SearchBySubstring (ILIKE).
const stubReKBSubstring = `(?s)FROM knowledge_chunks_1024.*ILIKE`

// stubReEXPVector matches ExperienceRepository.SearchByVector.
const stubReEXPVector = `(?s)FROM experiences_1024.*ORDER BY embedding <=>`

// stubReEXPKeyword matches ExperienceRepository.SearchByKeyword (ILIKE).
const stubReEXPKeyword = `(?s)FROM experiences_1024.*ILIKE`

// stubReTOOLVector matches ToolRepository.SearchByVector.
const stubReTOOLVector = `(?s)FROM tools.*ORDER BY embedding <=>`

// stubReTOOLKeyword matches ToolRepository.SearchByKeyword (ILIKE).
const stubReTOOLKeyword = `(?s)FROM tools.*ILIKE`

// stubErrDB is the canonical repository-error sentinel for degrade-path tests.
func stubErrDB() error { return apperrors.New("stub db unavailable") }

// stubSQLMock builds a sqlmock-backed *sql.DB (satisfies postgres.DBTX).
// ordered=false disables in-order expectation matching for the parallel
// searchSingleQuery paths; precision-chain tests keep the default in-order
// mode so short-circuit stage priority is provable.
func stubSQLMock(t *testing.T, ordered bool) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	if !ordered {
		mock.MatchExpectationsInOrder(false)
	}
	t.Cleanup(func() {
		// Best-effort teardown: sqlmock treats an un-expected Close as an
		// error ("call to database Close was not expected") — tests assert
		// ExpectationsWereMet themselves before cleanup runs, so that noise
		// is intentionally discarded here.
		_ = db.Close()
	})
	return db, mock
}

// stubKBRepo builds a real KnowledgeRepository over the given DBTX.
func stubKBRepo(db *sql.DB) *repositories.KnowledgeRepository {
	return repositories.NewKnowledgeRepository(db, nil)
}

// stubExpRepo builds a real ExperienceRepository over the given DBTX.
func stubExpRepo(db *sql.DB) *repositories.ExperienceRepository {
	return repositories.NewExperienceRepository(db)
}

// stubToolRepo builds a real ToolRepository over the given DBTX.
func stubToolRepo(db *sql.DB) *repositories.ToolRepository {
	return repositories.NewToolRepository(db)
}

// stubGuard builds a permissive RetrievalGuard (no rate limiting in tests).
func stubGuard(t *testing.T) *postgres.RetrievalGuard {
	t.Helper()
	guard := postgres.NewRetrievalGuard(1000, 3, time.Minute, time.Second)
	t.Cleanup(guard.Close)
	return guard
}

// stubPipeline is a thread-safe EmbeddingPipeline fake: immutable vec/err set
// at construction, atomic call counters (searchSingleQuery calls the pipeline
// from the errgroup vector goroutine, and Search fans out queries in parallel).
type stubPipeline struct {
	vec        []float64
	embedErr   error
	buildErr   error
	buildCalls atomic.Int64
	embedCalls atomic.Int64
}

func (p *stubPipeline) BuildSpec(kind memembed.EmbeddingKind, payload any) (memembed.EmbeddingSpec, error) {
	p.buildCalls.Add(1)
	if p.buildErr != nil {
		return memembed.EmbeddingSpec{}, p.buildErr
	}
	q, ok := payload.(string)
	if !ok {
		return memembed.EmbeddingSpec{}, apperrors.New("invalid query payload")
	}
	return memembed.BuildMemoryQuerySpec(q, stubModel, 1, 0), nil
}

func (p *stubPipeline) Embed(_ context.Context, _ memembed.EmbeddingSpec) ([]float64, error) {
	p.embedCalls.Add(1)
	if p.embedErr != nil {
		return nil, p.embedErr
	}
	return p.vec, nil
}

func (p *stubPipeline) Model() string { return stubModel }

func (p *stubPipeline) calls() (builds, embeds int64) {
	return p.buildCalls.Load(), p.embedCalls.Load()
}

// stubNewPipeline constructs a stubPipeline with fixed outputs.
func stubNewPipeline(vec []float64, embedErr error) *stubPipeline {
	return &stubPipeline{vec: vec, embedErr: embedErr}
}

// stubRetrievalSvc assembles a RetrievalService through the production
// constructor over sqlmock-backed repositories.
func stubRetrievalSvc(
	kb *repositories.KnowledgeRepository,
	exp *repositories.ExperienceRepository,
	tool *repositories.ToolRepository,
	pipe memembed.EmbeddingPipeline,
	guard *postgres.RetrievalGuard,
) *RetrievalService {
	svc := NewRetrievalService(nil, nil, nil, guard, kb, exp, tool)
	if pipe != nil {
		svc.SetEmbeddingPipeline(pipe)
	}
	return svc
}

// stubHybridSvc assembles a RetrievalService whose vector branch is reachable:
// searchSingleQuery only runs vector search when embeddingClient is non-nil and
// enabled (retrieval_search.go:44), and getEmbedding prefers the pipeline, so
// the dead HTTP client is never dialed.
func stubHybridSvc(kb *repositories.KnowledgeRepository, pipe memembed.EmbeddingPipeline, guard *postgres.RetrievalGuard) *RetrievalService {
	deadClient := pgembed.NewEmbeddingClient("http://127.0.0.1:1", stubModel, nil, time.Second)
	svc := NewRetrievalService(nil, deadClient, nil, guard, kb, nil, nil)
	if pipe != nil {
		svc.SetEmbeddingPipeline(pipe)
	}
	return svc
}

// stubSimpleSvc assembles a SimpleRetrievalService through the production
// constructor and attaches the pipeline.
func stubSimpleSvc(
	repo *repositories.KnowledgeRepository,
	pipe memembed.EmbeddingPipeline,
	cfg *SimpleRetrievalConfig,
) *SimpleRetrievalService {
	svc := NewSimpleRetrievalService(repo, nil, cfg)
	if pipe != nil {
		svc.SetEmbeddingPipeline(pipe)
	}
	return svc
}

// ── row fixtures (column order = repository rows.Scan order) ──────────────

// stubNow returns the current time for row-fixture timestamps.
func stubNow() time.Time { return time.Now() }

// Column sets shared by the row builders and their empty (zero-row) variants.
var (
	stubKBColsKeyword = []string{
		"id", "tenant_id", "content", "embedding_model", "embedding_version",
		"embedding_status", "source_type", "source", "metadata", "document_id",
		"chunk_index", "content_hash", "access_count", "created_at", "updated_at",
		"score",
	}
	stubKBColsSubstring = []string{
		"id", "tenant_id", "content", "embedding_model", "embedding_version",
		"embedding_status", "source_type", "source", "metadata", "document_id",
		"chunk_index", "content_hash", "access_count", "created_at", "updated_at",
	}
)

// stubKBSubstringRowsEmpty returns a zero-row result set for the substring query.
func stubKBSubstringRowsEmpty() *sqlmock.Rows {
	return sqlmock.NewRows(stubKBColsSubstring)
}

// stubKBKeywordRowsEmpty returns a zero-row result set for the keyword query.
func stubKBKeywordRowsEmpty() *sqlmock.Rows {
	return sqlmock.NewRows(stubKBColsKeyword)
}

// stubKBVectorRows builds rows for KnowledgeRepository.SearchByVector (17 cols).
func stubKBVectorRows(content string, similarity float64) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "tenant_id", "content", "embedding", "embedding_model", "embedding_version",
		"embedding_status", "source_type", "source", "metadata", "document_id",
		"chunk_index", "content_hash", "access_count", "created_at", "updated_at",
		"similarity",
	}).AddRow(
		"kb-vec-1", stubTenant, content, stubEmbeddingText, stubModel, 1,
		"completed", "document", "docs/readme.md", stubEmptyMeta, "doc-1",
		0, "hash-1", 0, time.Now(), time.Now(),
		similarity,
	)
}

// stubKBKeywordRows builds rows for KnowledgeRepository.SearchByKeyword (16 cols).
func stubKBKeywordRows(content string, score float64) *sqlmock.Rows {
	return sqlmock.NewRows(stubKBColsKeyword).AddRow(
		"kb-kw-1", stubTenant, content, stubModel, 1,
		"completed", "document", "docs/readme.md", stubEmptyMeta, "doc-1",
		0, "hash-2", 0, stubNow(), stubNow(),
		score,
	)
}

// stubKBSubstringRows builds rows for KnowledgeRepository.SearchBySubstring (15 cols).
func stubKBSubstringRows(content string) *sqlmock.Rows {
	return sqlmock.NewRows(stubKBColsSubstring).AddRow(
		"kb-ex-1", stubTenant, content, stubModel, 1,
		"completed", "document", "docs/readme.md", stubEmptyMeta, "doc-1",
		0, "hash-3", 0, stubNow(), stubNow(),
	)
}

// stubEXPVectorRows builds rows for ExperienceRepository.SearchByVector (16 cols).
func stubEXPVectorRows(output string, similarity float64) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "tenant_id", "type", "input", "output", "embedding", "embedding_model",
		"embedding_version", "score", "success", "agent_id", "metadata",
		"decay_at", "created_at", "usage_count", "similarity",
	}).AddRow(
		"exp-vec-1", stubTenant, "success", stubEXPInput, output, stubEmbeddingText, stubModel,
		1, 0.8, true, "agent-1", stubEmptyMeta,
		time.Now().Add(time.Hour), time.Now(), 2, similarity,
	)
}

// stubEXPKeywordRows builds rows for ExperienceRepository.SearchByKeyword (14 cols).
func stubEXPKeywordRows(input string, score float64) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "tenant_id", "type", "input", "output", "embedding_model",
		"embedding_version", "score", "success", "agent_id", "metadata",
		"decay_at", "created_at", "usage_count",
	}).AddRow(
		"exp-kw-1", stubTenant, "success", input, stubEXPOutput, stubModel,
		1, score, true, "agent-1", stubEmptyMeta,
		time.Now().Add(time.Hour), time.Now(), 2,
	)
}

// stubTOOLVectorRows builds rows for ToolRepository.SearchByVector (15 cols).
func stubTOOLVectorRows(description string, similarity float64) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "tenant_id", "name", "description", "embedding", "embedding_model",
		"embedding_version", "agent_type", "tags", "usage_count", "success_rate",
		"last_used_at", "metadata", "created_at", "similarity",
	}).AddRow(
		"tool-vec-1", stubTenant, stubToolName, description, stubEmbeddingText, stubModel,
		1, "worker", "{deploy}", 12, 0.92,
		time.Now(), stubEmptyMeta, time.Now(), similarity,
	)
}

// stubTOOLKeywordRows builds rows for ToolRepository.SearchByKeyword (14 cols).
func stubTOOLKeywordRows(name string, score float64) *sqlmock.Rows {
	_ = score // tool keyword scoring is computed server-side from usage/success
	return sqlmock.NewRows([]string{
		"id", "tenant_id", "name", "description", "embedding", "embedding_model",
		"embedding_version", "agent_type", "tags", "usage_count", "success_rate",
		"last_used_at", "metadata", "created_at",
	}).AddRow(
		"tool-kw-1", stubTenant, name, stubToolDescription, "", stubModel,
		1, "worker", "{deploy}", 12, 0.92,
		time.Now(), stubEmptyMeta, time.Now(),
	)
}
