// Package repositories provides data access layer for storage system.
package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	coreerrors "github.com/Timwood0x10/ares/internal/core/errors"
	"github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
)

// KnowledgeRepository provides data access for knowledge chunks.
// This implements CRUD operations and vector search for RAG knowledge base.
// It depends on the DBTX interface to support both database connections and transactions.
// dbPool is retained for transaction operations that require BeginTx.
type KnowledgeRepository struct {
	db     postgres.DBTX
	dbPool *sql.DB
}

// Ensure KnowledgeRepository implements KnowledgeRepositoryInterface.
var _ KnowledgeRepositoryInterface = (*KnowledgeRepository)(nil)

// NewKnowledgeRepository creates a new KnowledgeRepository instance.
// Args:
// db - database connection or transaction implementing DBTX interface.
// dbPool - optional database pool for transaction operations (can be nil for transaction-bound repositories).
// Returns new KnowledgeRepository instance.
func NewKnowledgeRepository(db postgres.DBTX, dbPool *sql.DB) *KnowledgeRepository {
	return &KnowledgeRepository{db: db, dbPool: dbPool}
}


// Create inserts a new knowledge chunk into the database.
// Args:
// ctx - database operation context.
// chunk - knowledge chunk to create.
// Returns error if insert operation fails.
func (r *KnowledgeRepository) Create(ctx context.Context, chunk *storage_models.KnowledgeChunk) error {
	// Convert metadata to JSON for database storage
	metadataJSON, err := json.Marshal(chunk.Metadata)
	if err != nil {
		return errors.Wrap(err, "marshal metadata")
	}

	// Handle nil or empty embedding
	var embeddingStr interface{}
	if len(chunk.Embedding) == 0 {
		// Empty embedding: set to NULL in database
		embeddingStr = nil
	} else {
		// Convert embedding vector to pgvector format
		embeddingStr = postgres.FormatVector(chunk.Embedding)
	}

	// Handle optional document_id
	var documentID interface{}
	if chunk.DocumentID != "" {
		documentID = chunk.DocumentID
	} else {
		documentID = nil
	}

	// Build query with conditional embedding handling
	var query string
	var args []interface{}

	// Check if CreatedAt and UpdatedAt are zero values (0001-01-01)
	// If zero, use NOW() from database instead
	createdAtIsZero := chunk.CreatedAt.IsZero()
	updatedAtIsZero := chunk.UpdatedAt.IsZero()

	if embeddingStr == nil {
		if createdAtIsZero && updatedAtIsZero {
			query = `
				INSERT INTO knowledge_chunks_1024
				(tenant_id, content, embedding, embedding_model, embedding_version,
				 embedding_status, source_type, source, metadata, document_id,
				 chunk_index, content_hash, access_count, created_at, updated_at)
				VALUES ($1, $2, NULL, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NOW(), NOW())
				ON CONFLICT (content_hash) DO UPDATE SET
					access_count = knowledge_chunks_1024.access_count + 1,
					updated_at = NOW()
				RETURNING id
			`
			args = []interface{}{
				chunk.TenantID, chunk.Content,
				chunk.EmbeddingModel, chunk.EmbeddingVersion, chunk.EmbeddingStatus,
				chunk.SourceType, chunk.Source, metadataJSON, documentID,
				chunk.ChunkIndex, chunk.ContentHash, chunk.AccessCount,
			}
		} else {
			query = `
				INSERT INTO knowledge_chunks_1024
				(tenant_id, content, embedding, embedding_model, embedding_version,
				 embedding_status, source_type, source, metadata, document_id,
				 chunk_index, content_hash, access_count, created_at, updated_at)
				VALUES ($1, $2, NULL, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
				ON CONFLICT (content_hash) DO UPDATE SET
					access_count = knowledge_chunks_1024.access_count + 1,
					updated_at = NOW()
				RETURNING id
			`
			args = []interface{}{
				chunk.TenantID, chunk.Content,
				chunk.EmbeddingModel, chunk.EmbeddingVersion, chunk.EmbeddingStatus,
				chunk.SourceType, chunk.Source, metadataJSON, documentID,
				chunk.ChunkIndex, chunk.ContentHash, chunk.AccessCount,
				chunk.CreatedAt, chunk.UpdatedAt,
			}
		}
	} else {
		if createdAtIsZero && updatedAtIsZero {
			query = `
				INSERT INTO knowledge_chunks_1024
				(tenant_id, content, embedding, embedding_model, embedding_version,
				 embedding_status, source_type, source, metadata, document_id,
				 chunk_index, content_hash, access_count, created_at, updated_at)
				VALUES ($1, $2, $3::vector, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, NOW(), NOW())
				ON CONFLICT (content_hash) DO UPDATE SET
					access_count = knowledge_chunks_1024.access_count + 1,
					updated_at = NOW()
				RETURNING id
			`
			args = []interface{}{
				chunk.TenantID, chunk.Content, embeddingStr,
				chunk.EmbeddingModel, chunk.EmbeddingVersion, chunk.EmbeddingStatus,
				chunk.SourceType, chunk.Source, metadataJSON, documentID,
				chunk.ChunkIndex, chunk.ContentHash, chunk.AccessCount,
			}
		} else {
			query = `
				INSERT INTO knowledge_chunks_1024
				(tenant_id, content, embedding, embedding_model, embedding_version,
				 embedding_status, source_type, source, metadata, document_id,
				 chunk_index, content_hash, access_count, created_at, updated_at)
				VALUES ($1, $2, $3::vector, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
				ON CONFLICT (content_hash) DO UPDATE SET
					access_count = knowledge_chunks_1024.access_count + 1,
					updated_at = NOW()
				RETURNING id
			`
			args = []interface{}{
				chunk.TenantID, chunk.Content, embeddingStr,
				chunk.EmbeddingModel, chunk.EmbeddingVersion, chunk.EmbeddingStatus,
				chunk.SourceType, chunk.Source, metadataJSON, documentID,
				chunk.ChunkIndex, chunk.ContentHash, chunk.AccessCount,
				chunk.CreatedAt, chunk.UpdatedAt,
			}
		}
	}

	var id string
	err = r.db.QueryRowContext(ctx, query, args...).Scan(&id)

	if err != nil {
		return errors.Wrap(err, "create knowledge chunk")
	}

	chunk.ID = id
	return nil
}

