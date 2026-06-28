// Package repositories provides data access layer for storage system.
package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"

	coreerrors "github.com/Timwood0x10/ares/internal/core/errors"
	"github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
)

// ExperienceRepository provides data access for agent experiences.
// This implements CRUD operations and vector search for experience storage.
// It depends on the DBTX interface to support both database connections and transactions.
type ExperienceRepository struct {
	db postgres.DBTX
}

// NewExperienceRepository creates a new ExperienceRepository instance.
// Args:
// db - database connection or transaction implementing DBTX interface.
// Returns new ExperienceRepository instance.
func NewExperienceRepository(db postgres.DBTX) *ExperienceRepository {
	return &ExperienceRepository{db: db}
}

// Create inserts a new experience into the database.
// Args:
// ctx - database operation context.
// exp - experience to create. ID should be empty to let database generate it.
// Returns error if insert operation fails.
func (r *ExperienceRepository) Create(ctx context.Context, exp *storage_models.Experience) error {
	// Convert metadata to JSON for database storage
	metadataJSON, err := json.Marshal(exp.Metadata)
	if err != nil {
		return errors.Wrap(err, "marshal metadata")
	}

	// Convert embedding to pgvector format
	embeddingStr := postgres.FormatVector(exp.Embedding)

	// Build query with optional decay_at and created_at
	var query string
	var args []interface{}

	// Check if CreatedAt is zero value (0001-01-01)
	// If zero, use NOW() from database instead
	createdAtIsZero := exp.CreatedAt.IsZero()

	if exp.DecayAt.IsZero() {
		// Don't set decay_at, let database use default value
		if createdAtIsZero {
			query = `
				INSERT INTO experiences_1024
				(tenant_id, type, input, output, embedding, embedding_model, embedding_version,
				 score, success, agent_id, metadata, created_at)
				VALUES ($1, $2, $3, $4, $5::vector, $6, $7, $8, $9, $10, $11, NOW())
				RETURNING id
			`
			args = []interface{}{
				exp.TenantID, exp.Type, exp.Input, exp.Output, embeddingStr,
				exp.EmbeddingModel, exp.EmbeddingVersion,
				exp.Score, exp.Success, exp.AgentID, metadataJSON,
			}
		} else {
			query = `
				INSERT INTO experiences_1024
				(tenant_id, type, input, output, embedding, embedding_model, embedding_version,
				 score, success, agent_id, metadata, created_at)
				VALUES ($1, $2, $3, $4, $5::vector, $6, $7, $8, $9, $10, $11, $12)
				RETURNING id
			`
			args = []interface{}{
				exp.TenantID, exp.Type, exp.Input, exp.Output, embeddingStr,
				exp.EmbeddingModel, exp.EmbeddingVersion,
				exp.Score, exp.Success, exp.AgentID, metadataJSON,
				exp.CreatedAt,
			}
		}
	} else {
		// Set decay_at explicitly
		if createdAtIsZero {
			query = `
				INSERT INTO experiences_1024
				(tenant_id, type, input, output, embedding, embedding_model, embedding_version,
				 score, success, agent_id, metadata, decay_at, created_at)
				VALUES ($1, $2, $3, $4, $5::vector, $6, $7, $8, $9, $10, $11, $12, NOW())
				RETURNING id
			`
			args = []interface{}{
				exp.TenantID, exp.Type, exp.Input, exp.Output, embeddingStr,
				exp.EmbeddingModel, exp.EmbeddingVersion,
				exp.Score, exp.Success, exp.AgentID, metadataJSON,
				exp.DecayAt,
			}
		} else {
			query = `
				INSERT INTO experiences_1024
				(tenant_id, type, input, output, embedding, embedding_model, embedding_version,
				 score, success, agent_id, metadata, decay_at, created_at)
				VALUES ($1, $2, $3, $4, $5::vector, $6, $7, $8, $9, $10, $11, $12, $13)
				RETURNING id
			`
			args = []interface{}{
				exp.TenantID, exp.Type, exp.Input, exp.Output, embeddingStr,
				exp.EmbeddingModel, exp.EmbeddingVersion,
				exp.Score, exp.Success, exp.AgentID, metadataJSON,
				exp.DecayAt, exp.CreatedAt,
			}
		}
	}

	var id string
	err = r.db.QueryRowContext(ctx, query, args...).Scan(&id)

	if err != nil {
		return errors.Wrap(err, "create experience")
	}

	exp.ID = id
	return nil
}

