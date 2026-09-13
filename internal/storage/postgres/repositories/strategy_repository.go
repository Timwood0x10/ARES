package repositories

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
)

// StrategyRow is a database row representation of an evolution strategy.
type StrategyRow struct {
	ID                   string
	Name                 string
	Version              int
	Params               map[string]any
	ParentID             string
	PromptTemplate       string
	StrategyMutationType string
	MutationDesc         string
	Score                float64
	CreatedAt            time.Time
	IsActive             bool
}

// StrategyRepository provides Postgres persistence for evolution strategies.
//
// UNSCHEMA-COMPATIBLE / NO PRODUCTION CALLERS. This repository assumes a
// one-row-per-strategy model keyed on VARCHAR id with ON CONFLICT (id)
// upsert. The authoritative evolution_strategies schema in migrate.go is
// append-only one-row-per-version keyed on BIGSERIAL id plus strategy_id, and
// is used by runtime/ares_evolution's PGStrategyStore (the only store wired in
// production). That schema cannot express this repository's upsert model, so
// the two are mutually incompatible — wiring this repository against the
// migrated table would fail at runtime, not merely read across tenants.
//
// Queries here still scope by tenant_id, but tenant safety is not the
// blocking concern; porting to the versioned schema is. Its tests run against
// a legacy-shaped table created in repository_test_helper.go.
type StrategyRepository struct {
	db       postgres.DBTX
	tenantID string
}

// NewStrategyRepository creates a new StrategyRepository.
//
// Args:
//
//	db - database connection or transaction implementing DBTX interface.
//
// Returns:
//
//	*StrategyRepository - the configured repository instance bound to the
//	default tenant. Use NewStrategyRepositoryWithTenant to scope a specific
//	tenant.
func NewStrategyRepository(db postgres.DBTX) *StrategyRepository {
	return NewStrategyRepositoryWithTenant(db, "")
}

// NewStrategyRepositoryWithTenant creates a StrategyRepository scoped to one
// tenant. An empty tenantID resolves to the default tenant.
func NewStrategyRepositoryWithTenant(db postgres.DBTX, tenantID string) *StrategyRepository {
	if tenantID == "" {
		tenantID = "default"
	}
	return &StrategyRepository{db: db, tenantID: tenantID}
}

// GetActive returns the currently active strategy for this repository's
// tenant, or nil when no strategy is marked active.
//
// Tenant-scoped: evolution_strategies now carries tenant_id, and the predicate
// confines this read to the bound tenant — another tenant's active strategy is
// invisible here.
//
// Args:
//
//	ctx - database operation context.
//
// Returns:
//
//	*StrategyRow - the active strategy row, or nil.
//	error - non-nil if query fails.
func (r *StrategyRepository) GetActive(ctx context.Context) (*StrategyRow, error) {
	query := `SELECT id, name, version, params, parent_id, prompt_template,
		strategy_mutation_type, mutation_desc, score, created_at, is_active
		FROM evolution_strategies WHERE is_active = true AND tenant_id = $1
		ORDER BY version DESC LIMIT 1`

	row := r.db.QueryRowContext(ctx, query, r.tenantID)

	var (
		id, name, parentID, promptTmpl, mutationType, mutationDesc string
		version                                                    int
		score                                                      float64
		createdAt                                                  time.Time
		paramsJSON                                                 []byte
		isActive                                                   bool
	)

	err := row.Scan(&id, &name, &version, &paramsJSON, &parentID,
		&promptTmpl, &mutationType, &mutationDesc, &score, &createdAt, &isActive)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, errors.ErrNotFound
		}
		return nil, errors.Wrap(err, "get active strategy")
	}

	params := make(map[string]any)
	if len(paramsJSON) > 0 {
		if err := json.Unmarshal(paramsJSON, &params); err != nil {
			return nil, errors.Wrap(err, "unmarshal params")
		}
	}

	return &StrategyRow{
		ID:                   id,
		Name:                 name,
		Version:              version,
		Params:               params,
		ParentID:             parentID,
		PromptTemplate:       promptTmpl,
		StrategyMutationType: mutationType,
		MutationDesc:         mutationDesc,
		Score:                score,
		CreatedAt:            createdAt,
		IsActive:             isActive,
	}, nil
}

// beginTxer abstracts transaction creation for *sql.DB.
// *sql.Tx and other DBTX implementations fall back to no-tx path.
type beginTxer interface {
	BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error)
}

// SetActive persists a strategy and marks it as active.
// Any previously active strategy is deactivated.
//
// Args:
//
//	ctx - database operation context.
//	s - the strategy to persist.
//
// Returns:
//
//	error - non-nil if insert or update fails.
func (r *StrategyRepository) SetActive(ctx context.Context, s StrategyRow) error {
	paramsJSON, err := json.Marshal(s.Params)
	if err != nil {
		return errors.Wrap(err, "marshal params")
	}

	if btx, ok := r.db.(beginTxer); ok {
		return r.setActiveTx(ctx, btx, s, paramsJSON)
	}

	return r.setActiveNoTx(ctx, s, paramsJSON)
}

