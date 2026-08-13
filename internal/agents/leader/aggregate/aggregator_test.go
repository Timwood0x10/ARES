package aggregate

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/core/models"
)

// helper to create a RecommendItem with the given ItemID.
func newItem(id string) *models.RecommendItem {
	return &models.RecommendItem{ItemID: id, Name: "item-" + id}
}

// helper to create a successful TaskResult with the given items.
func newSuccessResult(taskID string, items []*models.RecommendItem, createdAt time.Time) *models.TaskResult {
	return &models.TaskResult{
		TaskID:    taskID,
		Success:   true,
		Items:     items,
		CreatedAt: createdAt,
	}
}

// helper to create a failed TaskResult.
func newFailedResult(taskID string) *models.TaskResult {
	return &models.TaskResult{
		TaskID:  taskID,
		Success: false,
	}
}

// TestAggregate_SortByNone verifies that items retain their original order.
func TestAggregate_SortByNone(t *testing.T) {
	agg := NewResultAggregator(false, 10, SortByNone)

	now := time.Now()
	results := []*models.TaskResult{
		newSuccessResult("t1", []*models.RecommendItem{newItem("a"), newItem("b")}, now),
		newSuccessResult("t2", []*models.RecommendItem{newItem("c"), newItem("d")}, now),
	}

	res, err := agg.Aggregate(context.Background(), results, nil)
	require.NoError(t, err)
	require.Len(t, res.Items, 4)

	expected := []string{"a", "b", "c", "d"}
	for i, id := range expected {
		assert.Equal(t, id, res.Items[i].ItemID, "item at index %d should have ItemID %q", i, id)
	}
}

// TestAggregate_SortByPriority verifies descending priority order.
func TestAggregate_SortByPriority(t *testing.T) {
	agg := NewResultAggregator(false, 10, SortByPriority)

	now := time.Now()
	results := []*models.TaskResult{
		newSuccessResult("low", []*models.RecommendItem{newItem("low-item")}, now),
		newSuccessResult("high", []*models.RecommendItem{newItem("high-item")}, now),
		newSuccessResult("mid", []*models.RecommendItem{newItem("mid-item")}, now),
	}

	tasks := []*models.Task{
		{TaskID: "low", Priority: 1},
		{TaskID: "high", Priority: 10},
		{TaskID: "mid", Priority: 5},
	}

	res, err := agg.Aggregate(context.Background(), results, tasks)
	require.NoError(t, err)
	require.Len(t, res.Items, 3)

	assert.Equal(t, "high-item", res.Items[0].ItemID)
	assert.Equal(t, "mid-item", res.Items[1].ItemID)
	assert.Equal(t, "low-item", res.Items[2].ItemID)
}

// TestAggregate_SortByCreatedAt verifies newest-first ordering.
func TestAggregate_SortByCreatedAt(t *testing.T) {
	agg := NewResultAggregator(false, 10, SortByCreatedAt)

	oldest := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	middle := time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)
	newest := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	results := []*models.TaskResult{
		newSuccessResult("t1", []*models.RecommendItem{newItem("old")}, oldest),
		newSuccessResult("t2", []*models.RecommendItem{newItem("new")}, newest),
		newSuccessResult("t3", []*models.RecommendItem{newItem("mid")}, middle),
	}

	res, err := agg.Aggregate(context.Background(), results, nil)
	require.NoError(t, err)
	require.Len(t, res.Items, 3)

	assert.Equal(t, "new", res.Items[0].ItemID)
	assert.Equal(t, "mid", res.Items[1].ItemID)
	assert.Equal(t, "old", res.Items[2].ItemID)
}

// TestAggregate_UnknownSortBy verifies fallback to original order.
func TestAggregate_UnknownSortBy(t *testing.T) {
	agg := NewResultAggregator(false, 10, "unknown")

	now := time.Now()
	results := []*models.TaskResult{
		newSuccessResult("t1", []*models.RecommendItem{newItem("x"), newItem("y")}, now),
	}

	res, err := agg.Aggregate(context.Background(), results, nil)
	require.NoError(t, err)
	require.Len(t, res.Items, 2)

	assert.Equal(t, "x", res.Items[0].ItemID)
	assert.Equal(t, "y", res.Items[1].ItemID)
}

// TestAggregate_Deduplication verifies duplicate removal by ItemID.
func TestAggregate_Deduplication(t *testing.T) {
	agg := NewResultAggregator(true, 10, SortByNone)

	now := time.Now()
	dup := newItem("dup")
	results := []*models.TaskResult{
		newSuccessResult("t1", []*models.RecommendItem{newItem("unique"), dup}, now),
		newSuccessResult("t2", []*models.RecommendItem{dup, newItem("other")}, now),
	}

	res, err := agg.Aggregate(context.Background(), results, nil)
	require.NoError(t, err)

	assert.Len(t, res.Items, 3, "expected 3 items after deduplication")

	ids := make([]string, len(res.Items))
	for i, item := range res.Items {
		ids[i] = item.ItemID
	}
	assert.Contains(t, ids, "unique")
	assert.Contains(t, ids, "dup")
	assert.Contains(t, ids, "other")
}

// TestAggregate_MaxItemsLimit verifies truncation to maxItems.
func TestAggregate_MaxItemsLimit(t *testing.T) {
	agg := NewResultAggregator(false, 2, SortByNone)

	now := time.Now()
	results := []*models.TaskResult{
		newSuccessResult("t1", []*models.RecommendItem{
			newItem("1"), newItem("2"), newItem("3"),
		}, now),
		newSuccessResult("t2", []*models.RecommendItem{
			newItem("4"), newItem("5"),
		}, now),
	}

	res, err := agg.Aggregate(context.Background(), results, nil)
	require.NoError(t, err)
	assert.Len(t, res.Items, 2, "expected at most 2 items due to maxItems limit")
}

// TestAggregate_NilResults verifies nil elements are silently skipped.
func TestAggregate_NilResults(t *testing.T) {
	agg := NewResultAggregator(false, 10, SortByNone)

	now := time.Now()
	results := []*models.TaskResult{
		nil,
		newSuccessResult("t1", []*models.RecommendItem{newItem("ok")}, now),
		nil,
	}

	res, err := agg.Aggregate(context.Background(), results, nil)
	require.NoError(t, err)
	require.Len(t, res.Items, 1)
	assert.Equal(t, "ok", res.Items[0].ItemID)
}

// TestAggregate_EmptyResults verifies empty input produces empty result.
func TestAggregate_EmptyResults(t *testing.T) {
	agg := NewResultAggregator(false, 10, SortByNone)

	res, err := agg.Aggregate(context.Background(), []*models.TaskResult{}, nil)
	require.NoError(t, err)
	assert.Empty(t, res.Items)
	assert.Zero(t, res.MatchScore)
}

// TestAggregate_MatchScore verifies MatchScore = successCount / totalResults.
func TestAggregate_MatchScore(t *testing.T) {
	agg := NewResultAggregator(false, 10, SortByNone)

	now := time.Now()
	results := []*models.TaskResult{
		newSuccessResult("t1", []*models.RecommendItem{newItem("a")}, now),
		newSuccessResult("t2", []*models.RecommendItem{newItem("b")}, now),
		newFailedResult("t3"),
	}

	res, err := agg.Aggregate(context.Background(), results, nil)
	require.NoError(t, err)
	assert.InDelta(t, 2.0/3.0, res.MatchScore, 1e-9, "MatchScore should be 2/3")
}
