// Package repositories provides data access for distilled memories.
package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"goagentx/internal/errors"
	"goagentx/internal/storage/postgres"
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

var _ DistilledMemoryRepositoryInterface = (*DistilledMemoryRepository)(nil)

func NewDistilledMemoryRepository(db postgres.DBTX, dbPool *sql.DB) *DistilledMemoryRepository {
	return &DistilledMemoryRepository{
		db:     db,
		dbPool: dbPool,
	}
}

// setTenantContext sets the tenant context for RLS within a transaction using SET LOCAL.
// SET LOCAL ensures the setting is automatically reset when the transaction ends,
// preventing connection pool tenant context leaks.
// It uses parameterized query via PERFORM to prevent SQL injection.
func (r *DistilledMemoryRepository) setTenantContext(ctx context.Context, tx *sql.Tx, tenantID string) error {
	query := `SELECT set_config('app.tenant_id', $1, true)`
	if _, err := tx.ExecContext(ctx, query, tenantID); err != nil {
		return errors.Wrap(err, "set tenant context")
	}
	return nil
}

// withTenantTx starts a transaction, sets tenant context, and executes fn.
// The tenant context is automatically reset when the transaction commits or rolls back.
func (r *DistilledMemoryRepository) withTenantTx(ctx context.Context, tenantID string, fn func(tx *sql.Tx) error) error {
	if r.dbPool == nil {
		return fmt.Errorf("dbPool is required for tenant-isolated operations")
	}

	tx, err := r.dbPool.BeginTx(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "begin transaction")
	}
	committed := false
	defer func() {
		if !committed {
			if rbErr := tx.Rollback(); rbErr != nil {
				slog.Error("Failed to rollback transaction", "error", rbErr)
			}
		}
	}()

	if err := r.setTenantContext(ctx, tx, tenantID); err != nil {
		return err
	}

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return errors.Wrap(err, "commit transaction")
	}
	committed = true
	return nil
}

// Create creates a new distilled memory.
func (r *DistilledMemoryRepository) Create(ctx context.Context, memory *DistilledMemory) error {
	slog.InfoContext(ctx, "Starting distilled memory storage",
		"memory_id", memory.ID,
		"tenant_id", memory.TenantID,
		"user_id", memory.UserID,
		"session_id", memory.SessionID,
		"memory_type", memory.MemoryType,
		"importance", memory.Importance,
		"content_preview", truncateString(memory.Content, 50))

	metadataJSON, err := json.Marshal(memory.Metadata)
	if err != nil {
		return errors.Wrap(err, "marshal metadata")
	}

	return r.withTenantTx(ctx, memory.TenantID, func(tx *sql.Tx) error {
		query := `
            INSERT INTO distilled_memories
            (id, tenant_id, user_id, session_id, content, embedding, embedding_model,
             embedding_version, memory_type, importance, metadata, access_count,
             last_accessed_at, expires_at, created_at)
            VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
            ON CONFLICT DO NOTHING
        `

		_, err := tx.ExecContext(ctx, query,
			memory.ID, memory.TenantID, memory.UserID, memory.SessionID, memory.Content,
			postgres.FormatVector(memory.Embedding), memory.EmbeddingModel, memory.EmbeddingVersion,
			memory.MemoryType, memory.Importance, metadataJSON, memory.AccessCount,
			memory.LastAccessedAt, memory.ExpiresAt, memory.CreatedAt,
		)

		if err != nil {
			slog.ErrorContext(ctx, "Failed to INSERT distilled memory",
				"memory_id", memory.ID,
				"user_id", memory.UserID,
				"error", err)
			return errors.Wrap(err, "create distilled memory")
		}

		slog.InfoContext(ctx, "Successfully stored distilled memory",
			"memory_id", memory.ID,
			"user_id", memory.UserID,
			"tenant_id", memory.TenantID)
		return nil
	})
}

// SearchByVector searches for memories using vector similarity.
func (r *DistilledMemoryRepository) SearchByVector(ctx context.Context, embedding []float64, tenantID string, limit int) ([]*DistilledMemory, error) {
	slog.InfoContext(ctx, "Starting vector search",
		"tenant_id", tenantID,
		"embedding_dims", len(embedding),
		"limit", limit)

	var memories []*DistilledMemory

	err := r.withTenantTx(ctx, tenantID, func(tx *sql.Tx) error {
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
		rows, err := tx.QueryContext(ctx, query, vectorStr, limit)
		if err != nil {
			return errors.Wrap(err, "search distilled memories")
		}
		defer func() { _ = rows.Close() }()

		for rows.Next() {
			memory := &DistilledMemory{}
			var similarity float64
			var embeddingStr string
			var metadataStr string

			if err := rows.Scan(
				&memory.ID, &memory.TenantID, &memory.UserID, &memory.SessionID,
				&memory.Content, &embeddingStr, &memory.EmbeddingModel,
				&memory.EmbeddingVersion, &memory.MemoryType, &memory.Importance,
				&metadataStr, &memory.AccessCount, &memory.LastAccessedAt,
				&memory.ExpiresAt, &memory.CreatedAt, &similarity,
			); err != nil {
				slog.WarnContext(ctx, "Failed to scan search result row", "error", err)
				continue
			}

			memory.Embedding, err = postgres.ParseVectorString(embeddingStr)
			if err != nil {
				slog.WarnContext(ctx, "Failed to parse embedding in search result", "memory_id", memory.ID, "error", err)
				continue
			}

			if metadataStr != "" {
				if err := json.Unmarshal([]byte(metadataStr), &memory.Metadata); err != nil {
					slog.WarnContext(ctx, "Failed to parse metadata in search result", "memory_id", memory.ID, "error", err)
				}
			}

			memories = append(memories, memory)
		}

		if err := rows.Err(); err != nil {
			return errors.Wrap(err, "iterate search results")
		}

		slog.InfoContext(ctx, "SearchByVector query completed",
			"tenant_id", tenantID,
			"memories_found", len(memories))
		return nil
	})

	if err != nil {
		return nil, err
	}
	return memories, nil
}

