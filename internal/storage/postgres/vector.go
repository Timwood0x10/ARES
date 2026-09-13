package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/storage"
)

// defaultVectorDimension matches the VECTOR(1024) dimension used by the
// knowledge_chunks_1024 / experiences_1024 migrations. The previous
// hardcoded 1536 caused runtime failures because inserts did not match the
// migration schema. Override via CreateCollection when a different dimension
// is required.
const defaultVectorDimension = 1024

// VectorSearcher handles vector similarity search.
type VectorSearcher struct {
	db              DBTX
	embeddingConfig *EmbeddingConfig
}

// NewVectorSearcher creates a new VectorSearcher.
// Args:
// pool - database connection pool.
// embeddingConfig - embedding configuration for search limit settings.
// Returns new VectorSearcher instance.
func NewVectorSearcher(pool *Pool, embeddingConfig *EmbeddingConfig) *VectorSearcher {
	if embeddingConfig == nil {
		embeddingConfig = DefaultEmbeddingConfig()
	}
	return &VectorSearcher{
		db:              pool.db,
		embeddingConfig: embeddingConfig,
	}
}

// NewVectorSearcherWithDB creates a new VectorSearcher with a transaction or connection.
// Args:
// db - database transaction or connection.
// embeddingConfig - embedding configuration for search limit settings.
// Returns new VectorSearcher instance.
func NewVectorSearcherWithDB(db DBTX, embeddingConfig *EmbeddingConfig) *VectorSearcher {
	if embeddingConfig == nil {
		embeddingConfig = DefaultEmbeddingConfig()
	}
	return &VectorSearcher{
		db:              db,
		embeddingConfig: embeddingConfig,
	}
}

// SearchResult is an alias for storage.SearchResult.
//
// Deprecated: Use storage.SearchResult directly.
type SearchResult = storage.SearchResult

// Search performs a vector similarity search scoped to one tenant.
// This is a simplified implementation that uses pgvector if available.
func (v *VectorSearcher) Search(ctx context.Context, table, tenantID string, embedding []float64, limit int) ([]*SearchResult, error) {
	// Reject a negative limit: PostgreSQL interprets a negative LIMIT as
	// "no limit" and would return every row, turning a bounded search into
	// an unbounded query.
	if limit < 0 {
		return nil, fmt.Errorf("limit must not be negative: %d", limit)
	}
	// Tenant scope is mandatory: the table is tenant-scoped (tenant_id NOT
	// NULL) and an unscoped search would leak rows across tenants. Empty is
	// rejected (fail closed), same posture as compat/vector/pgvector.
	if tenantID == "" {
		return nil, fmt.Errorf("vector search: tenantID is required (tenant-scoped table %q)", table)
	}

	// Validate table name against whitelist (consistent with base_repository.go).
	safeTable, err := validateTable(table)
	if err != nil {
		return nil, errors.Wrap(err, "format table name")
	}

	// `embedding IS NOT NULL` is required, not cosmetic: the async embedding
	// worker backfills the column, so rows can legitimately exist with a NULL
	// embedding. `1 - (NULL <=> $1)` evaluates to NULL and the distance Scan
	// into a float64 then fails, which used to make a whole search error out
	// merely because it reached a not-yet-embedded row (same predicate
	// knowledge_repository.SearchByVector already carries).
	query := fmt.Sprintf(`
		SELECT id, 1 - (embedding <=> $1::vector) as distance, metadata
		FROM %s
		WHERE tenant_id = $3 AND embedding IS NOT NULL
		ORDER BY embedding <=> $1::vector
		LIMIT $2
	`, safeTable)

	embeddingJSON, err := json.Marshal(embedding)
	if err != nil {
		return nil, errors.Wrap(err, "marshal embedding")
	}

	rows, err := v.db.QueryContext(ctx, query, embeddingJSON, limit, tenantID)
	if err != nil {
		return nil, errors.Wrap(err, "vector search")
	}
	defer func() { _ = rows.Close() }()

	var results []*SearchResult
	for rows.Next() {
		var result SearchResult
		var metadataJSON []byte

		if err := rows.Scan(&result.ID, &result.Score, &metadataJSON); err != nil {
			return nil, errors.Wrap(err, "scan result")
		}

		if err := json.Unmarshal(metadataJSON, &result.Metadata); err != nil {
			return nil, errors.Wrap(err, "unmarshal metadata")
		}

		results = append(results, &result)
	}

	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "iterate search results")
	}

	if len(results) == 0 {
		return nil, errors.ErrRecordNotFound
	}

	return results, nil
}

