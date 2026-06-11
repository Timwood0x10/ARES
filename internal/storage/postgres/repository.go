package postgres

import (
	"context"
	"database/sql"
	"log/slog"

	coreerrors "goagent/internal/core/errors"
	"goagent/internal/core/models"
	"goagent/internal/errors"
	"goagent/internal/storage"
)

// DBTX is an interface that both *sql.DB and *sql.Tx satisfy.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

// Repository provides a unified interface for all data access.
type Repository struct {
	Session   *SessionRepository
	Recommend *RecommendRepository
	Profile   *ProfileRepository
	Vector    storage.VectorStore
	pool      *Pool
	tx        *sql.Tx
	close     func()
}

// NewRepository creates a new Repository with all sub-repositories.
func NewRepository(pool *Pool) *Repository {
	return &Repository{
		Session:   NewSessionRepository(pool),
		Recommend: NewRecommendRepository(pool),
		Profile:   NewProfileRepository(pool),
		Vector:    NewVectorSearcher(pool, pool.cfg.Embedding),
		pool:      pool,
	}
}

// Pool returns the underlying connection pool.
func (r *Repository) Pool() *Pool {
	return r.pool
}

// Close closes the repository and its pool.
func (r *Repository) Close() error {
	if r.close != nil {
		r.close()
	}
	if r.pool == nil {
		return nil
	}
	return r.pool.Close()
}

// IsTransaction returns true if this repository is in transaction mode.
func (r *Repository) IsTransaction() bool {
	return r.tx != nil
}

// Transaction executes a function within a transaction.
func (r *Repository) Transaction(ctx context.Context, fn func(repo *Repository) error) error {
	tx, err := r.pool.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	// Create transaction-level repositories that use the transaction
	txRepo := &Repository{
		Session:   NewSessionRepositoryWithDB(tx),
		Recommend: NewRecommendRepositoryWithDB(tx),
		Profile:   NewProfileRepositoryWithDB(tx),
		Vector:    NewVectorSearcherWithDB(tx, r.pool.cfg.Embedding),
		tx:        tx,
	}

	if err := fn(txRepo); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return errors.Wrapf(err, "transaction failed: rollback error: %v", rbErr)
		}
		return err
	}

	return tx.Commit()
}

// WithTransaction creates a new repository bound to a transaction.
func (r *Repository) WithTransaction(ctx context.Context) (*Repository, error) {
	tx, err := r.pool.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}

	txRepo := &Repository{
		Session:   NewSessionRepositoryWithDB(tx),
		Recommend: NewRecommendRepositoryWithDB(tx),
		Profile:   NewProfileRepositoryWithDB(tx),
		Vector:    NewVectorSearcherWithDB(tx, r.pool.cfg.Embedding),
		tx:        tx,
		close: func() {
			if tx != nil {
				if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
					slog.Warn("failed to rollback orphaned transaction", "error", err)
				}
			}
		},
	}

	return txRepo, nil
}

// Commit commits the transaction.
func (r *Repository) Commit() error {
	if r.tx == nil {
		return coreerrors.ErrNoTransaction
	}
	err := r.tx.Commit()
	if err == nil || err == sql.ErrTxDone {
		r.tx = nil
		r.close = nil
	}
	return err
}

// Rollback rolls back the transaction.
func (r *Repository) Rollback() error {
	if r.tx == nil {
		return nil
	}
	err := r.tx.Rollback()
	if err == nil || err == sql.ErrTxDone {
		r.tx = nil
		r.close = nil
	}
	return err
}

// SaveSession saves a session and its results.
func (r *Repository) SaveSession(ctx context.Context, session *models.Session, result *models.RecommendResult) error {
	return r.Transaction(ctx, func(txRepo *Repository) error {
		if err := txRepo.Session.Create(ctx, session); err != nil {
			return err
		}

		if result != nil {
			if err := txRepo.Recommend.Create(ctx, result); err != nil {
				return err
			}
		}

		return nil
	})
}

// GetSessionWithResult retrieves a session with its recommendation result.
func (r *Repository) GetSessionWithResult(ctx context.Context, sessionID string) (*models.Session, *models.RecommendResult, error) {
	session, err := r.Session.GetByID(ctx, sessionID)
	if err != nil {
		return nil, nil, err
	}

	result, err := r.Recommend.GetBySessionID(ctx, sessionID)
	if err != nil && err != coreerrors.ErrRecordNotFound {
		return nil, nil, err
	}

	return session, result, nil
}

// SaveProfile saves a user profile.
func (r *Repository) SaveProfile(ctx context.Context, profile *models.UserProfile) error {
	exists, err := r.Profile.Exists(ctx, profile.UserID)
	if err != nil {
		return err
	}

	if exists {
		return r.Profile.Update(ctx, profile)
	}

	return r.Profile.Create(ctx, profile)
}
