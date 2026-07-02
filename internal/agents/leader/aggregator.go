package leader

import (
	"context"
	"sort"

	"github.com/Timwood0x10/ares/internal/core/models"
)

// SortByNone disables sorting; items remain in their original order.
const SortByNone = "none"

// SortByPriority sorts items by the associated Task.Priority (descending).
const SortByPriority = "priority"

// SortByCreatedAt sorts items by TaskResult.CreatedAt (newest first).
const SortByCreatedAt = "created_at"

// indexedItem pairs a RecommendItem with its source result and priority for sorting.
type indexedItem struct {
	item     *models.RecommendItem
	result   *models.TaskResult
	priority int
}

// resultAggregator aggregates results from sub-agents.
type resultAggregator struct {
	enableDedupe bool
	maxItems     int
	sortBy       string
}

// NewResultAggregator creates a new ResultAggregator.
// sortBy controls the ordering of aggregated items and must be one of:
// SortByNone ("none"), SortByPriority ("priority"), or SortByCreatedAt ("created_at").
// An unrecognised value is treated as SortByNone.
func NewResultAggregator(enableDedupe bool, maxItems int, sortBy string) ResultAggregator {
	if maxItems <= 0 {
		maxItems = DefaultMaxItems
	}
	return &resultAggregator{
		enableDedupe: enableDedupe,
		maxItems:     maxItems,
		sortBy:       sortBy,
	}
}

// Aggregate combines results from all sub-agents.
// tasks is used for priority-based sorting and may be nil when sortBy is not "priority".
func (a *resultAggregator) Aggregate(ctx context.Context, results []*models.TaskResult, tasks []*models.Task) (*models.RecommendResult, error) {
	// Check for context cancellation before processing.
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	allItems := make([]indexedItem, 0)
	successCount := 0

	// Build a priority lookup when sorting by priority
	priorityMap := make(map[string]int)
	if a.sortBy == SortByPriority && tasks != nil {
		for _, t := range tasks {
			if t != nil {
				priorityMap[t.TaskID] = t.Priority
			}
		}
	}

	for _, result := range results {
		if result == nil {
			continue
		}
		if result.Success {
			successCount++
			for _, item := range result.Items {
				if item != nil {
					priority, ok := priorityMap[result.TaskID]
					if !ok && a.sortBy == SortByPriority {
						log.Warn("Task priority not found in priority map, using default 0", "task_id", result.TaskID)
					}
					allItems = append(allItems, indexedItem{
						item:     item,
						result:   result,
						priority: priority,
					})
				}
			}
		}
	}

	// Deduplicate if enabled
	if a.enableDedupe {
		allItems = deduplicateIndexedItems(allItems)
	}

	// Sort based on configuration
	switch a.sortBy {
	case SortByPriority:
		sort.SliceStable(allItems, func(i, j int) bool {
			return allItems[i].priority > allItems[j].priority
		})
	case SortByCreatedAt:
		sort.SliceStable(allItems, func(i, j int) bool {
			ti, tj := allItems[i].result.CreatedAt, allItems[j].result.CreatedAt
			// If both are zero, preserve original order (stable sort).
			if ti.IsZero() && tj.IsZero() {
				return false
			}
			// Zero times sort after non-zero times.
			if ti.IsZero() {
				return false
			}
			if tj.IsZero() {
				return true
			}
			return ti.After(tj)
		})
		// SortByNone or unrecognised: keep original order
	}

	// Limit items
	if len(allItems) > a.maxItems {
		allItems = allItems[:a.maxItems]
	}

	items := make([]*models.RecommendItem, 0, len(allItems))
	totalPrice := 0.0
	for _, indexed := range allItems {
		items = append(items, indexed.item)
		totalPrice += indexed.item.Price
	}

	result := models.NewRecommendResult("", "")
	result.Items = items
	result.TotalPrice = totalPrice

	if len(results) > 0 {
		result.MatchScore = float64(successCount) / float64(len(results))
	}

	return result, nil
}

// deduplicateIndexedItems removes duplicate indexedItems using ItemID or Name as the key.
func deduplicateIndexedItems(items []indexedItem) []indexedItem {
	seen := make(map[string]bool)
	result := make([]indexedItem, 0)

	for _, indexed := range items {
		if indexed.item == nil {
			continue
		}
		key := indexed.item.ItemID
		if key == "" {
			key = indexed.item.Name
		}
		if key == "" {
			log.Warn("dropping item during deduplication: both ItemID and Name are empty",
				"content", indexed.item.Content)
			continue
		}
		if !seen[key] {
			seen[key] = true
			result = append(result, indexed)
		}
	}

	return result
}