// AddEmbedding adds a vector embedding to the specified table, scoped to one
// tenant.
func (v *VectorSearcher) AddEmbedding(ctx context.Context, table, tenantID, id string, embedding []float64, metadata map[string]any) error {
	safeTable, err := validateTable(table)
	if err != nil {
		return errors.Wrap(err, "invalid table name")
	}

	// Tenant scope is mandatory, mirroring Search(): the table is
	// tenant-scoped (tenant_id NOT NULL), so a write without a tenant would
	// land under the empty tenant — fail closed, never a silent orphan row.
	if tenantID == "" {
		return fmt.Errorf("add embedding: tenantID is required (tenant-scoped table %q)", table)
	}

	// Validate embedding dimensions.
	// Maximum supported dimension for pgvector is 2000.
	const maxDimension = 2000
	if len(embedding) == 0 {
		return errors.New("embedding cannot be empty")
	}
	if len(embedding) > maxDimension {
		return fmt.Errorf("embedding dimension too large: %d (max %d)", len(embedding), maxDimension)
	}

	// Validate id
	if err := validateSQLIdentifier(id); err != nil {
		return errors.Wrap(err, "invalid id")
	}

	embeddingJSON, err := json.Marshal(embedding)
	if err != nil {
		return errors.Wrap(err, "marshal embedding")
	}

	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return errors.Wrap(err, "marshal metadata")
	}

	query := fmt.Sprintf(`
	   INSERT INTO %s (id, tenant_id, embedding, metadata)
	  VALUES ($1, $2, $3::vector, $4)
	 `, safeTable)

	_, err = v.db.ExecContext(ctx, query, id, tenantID, embeddingJSON, metadataJSON)
	if err != nil {
		return errors.Wrap(err, "add embedding")
	}

	return nil
}

// DeleteEmbedding deletes a vector embedding scoped to one tenant. The delete
// only removes the row when its tenant_id matches: a caller can never remove
// another tenant's embedding by id alone (the same tenant boundary Search()
// and AddEmbedding() enforce). Idempotent — deleting a non-existent id, or an
// id owned by a different tenant, is a silent no-op (see the predicate comment
// below for why the two are deliberately not distinguished).
func (v *VectorSearcher) DeleteEmbedding(ctx context.Context, table, tenantID, id string) error {
	if tenantID == "" {
		return fmt.Errorf("delete embedding: tenantID is required (tenant-scoped table %q)", table)
	}
	safeTable, err := validateTable(table)
	if err != nil {
		return errors.Wrap(err, "invalid table name")
	}

	// Validate id
	if err := validateSQLIdentifier(id); err != nil {
		return errors.Wrap(err, "invalid id")
	}

	// Tenant predicate, same boundary as Search()/AddEmbedding: a delete
	// keyed on id alone could remove another tenant's row given its id. The
	// predicate makes that structurally impossible. Zero affected rows is
	// NOT an error (idempotent delete, matching the repositories' Delete
	// contract): "not found" and "owned by a different tenant" are both a
	// no-op for this caller, and no pre-SELECT is done — a check-then-delete
	// would only add a TOCTOU window and a second round trip.
	query := fmt.Sprintf(`DELETE FROM %s WHERE id = $1 AND tenant_id = $2`, safeTable)

	_, err = v.db.ExecContext(ctx, query, id, tenantID)
	if err != nil {
		return errors.Wrap(err, "delete embedding")
	}

	return nil
}