// CreateBatch inserts multiple knowledge chunks in a single transaction.
// Args:
// ctx - database operation context.
// chunks - knowledge chunks to create.
// Returns error if any insert operation fails or if transaction pool is not available.
// Note: This method fills the ID field for each chunk after successful insertion.
func (r *KnowledgeRepository) CreateBatch(ctx context.Context, chunks []*storage_models.KnowledgeChunk) error {
	if len(chunks) == 0 {
		return nil
	}

	if r.dbPool == nil {
		return coreerrors.ErrNoTransaction
	}

	tx, err := r.dbPool.BeginTx(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "begin transaction")
	}
	committed := false
	defer func() {
		if !committed {
			if err := tx.Rollback(); err != nil {
				slog.Error("Failed to rollback transaction", "error", err)
			}
		}
	}()

	for i, chunk := range chunks {
		// Convert metadata to JSON for database storage
		metadataJSON, err := json.Marshal(chunk.Metadata)
		if err != nil {
			return errors.Wrap(err, "marshal metadata")
		}

		// Handle nil or empty embedding vector
		var embeddingStr interface{}
		if len(chunk.Embedding) == 0 {
			embeddingStr = nil
		} else {
			embeddingStr = postgres.FormatVector(chunk.Embedding)
		}

		// Handle optional document_id
		var documentID interface{}
		if chunk.DocumentID != "" {
			documentID = chunk.DocumentID
		} else {
			documentID = nil
		}

		// Build INSERT dynamically to handle optional embedding and timestamps.
		// Three variable parts:
		//   1. embedding:  NULL (no vector) or ::vector cast
		//   2. created_at: NOW() or $N
		//   3. updated_at: NOW() or $N
		// Using dynamic SQL avoids 4-way branch explosion (embedding×timestamp)
		// while keeping a single query template with positional parameters.
		columns := "tenant_id, content, embedding, embedding_model, embedding_version, " +
			"embedding_status, source_type, source, metadata, document_id, " +
			"chunk_index, content_hash, access_count, created_at, updated_at"

		var embeddingPlaceholder string
		if embeddingStr == nil {
			embeddingPlaceholder = "NULL"
		} else {
			embeddingPlaceholder = "$3::vector"
		}

		createdAtPlaceholder := "$14"
		if chunk.CreatedAt.IsZero() {
			createdAtPlaceholder = "NOW()"
		}
		updatedAtPlaceholder := "$15"
		if chunk.UpdatedAt.IsZero() {
			updatedAtPlaceholder = "NOW()"
		}

		query := fmt.Sprintf(`
			INSERT INTO knowledge_chunks_1024
			(%s)
			VALUES ($1, $2, %s, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, %s, %s)
			ON CONFLICT (content_hash) DO UPDATE SET
				access_count = knowledge_chunks_1024.access_count + 1,
				updated_at = NOW()
			RETURNING id`, columns, embeddingPlaceholder, createdAtPlaceholder, updatedAtPlaceholder) // #nosec G201

		// Always send CreatedAt and UpdatedAt; they are ignored via NOW() placeholder.
		var id string
		qerr := tx.QueryRowContext(ctx, query,
			chunk.TenantID, chunk.Content, embeddingStr,
			chunk.EmbeddingModel, chunk.EmbeddingVersion, chunk.EmbeddingStatus,
			chunk.SourceType, chunk.Source, metadataJSON, documentID,
			chunk.ChunkIndex, chunk.ContentHash, chunk.AccessCount,
			chunk.CreatedAt, chunk.UpdatedAt,
		).Scan(&id)

		if qerr != nil {
			return errors.Wrapf(qerr, "create knowledge chunk %d", i)
		}

		// Fill the ID for the chunk
		chunks[i].ID = id
	}

	if err := tx.Commit(); err != nil {
		return errors.Wrap(err, "commit transaction")
	}
	committed = true

	return nil
}

