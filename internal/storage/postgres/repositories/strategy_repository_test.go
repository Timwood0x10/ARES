//go:build integration
// +build integration

package repositories

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStrategyRepository_IsActive verifies is_active is populated on read.
// Regression: the is_active column was never selected by GetActive/List, so
// StrategyRow.IsActive was always false regardless of the stored value.
func TestStrategyRepository_IsActive(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	repo := NewStrategyRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.SetActive(ctx, StrategyRow{
		ID:      "strat-a",
		Name:    "baseline",
		Version: 1,
		Params:  map[string]any{"pop": 10},
		Score:   0.5,
	}))

	active, err := repo.GetActive(ctx)
	require.NoError(t, err)
	require.NotNil(t, active)
	assert.Equal(t, "strat-a", active.ID)
	assert.True(t, active.IsActive, "GetActive should report IsActive=true")

	listed, err := repo.List(ctx, 10)
	require.NoError(t, err)
	require.NotEmpty(t, listed)
	var found bool
	for _, s := range listed {
		if s.ID == "strat-a" {
			found = true
			assert.True(t, s.IsActive, "active strategy should have IsActive=true in List")
		}
	}
	assert.True(t, found, "strategy should appear in List")

	// Activating a second strategy deactivates the first.
	require.NoError(t, repo.SetActive(ctx, StrategyRow{
		ID:      "strat-b",
		Name:    "improved",
		Version: 2,
		Params:  map[string]any{"pop": 12},
		Score:   0.7,
	}))

	active, err = repo.GetActive(ctx)
	require.NoError(t, err)
	require.NotNil(t, active)
	assert.Equal(t, "strat-b", active.ID)
	assert.True(t, active.IsActive)

	listed, err = repo.List(ctx, 10)
	require.NoError(t, err)
	for _, s := range listed {
		switch s.ID {
		case "strat-a":
			assert.False(t, s.IsActive, "deactivated strategy should have IsActive=false")
		case "strat-b":
			assert.True(t, s.IsActive, "newly activated strategy should have IsActive=true")
		}
	}
}
