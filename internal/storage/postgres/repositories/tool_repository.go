// Package repositories provides data access layer for storage system.
package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"

	coreerrors "github.com/Timwood0x10/goagent/internal/core/errors"
	"github.com/Timwood0x10/goagent/internal/errors"
	"github.com/Timwood0x10/goagent/internal/storage/postgres"
	storage_models "github.com/Timwood0x10/goagent/internal/storage/postgres/models"
)

// ToolRepository provides data access for tool definitions.
// This implements CRUD operations and semantic search for tools.
// It depends on the DBTX interface to support both database connections and transactions.
type ToolRepository struct {
	db postgres.DBTX
}

// NewToolRepository creates a new ToolRepository instance.
// Args:
// db - database connection or transaction implementing DBTX interface.
// Returns new ToolRepository instance.
func NewToolRepository(db postgres.DBTX) *ToolRepository {
	return &ToolRepository{db: db}
}

// Create inserts a new tool into the database.
// Args:
// ctx - database operation context.
// tool - tool to create.
// Returns error if insert operation fails.
func (r *ToolRepository) Create(ctx context.Context, tool *storage_models.Tool) error {
	// Convert metadata to JSON for database storage
	metadataJSON, err := json.Marshal(tool.Metadata)
	if err != nil {
		return errors.Wrap(err, "marshal metadata")
	}

	// Convert embedding to pgvector format
	embeddingStr := float64ToVectorString(tool.Embedding)

	// Build query based on whether ID is provided
	var query string
	var args []interface{}

	// Check if CreatedAt is zero value (0001-01-01)
	// If zero, use NOW() from database instead
	createdAtIsZero := tool.CreatedAt.IsZero()

	if tool.ID == "" {
		// Insert with auto-generated ID
		if createdAtIsZero {
			query = `
				INSERT INTO tools
				(tenant_id, name, description, embedding, embedding_model, embedding_version,
				 agent_type, tags, usage_count, success_rate, last_used_at, metadata, created_at)
				VALUES ($1, $2, $3, $4::vector, $5, $6, $7, $8, $9, $10, $11, $12, NOW())
				ON CONFLICT (tenant_id, name) DO UPDATE SET
					description = EXCLUDED.description,
					embedding = EXCLUDED.embedding,
					embedding_model = EXCLUDED.embedding_model,
					embedding_version = EXCLUDED.embedding_version,
					agent_type = EXCLUDED.agent_type,
					tags = EXCLUDED.tags,
					metadata = EXCLUDED.metadata
				RETURNING id
			`
			args = []interface{}{
				tool.TenantID, tool.Name, tool.Description,
				embeddingStr, tool.EmbeddingModel, tool.EmbeddingVersion,
				tool.AgentType, tool.Tags, tool.UsageCount, tool.SuccessRate,
				tool.LastUsedAt, metadataJSON,
			}
		} else {
			query = `
				INSERT INTO tools
				(tenant_id, name, description, embedding, embedding_model, embedding_version,
				 agent_type, tags, usage_count, success_rate, last_used_at, metadata, created_at)
				VALUES ($1, $2, $3, $4::vector, $5, $6, $7, $8, $9, $10, $11, $12, $13)
				ON CONFLICT (tenant_id, name) DO UPDATE SET
					description = EXCLUDED.description,
					embedding = EXCLUDED.embedding,
					embedding_model = EXCLUDED.embedding_model,
					embedding_version = EXCLUDED.embedding_version,
					agent_type = EXCLUDED.agent_type,
					tags = EXCLUDED.tags,
					metadata = EXCLUDED.metadata
				RETURNING id
			`
			args = []interface{}{
				tool.TenantID, tool.Name, tool.Description,
				embeddingStr, tool.EmbeddingModel, tool.EmbeddingVersion,
				tool.AgentType, tool.Tags, tool.UsageCount, tool.SuccessRate,
				tool.LastUsedAt, metadataJSON, tool.CreatedAt,
			}
		}
	} else {
		// Insert with specified ID
		if createdAtIsZero {
			query = `
				INSERT INTO tools
				(id, tenant_id, name, description, embedding, embedding_model, embedding_version,
				 agent_type, tags, usage_count, success_rate, last_used_at, metadata, created_at)
				VALUES ($1, $2, $3, $4, $5::vector, $6, $7, $8, $9, $10, $11, $12, $13, NOW())
				ON CONFLICT (tenant_id, name) DO UPDATE SET
					description = EXCLUDED.description,
					embedding = EXCLUDED.embedding,
					embedding_model = EXCLUDED.embedding_model,
					embedding_version = EXCLUDED.embedding_version,
					agent_type = EXCLUDED.agent_type,
					tags = EXCLUDED.tags,
					metadata = EXCLUDED.metadata
				RETURNING id
			`
			args = []interface{}{
				tool.ID, tool.TenantID, tool.Name, tool.Description,
				embeddingStr, tool.EmbeddingModel, tool.EmbeddingVersion,
				tool.AgentType, tool.Tags, tool.UsageCount, tool.SuccessRate,
				tool.LastUsedAt, metadataJSON,
			}
		} else {
			query = `
				INSERT INTO tools
				(id, tenant_id, name, description, embedding, embedding_model, embedding_version,
				 agent_type, tags, usage_count, success_rate, last_used_at, metadata, created_at)
				VALUES ($1, $2, $3, $4, $5::vector, $6, $7, $8, $9, $10, $11, $12, $13, $14)
				ON CONFLICT (tenant_id, name) DO UPDATE SET
					description = EXCLUDED.description,
					embedding = EXCLUDED.embedding,
					embedding_model = EXCLUDED.embedding_model,
					embedding_version = EXCLUDED.embedding_version,
					agent_type = EXCLUDED.agent_type,
					tags = EXCLUDED.tags,
					metadata = EXCLUDED.metadata
				RETURNING id
			`
			args = []interface{}{
				tool.ID, tool.TenantID, tool.Name, tool.Description,
				embeddingStr, tool.EmbeddingModel, tool.EmbeddingVersion,
				tool.AgentType, tool.Tags, tool.UsageCount, tool.SuccessRate,
				tool.LastUsedAt, metadataJSON, tool.CreatedAt,
			}
		}
	}

	var id string
	err = r.db.QueryRowContext(ctx, query, args...).Scan(&id)

	if err != nil {
		return errors.Wrap(err, "create tool")
	}

	tool.ID = id
	return nil
}