// vectorCollectionDDL returns the CREATE TABLE statement for an ad-hoc vector
// collection. CreateVectorTable and CreateCollection both route through it so
// the column contract is defined exactly once.
//
// tenant_id is part of the schema because Search filters on it
// (`WHERE tenant_id = $3`); a table without the column made every search fail
// with `column "tenant_id" does not exist`.
//
// TODO(tech-debt): DeleteEmbedding still carries no tenant parameter, so a
// delete is keyed on id alone and does not respect the tenant boundary that
// Search() enforces. AddEmbedding was made tenant-scoped (breaking
// storage.VectorStore change, decision locked in review); extending
// DeleteEmbedding the same way stays deferred because it is not part of the
// interface.
func vectorCollectionDDL(table string, dimension int) string {
	return fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id VARCHAR(255) PRIMARY KEY,
			tenant_id TEXT NOT NULL DEFAULT '',
			embedding VECTOR(%d),
			metadata JSONB,
			created_at TIMESTAMP DEFAULT NOW()
		)
	`, table, dimension)
}

// CreateVectorTable creates a table with vector support.
// This is a simplified implementation - in production use proper pgvector setup.
// The vector dimension defaults to defaultVectorDimension (1024) to match the
// migrations; callers needing a different dimension should use CreateCollection.
func (v *VectorSearcher) CreateVectorTable(ctx context.Context, table string, metadataSchema string) error {
	safeTable, err := validateTable(table)
	if err != nil {
		return errors.Wrap(err, "invalid table name")
	}

	// Use the migration-consistent dimension (1024). The previous hardcoded
	// 1536 caused runtime failures because the migrations create VECTOR(1024).
	dim := defaultVectorDimension
	if dim < 1 || dim > 2000 {
		return fmt.Errorf("invalid dimension: %d (must be 1-2000)", dim)
	}

	createTable := vectorCollectionDDL(safeTable, dim)
	if _, err = v.db.ExecContext(ctx, createTable); err != nil {
		return errors.Wrap(err, "create vector table")
	}

	createIndex := fmt.Sprintf(
		`CREATE INDEX IF NOT EXISTS %s_embedding_idx ON %s USING ivfflat (embedding vector_cosine_ops)`,
		safeTable, safeTable,
	)
	if _, err = v.db.ExecContext(ctx, createIndex); err != nil {
		return errors.Wrap(err, "create vector index")
	}

	return nil
}

// CreateCollection creates a vector collection. Implements storage.VectorStore.
func (v *VectorSearcher) CreateCollection(ctx context.Context, name string, dimension int) error {
	safeName, err := validateTable(name)
	if err != nil {
		return errors.Wrap(err, "invalid collection name")
	}
	if dimension < 1 || dimension > 2000 {
		return fmt.Errorf("invalid dimension: %d (must be 1-2000)", dimension)
	}

	query := vectorCollectionDDL(safeName, dimension)
	if _, err := v.db.ExecContext(ctx, query); err != nil {
		return errors.Wrap(err, "create collection")
	}

	// Backfill the column for collections created before this fix:
	// CREATE TABLE IF NOT EXISTS is inert on an existing table, so those
	// tables would keep failing every Search with a missing-column error.
	// Idempotent, so it is safe on the freshly created table too.
	alter := fmt.Sprintf(
		`ALTER TABLE %s ADD COLUMN IF NOT EXISTS tenant_id TEXT NOT NULL DEFAULT ''`,
		safeName,
	)
	if _, err := v.db.ExecContext(ctx, alter); err != nil {
		return errors.Wrap(err, "add tenant_id column")
	}

	indexQuery := fmt.Sprintf(`
		CREATE INDEX IF NOT EXISTS %s_embedding_idx
		ON %s USING ivfflat (embedding vector_cosine_ops)
	`, safeName, safeName)

	if _, err := v.db.ExecContext(ctx, indexQuery); err != nil {
		return errors.Wrap(err, "create vector index")
	}

	return nil
}
