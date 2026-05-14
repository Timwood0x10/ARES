// Package repositories provides data access for distilled memories.
package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Timwood0x10/goagent/internal/errors"
	"github.com/Timwood0x10/goagent/internal/storage/postgres"
)

// DistilledMemory represents a distilled memory from conversation history.
type DistilledMemory struct {
	ID               string
	TenantID         string
	UserID           string
	SessionID        string
	Content          string
	Embedding        []float64
	EmbeddingModel   string
	EmbeddingVersion int
	MemoryType       string
	Importance       float64
	Metadata         map[string]interface{}
	AccessCount      int
	LastAccessedAt   *time.Time
	ExpiresAt        time.Time
	CreatedAt        time.Time
}

// DistilledMemoryRepository provides data access for distilled memories.
type DistilledMemoryRepository struct {
	db     postgres.DBTX
	dbPool *sql.DB
}

// Ensure DistilledMemoryRepository implements DistilledMemoryRepositoryInterface.
var _ DistilledMemoryRepositoryInterface = (*DistilledMemoryRepository)(nil)

// NewDistilledMemoryRepository creates a new DistilledMemoryRepository.
func NewDistilledMemoryRepository(db postgres.DBTX, dbPool *sql.DB) *DistilledMemoryRepository {
	return &DistilledMemoryRepository{
		db:     db,
		dbPool: dbPool,
	}
}

// parseVectorString converts pgvector string format to []float64.
// Note: This function is also defined in knowledge_repository.go to avoid import cycles.
func parseDistilledVectorString(vecStr string) ([]float64, error) {
	if len(vecStr) == 0 {
		return []float64{}, nil
	}

	vecStr = strings.Trim(vecStr, "[]")
	if vecStr == "" {
		return []float64{}, nil
	}

	parts := strings.Split(vecStr, ",")
	result := make([]float64, len(parts))
	for i, part := range parts {
		val, err := fmt.Sscanf(strings.TrimSpace(part), "%f", &result[i])
		if err != nil {
			return nil, errors.Wrap(err, "failed to parse vector component")
		}
		if val != 1 {
			return nil, fmt.Errorf("failed to parse vector component: expected 1 match, got %d", val)
		}
	}

	return result, nil
}

// Create creates a new distilled memory.
func (r *DistilledMemoryRepository) Create(ctx context.Context, memory *DistilledMemory) error {
	// Detailed logging before storage
	slog.InfoContext(ctx, "📝 [Storage] Starting distilled memory storage",
		"memory_id", memory.ID,
		"tenant_id", memory.TenantID,
		"user_id", memory.UserID,
		"session_id", memory.SessionID,
		"memory_type", memory.MemoryType,
		"importance", memory.Importance,
		"content_preview", truncateString(memory.Content, 50))

	// Set tenant context for RLS
	// SET statement does not support parameterized queries, need to use manual escaping
	quotedTenantID := "'" + strings.ReplaceAll(memory.TenantID, "'", "''") + "'"
	query := fmt.Sprintf("SET app.tenant_id TO %s", quotedTenantID)
	if _, err := r.db.ExecContext(ctx, query); err != nil {
		slog.ErrorContext(ctx, "Failed to set tenant context",
			"tenant_id", memory.TenantID,
			"error", err)
		return errors.Wrap(err, "set tenant context")
	}
	slog.InfoContext(ctx, "Tenant context set successfully", "tenant_id", memory.TenantID)

	// Convert metadata to JSON
	metadataJSON, err := json.Marshal(memory.Metadata)
	if err != nil {
		return errors.Wrap(err, "marshal metadata")
	}

	query = `
		INSERT INTO distilled_memories
		(id, tenant_id, user_id, session_id, content, embedding, embedding_model,
		 embedding_version, memory_type, importance, metadata, access_count,
		 last_accessed_at, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		ON CONFLICT DO NOTHING
	`

	// Log the SQL parameters before execution
	slog.InfoContext(ctx, "🔍 [Storage] Executing INSERT with parameters",
		"memory_id", memory.ID,
		"tenant_id", memory.TenantID,
		"user_id", fmt.Sprintf("'%s' (length: %d)", memory.UserID, len(memory.UserID)),
		"session_id", memory.SessionID,
		"content_length", len(memory.Content),
		"embedding_dims", len(memory.Embedding))

	_, err = r.db.ExecContext(ctx, query,
		memory.ID, memory.TenantID, memory.UserID, memory.SessionID, memory.Content,
		postgres.FormatVector(memory.Embedding), memory.EmbeddingModel, memory.EmbeddingVersion,
		memory.MemoryType, memory.Importance, metadataJSON, memory.AccessCount,
		memory.LastAccessedAt, memory.ExpiresAt, memory.CreatedAt,
	)

	if err != nil {
		slog.ErrorContext(ctx, "❌ [Storage] Failed to INSERT distilled memory",
			"memory_id", memory.ID,
			"user_id", memory.UserID,
			"error", err)
		return errors.Wrap(err, "create distilled memory")
	}

	slog.InfoContext(ctx, "✅ [Storage] Successfully stored distilled memory",
		"memory_id", memory.ID,
		"user_id", memory.UserID,
		"tenant_id", memory.TenantID)

	return nil
}