// GetByID retrieves a tool by ID.
// Args:
// ctx - database operation context.
// id - tool ID, must be non-empty.
// Returns tool or error if not found or invalid argument.
func (r *ToolRepository) GetByID(ctx context.Context, id string) (*storage_models.Tool, error) {
	if id == "" {
		return nil, coreerrors.ErrInvalidArgument
	}

	query := `
		SELECT id, tenant_id, name, description, embedding::text, embedding_model, embedding_version,
			   agent_type, tags, usage_count, success_rate, last_used_at, metadata::text, created_at
		FROM tools
		WHERE id = $1
	`

	tool := &storage_models.Tool{}
	var embeddingStr, metadataStr string
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&tool.ID, &tool.TenantID, &tool.Name, &tool.Description,
		&embeddingStr, &tool.EmbeddingModel, &tool.EmbeddingVersion,
		&tool.AgentType, &tool.Tags, &tool.UsageCount, &tool.SuccessRate,
		&tool.LastUsedAt, &metadataStr, &tool.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, coreerrors.ErrRecordNotFound
	}
	if err != nil {
		return nil, errors.Wrap(err, "get tool by id")
	}

	// Parse embedding string to float64 array
	tool.Embedding, err = parseVectorString(embeddingStr)
	if err != nil {
		return nil, errors.Wrap(err, "parse embedding")
	}

	// Parse metadata JSON string to map
	if metadataStr != "" {
		if err := json.Unmarshal([]byte(metadataStr), &tool.Metadata); err != nil {
			return nil, errors.Wrap(err, "parse metadata")
		}
	}

	return tool, nil
}