// GetByID retrieves a knowledge chunk by ID.
// Args:
// ctx - database operation context.
// id - knowledge chunk ID, must be non-empty.
// Returns knowledge chunk or error if not found or invalid argument.
func (r *KnowledgeRepository) GetByID(ctx context.Context, id string) (*storage_models.KnowledgeChunk, error) {
	if id == "" {
		return nil, coreerrors.ErrInvalidArgument
	}

	query := `
		SELECT id, tenant_id, content, embedding::text, embedding_model, embedding_version,
			   embedding_status, source_type, source, metadata::text, document_id,
			   chunk_index, content_hash, access_count, created_at, updated_at
		FROM knowledge_chunks_1024
		WHERE id = $1
	`

	chunk := &storage_models.KnowledgeChunk{}
	var embeddingStr, metadataStr string
	var documentID sql.NullString
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&chunk.ID, &chunk.TenantID, &chunk.Content, &embeddingStr,
		&chunk.EmbeddingModel, &chunk.EmbeddingVersion, &chunk.EmbeddingStatus,
		&chunk.SourceType, &chunk.Source, &metadataStr, &documentID,
		&chunk.ChunkIndex, &chunk.ContentHash, &chunk.AccessCount,
		&chunk.CreatedAt, &chunk.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, coreerrors.ErrRecordNotFound
	}
	if err != nil {
		return nil, errors.Wrap(err, "get knowledge chunk by id")
	}

	// Parse embedding string to float64 array
	chunk.Embedding, err = postgres.ParseVectorString(embeddingStr)
	if err != nil {
		return nil, errors.Wrap(err, "parse embedding")
	}

	// Parse metadata JSON string to map
	if metadataStr != "" {
		if err := json.Unmarshal([]byte(metadataStr), &chunk.Metadata); err != nil {
			return nil, errors.Wrap(err, "parse metadata")
		}
	}

	// Handle nullable document_id
	if documentID.Valid {
		chunk.DocumentID = documentID.String
	}

	return chunk, nil
}