// SearchByVector searches for memories using vector similarity.
func (r *DistilledMemoryRepository) SearchByVector(ctx context.Context, embedding []float64, tenantID string, limit int) ([]*DistilledMemory, error) {
	// Detailed logging before search
	slog.InfoContext(ctx, "🔍 [Storage] Starting vector search",
		"tenant_id", tenantID,
		"embedding_dims", len(embedding),
		"embedding_preview", fmt.Sprintf("[%v]", embedding[:min(10, len(embedding))]),
		"limit", limit)

	// Set tenant context for RLS
	// SET statement does not support parameterized queries, need to use manual escaping
	quotedTenantID := "'" + strings.ReplaceAll(tenantID, "'", "''") + "'"
	setQuery := fmt.Sprintf("SET app.tenant_id TO %s", quotedTenantID)
	if _, err := r.db.ExecContext(ctx, setQuery); err != nil {
		slog.ErrorContext(ctx, "Failed to set tenant context in SearchByVector",
			"tenant_id", tenantID,
			"error", err)
		return nil, errors.Wrap(err, "set tenant context")
	}
	slog.InfoContext(ctx, "Tenant context set in SearchByVector", "tenant_id", tenantID)

	query := `
		SELECT id, tenant_id, user_id, session_id, content, embedding::text,
			   embedding_model, embedding_version, memory_type, importance,
			   metadata, access_count, last_accessed_at, expires_at, created_at,
			   1 - (embedding <=> $1::vector) as similarity
		FROM distilled_memories
		WHERE expires_at > NOW()
		ORDER BY embedding <=> $1::vector
		LIMIT $2
	`

	vectorStr := postgres.FormatVector(embedding)
	rows, err := r.db.QueryContext(ctx, query, vectorStr, limit)
	if err != nil {
		slog.ErrorContext(ctx, "❌ [Storage] Failed to execute SearchByVector query",
			"tenant_id", tenantID,
			"error", err)
		return nil, errors.Wrap(err, "search distilled memories")
	}
	defer func() { _ = rows.Close() }()

	memories := make([]*DistilledMemory, 0)
	for rows.Next() {
		memory := &DistilledMemory{}
		var similarity float64
		var embeddingStr string
		var metadataStr string

		err := rows.Scan(
			&memory.ID, &memory.TenantID, &memory.UserID, &memory.SessionID,
			&memory.Content, &embeddingStr, &memory.EmbeddingModel,
			&memory.EmbeddingVersion, &memory.MemoryType, &memory.Importance,
			&metadataStr, &memory.AccessCount, &memory.LastAccessedAt,
			&memory.ExpiresAt, &memory.CreatedAt, &similarity,
		)
		if err != nil {
			slog.WarnContext(ctx, "⚠️ [Storage] Failed to scan search result row", "error", err)
			continue
		}

		memory.Embedding, err = parseDistilledVectorString(embeddingStr)
		if err != nil {
			slog.WarnContext(ctx, "⚠️ [Storage] Failed to parse embedding in search result", "memory_id", memory.ID, "error", err)
			continue
		}

		// Parse metadata JSON string to map
		if metadataStr != "" {
			if err := json.Unmarshal([]byte(metadataStr), &memory.Metadata); err != nil {
				slog.WarnContext(ctx, "⚠️ [Storage] Failed to parse metadata in search result", "memory_id", memory.ID, "error", err)
			}
		}

		memories = append(memories, memory)
	}

	if err := rows.Err(); err != nil {
		slog.WarnContext(ctx, "Failed to iterate search results", "error", err)
		return nil, errors.Wrap(err, "iterate search results")
	}

	slog.InfoContext(ctx, "SearchByVector query completed",
		"tenant_id", tenantID,
		"memories_found", len(memories))

	// Log detailed memory contents for debugging
	for i, mem := range memories {
		slog.InfoContext(ctx, "📋 [Storage] Retrieved memory details",
			"index", i+1,
			"memory_id", mem.ID,
			"user_id", mem.UserID,
			"memory_type", mem.MemoryType,
			"importance", mem.Importance,
			"content_preview", truncateString(mem.Content, 100),
			"similarity", mem.Metadata["similarity"])
	}

	return memories, nil
}