func (r *StrategyRepository) setActiveTx(ctx context.Context, db beginTxer, s StrategyRow, paramsJSON []byte) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return errors.Wrap(err, "begin tx")
	}
	committed := false
	defer func() {
		if !committed {
			if err := tx.Rollback(); err != nil {
				log.Warn("strategy repo: rollback", "error", err)
			}
		}
	}()

	deactivateQ := `UPDATE evolution_strategies SET is_active = false WHERE is_active = true AND tenant_id = $1`
	// RowsAffected is intentionally ignored: 0 affected rows means no
	// previously active strategy (normal on first deployment).
	if _, err := tx.ExecContext(ctx, deactivateQ, r.tenantID); err != nil {
		return errors.Wrap(err, "deactivate strategies")
	}

	insertQ := r.activeInsertQuery()

	now := time.Now()
	createdAt := s.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}

	if _, err = tx.ExecContext(ctx, insertQ,
		r.tenantID, s.ID, s.Name, s.Version, paramsJSON,
		s.ParentID, s.PromptTemplate,
		s.StrategyMutationType, s.MutationDesc,
		s.Score, createdAt,
	); err != nil {
		return errors.Wrap(err, "insert strategy")
	}

	committed = true
	return errors.Wrap(tx.Commit(), "commit tx")
}

// activeInsertQuery builds the strategy upsert used by SetActive. The
// ON CONFLICT (id) upsert matters for re-activation and rollback: the row
// for a previously deployed strategy already exists, and a plain INSERT hit
// the primary key and rolled back the whole transaction — deactivation
// included — so rolling back to a KNOWN strategy always failed.
func (r *StrategyRepository) activeInsertQuery() string {
	return `INSERT INTO evolution_strategies
		(tenant_id, id, is_active, name, version, params, parent_id, prompt_template,
		 strategy_mutation_type, mutation_desc, score, created_at, updated_at)
		VALUES ($1, $2, true, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW())
		ON CONFLICT (id) DO UPDATE SET
			is_active = true,
			name = EXCLUDED.name,
			version = EXCLUDED.version,
			params = EXCLUDED.params,
			parent_id = EXCLUDED.parent_id,
			prompt_template = EXCLUDED.prompt_template,
			strategy_mutation_type = EXCLUDED.strategy_mutation_type,
			mutation_desc = EXCLUDED.mutation_desc,
			score = EXCLUDED.score,
			updated_at = NOW()
		-- id is not tenant-qualified: only re-activate a row this tenant
		-- already owns. Another tenant's strategy with the same id must not
		-- be taken over (the SET used to re-bind tenant_id unconditionally).
		WHERE evolution_strategies.tenant_id = EXCLUDED.tenant_id`
}

// setActiveNoTx mirrors setActiveTx for a handle that cannot begin a
// transaction. Without a transaction the two statements cannot be atomic, so
// their ORDER is the safety property: the new strategy is inserted/activated
// FIRST, then every other active row is deactivated. A failed insert therefore
// leaves the previously active strategy in place. The old order deactivated
// everything first, so an insert failure left the system with NO active
// strategy at all.
func (r *StrategyRepository) setActiveNoTx(ctx context.Context, s StrategyRow, paramsJSON []byte) error {
	insertQ := r.activeInsertQuery()

	now := time.Now()
	createdAt := s.CreatedAt
	if createdAt.IsZero() {
		createdAt = now
	}

	if _, err := r.db.ExecContext(ctx, insertQ,
		r.tenantID, s.ID, s.Name, s.Version, paramsJSON,
		s.ParentID, s.PromptTemplate,
		s.StrategyMutationType, s.MutationDesc,
		s.Score, createdAt,
	); err != nil {
		return errors.Wrap(err, "insert strategy")
	}

	// Deactivate every OTHER active strategy for this tenant, excluding the one
	// just activated.
	deactivateQ := `UPDATE evolution_strategies SET is_active = false WHERE is_active = true AND tenant_id = $1 AND id <> $2`
	if _, err := r.db.ExecContext(ctx, deactivateQ, r.tenantID, s.ID); err != nil {
		return errors.Wrap(err, "deactivate strategies")
	}
	return nil
}

// List returns the last n strategies ordered by version descending.
//
// Args:
//
//	ctx - database operation context.
//	n - maximum number of strategies to return.
//
// Returns:
//
//	[]StrategyRow - the strategy list (never nil).
//	error - non-nil if query fails.
func (r *StrategyRepository) List(ctx context.Context, n int) ([]StrategyRow, error) {
	query := `SELECT id, name, version, params, parent_id, prompt_template,
		strategy_mutation_type, mutation_desc, score, created_at, is_active
		FROM evolution_strategies WHERE tenant_id = $1 ORDER BY version DESC LIMIT $2`

	rows, err := r.db.QueryContext(ctx, query, r.tenantID, n)
	if err != nil {
		return nil, errors.Wrap(err, "list strategies")
	}
	defer func() {
		if err := rows.Close(); err != nil {
			log.Warn("strategy repo: close rows", "error", err)
		}
	}()

	var strategies []StrategyRow
	for rows.Next() {
		var (
			id, name, parentID, promptTmpl, mutationType, mutationDesc string
			version                                                    int
			score                                                      float64
			createdAt                                                  time.Time
			paramsJSON                                                 []byte
			isActive                                                   bool
		)

		if err := rows.Scan(&id, &name, &version, &paramsJSON, &parentID,
			&promptTmpl, &mutationType, &mutationDesc, &score, &createdAt, &isActive); err != nil {
			return nil, errors.Wrap(err, "scan strategy")
		}

		params := make(map[string]any)
		if len(paramsJSON) > 0 {
			if err := json.Unmarshal(paramsJSON, &params); err != nil {
				return nil, errors.Wrap(err, "unmarshal params")
			}
		}

		strategies = append(strategies, StrategyRow{
			ID:                   id,
			Name:                 name,
			Version:              version,
			Params:               params,
			ParentID:             parentID,
			PromptTemplate:       promptTmpl,
			StrategyMutationType: mutationType,
			MutationDesc:         mutationDesc,
			Score:                score,
			CreatedAt:            createdAt,
			IsActive:             isActive,
		})
	}

	if strategies == nil {
		strategies = make([]StrategyRow, 0)
	}

	return strategies, rows.Err()
}