// Update updates an existing knowledge chunk.
// Args:
// ctx - database operation context.
// chunk - knowledge chunk with updated values.
// Returns error if update operation fails.
func (r *KnowledgeRepository) Update(ctx context.Context, chunk *storage_models.KnowledgeChunk) error {
	// Convert metadata to JSON for database storage
	metadataJSON, err := json.Marshal(chunk.Metadata)
	if err != nil {
		return errors.Wrap(err, "marshal metadata")
	}

	// Convert embedding vector to pgvector format
	embeddingStr := postgres.FormatVector(chunk.Embedding)

	// Handle optional document_id
	var documentID interface{}
	if chunk.DocumentID != "" {
		documentID = chunk.DocumentID
	} else {
		documentID = nil
	}

	query := `
		UPDATE knowledge_chunks_1024
		SET content = $2, embedding = $3::vector, embedding_status = $4,
			source_type = $5, source = $6, metadata = $7,
			document_id = $8, chunk_index = $9, access_count = $10, updated_at = NOW()
		WHERE id = $1
	`

	result, err := r.db.ExecContext(ctx, query,
		chunk.ID, chunk.Content, embeddingStr, chunk.EmbeddingStatus,
		chunk.SourceType, chunk.Source, metadataJSON,
		documentID, chunk.ChunkIndex, chunk.AccessCount,
	)
	if err != nil {
		return errors.Wrap(err, "update knowledge chunk")
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "get rows affected")
	}

	if rows == 0 {
		return coreerrors.ErrRecordNotFound
	}

	return nil
}

// Delete removes a knowledge chunk by its ID.
// Args:
// ctx - database operation context.
// id - knowledge chunk identifier.
// Returns error if delete operation fails.
func (r *KnowledgeRepository) Delete(ctx context.Context, id string) error {
	return postgres.DeleteByID(ctx, r.db, "knowledge_chunks_1024", id)
}

// SearchByVector performs vector similarity search.
// Args:
// ctx - database operation context.
// embedding - query vector embedding.
// tenantID - tenant identifier for isolation.
// limit - maximum number of results to return.
// Returns list of similar knowledge chunks ordered by similarity.
func (r *KnowledgeRepository) SearchByVector(ctx context.Context, embedding []float64, tenantID string, limit int) ([]*storage_models.KnowledgeChunk, error) {
	slog.Info("SearchByVector called",
		"embedding_length", len(embedding),
		"tenant_id", tenantID,
		"limit", limit)

	query := `
		SELECT id, tenant_id, content, embedding::text, embedding_model, embedding_version,
			   embedding_status, source_type, source, metadata::text, document_id,
			   chunk_index, content_hash, access_count, created_at, updated_at,
			   1 - (embedding <=> $1::vector) as similarity
		FROM knowledge_chunks_1024
		WHERE tenant_id = $2
		  AND embedding_status = 'completed'
		ORDER BY embedding <=> $1::vector
		LIMIT $3
	`

	// Convert embedding to PostgreSQL vector format
	vectorStr := postgres.FormatVector(embedding)
	previewLen := len(vectorStr)
	if previewLen > 100 {
		previewLen = 100
	}
	slog.Info("Vector search query", "vector_length", len(vectorStr), "vector_preview", vectorStr[:previewLen])

	rows, err := r.db.QueryContext(ctx, query, vectorStr, tenantID, limit)
	if err != nil {
		slog.Error("Vector search query failed", "error", err)
		return nil, errors.Wrap(err, "vector search")
	}

	slog.Info("Vector search query succeeded")
	defer func() { _ = rows.Close() }()

	chunks := make([]*storage_models.KnowledgeChunk, 0)
	rowCount := 0
	for rows.Next() {
		rowCount++
		chunk := &storage_models.KnowledgeChunk{}
		var similarity float64
		var embeddingStr, metadataStr string
		var documentID sql.NullString

		err := rows.Scan(
			&chunk.ID, &chunk.TenantID, &chunk.Content, &embeddingStr,
			&chunk.EmbeddingModel, &chunk.EmbeddingVersion, &chunk.EmbeddingStatus,
			&chunk.SourceType, &chunk.Source, &metadataStr, &documentID,
			&chunk.ChunkIndex, &chunk.ContentHash, &chunk.AccessCount,
			&chunk.CreatedAt, &chunk.UpdatedAt, &similarity,
		)
		if err != nil {
			slog.Warn("Failed to scan row", "row", rowCount, "error", err)
			continue
		}

		// Parse embedding string to []float64
		chunk.Embedding, err = postgres.ParseVectorString(embeddingStr)
		if err != nil {
			slog.Warn("Failed to parse embedding", "row", rowCount, "error", err)
			continue
		}

		// Parse metadata JSON string to map
		if metadataStr != "" {
			if err := json.Unmarshal([]byte(metadataStr), &chunk.Metadata); err != nil {
				slog.Warn("Failed to parse metadata", "row", rowCount, "error", err)
				chunk.Metadata = make(map[string]interface{})
			}
		}

		// Handle nullable document_id
		if documentID.Valid {
			chunk.DocumentID = documentID.String
		}

		// Store similarity in metadata for downstream processing
		// SQL query already computes similarity as: 1 - cosine_distance
		// where cosine_distance range is [0,2], so similarity range is [-1,1]
		if chunk.Metadata == nil {
			chunk.Metadata = make(map[string]interface{})
		}
		chunk.Metadata["similarity"] = similarity
		chunks = append(chunks, chunk)
	}

	if err := rows.Err(); err != nil {
		slog.Error("Failed to iterate knowledge chunks", "error", err)
		return nil, errors.Wrap(err, "iterate knowledge chunks")
	}

	slog.Info("Vector search completed", "rows_scanned", rowCount, "chunks_returned", len(chunks))

	return chunks, nil
}