// GetByUserID retrieves memories for a specific user.
func (r *DistilledMemoryRepository) GetByUserID(ctx context.Context, tenantID, userID string, limit int) ([]*DistilledMemory, error) {
	var err error

	// Detailed logging before query
	slog.InfoContext(ctx, "🔍 [Storage] Starting GetByUserID query",
		"tenant_id", tenantID,
		"user_id", userID,
		"limit", limit)

	// Set tenant context for RLS
	// SET statement does not support parameterized queries, need to use manual escaping
	quotedTenantID := "'" + strings.ReplaceAll(tenantID, "'", "''") + "'"
	setQuery := fmt.Sprintf("SET app.tenant_id TO %s", quotedTenantID)
	if _, err = r.db.ExecContext(ctx, setQuery); err != nil {
		slog.ErrorContext(ctx, "Failed to set tenant context in GetByUserID",
			"tenant_id", tenantID,
			"error", err)
		return nil, errors.Wrap(err, "set tenant context")
	}
	slog.InfoContext(ctx, "✅ [Storage] Tenant context set in GetByUserID", "tenant_id", tenantID)

	selectQuery := `
		SELECT id, tenant_id, user_id, session_id, content, embedding::text,
			   embedding_model, embedding_version, memory_type, importance,
			   metadata, access_count, last_accessed_at, expires_at, created_at
		FROM distilled_memories
		WHERE user_id = $1
		  AND expires_at > NOW()
		ORDER BY importance DESC, created_at DESC
		LIMIT $2
	`

	rows, err := r.db.QueryContext(ctx, selectQuery, userID, limit)
	if err != nil {
		slog.ErrorContext(ctx, "❌ [Storage] Failed to execute GetByUserID query",
			"user_id", userID,
			"error", err)
		return nil, errors.Wrap(err, "get memories by user")
	}
	defer func() { _ = rows.Close() }()

	memories := make([]*DistilledMemory, 0)
	for rows.Next() {
		memory := &DistilledMemory{}
		var embeddingStr string
		var metadataStr string

		err := rows.Scan(
			&memory.ID, &memory.TenantID, &memory.UserID, &memory.SessionID,
			&memory.Content, &embeddingStr, &memory.EmbeddingModel,
			&memory.EmbeddingVersion, &memory.MemoryType, &memory.Importance,
			&metadataStr, &memory.AccessCount, &memory.LastAccessedAt,
			&memory.ExpiresAt, &memory.CreatedAt,
		)
		if err != nil {
			slog.WarnContext(ctx, "⚠️ [Storage] Failed to scan memory row", "error", err)
			continue
		}

		memory.Embedding, err = parseDistilledVectorString(embeddingStr)
		if err != nil {
			slog.WarnContext(ctx, "⚠️ [Storage] Failed to parse embedding", "memory_id", memory.ID, "error", err)
			continue
		}

		// Parse metadata JSON string to map
		if metadataStr != "" {
			if err := json.Unmarshal([]byte(metadataStr), &memory.Metadata); err != nil {
				slog.WarnContext(ctx, "⚠️ [Storage] Failed to parse metadata", "memory_id", memory.ID, "error", err)
			}
		}

		memories = append(memories, memory)
	}

	if err := rows.Err(); err != nil {
		slog.WarnContext(ctx, "Failed to iterate memories", "error", err)
		return nil, errors.Wrap(err, "iterate memories")
	}

	slog.InfoContext(ctx, "GetByUserID query completed",
		"tenant_id", tenantID,
		"user_id", userID,
		"memories_found", len(memories))

	return memories, nil
}

