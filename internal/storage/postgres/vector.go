package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	coreerrors "goagentx/internal/core/errors"
	"goagentx/internal/errors"
	"goagentx/internal/storage"
)

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
// Deprecated: Use storage.SearchResult directly.
type SearchResult = storage.SearchResult

// Search performs a vector similarity search.
// This is a simplified implementation that uses pgvector if available.
func (v *VectorSearcher) Search(ctx context.Context, table string, embedding []float64, limit int) ([]*SearchResult, error) {
	// Validate table name to prevent SQL injection
	if err := sanitizeSQLTable(table); err != nil {
		return nil, errors.Wrap(err, "invalid table name")
	}

	// Validate limit to prevent excessive results
	// Use configured max vector search limit
	maxLimit := v.embeddingConfig.MaxVectorSearchLimit
	if limit <= 0 || limit > maxLimit {
		return nil, fmt.Errorf("invalid limit: %d (must be 1-%d)", limit, maxLimit)
	}

	safeTable, err := safeFormatTable(table)
	if err != nil {
		return nil, errors.Wrap(err, "format table name")
	}

	query := fmt.Sprintf(`
		SELECT id, 1 - (embedding <=> $1) as distance, metadata
		FROM %s
		ORDER BY embedding <=> $1
		LIMIT $2
	`, safeTable)

	embeddingJSON, err := json.Marshal(embedding)
	if err != nil {
		return nil, errors.Wrap(err, "marshal embedding")
	}

	rows, err := v.db.QueryContext(ctx, query, embeddingJSON, limit)
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
		return nil, coreerrors.ErrRecordNotFound
	}

	return results, nil
}

// AddEmbedding adds a vector embedding to the specified table.
func (v *VectorSearcher) AddEmbedding(ctx context.Context, table, id string, embedding []float64, metadata map[string]any) error {
	// Validate table name to prevent SQL injection
	if err := sanitizeSQLTable(table); err != nil {
		return errors.Wrap(err, "invalid table name")
	}

	// Validate embedding dimensions.
	// Maximum supported dimension for pgvector is 2000.
	const maxDimension = 2000
	if len(embedding) == 0 {
		return fmt.Errorf("embedding cannot be empty")
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

	safeTable, err := safeFormatTable(table)
	if err != nil {
		return errors.Wrap(err, "format table name")
	}

	query := fmt.Sprintf(`
		INSERT INTO %s (id, embedding, metadata)
		VALUES ($1, $2, $3)
	`, safeTable)

	_, err = v.db.ExecContext(ctx, query, id, embeddingJSON, metadataJSON)
	if err != nil {
		return errors.Wrap(err, "add embedding")
	}

	return nil
}

// DeleteEmbedding deletes a vector embedding.
func (v *VectorSearcher) DeleteEmbedding(ctx context.Context, table, id string) error {
	// Validate table name to prevent SQL injection
	if err := sanitizeSQLTable(table); err != nil {
		return errors.Wrap(err, "invalid table name")
	}

	// Validate id
	if err := validateSQLIdentifier(id); err != nil {
		return errors.Wrap(err, "invalid id")
	}

	safeTable, err := safeFormatTable(table)
	if err != nil {
		return errors.Wrap(err, "format table name")
	}

	query := fmt.Sprintf(`DELETE FROM %s WHERE id = $1`, safeTable)

	_, err = v.db.ExecContext(ctx, query, id)
	if err != nil {
		return errors.Wrap(err, "delete embedding")
	}

	return nil
}

// CreateVectorTable creates a table with vector support.
// This is a simplified implementation - in production use proper pgvector setup.
func (v *VectorSearcher) CreateVectorTable(ctx context.Context, table string, metadataSchema string) error {
	// Validate table name to prevent SQL injection
	if err := sanitizeSQLTable(table); err != nil {
		return errors.Wrap(err, "invalid table name")
	}

	// Validate dimension (should be between 1 and 2000)
	// Default dimension for common embedding models (OpenAI: 1536, e5-large: 1024)
	// This should be configurable based on the model being used
	dim := 1536
	if dim < 1 || dim > 2000 {
		return fmt.Errorf("invalid dimension: %d (must be 1-2000)", dim)
	}

	safeTable, err := safeFormatTable(table)
	if err != nil {
		return errors.Wrap(err, "format table name")
	}

	query := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id VARCHAR(255) PRIMARY KEY,
			embedding VECTOR(%d),
			metadata JSONB,
			created_at TIMESTAMP DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS %s_embedding_idx ON %s USING ivfflat (embedding vector_cosine_ops);
	`, safeTable, dim, safeTable, safeTable)

	_, err = v.db.ExecContext(ctx, query)
	if err != nil {
		return errors.Wrap(err, "create vector table")
	}

	return nil
}

// CreateCollection creates a vector collection. Implements storage.VectorStore.
func (v *VectorSearcher) CreateCollection(ctx context.Context, name string, dimension int) error {
	if err := sanitizeSQLTable(name); err != nil {
		return errors.Wrap(err, "invalid collection name")
	}
	if dimension < 1 || dimension > 2000 {
		return fmt.Errorf("invalid dimension: %d (must be 1-2000)", dimension)
	}

	safeName, err := safeFormatTable(name)
	if err != nil {
		return errors.Wrap(err, "format collection name")
	}

	query := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			id VARCHAR(255) PRIMARY KEY,
			embedding VECTOR(%d),
			metadata JSONB,
			created_at TIMESTAMP DEFAULT NOW()
		)
	`, safeName, dimension)

	if _, err := v.db.ExecContext(ctx, query); err != nil {
		return errors.Wrap(err, "create collection")
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