// SearchByKeyword performs full-text search using BM25.
// Args:
// ctx - database operation context.
// query - search query text.
// tenantID - tenant identifier for isolation.
// limit - maximum number of results to return.
// Returns list of matching knowledge chunks ordered by relevance.
func (r *KnowledgeRepository) SearchByKeyword(ctx context.Context, query, tenantID string, limit int) ([]*storage_models.KnowledgeChunk, error) {
	sqlQuery := `
        SELECT id, tenant_id, content, embedding::text, embedding_model, embedding_version,
               embedding_status, source_type, source, metadata::text, document_id,
               chunk_index, content_hash, access_count, created_at, updated_at,
               ts_rank(tsv, plainto_tsquery('simple', $1)) as score
        FROM knowledge_chunks_1024
        WHERE tsv @@ plainto_tsquery('simple', $1)
          AND tenant_id = $2
          AND embedding_status = 'completed'
        ORDER BY ts_rank(tsv, plainto_tsquery('simple', $1)) DESC
        LIMIT $3
    `

	rows, err := r.db.QueryContext(ctx, sqlQuery, query, tenantID, limit)
	if err != nil {
		return nil, errors.Wrap(err, "keyword search")
	}
	defer func() { _ = rows.Close() }()

	chunks := make([]*storage_models.KnowledgeChunk, 0)
	for rows.Next() {
		chunk := &storage_models.KnowledgeChunk{}
		var score float64
		var embeddingStr, metadataStr string
		var documentID sql.NullString
		err := rows.Scan(
			&chunk.ID, &chunk.TenantID, &chunk.Content, &embeddingStr,
			&chunk.EmbeddingModel, &chunk.EmbeddingVersion, &chunk.EmbeddingStatus,
			&chunk.SourceType, &chunk.Source, &metadataStr, &documentID,
			&chunk.ChunkIndex, &chunk.ContentHash, &chunk.AccessCount,
			&chunk.CreatedAt, &chunk.UpdatedAt, &score,
		)
		if err != nil {
			continue
		}

		chunk.Embedding, err = postgres.ParseVectorString(embeddingStr)
		if err != nil {
			slog.WarnContext(ctx, "Failed to parse embedding in keyword search", "error", err)
			continue
		}

		if metadataStr != "" {
			if err := json.Unmarshal([]byte(metadataStr), &chunk.Metadata); err != nil {
				chunk.Metadata = make(map[string]interface{})
			}
		}

		if documentID.Valid {
			chunk.DocumentID = documentID.String
		}

		if chunk.Metadata == nil {
			chunk.Metadata = make(map[string]interface{})
		}
		chunk.Metadata["keyword_score"] = score
		chunks = append(chunks, chunk)
	}

	if err := rows.Err(); err != nil {
		slog.Error("Failed to iterate knowledge chunks", "error", err)
		return nil, errors.Wrap(err, "iterate knowledge chunks")
	}

	return chunks, nil
}