// UpdateAccessCount updates the access count and last accessed time.
func (r *DistilledMemoryRepository) UpdateAccessCount(ctx context.Context, id string) error {
	query := `
		UPDATE distilled_memories
		SET access_count = access_count + 1,
		    last_accessed_at = NOW()
		WHERE id = $1
	`

	_, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return errors.Wrap(err, "update access count")
	}

	return nil
}

// GetByMemoryType retrieves memories by memory type.
// Args:
// ctx - database operation context.
// tenantID - tenant identifier for isolation.
// memoryType - memory type to filter by.
// limit - maximum number of results to return.
// Returns list of memories ordered by importance (descending).
func (r *DistilledMemoryRepository) GetByMemoryType(ctx context.Context, tenantID, memoryType string, limit int) ([]*DistilledMemory, error) {
	// Set tenant context for RLS
	// SET statement does not support parameterized queries, need to use manual escaping
	quotedTenantID := "'" + strings.ReplaceAll(tenantID, "'", "''") + "'"
	setQuery := fmt.Sprintf("SET app.tenant_id TO %s", quotedTenantID)
	if _, err := r.db.ExecContext(ctx, setQuery); err != nil {
		return nil, errors.Wrap(err, "set tenant context")
	}

	query := `
		SELECT id, tenant_id, user_id, session_id, content, embedding::text,
			   embedding_model, embedding_version, memory_type, importance,
			   metadata, access_count, last_accessed_at, expires_at, created_at
		FROM distilled_memories
		WHERE memory_type = $1
		  AND expires_at > NOW()
		ORDER BY importance DESC, created_at DESC
		LIMIT $2
	`

	rows, err := r.db.QueryContext(ctx, query, memoryType, limit)
	if err != nil {
		return nil, errors.Wrap(err, "get memories by type")
	}
	defer func() { _ = rows.Close() }()

	memories := make([]*DistilledMemory, 0)
	for rows.Next() {
		memory := &DistilledMemory{}
		var embeddingStr string
		var metadataStr string

		err := rows.Scan(
			&memory.ID, &memory.TenantID, &memory.UserID, &memory.SessionID,
			&memory.Content, &embeddingStr, &memory.EmbeddingModel,
			&memory.EmbeddingVersion, &memory.MemoryType, &memory.Importance,
			&metadataStr, &memory.AccessCount, &memory.LastAccessedAt,
			&memory.ExpiresAt, &memory.CreatedAt,
		)
		if err != nil {
			return nil, errors.Wrap(err, "scan memory")
		}

		// Parse embedding string to float64 array
		memory.Embedding, err = parseDistilledVectorString(embeddingStr)
		if err != nil {
			return nil, errors.Wrap(err, "parse embedding")
		}

		// Parse metadata JSON string to map
		if metadataStr != "" {
			if err := json.Unmarshal([]byte(metadataStr), &memory.Metadata); err != nil {
				memory.Metadata = make(map[string]interface{})
			}
		}

		memories = append(memories, memory)
	}

	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "iterate memories")
	}

	return memories, nil
}