// GetByID retrieves an experience by ID.
// Args:
// ctx - database operation context.
// id - experience ID, must be non-empty.
// Returns experience or error if not found or invalid argument.
func (r *ExperienceRepository) GetByID(ctx context.Context, id string) (*storage_models.Experience, error) {
	if id == "" {
		return nil, coreerrors.ErrInvalidArgument
	}

	query := `
		SELECT id, tenant_id, type, input, output, embedding_model, embedding_version,
			   score, success, agent_id, metadata::text, decay_at, created_at
		FROM experiences_1024
		WHERE id = $1
	`

	exp := &storage_models.Experience{}
	var metadataStr string
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&exp.ID, &exp.TenantID, &exp.Type, &exp.Input, &exp.Output,
		&exp.EmbeddingModel, &exp.EmbeddingVersion,
		&exp.Score, &exp.Success, &exp.AgentID, &metadataStr,
		&exp.DecayAt, &exp.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, coreerrors.ErrRecordNotFound
	}
	if err != nil {
		return nil, errors.Wrap(err, "get experience by id")
	}

	// Parse metadata JSON string to map
	if metadataStr != "" {
		if err := json.Unmarshal([]byte(metadataStr), &exp.Metadata); err != nil {
			return nil, errors.Wrap(err, "parse metadata")
		}
	}

	return exp, nil
}