// ListByDocument retrieves all chunks for a specific document.
// Args:
// ctx - database operation context.
// documentID - document identifier.
// tenantID - tenant identifier for isolation.
// Returns list of knowledge chunks ordered by chunk index.
func (r *KnowledgeRepository) ListByDocument(ctx context.Context, documentID, tenantID string) ([]*storage_models.KnowledgeChunk, error) {
	query := `
        SELECT id, tenant_id, content, embedding::text, embedding_model, embedding_version,
               embedding_status, source_type, source, metadata::text, document_id,
               chunk_index, content_hash, access_count, created_at, updated_at
        FROM knowledge_chunks_1024
        WHERE document_id = $1 AND tenant_id = $2
        ORDER BY chunk_index ASC
    `

	rows, err := r.db.QueryContext(ctx, query, documentID, tenantID)
	if err != nil {
		return nil, errors.Wrap(err, "list chunks by document")
	}
	defer func() { _ = rows.Close() }()

	chunks := make([]*storage_models.KnowledgeChunk, 0)
	for rows.Next() {
		chunk := &storage_models.KnowledgeChunk{}
		var embeddingStr, metadataStr string
		var docID sql.NullString
		err := rows.Scan(
			&chunk.ID, &chunk.TenantID, &chunk.Content, &embeddingStr,
			&chunk.EmbeddingModel, &chunk.EmbeddingVersion, &chunk.EmbeddingStatus,
			&chunk.SourceType, &chunk.Source, &metadataStr, &docID,
			&chunk.ChunkIndex, &chunk.ContentHash, &chunk.AccessCount,
			&chunk.CreatedAt, &chunk.UpdatedAt,
		)
		if err != nil {
			continue
		}

		chunk.Embedding, err = postgres.ParseVectorString(embeddingStr)
		if err != nil {
			slog.WarnContext(ctx, "Failed to parse embedding in ListByDocument", "error", err)
			continue
		}

		if metadataStr != "" {
			if err := json.Unmarshal([]byte(metadataStr), &chunk.Metadata); err != nil {
				chunk.Metadata = make(map[string]interface{})
			}
		}

		if docID.Valid {
			chunk.DocumentID = docID.String
		}

		chunks = append(chunks, chunk)
	}

	if err := rows.Err(); err != nil {
		slog.Error("Failed to iterate knowledge chunks", "error", err)
		return nil, errors.Wrap(err, "iterate knowledge chunks")
	}

	return chunks, nil
}

// SearchBySubstring performs exact substring matching using ILIKE.
// This is used for precision mode to find exact matches in content.
// Args:
// ctx - database operation context.
// query - search query string for substring matching.
// tenantID - tenant identifier for isolation.
// limit - maximum number of results to return.
const maxSearchBySubstringLimit = 100