// DeleteExpired removes expired memories.
func (r *DistilledMemoryRepository) DeleteExpired(ctx context.Context) (int64, error) {
	query := `DELETE FROM distilled_memories WHERE expires_at <= NOW()`

	result, err := r.db.ExecContext(ctx, query)
	if err != nil {
		return 0, errors.Wrap(err, "delete expired memories")
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return 0, errors.Wrap(err, "get rows affected")
	}

	return rows, nil
}

// Update updates an existing distilled memory.
// Args:
// ctx - database operation context.
// memory - memory with updated values, ID must be set.
// Returns error if update operation fails.
func (r *DistilledMemoryRepository) Update(ctx context.Context, memory *DistilledMemory) error {
	if memory.ID == "" {
		return fmt.Errorf("memory ID is required")
	}

	// Set tenant context for RLS
	// SET statement does not support parameterized queries, need to use manual escaping
	quotedTenantID := "'" + strings.ReplaceAll(memory.TenantID, "'", "''") + "'"
	setQuery := fmt.Sprintf("SET app.tenant_id TO %s", quotedTenantID)
	if _, err := r.db.ExecContext(ctx, setQuery); err != nil {
		return errors.Wrap(err, "set tenant context")
	}

	// Convert metadata to JSON for database storage
	metadataJSON, err := json.Marshal(memory.Metadata)
	if err != nil {
		return errors.Wrap(err, "marshal metadata")
	}

	// Convert embedding to pgvector format
	embeddingStr := float64ToVectorString(memory.Embedding)

	query := `
		UPDATE distilled_memories
		SET content = $2,
		    embedding = $3::vector,
		    embedding_model = $4,
		    embedding_version = $5,
		    memory_type = $6,
		    importance = $7,
		    metadata = $8,
		    updated_at = NOW()
		WHERE id = $1
	`

	result, err := r.db.ExecContext(ctx, query,
		memory.ID, memory.Content, embeddingStr, memory.EmbeddingModel,
		memory.EmbeddingVersion, memory.MemoryType, memory.Importance, metadataJSON,
	)
	if err != nil {
		return errors.Wrap(err, "update distilled memory")
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "get rows affected")
	}

	if rows == 0 {
		return fmt.Errorf("memory not found")
	}

	return nil
}

// Delete removes a distilled memory by its ID.
// Args:
// ctx - database operation context.
// tenantID - tenant identifier for isolation.
// id - memory identifier.
// Returns error if delete operation fails.
func (r *DistilledMemoryRepository) Delete(ctx context.Context, tenantID, id string) error {
	if id == "" {
		return fmt.Errorf("memory ID is required")
	}

	// Set tenant context for RLS
	// SET statement does not support parameterized queries, need to use manual escaping
	quotedTenantID := "'" + strings.ReplaceAll(tenantID, "'", "''") + "'"
	setQuery := fmt.Sprintf("SET app.tenant_id TO %s", quotedTenantID)
	if _, err := r.db.ExecContext(ctx, setQuery); err != nil {
		return errors.Wrap(err, "set tenant context")
	}

	query := `DELETE FROM distilled_memories WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return errors.Wrap(err, "delete distilled memory")
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "get rows affected")
	}

	if rows == 0 {
		return fmt.Errorf("memory not found")
	}

	return nil
}

// truncateString truncates a string to the specified maximum length.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
