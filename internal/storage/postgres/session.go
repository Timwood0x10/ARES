package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/errors"
)

// SessionRepository handles session persistence.
//
// Tenant scoping: sessions carries a tenant_id column; every query filters on
// the tenant bound at construction (empty → default tenant). No production
// callers today, but kept tenant-safe so a revival cannot read another
// tenant's sessions.
type SessionRepository struct {
	db       DBTX
	tenantID string
}

// NewSessionRepository creates a new SessionRepository bound to the default tenant.
func NewSessionRepository(pool *Pool) *SessionRepository {
	return NewSessionRepositoryWithTenant(pool.db, "")
}

// NewSessionRepositoryWithDB creates a new SessionRepository with a transaction or connection,
// bound to the default tenant.
func NewSessionRepositoryWithDB(db DBTX) *SessionRepository {
	return NewSessionRepositoryWithTenant(db, "")
}

// NewSessionRepositoryWithTenant creates a SessionRepository scoped to one tenant.
// An empty tenantID resolves to the default tenant.
func NewSessionRepositoryWithTenant(db DBTX, tenantID string) *SessionRepository {
	if tenantID == "" {
		tenantID = DefaultTenantID
	}
	return &SessionRepository{db: db, tenantID: tenantID}
}

// Create creates a new session.
func (r *SessionRepository) Create(ctx context.Context, session *models.Session) error {
	profileJSON, err := json.Marshal(session.UserProfile)
	if err != nil {
		return errors.Wrap(err, "marshal profile")
	}

	metadataJSON, err := json.Marshal(session.Metadata)
	if err != nil {
		return errors.Wrap(err, "marshal metadata")
	}

	query := `
		INSERT INTO sessions (session_id, tenant_id, user_id, input, status, user_profile, metadata, created_at, updated_at, expired_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`

	// Pass nil for zero ExpiredAt so PostgreSQL stores NULL instead of the
	// zero time (year 1), which would make every session look expired on the
	// first CleanupExpired run.
	var expiredAt any
	if !session.ExpiredAt.IsZero() {
		expiredAt = session.ExpiredAt
	}

	_, err = r.db.ExecContext(ctx, query,
		session.SessionID,
		r.tenantID,
		session.UserID,
		session.Input,
		session.Status,
		profileJSON,
		metadataJSON,
		session.CreatedAt,
		session.UpdatedAt,
		expiredAt,
	)
	if err != nil {
		return errors.Wrap(err, "insert session")
	}

	return nil
}

// GetByID retrieves a session by ID.
func (r *SessionRepository) GetByID(ctx context.Context, sessionID string) (*models.Session, error) {
	query := `
		SELECT session_id, user_id, input, status, user_profile, metadata, created_at, updated_at, expired_at
		FROM sessions WHERE session_id = $1 AND tenant_id = $2
	`

	var session models.Session
	var profileJSON, metadataJSON []byte
	var expiredAt sql.NullTime

	err := r.db.QueryRowContext(ctx, query, sessionID, r.tenantID).Scan(
		&session.SessionID,
		&session.UserID,
		&session.Input,
		&session.Status,
		&profileJSON,
		&metadataJSON,
		&session.CreatedAt,
		&session.UpdatedAt,
		&expiredAt,
	)
	if err == sql.ErrNoRows {
		return nil, errors.ErrRecordNotFound
	}
	if err != nil {
		return nil, errors.Wrap(err, "query session")
	}
	if expiredAt.Valid {
		session.ExpiredAt = expiredAt.Time
	}

	if err := json.Unmarshal(profileJSON, &session.UserProfile); err != nil {
		return nil, errors.Wrap(err, "unmarshal profile")
	}
	if err := json.Unmarshal(metadataJSON, &session.Metadata); err != nil {
		return nil, errors.Wrap(err, "unmarshal metadata")
	}

	return &session, nil
}

// Update updates a session.
func (r *SessionRepository) Update(ctx context.Context, session *models.Session) error {
	profileJSON, err := json.Marshal(session.UserProfile)
	if err != nil {
		return errors.Wrap(err, "marshal profile")
	}

	metadataJSON, err := json.Marshal(session.Metadata)
	if err != nil {
		return errors.Wrap(err, "marshal metadata")
	}

	query := `
		UPDATE sessions
		SET status = $1, user_profile = $2, metadata = $3, updated_at = $4
		WHERE session_id = $5 AND tenant_id = $6
	`

	result, err := r.db.ExecContext(ctx, query,
		session.Status,
		profileJSON,
		metadataJSON,
		time.Now(),
		session.SessionID,
		r.tenantID,
	)
	if err != nil {
		return errors.Wrap(err, "update session")
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "rows affected")
	}
	if rowsAffected == 0 {
		return errors.ErrRecordNotFound
	}

	return nil
}

// Delete deletes a session.
func (r *SessionRepository) Delete(ctx context.Context, sessionID string) error {
	query := `DELETE FROM sessions WHERE session_id = $1 AND tenant_id = $2`

	result, err := r.db.ExecContext(ctx, query, sessionID, r.tenantID)
	if err != nil {
		return errors.Wrap(err, "delete session")
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "rows affected")
	}
	if rowsAffected == 0 {
		return errors.ErrRecordNotFound
	}

	return nil
}

// ListByUserID lists sessions by user ID.
func (r *SessionRepository) ListByUserID(ctx context.Context, userID string, limit, offset int) ([]*models.Session, error) {
	query := `
		SELECT session_id, user_id, input, status, user_profile, metadata, created_at, updated_at, expired_at
		FROM sessions WHERE user_id = $1 AND tenant_id = $2
		ORDER BY created_at DESC
		LIMIT $3 OFFSET $4
	`

	rows, err := r.db.QueryContext(ctx, query, userID, r.tenantID, limit, offset)
	if err != nil {
		return nil, errors.Wrap(err, "query sessions")
	}
	defer func() { _ = rows.Close() }()

	var sessions []*models.Session
	for rows.Next() {
		var session models.Session
		var profileJSON, metadataJSON []byte
		// expired_at is nullable (Create binds NULL for a zero ExpiredAt);
		// scanning into time.Time fails on NULL and kills the whole listing.
		var expiredAt sql.NullTime

		if err := rows.Scan(
			&session.SessionID,
			&session.UserID,
			&session.Input,
			&session.Status,
			&profileJSON,
			&metadataJSON,
			&session.CreatedAt,
			&session.UpdatedAt,
			&expiredAt,
		); err != nil {
			return nil, errors.Wrap(err, "scan session")
		}
		if expiredAt.Valid {
			session.ExpiredAt = expiredAt.Time
		}

		if err := json.Unmarshal(profileJSON, &session.UserProfile); err != nil {
			return nil, errors.Wrap(err, "unmarshal profile")
		}
		if err := json.Unmarshal(metadataJSON, &session.Metadata); err != nil {
			return nil, errors.Wrap(err, "unmarshal metadata")
		}

		sessions = append(sessions, &session)
	}

	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "iterate sessions")
	}

	return sessions, nil
}

// CleanupExpired removes expired sessions.
func (r *SessionRepository) CleanupExpired(ctx context.Context) (int64, error) {
	query := `DELETE FROM sessions WHERE expired_at IS NOT NULL AND expired_at < $1 AND tenant_id = $2`

	result, err := r.db.ExecContext(ctx, query, time.Now(), r.tenantID)
	if err != nil {
		return 0, errors.Wrap(err, "cleanup expired")
	}

	return result.RowsAffected()
}