// GetByName retrieves a tool by its name within a tenant.
// Args:
// ctx - database operation context.
// name - tool name.
// tenantID - tenant identifier.
// Returns tool or error if not found.
func (r *ToolRepository) GetByName(ctx context.Context, name, tenantID string) (*storage_models.Tool, error) {
	query := `
		SELECT id, tenant_id, name, description, embedding::text, embedding_model, embedding_version,
			   agent_type, tags, usage_count, success_rate, last_used_at, metadata::text, created_at
		FROM tools
		WHERE name = $1 AND tenant_id = $2
	`

	tool := &storage_models.Tool{}
	var embeddingStr, metadataStr string
	err := r.db.QueryRowContext(ctx, query, name, tenantID).Scan(
		&tool.ID, &tool.TenantID, &tool.Name, &tool.Description,
		&embeddingStr, &tool.EmbeddingModel, &tool.EmbeddingVersion,
		&tool.AgentType, &tool.Tags, &tool.UsageCount, &tool.SuccessRate,
		&tool.LastUsedAt, &metadataStr, &tool.CreatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, coreerrors.ErrRecordNotFound
	}
	if err != nil {
		return nil, errors.Wrap(err, "get tool by name")
	}

	// Parse embedding string to float64 array
	tool.Embedding, err = parseVectorString(embeddingStr)
	if err != nil {
		return nil, errors.Wrap(err, "parse embedding")
	}

	// Parse metadata JSON string to map
	if metadataStr != "" {
		if err := json.Unmarshal([]byte(metadataStr), &tool.Metadata); err != nil {
			return nil, errors.Wrap(err, "parse metadata")
		}
	}

	return tool, nil
}

