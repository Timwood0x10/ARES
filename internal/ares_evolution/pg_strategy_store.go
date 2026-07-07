package evolution

import (
	"context"
	"errors"
	"fmt"

	apperrors "github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/storage/postgres/repositories"
)

// PGStrategyStore wraps a StrategyRepository to implement the StrategyStore interface.
type PGStrategyStore struct {
	repo *repositories.StrategyRepository
}

// NewPGStrategyStore creates a PG-backed strategy store.
//
// Args:
//
//	repo - the postgres strategy repository (must not be nil).
//
// Returns:
//
//	*PGStrategyStore - the configured store.
//	error - non-nil if repo is nil.
func NewPGStrategyStore(repo *repositories.StrategyRepository) (*PGStrategyStore, error) {
	if repo == nil {
		return nil, fmt.Errorf("strategy repository must not be nil")
	}
	return &PGStrategyStore{repo: repo}, nil
}

// GetActive returns the currently deployed strategy.
// Returns nil if no strategy has been stored yet.
//
// Args:
//
//	ctx - operation context for store lookup.
//
// Returns:
//
//	*Strategy - the active strategy, or nil.
//	error - non-nil if store lookup fails.
func (s *PGStrategyStore) GetActive(ctx context.Context) (*Strategy, error) {
	row, err := s.repo.GetActive(ctx)
	if err != nil {
		if errors.Is(err, apperrors.ErrNotFound) {
			return nil, err
		}
		return nil, fmt.Errorf("pg get active: %w", err)
	}
	return &Strategy{
		ID:                   row.ID,
		Name:                 row.Name,
		Version:              row.Version,
		Params:               row.Params,
		ParentID:             row.ParentID,
		PromptTemplate:       row.PromptTemplate,
		StrategyMutationType: row.StrategyMutationType,
		MutationDesc:         row.MutationDesc,
		Score:                row.Score,
		CreatedAt:            row.CreatedAt,
	}, nil
}

// SetActive persists a strategy as the active deployment.
//
// Args:
//
//	ctx - operation context for store write.
//	st - the strategy to persist (must not be nil).
//
// Returns:
//
//	error - non-nil if strategy is nil or store operation fails.
func (s *PGStrategyStore) SetActive(ctx context.Context, st *Strategy) error {
	if st == nil {
		return fmt.Errorf("strategy must not be nil")
	}
	row := repositories.StrategyRow{
		ID:                   st.ID,
		Name:                 st.Name,
		Version:              st.Version,
		Params:               st.Params,
		ParentID:             st.ParentID,
		PromptTemplate:       st.PromptTemplate,
		StrategyMutationType: st.StrategyMutationType,
		MutationDesc:         st.MutationDesc,
		Score:                st.Score,
		CreatedAt:            st.CreatedAt,
	}
	if err := s.repo.SetActive(ctx, row); err != nil {
		return fmt.Errorf("pg set active: %w", err)
	}
	log.Info("[PGStrategyStore] Strategy persisted",
		"strategy_id", st.ID,
		"version", st.Version,
		"score", st.Score,
	)
	return nil
}

// GetHistory returns the last n strategies for the given strategy ID.
// Delegates to List with the strategy ID filter (for now, returns full list).
//
// Args:
//
//	ctx - operation context for store lookup.
//	id - strategy ID to filter by (unused currently, returns all).
//	n - maximum number of strategies to return.
//
// Returns:
//
//	[]*Strategy - the strategy list.
//	error - non-nil if query fails.
func (s *PGStrategyStore) GetHistory(ctx context.Context, id string, n int) ([]*Strategy, error) {
	rows, err := s.repo.List(ctx, n)
	if err != nil {
		return nil, fmt.Errorf("pg get history: %w", err)
	}

	strategies := make([]*Strategy, len(rows))
	for i, row := range rows {
		strategies[i] = &Strategy{
			ID:                   row.ID,
			Name:                 row.Name,
			Version:              row.Version,
			Params:               row.Params,
			ParentID:             row.ParentID,
			PromptTemplate:       row.PromptTemplate,
			StrategyMutationType: row.StrategyMutationType,
			MutationDesc:         row.MutationDesc,
			Score:                row.Score,
			CreatedAt:            row.CreatedAt,
		}
	}
	return strategies, nil
}

// List returns the last n strategies ordered by version descending.
//
// Args:
//
//	ctx - operation context for store lookup.
//	n - maximum number of strategies to return.
//
// Returns:
//
//	[]Strategy - the strategy list (never nil).
//	error - non-nil if query fails.
func (s *PGStrategyStore) List(ctx context.Context, n int) ([]Strategy, error) {
	rows, err := s.repo.List(ctx, n)
	if err != nil {
		return nil, fmt.Errorf("pg list: %w", err)
	}

	strategies := make([]Strategy, len(rows))
	for i, row := range rows {
		strategies[i] = Strategy{
			ID:                   row.ID,
			Name:                 row.Name,
			Version:              row.Version,
			Params:               row.Params,
			ParentID:             row.ParentID,
			PromptTemplate:       row.PromptTemplate,
			StrategyMutationType: row.StrategyMutationType,
			MutationDesc:         row.MutationDesc,
			Score:                row.Score,
			CreatedAt:            row.CreatedAt,
		}
	}
	return strategies, nil
}