// GetByUserID retrieves memories for a specific user.
func (r *DistilledMemoryRepository) GetByUserID(ctx context.Context, tenantID, userID string, limit int) ([]*DistilledMemory, error) {
	var memories []*DistilledMemory

	err := r.withTenantTx(ctx, tenantID, func(tx *sql.Tx) error {
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

		rows, err := tx.QueryContext(ctx, selectQuery, userID, limit)
		if err != nil {
			return errors.Wrap(err, "get memories by user")
		}
		defer func() { _ = rows.Close() }()

		for rows.Next() {
			memory := &DistilledMemory{}
			var embeddingStr string
			var metadataStr string

			if err := rows.Scan(
				&memory.ID, &memory.TenantID, &memory.UserID, &memory.SessionID,
				&memory.Content, &embeddingStr, &memory.EmbeddingModel,
				&memory.EmbeddingVersion, &memory.MemoryType, &memory.Importance,
				&metadataStr, &memory.AccessCount, &memory.LastAccessedAt,
				&memory.ExpiresAt, &memory.CreatedAt,
			); err != nil {
				slog.WarnContext(ctx, "Failed to scan memory row", "error", err)
				continue
			}

			memory.Embedding, err = postgres.ParseVectorString(embeddingStr)
			if err != nil {
				slog.WarnContext(ctx, "Failed to parse embedding", "memory_id", memory.ID, "error", err)
				continue
			}

			if metadataStr != "" {
				if err := json.Unmarshal([]byte(metadataStr), &memory.Metadata); err != nil {
					slog.WarnContext(ctx, "Failed to parse metadata", "memory_id", memory.ID, "error", err)
				}
			}

			memories = append(memories, memory)
		}

		if err := rows.Err(); err != nil {
			return errors.Wrap(err, "iterate memories")
		}

		slog.InfoContext(ctx, "GetByUserID query completed",
			"tenant_id", tenantID,
			"user_id", userID,
			"memories_found", len(memories))
		return nil
	})

	if err != nil {
		return nil, err
	}
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
	var memories []*DistilledMemory

	err := r.withTenantTx(ctx, tenantID, func(tx *sql.Tx) error {
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

		rows, err := tx.QueryContext(ctx, query, memoryType, limit)
		if err != nil {
			return errors.Wrap(err, "get memories by type")
		}
		defer func() { _ = rows.Close() }()

		for rows.Next() {
			memory := &DistilledMemory{}
			var embeddingStr string
			var metadataStr string

			if err := rows.Scan(
				&memory.ID, &memory.TenantID, &memory.UserID, &memory.SessionID,
				&memory.Content, &embeddingStr, &memory.EmbeddingModel,
				&memory.EmbeddingVersion, &memory.MemoryType, &memory.Importance,
				&metadataStr, &memory.AccessCount, &memory.LastAccessedAt,
				&memory.ExpiresAt, &memory.CreatedAt,
			); err != nil {
				return errors.Wrap(err, "scan memory")
			}

			memory.Embedding, err = postgres.ParseVectorString(embeddingStr)
			if err != nil {
				return errors.Wrap(err, "parse embedding")
			}

			if metadataStr != "" {
				if err := json.Unmarshal([]byte(metadataStr), &memory.Metadata); err != nil {
					memory.Metadata = make(map[string]interface{})
				}
			}

			memories = append(memories, memory)
		}

		if err := rows.Err(); err != nil {
			return errors.Wrap(err, "iterate memories")
		}

		return nil
	})

	if err != nil {
		return nil, err
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

	metadataJSON, err := json.Marshal(memory.Metadata)
	if err != nil {
		return errors.Wrap(err, "marshal metadata")
	}

	return r.withTenantTx(ctx, memory.TenantID, func(tx *sql.Tx) error {
		embeddingStr := postgres.FormatVector(memory.Embedding)

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

		result, err := tx.ExecContext(ctx, query,
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
	})
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

	return r.withTenantTx(ctx, tenantID, func(tx *sql.Tx) error {
		query := `DELETE FROM distilled_memories WHERE id = $1`

		result, err := tx.ExecContext(ctx, query, id)
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
	})
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
