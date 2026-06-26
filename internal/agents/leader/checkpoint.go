package leader

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
)

// Sentinel errors for checkpoint operations.
var (
	ErrCheckpointRepoNotInitialized = errors.New("checkpoint repository not initialized")
	ErrCheckpointNil                = errors.New("checkpoint cannot be nil")
	ErrEmptyLeaderID                = errors.New("leader ID cannot be empty")
)

// LeaderCheckpoint represents a leader's state snapshot for recovery.
type LeaderCheckpoint struct {
	LeaderID  string          `json:"leader_id"`
	SessionID string          `json:"session_id"`
	Status    string          `json:"status"`
	Metadata  json.RawMessage `json:"metadata"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// CheckpointRepository provides persistence for leader checkpoints.
type CheckpointRepository struct {
	pool *postgres.Pool
}

// NewCheckpointRepository creates a CheckpointRepository.
// Returns nil if pool is nil.
func NewCheckpointRepository(pool *postgres.Pool) *CheckpointRepository {
	if pool == nil {
		return nil
	}
	return &CheckpointRepository{pool: pool}
}

// Save upserts a leader checkpoint into the leader_checkpoints table.
func (r *CheckpointRepository) Save(ctx context.Context, cp *LeaderCheckpoint) error {
	if r == nil || r.pool == nil {
		return ErrCheckpointRepoNotInitialized
	}
	if cp == nil {
		return ErrCheckpointNil
	}
	if cp.LeaderID == "" {
		return ErrEmptyLeaderID
	}

	metadata := cp.Metadata
	if len(metadata) == 0 {
		metadata = json.RawMessage("{}")
	}

	query := `INSERT INTO leader_checkpoints (leader_id, session_id, status, metadata, updated_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (leader_id) DO UPDATE SET
			session_id = EXCLUDED.session_id,
			status = EXCLUDED.status,
			metadata = EXCLUDED.metadata,
			updated_at = NOW()`

	if _, err := r.pool.Exec(ctx, query, cp.LeaderID, cp.SessionID, cp.Status, string(metadata)); err != nil {
		return errors.Wrap(err, "save checkpoint")
	}
	return nil
}

// GetLatest retrieves the most recent checkpoint for a leader.
// Returns (nil, nil) if no checkpoint exists.
func (r *CheckpointRepository) GetLatest(ctx context.Context, leaderID string) (*LeaderCheckpoint, error) {
	if r == nil || r.pool == nil {
		return nil, ErrCheckpointRepoNotInitialized
	}
	if leaderID == "" {
		return nil, ErrEmptyLeaderID
	}

	query := `SELECT leader_id, session_id, status, metadata, updated_at
		FROM leader_checkpoints
		WHERE leader_id = $1`

	cp := &LeaderCheckpoint{}
	var metadataStr string
	err := r.pool.QueryRow(ctx, query, leaderID).Scan(
		&cp.LeaderID, &cp.SessionID, &cp.Status, &metadataStr, &cp.UpdatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, errors.Wrap(err, "get latest checkpoint")
	}

	cp.Metadata = json.RawMessage(metadataStr)
	return cp, nil
}

// Delete removes a leader checkpoint by leader ID.
func (r *CheckpointRepository) Delete(ctx context.Context, leaderID string) error {
	if r == nil || r.pool == nil {
		return ErrCheckpointRepoNotInitialized
	}
	if leaderID == "" {
		return ErrEmptyLeaderID
	}

	query := `DELETE FROM leader_checkpoints WHERE leader_id = $1`
	if _, err := r.pool.Exec(ctx, query, leaderID); err != nil {
		return errors.Wrap(err, "delete checkpoint")
	}
	return nil
}