// Returns list of knowledge chunks matching the substring or error if search fails.
func (r *KnowledgeRepository) SearchBySubstring(ctx context.Context, query, tenantID string, limit int) ([]*storage_models.KnowledgeChunk, error) {
	if limit <= 0 {
		limit = 5
	}
	if limit > maxSearchBySubstringLimit {
		limit = maxSearchBySubstringLimit
	}

	escapedQuery := postgres.EscapeILIKEPattern(query)

	sqlQuery := `
        SELECT id, tenant_id, content, embedding::text, embedding_model, embedding_version,
               embedding_status, source_type, source, metadata::text, document_id,
               chunk_index, content_hash, access_count, created_at, updated_at
        FROM knowledge_chunks_1024
        WHERE content ILIKE '%' || $1 || '%' ESCAPE '\'
          AND tenant_id = $2
          AND embedding_status = 'completed'
        ORDER BY created_at DESC
        LIMIT $3
    `

	rows, err := r.db.QueryContext(ctx, sqlQuery, escapedQuery, tenantID, limit)
	if err != nil {
		return nil, errors.Wrap(err, "substring search")
	}
	defer func() { _ = rows.Close() }()

	chunks := make([]*storage_models.KnowledgeChunk, 0)
	for rows.Next() {
		chunk := &storage_models.KnowledgeChunk{}
		var embeddingStr, metadataStr string
		var documentID sql.NullString
		err := rows.Scan(
			&chunk.ID, &chunk.TenantID, &chunk.Content, &embeddingStr,
			&chunk.EmbeddingModel, &chunk.EmbeddingVersion, &chunk.EmbeddingStatus,
			&chunk.SourceType, &chunk.Source, &metadataStr, &documentID,
			&chunk.ChunkIndex, &chunk.ContentHash, &chunk.AccessCount,
			&chunk.CreatedAt, &chunk.UpdatedAt,
		)
		if err != nil {
			continue
		}

		chunk.Embedding, err = postgres.ParseVectorString(embeddingStr)
		if err != nil {
			slog.WarnContext(ctx, "Failed to parse embedding in SearchBySubstring", "error", err)
			continue
		}

		if metadataStr != "" {
			if err := json.Unmarshal([]byte(metadataStr), &chunk.Metadata); err != nil {
				chunk.Metadata = make(map[string]interface{})
			}
		}

		if documentID.Valid {
			chunk.DocumentID = documentID.String
		}

		chunks = append(chunks, chunk)
	}

	if err := rows.Err(); err != nil {
		slog.Error("Failed to iterate knowledge chunks", "error", err)
		return nil, errors.Wrap(err, "iterate knowledge chunks")
	}

	return chunks, nil
}

// UpdateEmbedding updates the embedding for a knowledge chunk.
// Args:
// ctx - database operation context.
// id - knowledge chunk identifier.
// embedding - vector embedding.
// model - embedding model name.
// version - embedding model version.
// Returns error if update operation fails.
func (r *KnowledgeRepository) UpdateEmbedding(ctx context.Context, id string, embedding []float64, model string, version int) error {
	embeddingStr := postgres.FormatVector(embedding)

	query := `
        UPDATE knowledge_chunks_1024
        SET embedding = $2::vector, embedding_model = $3, embedding_version = $4,
            embedding_status = 'completed', embedding_processed_at = NOW(),
            updated_at = NOW()
        WHERE id = $1
    `

	result, err := r.db.ExecContext(ctx, query, id, embeddingStr, model, version)
	if err != nil {
		return errors.Wrap(err, "update embedding")
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "get rows affected")
	}

	if rows == 0 {
		return coreerrors.ErrRecordNotFound
	}

	return nil
}

// UpdateEmbeddingStatus updates the embedding processing status.
// Args:
// ctx - database operation context.
// id - knowledge chunk identifier.
// status - new embedding status.
// error - error message if status is failed.
// Returns error if update operation fails.
func (r *KnowledgeRepository) UpdateEmbeddingStatus(ctx context.Context, id, status, errorMsg string) error {
	query := `
		UPDATE knowledge_chunks_1024
		SET embedding_status = $2, embedding_error = $3, updated_at = NOW()
		WHERE id = $1
	`

	result, err := r.db.ExecContext(ctx, query, id, status, errorMsg)
	if err != nil {
		return errors.Wrap(err, "update embedding status")
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "get rows affected")
	}

	if rows == 0 {
		return coreerrors.ErrRecordNotFound
	}

	return nil
}

// CleanupExpired removes knowledge chunks that are no longer needed.
// Args:
// ctx - database operation context.
// olderThan - cutoff time for deletion.
// Returns number of deleted chunks or error if operation fails.
func (r *KnowledgeRepository) CleanupExpired(ctx context.Context, olderThan time.Time) (int64, error) {
	query := `
		DELETE FROM knowledge_chunks_1024
		WHERE updated_at < $1
		  AND access_count < 10
	`

	result, err := r.db.ExecContext(ctx, query, olderThan)
	if err != nil {
		return 0, errors.Wrap(err, "cleanup expired chunks")
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return 0, errors.Wrap(err, "get rows affected")
	}

	return rows, nil
}
