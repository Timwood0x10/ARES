package evolution

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"
)

// StoreDB defines the minimal database interface needed by EvolutionStore.
// Both *sql.DB and *sql.Tx satisfy this interface.
type StoreDB interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

// EvolutionStore implements StrategyStore using a relational database.
// It persists strategies and manages the active deployment marker.
// Tables must be created by running the evolution migration DDL.
type EvolutionStore struct {
	db StoreDB
}

// NewEvolutionStore creates a new EvolutionStore instance.
//
// Args:
//
//	db - database connection implementing StoreDB.
//
// Returns:
//
//	*EvolutionStore - the store instance.
func NewEvolutionStore(db StoreDB) *EvolutionStore {
	return &EvolutionStore{db: db}
}

// Compile-time interface compliance check.
var _ StrategyStore = (*EvolutionStore)(nil)

// GetActive returns the currently deployed strategy.
// Returns nil if no strategy has been stored yet.
func (s *EvolutionStore) GetActive(ctx context.Context) (*Strategy, error) {
	query := `
		SELECT id, COALESCE(parent_id, ''), COALESCE(name, ''), version, params,
		       COALESCE(prompt_template, ''), COALESCE(strategy_mutation_type, ''),
		       COALESCE(mutation_desc, ''), score, created_at
		FROM evolution_strategies
		WHERE is_active = TRUE
		ORDER BY version DESC
		LIMIT 1
	`

	strategy := &Strategy{}
	var paramsBytes []byte
	var createdAt time.Time

	err := s.db.QueryRowContext(ctx, query).Scan(
		&strategy.ID,
		&strategy.ParentID,
		&strategy.Name,
		&strategy.Version,
		&paramsBytes,
		&strategy.PromptTemplate,
		&strategy.StrategyMutationType,
		&strategy.MutationDesc,
		&strategy.Score,
		&createdAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	strategy.CreatedAt = createdAt

	if len(paramsBytes) > 0 {
		if err := json.Unmarshal(paramsBytes, &strategy.Params); err != nil {
			return nil, err
		}
	}

	return strategy, nil
}

// SetActive persists a strategy as the active deployment.
// It uses a two-step process: first deactivates all existing strategies,
// then inserts/updates the new active strategy. When StoreDB is *sql.Tx,
// both operations share the same transaction for atomicity.
func (s *EvolutionStore) SetActive(ctx context.Context, st Strategy) error {
	paramsJSON, err := json.Marshal(st.Params)
	if err != nil {
		return err
	}

	if _, err := s.db.ExecContext(ctx,
		`UPDATE evolution_strategies SET is_active = FALSE WHERE is_active = TRUE`); err != nil {
		return err
	}

	createdAt := st.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}

	query := `
		INSERT INTO evolution_strategies
			(id, parent_id, name, version, params, prompt_template,
			 strategy_mutation_type, mutation_desc, score, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, TRUE, $10, NOW())
		ON CONFLICT (id) DO UPDATE SET
			is_active = TRUE,
			score = $9,
			updated_at = NOW()
	`
	_, err = s.db.ExecContext(ctx, query,
		st.ID, st.ParentID, st.Name, st.Version, paramsJSON,
		st.PromptTemplate, st.StrategyMutationType, st.MutationDesc,
		st.Score, createdAt,
	)
	return err
}

// List returns the last n strategies ordered by version descending.
func (s *EvolutionStore) List(ctx context.Context, n int) ([]Strategy, error) {
	query := `
		SELECT id, COALESCE(parent_id, ''), COALESCE(name, ''), version, params,
		       COALESCE(prompt_template, ''), COALESCE(strategy_mutation_type, ''),
		       COALESCE(mutation_desc, ''), score, created_at
		FROM evolution_strategies
		ORDER BY version DESC
		LIMIT $1
	`

	rows, err := s.db.QueryContext(ctx, query, n)
	if err != nil {
		return nil, err
	}
	defer func() {
		err := rows.Close()
		if err != nil {
			slog.Error("error closing rows", "error", err)
		}
	}()

	var strategies []Strategy
	for rows.Next() {
		var st Strategy
		var paramsBytes []byte
		var createdAt time.Time

		if err := rows.Scan(
			&st.ID, &st.ParentID, &st.Name, &st.Version, &paramsBytes,
			&st.PromptTemplate, &st.StrategyMutationType, &st.MutationDesc,
			&st.Score, &createdAt,
		); err != nil {
			return nil, err
		}

		st.CreatedAt = createdAt

		if len(paramsBytes) > 0 {
			if err := json.Unmarshal(paramsBytes, &st.Params); err != nil {
				return nil, err
			}
		}

		strategies = append(strategies, st)
	}

	return strategies, rows.Err()
}
