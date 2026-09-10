// Package pgvector is the official pgvector-backed vector store adapter for ARES.
//
// It wraps github.com/Timwood0x10/ares/internal/storage/postgres under the
// compat/vector.VectorStore interface so a PostgreSQL+pgvector service plugs
// into the ARES runtime via compat.RegisterVector("pgvector", …).
//
// The adapter stores vectors in the knowledge_chunks_1024 table (the same
// table used by internal/repositories.KnowledgeRepository), giving it a
// single unified surface for vector upsert + similarity search.
package pgvector

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/compat/vector"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
	"github.com/Timwood0x10/ares/internal/storage/postgres/repositories"
)

// Adapter satisfies compat/vector.VectorStore against pgvector.
type Adapter struct {
	mu    sync.Mutex // guards pool and repo against concurrent Close
	pool  *postgres.Pool
	repo  *repositories.KnowledgeRepository
	table string // defaults to "knowledge_chunks_1024"
}

// New constructs an Adapter from a raw config map.
//
// Recognized keys:
//
//	pool    *postgres.Pool — REQUIRED. PostgreSQL connection pool.
//	table   string         — vector table name; defaults to "knowledge_chunks_1024".
func New(config map[string]any) (*Adapter, error) {
	pool, _ := config["pool"].(*postgres.Pool)
	if pool == nil {
		return nil, fmt.Errorf("compat/vector/pgvector: pool is required")
	}
	table, _ := config["table"].(string)
	if table == "" {
		table = "knowledge_chunks_1024"
	}
	// repositories.KnowledgeRepository hardcodes the knowledge_chunks_1024
	// table in every query, so a custom table name cannot be honored. Fail
	// fast instead of silently ignoring the configuration.
	if table != "knowledge_chunks_1024" {
		return nil, fmt.Errorf("compat/vector/pgvector: table %q is not supported: repositories.KnowledgeRepository hardcodes knowledge_chunks_1024", table)
	}
	db := pool.GetDB()
	repo := repositories.NewKnowledgeRepository(db, db)
	return &Adapter{pool: pool, repo: repo, table: table}, nil
}

// ErrClosed is returned by every operation after Close. The adapter drops
// its pool/repo references on Close, so a later (or concurrent) call must
// fail cleanly instead of dereferencing a nil backend (#45).
var ErrClosed = fmt.Errorf("compat/vector/pgvector: adapter is closed")

// currentRepo returns the live repository, or ErrClosed once Close ran.
// The repo is snapshotted under the mutex so a Close racing an in-flight
// Search cannot nil it out mid-call.
func (a *Adapter) currentRepo() (*repositories.KnowledgeRepository, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.repo == nil {
		return nil, ErrClosed
	}
	return a.repo, nil
}

// currentPool returns the live pool, or ErrClosed once Close ran.
func (a *Adapter) currentPool() (*postgres.Pool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pool == nil {
		return nil, ErrClosed
	}
	return a.pool, nil
}

// Search returns the top-k nearest neighbors for the given query vector,
// filtered to tenantID. Each result carries the stored ID, its raw content,
// a similarity score in [0,1], and metadata forwarded as string-keyed tags.
func (a *Adapter) Search(ctx context.Context, query []float64, tenantID string, topK int) ([]vector.Result, error) {
	if tenantID == "" {
		// Upsert rejects an empty tenant; Search must too, or the pair is
		// asymmetric — a reader with no tenant could see rows from every
		// tenant that ever wrote (the repository treats "" as its own
		// scope, not as "all").
		return nil, fmt.Errorf("compat/vector/pgvector: tenantID must not be empty")
	}
	repo, err := a.currentRepo()
	if err != nil {
		return nil, err
	}
	if len(query) == 0 {
		return nil, fmt.Errorf("compat/vector/pgvector: query vector must not be empty")
	}
	if topK <= 0 {
		topK = 10
	}

	chunks, err := repo.SearchByVector(ctx, query, tenantID, topK)
	if err != nil {
		return nil, fmt.Errorf("compat/vector/pgvector: search: %w", err)
	}

	results := make([]vector.Result, 0, len(chunks))
	for _, c := range chunks {
		score := 0.0
		if v, ok := c.Metadata["similarity"].(float64); ok {
			score = v
		}
		results = append(results, vector.Result{
			ID:       c.ID,
			Content:  c.Content,
			Score:    score,
			Metadata: flattenMetadata(c.Metadata),
		})
	}
	return results, nil
}

// Upsert inserts or updates a batch of vectors identified by ID.
// Each item is stored as a KnowledgeChunk with embedding_status='completed'.
// The whole batch is committed atomically via the repository's transactional
// CreateBatch so a mid-batch failure cannot leave partial writes behind.
func (a *Adapter) Upsert(ctx context.Context, tenantID string, items []vector.Item) error {
	repo, err := a.currentRepo()
	if err != nil {
		return err
	}
	if tenantID == "" {
		return fmt.Errorf("compat/vector/pgvector: tenantID must not be empty")
	}
	chunks := make([]*storage_models.KnowledgeChunk, 0, len(items))
	for _, it := range items {
		if it.ID == "" {
			return fmt.Errorf("compat/vector/pgvector: item ID must not be empty")
		}
		chunks = append(chunks, &storage_models.KnowledgeChunk{
			ID:               it.ID,
			TenantID:         tenantID,
			Content:          it.Content,
			Embedding:        it.Vector,
			EmbeddingModel:   "compat-pgvector",
			EmbeddingVersion: 1,
			EmbeddingStatus:  storage_models.EmbeddingStatusCompleted,
			SourceType:       "compat",
			Source:           "compat",
			Metadata:         inflateMetadata(it.Metadata),
		})
	}
	if err := repo.CreateBatch(ctx, chunks); err != nil {
		return fmt.Errorf("compat/vector/pgvector: upsert batch: %w", err)
	}
	return nil
}

// HealthCheck reports whether the backend is reachable and usable.
func (a *Adapter) HealthCheck(ctx context.Context) error {
	pool, err := a.currentPool()
	if err != nil {
		return err
	}
	db := pool.GetDB()
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	row := db.QueryRowContext(checkCtx, "SELECT 1")
	var n int
	if err := row.Scan(&n); err != nil {
		return fmt.Errorf("compat/vector/pgvector: health check: %w", err)
	}
	return nil
}

// Close releases backend-specific resources. The underlying Pool is owned by
// the caller; this adapter only drops its references and does NOT close the pool.
// The mutex synchronizes with the readers' snapshots (currentRepo/currentPool),
// so a concurrent operation sees either a live adapter or a clean ErrClosed —
// never a nil dereference.
func (a *Adapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pool = nil
	a.repo = nil
	return nil
}

// flattenMetadata converts the repository's map[string]interface{} metadata
// (which carries similarity, source_type, etc.) into the string-typed metadata
// shape returned by compat/vector.Result. Non-string values are JSON-encoded.
func flattenMetadata(in map[string]interface{}) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if s, ok := v.(string); ok {
			out[k] = s
			continue
		}
		out[k] = fmt.Sprintf("%v", v)
	}
	return out
}

// inflateMetadata converts the compat/vector.Item's string-typed metadata back
// into the repository's map[string]interface{} shape. Keys are preserved; the
// caller is responsible for any richer typing on the consume side.
func inflateMetadata(in map[string]string) map[string]interface{} {
	if len(in) == 0 {
		return make(map[string]interface{})
	}
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// Compile-time interface assertions.
var (
	_ vector.VectorStore = (*Adapter)(nil)
	_                    = sql.ErrNoRows // keep database/sql referenced for future row-scan paths
)