// Update updates an existing tool.
// Args:
// ctx - database operation context.
// tool - tool with updated values.
// Returns error if update operation fails.
func (r *ToolRepository) Update(ctx context.Context, tool *storage_models.Tool) error {
	// Convert metadata to JSON for database storage
	metadataJSON, err := json.Marshal(tool.Metadata)
	if err != nil {
		return errors.Wrap(err, "marshal metadata")
	}

	// Convert embedding to pgvector format
	embeddingStr := float64ToVectorString(tool.Embedding)

	query := `
		UPDATE tools
		SET name = $2, description = $3, embedding = $4::vector, embedding_model = $5,
			embedding_version = $6, agent_type = $7, tags = $8, metadata = $9
		WHERE id = $1
	`

	result, err := r.db.ExecContext(ctx, query,
		tool.ID, tool.Name, tool.Description, embeddingStr,
		tool.EmbeddingModel, tool.EmbeddingVersion, tool.AgentType,
		tool.Tags, metadataJSON,
	)
	if err != nil {
		return errors.Wrap(err, "update tool")
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

// Delete removes a tool by its ID.
// Args:
// ctx - database operation context.
// id - tool identifier.
// Returns error if delete operation fails.
func (r *ToolRepository) Delete(ctx context.Context, id string) error {
	query := `DELETE FROM tools WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return errors.Wrap(err, "delete tool")
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

// SearchByVector performs semantic search for tools using vector embedding.
// Args:
// ctx - database operation context.
// embedding - query vector embedding.
// tenantID - tenant identifier for isolation.
// limit - maximum number of results to return.
// Returns list of similar tools ordered by similarity.
func (r *ToolRepository) SearchByVector(ctx context.Context, embedding []float64, tenantID string, limit int) ([]*storage_models.Tool, error) {
	embeddingStr := float64ToVectorString(embedding)
	query := `
		SELECT id, tenant_id, name, description, embedding::text, embedding_model, embedding_version,
			   agent_type, tags, usage_count, success_rate, last_used_at, metadata::text, created_at,
			   1 - (embedding <=> $1::vector) as similarity
		FROM tools
		WHERE tenant_id = $2
		  AND embedding IS NOT NULL
		ORDER BY embedding <=> $1::vector
		LIMIT $3
	`

	rows, err := r.db.QueryContext(ctx, query, embeddingStr, tenantID, limit)
	if err != nil {
		return nil, errors.Wrap(err, "vector search")
	}
	defer func() { _ = rows.Close() }()

	tools := make([]*storage_models.Tool, 0)
	for rows.Next() {
		tool := &storage_models.Tool{}
		var similarity float64
		var embeddingStr, metadataStr string
		err := rows.Scan(
			&tool.ID, &tool.TenantID, &tool.Name, &tool.Description,
			&embeddingStr, &tool.EmbeddingModel, &tool.EmbeddingVersion,
			&tool.AgentType, &tool.Tags, &tool.UsageCount, &tool.SuccessRate,
			&tool.LastUsedAt, &metadataStr, &tool.CreatedAt, &similarity,
		)
		if err != nil {
			continue
		}

		// Parse embedding string to float64 array
		tool.Embedding, err = parseVectorString(embeddingStr)
		if err != nil {
			continue
		}

		// Parse metadata JSON string to map
		if metadataStr != "" {
			if err := json.Unmarshal([]byte(metadataStr), &tool.Metadata); err != nil {
				tool.Metadata = make(map[string]interface{})
			}
		}

		if tool.Metadata == nil {
			tool.Metadata = make(map[string]interface{})
		}
		// SQL query already computes similarity as: 1 - cosine_distance
		// where cosine_distance range is [0,2], so similarity range is [-1,1]
		tool.Metadata["similarity"] = similarity
		tools = append(tools, tool)
	}

	if err := rows.Err(); err != nil {
		slog.Error("Failed to iterate tools", "error", err)
		return nil, errors.Wrap(err, "iterate tools")
	}

	return tools, nil
}

// SearchByKeyword performs keyword-based search for tools.
// Args:
// ctx - database operation context.
// query - search query text.
// tenantID - tenant identifier for isolation.
// limit - maximum number of results to return.
// Returns list of matching tools ordered by relevance.
func (r *ToolRepository) SearchByKeyword(ctx context.Context, query, tenantID string, limit int) ([]*storage_models.Tool, error) {
	sqlQuery := `
		SELECT id, tenant_id, name, description, embedding::text, embedding_model, embedding_version,
			   agent_type, tags, usage_count, success_rate, last_used_at, metadata::text, created_at
		FROM tools
		WHERE (name ILIKE '%' || $1 || '%' OR description ILIKE '%' || $1 || '%')
		  AND tenant_id = $2
		ORDER BY usage_count DESC, success_rate DESC
		LIMIT $3
	`

	rows, err := r.db.QueryContext(ctx, sqlQuery, query, tenantID, limit)
	if err != nil {
		return nil, errors.Wrap(err, "keyword search")
	}
	defer func() { _ = rows.Close() }()

	tools := make([]*storage_models.Tool, 0)
	for rows.Next() {
		tool := &storage_models.Tool{}
		var embeddingStr, metadataStr string
		err := rows.Scan(
			&tool.ID, &tool.TenantID, &tool.Name, &tool.Description,
			&embeddingStr, &tool.EmbeddingModel, &tool.EmbeddingVersion,
			&tool.AgentType, &tool.Tags, &tool.UsageCount, &tool.SuccessRate,
			&tool.LastUsedAt, &metadataStr, &tool.CreatedAt,
		)
		if err != nil {
			continue
		}

		// Parse embedding string to float64 array
		tool.Embedding, err = parseVectorString(embeddingStr)
		if err != nil {
			continue
		}

		// Parse metadata JSON string to map
		if metadataStr != "" {
			if err := json.Unmarshal([]byte(metadataStr), &tool.Metadata); err != nil {
				tool.Metadata = make(map[string]interface{})
			}
		}

		tools = append(tools, tool)
	}

	if err := rows.Err(); err != nil {
		slog.Error("Failed to iterate tools", "error", err)
		return nil, errors.Wrap(err, "iterate tools")
	}

	return tools, nil
}

// ListAll retrieves all tools for a tenant.
// Args:
// ctx - database operation context.
// tenantID - tenant identifier for isolation.
// limit - maximum number of results to return.
// Returns list of tools ordered by usage count (descending).
func (r *ToolRepository) ListAll(ctx context.Context, tenantID string, limit int) ([]*storage_models.Tool, error) {
	query := `
		SELECT id, tenant_id, name, description, embedding::text, embedding_model, embedding_version,
			   agent_type, tags, usage_count, success_rate, last_used_at, metadata::text, created_at
		FROM tools
		WHERE tenant_id = $1
		ORDER BY usage_count DESC, success_rate DESC
		LIMIT $2
	`

	rows, err := r.db.QueryContext(ctx, query, tenantID, limit)
	if err != nil {
		return nil, errors.Wrap(err, "list tools")
	}
	defer func() { _ = rows.Close() }()

	tools := make([]*storage_models.Tool, 0)
	for rows.Next() {
		tool := &storage_models.Tool{}
		var embeddingStr, metadataStr string
		err := rows.Scan(
			&tool.ID, &tool.TenantID, &tool.Name, &tool.Description,
			&embeddingStr, &tool.EmbeddingModel, &tool.EmbeddingVersion,
			&tool.AgentType, &tool.Tags, &tool.UsageCount, &tool.SuccessRate,
			&tool.LastUsedAt, &metadataStr, &tool.CreatedAt,
		)
		if err != nil {
			continue
		}

		// Parse embedding string to float64 array
		tool.Embedding, err = parseVectorString(embeddingStr)
		if err != nil {
			continue
		}

		// Parse metadata JSON string to map
		if metadataStr != "" {
			if err := json.Unmarshal([]byte(metadataStr), &tool.Metadata); err != nil {
				tool.Metadata = make(map[string]interface{})
			}
		}

		tools = append(tools, tool)
	}

	if err := rows.Err(); err != nil {
		slog.Error("Failed to iterate tools", "error", err)
		return nil, errors.Wrap(err, "iterate tools")
	}

	return tools, nil
}

// ListByAgentType retrieves tools by agent type.
// Args:
// ctx - database operation context.
// agentType - agent type filter.
// tenantID - tenant identifier for isolation.
// limit - maximum number of results to return.
// Returns list of tools ordered by usage count (descending).
func (r *ToolRepository) ListByAgentType(ctx context.Context, agentType, tenantID string, limit int) ([]*storage_models.Tool, error) {
	query := `
		SELECT id, tenant_id, name, description, embedding::text, embedding_model, embedding_version,
			   agent_type, tags, usage_count, success_rate, last_used_at, metadata::text, created_at
		FROM tools
		WHERE agent_type = $1 AND tenant_id = $2
		ORDER BY usage_count DESC, success_rate DESC
		LIMIT $3
	`

	rows, err := r.db.QueryContext(ctx, query, agentType, tenantID, limit)
	if err != nil {
		return nil, errors.Wrap(err, "list tools by agent type")
	}
	defer func() { _ = rows.Close() }()

	tools := make([]*storage_models.Tool, 0)
	for rows.Next() {
		tool := &storage_models.Tool{}
		var embeddingStr, metadataStr string
		err := rows.Scan(
			&tool.ID, &tool.TenantID, &tool.Name, &tool.Description,
			&embeddingStr, &tool.EmbeddingModel, &tool.EmbeddingVersion,
			&tool.AgentType, &tool.Tags, &tool.UsageCount, &tool.SuccessRate,
			&tool.LastUsedAt, &metadataStr, &tool.CreatedAt,
		)
		if err != nil {
			continue
		}

		// Parse embedding string to float64 array
		tool.Embedding, err = parseVectorString(embeddingStr)
		if err != nil {
			continue
		}

		// Parse metadata JSON string to map
		if metadataStr != "" {
			if err := json.Unmarshal([]byte(metadataStr), &tool.Metadata); err != nil {
				tool.Metadata = make(map[string]interface{})
			}
		}

		tools = append(tools, tool)
	}

	if err := rows.Err(); err != nil {
		slog.Error("Failed to iterate tools", "error", err)
		return nil, errors.Wrap(err, "iterate tools")
	}

	return tools, nil
}

// UpdateUsage updates the usage statistics for a tool.
// Args:
// ctx - database operation context.
// id - tool identifier.
// success - whether the tool execution was successful.
// Returns error if update operation fails.
func (r *ToolRepository) UpdateUsage(ctx context.Context, id string, success bool) error {
	query := `
		UPDATE tools
		SET usage_count = usage_count + 1,
			success_rate = CASE 
				WHEN $2 THEN success_rate * 0.9 + 1.0 * 0.1
				ELSE success_rate * 0.9 + 0.0 * 0.1
			END,
			last_used_at = NOW()
		WHERE id = $1
	`

	result, err := r.db.ExecContext(ctx, query, id, success)
	if err != nil {
		return errors.Wrap(err, "update tool usage")
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

// UpdateEmbedding updates the embedding for a tool.
// Args:
// ctx - database operation context.
// id - tool identifier.
// embedding - vector embedding.
// model - embedding model name.
// version - embedding model version.
// Returns error if update operation fails.
func (r *ToolRepository) UpdateEmbedding(ctx context.Context, id string, embedding []float64, model string, version int) error {
	embeddingStr := float64ToVectorString(embedding)
	query := `
		UPDATE tools
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

// ListByTags retrieves tools by tags.
// Args:
// ctx - database operation context.
// tags - tags to filter by.
// tenantID - tenant identifier for isolation.
// limit - maximum number of results to return.
// Returns list of tools that match any of the tags.
func (r *ToolRepository) ListByTags(ctx context.Context, tags []string, tenantID string, limit int) ([]*storage_models.Tool, error) {
	query := `
		SELECT id, tenant_id, name, description, embedding::text, embedding_model, embedding_version,
			   agent_type, tags, usage_count, success_rate, last_used_at, metadata::text, created_at
		FROM tools
		WHERE tenant_id = $1
		  AND tags && $2
		ORDER BY usage_count DESC, success_rate DESC
		LIMIT $3
	`

	rows, err := r.db.QueryContext(ctx, query, tenantID, tags, limit)
	if err != nil {
		return nil, errors.Wrap(err, "list tools by tags")
	}
	defer func() { _ = rows.Close() }()

	tools := make([]*storage_models.Tool, 0)
	for rows.Next() {
		tool := &storage_models.Tool{}
		var embeddingStr, metadataStr string
		err := rows.Scan(
			&tool.ID, &tool.TenantID, &tool.Name, &tool.Description,
			&embeddingStr, &tool.EmbeddingModel, &tool.EmbeddingVersion,
			&tool.AgentType, &tool.Tags, &tool.UsageCount, &tool.SuccessRate,
			&tool.LastUsedAt, &metadataStr, &tool.CreatedAt,
		)
		if err != nil {
			continue
		}

		// Parse embedding string to float64 array
		tool.Embedding, err = parseVectorString(embeddingStr)
		if err != nil {
			continue
		}

		// Parse metadata JSON string to map
		if metadataStr != "" {
			if err := json.Unmarshal([]byte(metadataStr), &tool.Metadata); err != nil {
				tool.Metadata = make(map[string]interface{})
			}
		}

		tools = append(tools, tool)
	}

	if err := rows.Err(); err != nil {
		slog.Error("Failed to iterate tools", "error", err)
		return nil, errors.Wrap(err, "iterate tools")
	}

	return tools, nil
}