// Update updates an existing experience.
// Args:
// ctx - database operation context.
// exp - experience with updated values.
// Returns error if update operation fails.
func (r *ExperienceRepository) Update(ctx context.Context, exp *storage_models.Experience) error {
	// Convert metadata to JSON for database storage
	metadataJSON, err := json.Marshal(exp.Metadata)
	if err != nil {
		return errors.Wrap(err, "marshal metadata")
	}

	// Convert embedding to pgvector format
	embeddingStr := postgres.FormatVector(exp.Embedding)

	query := `
		UPDATE experiences_1024
		SET type = $2, input = $3, output = $4, embedding = $5::vector,
			embedding_model = $6, embedding_version = $7, score = $8,
			success = $9, agent_id = $10, metadata = $11
		WHERE id = $1
	`

	result, err := r.db.ExecContext(ctx, query,
		exp.ID, exp.Type, exp.Input, exp.Output, embeddingStr,
		exp.EmbeddingModel, exp.EmbeddingVersion, exp.Score,
		exp.Success, exp.AgentID, metadataJSON,
	)
	if err != nil {
		return errors.Wrap(err, "update experience")
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

// Delete removes an experience by its ID.
// Args:
// ctx - database operation context.
// id - experience identifier.
// Returns error if delete operation fails.
func (r *ExperienceRepository) Delete(ctx context.Context, id string) error {
	return postgres.DeleteByID(ctx, r.db, "experiences_1024", id)
}

// SearchByVector performs vector similarity search for experiences.
// Args:
// ctx - database operation context.
// embedding - query vector embedding.
// tenantID - tenant identifier for isolation.
// limit - maximum number of results to return.
// Returns list of similar experiences ordered by similarity.
func (r *ExperienceRepository) SearchByVector(ctx context.Context, embedding []float64, tenantID string, limit int) ([]*storage_models.Experience, error) {
	// Convert embedding to pgvector format
	embeddingStr := postgres.FormatVector(embedding)

	query := `
		SELECT id, tenant_id, type, input, output, embedding::text, embedding_model, embedding_version,
			   score, success, agent_id, metadata::text, decay_at, created_at,
			   1 - (embedding <=> $1::vector) as similarity
		FROM experiences_1024
		WHERE tenant_id = $2
		  AND (decay_at IS NULL OR decay_at > NOW())
		ORDER BY embedding <=> $1::vector
		LIMIT $3
	`

	rows, err := r.db.QueryContext(ctx, query, embeddingStr, tenantID, limit)
	if err != nil {
		return nil, errors.Wrap(err, "vector search")
	}
	defer func() { _ = rows.Close() }()

	experiences := make([]*storage_models.Experience, 0)
	for rows.Next() {
		exp := &storage_models.Experience{}
		var similarity float64
		var embeddingStr, metadataStr string
		err := rows.Scan(
			&exp.ID, &exp.TenantID, &exp.Type, &exp.Input, &exp.Output,
			&embeddingStr, &exp.EmbeddingModel, &exp.EmbeddingVersion,
			&exp.Score, &exp.Success, &exp.AgentID, &metadataStr,
			&exp.DecayAt, &exp.CreatedAt, &similarity,
		)
		if err != nil {
			continue
		}

		// Parse embedding string to float64 array
		exp.Embedding, err = postgres.ParseVectorString(embeddingStr)
		if err != nil {
			continue
		}

		// Parse metadata JSON string to map
		if metadataStr != "" {
			if err := json.Unmarshal([]byte(metadataStr), &exp.Metadata); err != nil {
				exp.Metadata = make(map[string]interface{})
			}
		}

		if exp.Metadata == nil {
			exp.Metadata = make(map[string]interface{})
		}
		// SQL query already computes similarity as: 1 - cosine_distance
		// where cosine_distance range is [0,2], so similarity range is [-1,1]
		exp.Metadata["similarity"] = similarity
		experiences = append(experiences, exp)
	}

	if err := rows.Err(); err != nil {
		slog.Error("Failed to iterate experiences", "error", err)
		return nil, errors.Wrap(err, "iterate experiences")
	}

	return experiences, nil
}

// SearchByKeyword performs keyword-based search for experiences using BM25-style matching.
// This uses PostgreSQL ILIKE for case-insensitive text search.
// Args:
// ctx - database operation context.
// query - search query text.
// tenantID - tenant identifier for isolation.
// limit - maximum number of results to return.
// Returns list of matching experiences ordered by score and recency.
func (r *ExperienceRepository) SearchByKeyword(ctx context.Context, query, tenantID string, limit int) ([]*storage_models.Experience, error) {
	if query == "" {
		return []*storage_models.Experience{}, nil
	}

	escapedQuery := postgres.EscapeILIKEPattern(query)

	sqlQuery := `
        SELECT id, tenant_id, type, input, output, embedding_model, embedding_version,
               score, success, agent_id, metadata::text, decay_at, created_at
        FROM experiences_1024
        WHERE (input ILIKE '%' || $1 || '%' ESCAPE '\' OR output ILIKE '%' || $1 || '%' ESCAPE '\')
          AND tenant_id = $2
          AND (decay_at IS NULL OR decay_at > NOW())
        ORDER BY score DESC, created_at DESC
        LIMIT $3
    `

	rows, err := r.db.QueryContext(ctx, sqlQuery, escapedQuery, tenantID, limit)
	if err != nil {
		return nil, errors.Wrap(err, "keyword search")
	}
	defer func() { _ = rows.Close() }()

	experiences := make([]*storage_models.Experience, 0)
	for rows.Next() {
		exp := &storage_models.Experience{}
		var metadataStr string
		err := rows.Scan(
			&exp.ID, &exp.TenantID, &exp.Type, &exp.Input, &exp.Output,
			&exp.EmbeddingModel, &exp.EmbeddingVersion,
			&exp.Score, &exp.Success, &exp.AgentID, &metadataStr,
			&exp.DecayAt, &exp.CreatedAt,
		)
		if err != nil {
			continue
		}

		// Parse metadata JSON string to map
		if metadataStr != "" {
			if err := json.Unmarshal([]byte(metadataStr), &exp.Metadata); err != nil {
				exp.Metadata = make(map[string]interface{})
			}
		}

		experiences = append(experiences, exp)
	}

	if err := rows.Err(); err != nil {
		slog.Error("Failed to iterate experiences", "error", err)
		return nil, errors.Wrap(err, "iterate experiences")
	}

	return experiences, nil
}

// ListByType retrieves experiences by type.
// Args:
// ctx - database operation context.
// expType - experience type filter.
// tenantID - tenant identifier for isolation.
// limit - maximum number of results to return.
// Returns list of experiences ordered by score (descending).
func (r *ExperienceRepository) ListByType(ctx context.Context, expType, tenantID string, limit int) ([]*storage_models.Experience, error) {
	query := `
		SELECT id, tenant_id, type, input, output, embedding_model, embedding_version,
			   score, success, agent_id, metadata::text, decay_at, created_at
		FROM experiences_1024
		WHERE type = $1
		  AND tenant_id = $2
		  AND (decay_at IS NULL OR decay_at > NOW())
		ORDER BY score DESC, created_at DESC
		LIMIT $3
	`

	rows, err := r.db.QueryContext(ctx, query, expType, tenantID, limit)
	if err != nil {
		return nil, errors.Wrap(err, "list experiences by type")
	}
	defer func() { _ = rows.Close() }()

	experiences := make([]*storage_models.Experience, 0)
	for rows.Next() {
		exp := &storage_models.Experience{}
		var metadataStr string
		err := rows.Scan(
			&exp.ID, &exp.TenantID, &exp.Type, &exp.Input, &exp.Output,
			&exp.EmbeddingModel, &exp.EmbeddingVersion,
			&exp.Score, &exp.Success, &exp.AgentID, &metadataStr,
			&exp.DecayAt, &exp.CreatedAt,
		)
		if err != nil {
			continue
		}

		// Parse metadata JSON string to map
		if metadataStr != "" {
			if err := json.Unmarshal([]byte(metadataStr), &exp.Metadata); err != nil {
				exp.Metadata = make(map[string]interface{})
			}
		}

		experiences = append(experiences, exp)
	}

	if err := rows.Err(); err != nil {
		slog.Error("Failed to iterate experiences", "error", err)
		return nil, errors.Wrap(err, "iterate experiences")
	}

	return experiences, nil
}

// UpdateScore updates the score of an experience.
// Args:
// ctx - database operation context.
// id - experience identifier.
// score - new score value (0-1).
// Returns error if update operation fails.
func (r *ExperienceRepository) UpdateScore(ctx context.Context, id string, score float64) error {
	query := `
		UPDATE experiences_1024
		SET score = $2
		WHERE id = $1
	`

	result, err := r.db.ExecContext(ctx, query, id, score)
	if err != nil {
		return errors.Wrap(err, "update experience score")
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

// ListByAgent retrieves experiences for a specific agent.
// Args:
// ctx - database operation context.
// agentID - agent identifier.
// tenantID - tenant identifier for isolation.
// limit - maximum number of results to return.
// Returns list of experiences ordered by created time (descending).
func (r *ExperienceRepository) ListByAgent(ctx context.Context, agentID, tenantID string, limit int) ([]*storage_models.Experience, error) {
	query := `
		SELECT id, tenant_id, type, input, output, embedding_model, embedding_version,
			   score, success, agent_id, metadata::text, decay_at, created_at
		FROM experiences_1024
		WHERE agent_id = $1
		  AND tenant_id = $2
		  AND (decay_at IS NULL OR decay_at > NOW())
		ORDER BY created_at DESC
		LIMIT $3
	`

	rows, err := r.db.QueryContext(ctx, query, agentID, tenantID, limit)
	if err != nil {
		return nil, errors.Wrap(err, "list experiences by agent")
	}
	defer func() { _ = rows.Close() }()

	experiences := make([]*storage_models.Experience, 0)
	for rows.Next() {
		exp := &storage_models.Experience{}
		var metadataStr string
		err := rows.Scan(
			&exp.ID, &exp.TenantID, &exp.Type, &exp.Input, &exp.Output,
			&exp.EmbeddingModel, &exp.EmbeddingVersion,
			&exp.Score, &exp.Success, &exp.AgentID, &metadataStr,
			&exp.DecayAt, &exp.CreatedAt,
		)
		if err != nil {
			continue
		}

		// Parse metadata JSON string to map
		if metadataStr != "" {
			if err := json.Unmarshal([]byte(metadataStr), &exp.Metadata); err != nil {
				exp.Metadata = make(map[string]interface{})
			}
		}

		experiences = append(experiences, exp)
	}

	if err := rows.Err(); err != nil {
		slog.Error("Failed to iterate experiences", "error", err)
		return nil, errors.Wrap(err, "iterate experiences")
	}

	return experiences, nil
}

// UpdateEmbedding updates the embedding for an experience.
// Args:
// ctx - database operation context.
// id - experience identifier.
// embedding - vector embedding.
// model - embedding model name.
// version - embedding model version.
// Returns error if update operation fails.
func (r *ExperienceRepository) UpdateEmbedding(ctx context.Context, id string, embedding []float64, model string, version int) error {
	// Convert embedding to pgvector format
	embeddingStr := postgres.FormatVector(embedding)

	query := `
		UPDATE experiences_1024
		SET embedding = $2::vector, embedding_model = $3, embedding_version = $4
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

// CleanupExpired removes experiences that have decayed.
// Args:
// ctx - database operation context.
// Returns number of deleted experiences or error if operation fails.
func (r *ExperienceRepository) CleanupExpired(ctx context.Context) (int64, error) {
	query := `
		DELETE FROM experiences_1024
		WHERE decay_at IS NOT NULL AND decay_at < NOW()
	`

	result, err := r.db.ExecContext(ctx, query)
	if err != nil {
		return 0, errors.Wrap(err, "cleanup expired experiences")
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return 0, errors.Wrap(err, "get rows affected")
	}

	return rows, nil
}

// GetStatistics returns statistics about experiences.
// Args:
// ctx - database operation context.
// tenantID - tenant identifier for isolation.
// Returns experience statistics or error if query fails.
func (r *ExperienceRepository) GetStatistics(ctx context.Context, tenantID string) (map[string]int64, error) {
	query := `
		SELECT
			type,
			COUNT(*) as count
		FROM experiences_1024
		WHERE tenant_id = $1
		  AND (decay_at IS NULL OR decay_at > NOW())
		GROUP BY type
	`

	rows, err := r.db.QueryContext(ctx, query, tenantID)
	if err != nil {
		return nil, errors.Wrap(err, "get experience statistics")
	}
	defer func() { _ = rows.Close() }()

	stats := make(map[string]int64)
	for rows.Next() {
		var expType string
		var count int64
		if err := rows.Scan(&expType, &count); err != nil {
			continue
		}
		stats[expType] = count
	}

	if err := rows.Err(); err != nil {
		slog.Error("Failed to iterate experience statistics", "error", err)
		return nil, errors.Wrap(err, "iterate experience statistics")
	}

	return stats, nil
}

// IncrementUsageCount increments the usage count of an experience.
// This is used as a reinforcement signal when an experience is successfully used.
// Args:
// ctx - database operation context.
// id - experience identifier.
// Returns error if update operation fails.
func (r *ExperienceRepository) IncrementUsageCount(ctx context.Context, id string) error {
	if id == "" {
		return coreerrors.ErrInvalidArgument
	}

	query := `
		UPDATE experiences_1024
		SET usage_count = COALESCE(usage_count, 0) + 1,
		    updated_at = NOW()
		WHERE id = $1
	`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return errors.Wrap(err, "increment usage count")
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

// DecrementRank decreases the score of an experience as negative feedback.
// This applies a penalty to the score when an experience leads to a failed task.
// The penalty is capped to prevent the score from going below 0.
// Args:
// ctx - database operation context.
// id - experience identifier.
// Returns error if update operation fails.
func (r *ExperienceRepository) DecrementRank(ctx context.Context, id string) error {
	if id == "" {
		return coreerrors.ErrInvalidArgument
	}

	// Apply a 10% score penalty, with a floor of 0.
	query := `
		UPDATE experiences_1024
		SET score = GREATEST(score - (score * 0.1), 0),
		    updated_at = NOW()
		WHERE id = $1
	`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return errors.Wrap(err, "decrement rank")
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
