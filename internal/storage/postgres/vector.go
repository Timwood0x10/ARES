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

	query := fmt.Sprintf(`
		SELECT id, 1 - (embedding <=> $1::vector) as distance, metadata
		FROM %s
		WHERE tenant_id = $3
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

// AddEmbedding adds a vector embedding to the specified table.
func (v *VectorSearcher) AddEmbedding(ctx context.Context, table, id string, embedding []float64, metadata map[string]any) error {
	safeTable, err := validateTable(table)
	if err != nil {
		return errors.Wrap(err, "invalid table name")
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
	   INSERT INTO %s (id, embedding, metadata)
	  VALUES ($1, $2::vector, $3)
	 `, safeTable)

	_, err = v.db.ExecContext(ctx, query, id, embeddingJSON, metadataJSON)
	if err != nil {
		return errors.Wrap(err, "add embedding")
	}

	return nil
}

// DeleteEmbedding deletes a vector embedding.
func (v *VectorSearcher) DeleteEmbedding(ctx context.Context, table, id string) error {
	safeTable, err := validateTable(table)
	if err != nil {
		return errors.Wrap(err, "invalid table name")
	}

	// Validate id
	if err := validateSQLIdentifier(id); err != nil {
		return errors.Wrap(err, "invalid id")
	}

	// TODO(tech-debt): the delete is keyed on id alone, so it does not respect
	// the tenant boundary that Search() enforces. Only ids of rows written
	// through AddEmbedding (all under the empty tenant) are addressable in
	// practice, but scoping this properly needs a tenantID parameter — the
	// same deferred storage.VectorStore interface change noted on
	// CreateCollection.
	query := fmt.Sprintf(`DELETE FROM %s WHERE id = $1`, safeTable)

	_, err = v.db.ExecContext(ctx, query, id)
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
// TODO(tech-debt): AddEmbedding/DeleteEmbedding carry no tenant parameter yet,
// so rows written through them land under the empty tenant and are invisible
// to a tenant-scoped Search — fail closed, never a cross-tenant leak. Making
// ad-hoc collections genuinely tenant-scoped requires extending the
// storage.VectorStore interface (a breaking API change), which is deliberately
// deferred pending an explicit decision.
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
