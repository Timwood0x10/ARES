// Package experience provides unit tests for the EvidenceAggregator.
package experience

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// MockExecutionExperienceStore is a mock implementation of ExecutionExperienceStore
// for testing purposes. It is thread-safe.
type MockExecutionExperienceStore struct {
	mu          sync.RWMutex
	experiences []NormalizedExecutionExperience
	queryErr    error
	appendErr   error
}

// NewMockExecutionExperienceStore creates a new mock store with empty experiences.
func NewMockExecutionExperienceStore() *MockExecutionExperienceStore {
	return &MockExecutionExperienceStore{
		experiences: make([]NormalizedExecutionExperience, 0),
	}
}

// Query retrieves execution experiences filtered by strategy_id and time range.
func (s *MockExecutionExperienceStore) Query(ctx context.Context, strategyID string, startTime, endTime time.Time) ([]NormalizedExecutionExperience, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.queryErr != nil {
		return nil, s.queryErr
	}

	var result []NormalizedExecutionExperience
	for _, exp := range s.experiences {
		if (strategyID == "" || exp.StrategyID == strategyID) &&
			exp.Timestamp.Compare(startTime) >= 0 &&
			exp.Timestamp.Compare(endTime) <= 0 {
			result = append(result, exp)
		}
	}

	return result, nil
}

// QueryByTaskType retrieves execution experiences for a specific task type.
func (s *MockExecutionExperienceStore) QueryByTaskType(ctx context.Context, taskType string, limit int) ([]NormalizedExecutionExperience, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.queryErr != nil {
		return nil, s.queryErr
	}

	var result []NormalizedExecutionExperience
	for _, exp := range s.experiences {
		if exp.TaskType == taskType {
			result = append(result, exp)
		}
	}

	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}

	return result, nil
}

// Append adds a normalized execution experience to the store.
func (s *MockExecutionExperienceStore) Append(ctx context.Context, exp NormalizedExecutionExperience) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.appendErr != nil {
		return s.appendErr
	}

	s.experiences = append(s.experiences, exp)
	return nil
}

// SetQueryError sets the error to return on Query operations.
func (s *MockExecutionExperienceStore) SetQueryErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queryErr = err
}

// SetAppendError sets the error to return on Append operations.
func (s *MockExecutionExperienceStore) SetAppendErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendErr = err
}

// AddExperiences adds multiple experiences to the store for testing.
func (s *MockExecutionExperienceStore) AddExperiences(exps []NormalizedExecutionExperience) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.experiences = append(s.experiences, exps...)
}

// Clear removes all experiences from the store.
func (s *MockExecutionExperienceStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.experiences = make([]NormalizedExecutionExperience, 0)
}

// createTestExperiences creates a slice of test experiences with specified parameters.
func createTestExperiences(count int, strategyID, taskType string, baseTime time.Time) []NormalizedExecutionExperience {
	exps := make([]NormalizedExecutionExperience, count)
	for i := 0; i < count; i++ {
		exps[i] = NormalizedExecutionExperience{
			StrategyID:    strategyID,
			TaskType:      taskType,
			Success:       float64(i % 2), // Alternating success/failure.
			LatencyMs:     0.5 + float64(i%10)*0.05,
			RetryCount:    0.8,
			ErrorRate:     0.9,
			ToolChain:     "tool_chain_hash_" + strategyID,
			ResultQuality: 0.7 + float64(i%5)*0.05,
			TokenCost:     0.6,
			WallTime:      0.75,
			Timestamp:     baseTime.Add(time.Duration(i) * time.Second),
		}
	}
	return exps
}

func TestNewDefaultEvidenceAggregator(t *testing.T) {
	store := NewMockExecutionExperienceStore()

	t.Run("with default config", func(t *testing.T) {
		agg := NewDefaultEvidenceAggregator(store, nil)
		if agg == nil {
			t.Fatal("expected aggregator to be created")
		}
		if agg.store == nil {
			t.Error("expected store to be set")
		}
		if agg.config == nil {
			t.Error("expected config to be set")
		}
		if agg.cache == nil {
			t.Error("expected cache to be initialized with default config")
		}
	})

	t.Run("with custom config", func(t *testing.T) {
		config := &AggregatorConfig{
			MinSampleCount:  5,
			MaxSampleCount:  500,
			CacheTTLSeconds: 60,
			EnableCache:     true,
		}
		agg := NewDefaultEvidenceAggregator(store, config)
		if agg.config.MinSampleCount != 5 {
			t.Errorf("expected MinSampleCount=5, got %d", agg.config.MinSampleCount)
		}
		if agg.config.MaxSampleCount != 500 {
			t.Errorf("expected MaxSampleCount=500, got %d", agg.config.MaxSampleCount)
		}
	})

	t.Run("with cache disabled", func(t *testing.T) {
		config := &AggregatorConfig{
			EnableCache: false,
		}
		agg := NewDefaultEvidenceAggregator(store, config)
		if agg.cache != nil {
			t.Error("expected cache to be nil when disabled")
		}
	})
}

func TestAggregate(t *testing.T) {
	store := NewMockExecutionExperienceStore()
	agg := NewDefaultEvidenceAggregator(store, nil)
	ctx := context.Background()
	baseTime := time.Now().Add(-time.Hour)

	t.Run("empty store returns empty evidence", func(t *testing.T) {
		evidence, err := agg.Aggregate(ctx, "strategy-1")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if evidence.StrategyID != "strategy-1" {
			t.Errorf("expected StrategyID='strategy-1', got '%s'", evidence.StrategyID)
		}
		if evidence.SampleCount != 0 {
			t.Errorf("expected SampleCount=0, got %d", evidence.SampleCount)
		}
	})

	t.Run("aggregates experiences correctly", func(t *testing.T) {
		exps := createTestExperiences(20, "strategy-2", "code_gen", baseTime)
		store.AddExperiences(exps)

		evidence, err := agg.Aggregate(ctx, "strategy-2")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if evidence.StrategyID != "strategy-2" {
			t.Errorf("expected StrategyID='strategy-2', got '%s'", evidence.StrategyID)
		}
		if evidence.SampleCount != 20 {
			t.Errorf("expected SampleCount=20, got %d", evidence.SampleCount)
		}
		// Success rate should be ~0.5 (alternating success/failure).
		if evidence.SuccessRate < 0.4 || evidence.SuccessRate > 0.6 {
			t.Errorf("expected SuccessRate~0.5, got %.2f", evidence.SuccessRate)
		}
	})

	t.Run("returns error for empty strategy_id", func(t *testing.T) {
		_, err := agg.Aggregate(ctx, "")
		if err == nil {
			t.Error("expected error for empty strategy_id")
		}
	})

	t.Run("handles store error", func(t *testing.T) {
		store.SetQueryErr(errors.New("store error"))
		_, err := agg.Aggregate(ctx, "strategy-3")
		if err == nil {
			t.Error("expected error from store")
		}
		store.SetQueryErr(nil)
	})

	t.Run("uses cache on second call", func(t *testing.T) {
		store.Clear()
		exps := createTestExperiences(10, "strategy-4", "analysis", baseTime)
		store.AddExperiences(exps)

		// First call should compute.
		evidence1, err := agg.Aggregate(ctx, "strategy-4")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		// Clear store - second call should use cache.
		store.Clear()
		evidence2, err := agg.Aggregate(ctx, "strategy-4")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		// Cache should return same evidence even though store is empty.
		if evidence1.SampleCount != evidence2.SampleCount {
			t.Errorf("expected cached evidence, got different SampleCount: %d vs %d",
				evidence1.SampleCount, evidence2.SampleCount)
		}
	})
}

func TestAggregateByTaskType(t *testing.T) {
	store := NewMockExecutionExperienceStore()
	config := &AggregatorConfig{EnableCache: false}
	agg := NewDefaultEvidenceAggregator(store, config)
	ctx := context.Background()
	baseTime := time.Now().Add(-time.Hour)

	t.Run("empty store returns empty evidence", func(t *testing.T) {
		evidence, err := agg.AggregateByTaskType(ctx, "code_gen")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if evidence.TaskType != "code_gen" {
			t.Errorf("expected TaskType='code_gen', got '%s'", evidence.TaskType)
		}
		if evidence.SampleCount != 0 {
			t.Errorf("expected SampleCount=0, got %d", evidence.SampleCount)
		}
	})

	t.Run("aggregates experiences across strategies", func(t *testing.T) {
		exps1 := createTestExperiences(10, "strategy-1", "code_gen", baseTime)
		exps2 := createTestExperiences(10, "strategy-2", "code_gen", baseTime)
		store.AddExperiences(exps1)
		store.AddExperiences(exps2)

		evidence, err := agg.AggregateByTaskType(ctx, "code_gen")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if evidence.TaskType != "code_gen" {
			t.Errorf("expected TaskType='code_gen', got '%s'", evidence.TaskType)
		}
		if evidence.SampleCount != 20 {
			t.Errorf("expected SampleCount=20, got %d", evidence.SampleCount)
		}
	})

	t.Run("returns error for empty task_type", func(t *testing.T) {
		_, err := agg.AggregateByTaskType(ctx, "")
		if err == nil {
			t.Error("expected error for empty task_type")
		}
	})

	t.Run("handles store error", func(t *testing.T) {
		store.SetQueryErr(errors.New("store error"))
		_, err := agg.AggregateByTaskType(ctx, "analysis")
		if err == nil {
			t.Error("expected error from store")
		}
		store.SetQueryErr(nil)
	})
}

func TestAggregateByTimeWindow(t *testing.T) {
	store := NewMockExecutionExperienceStore()
	agg := NewDefaultEvidenceAggregator(store, nil)
	ctx := context.Background()
	now := time.Now()

	t.Run("hourly window", func(t *testing.T) {
		// Add experiences within current hour.
		exps := createTestExperiences(5, "strategy-1", "task", now.Truncate(time.Hour).Add(10*time.Minute))
		store.AddExperiences(exps)

		evidence, err := agg.AggregateByTimeWindow(ctx, "strategy-1", "hourly")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if evidence.SampleCount != 5 {
			t.Errorf("expected SampleCount=5, got %d", evidence.SampleCount)
		}
	})

	t.Run("daily window", func(t *testing.T) {
		store.Clear()
		// Add experiences within current day.
		exps := createTestExperiences(10, "strategy-2", "task", now.Truncate(24*time.Hour).Add(time.Hour))
		store.AddExperiences(exps)

		evidence, err := agg.AggregateByTimeWindow(ctx, "strategy-2", "daily")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if evidence.SampleCount != 10 {
			t.Errorf("expected SampleCount=10, got %d", evidence.SampleCount)
		}
	})

	t.Run("weekly window", func(t *testing.T) {
		store.Clear()
		// Add experiences within current week.
		dayOffset := int(now.Weekday() - time.Monday)
		if dayOffset < 0 {
			dayOffset += 7
		}
		weekStart := now.AddDate(0, 0, -dayOffset).Truncate(24 * time.Hour)
		exps := createTestExperiences(15, "strategy-3", "task", weekStart.Add(24*time.Hour))
		store.AddExperiences(exps)

		evidence, err := agg.AggregateByTimeWindow(ctx, "strategy-3", "weekly")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if evidence.SampleCount != 15 {
			t.Errorf("expected SampleCount=15, got %d", evidence.SampleCount)
		}
	})

	t.Run("invalid window returns error", func(t *testing.T) {
		_, err := agg.AggregateByTimeWindow(ctx, "strategy-1", "monthly")
		if err == nil {
			t.Error("expected error for invalid window")
		}
	})

	t.Run("returns error for empty strategy_id", func(t *testing.T) {
		_, err := agg.AggregateByTimeWindow(ctx, "", "hourly")
		if err == nil {
			t.Error("expected error for empty strategy_id")
		}
	})

	t.Run("filters experiences outside window", func(t *testing.T) {
		store.Clear()
		// Add experiences from yesterday (outside current daily window).
		oldExps := createTestExperiences(5, "strategy-4", "task", now.AddDate(0, 0, -1))
		store.AddExperiences(oldExps)

		evidence, err := agg.AggregateByTimeWindow(ctx, "strategy-4", "daily")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if evidence.SampleCount != 0 {
			t.Errorf("expected SampleCount=0 (outside window), got %d", evidence.SampleCount)
		}
	})
}

func TestRefreshAll(t *testing.T) {
	store := NewMockExecutionExperienceStore()
	agg := NewDefaultEvidenceAggregator(store, nil)
	ctx := context.Background()
	baseTime := time.Now().Add(-time.Hour)

	t.Run("clears cache", func(t *testing.T) {
		exps := createTestExperiences(10, "strategy-1", "task", baseTime)
		store.AddExperiences(exps)

		// Populate cache.
		_, err := agg.Aggregate(ctx, "strategy-1")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		// Refresh should clear cache.
		err = agg.RefreshAll(ctx)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		// Verify cache is cleared by checking internal state.
		agg.cache.mu.RLock()
		cacheLen := len(agg.cache.byStrategy)
		agg.cache.mu.RUnlock()

		if cacheLen != 0 {
			t.Errorf("expected cache to be cleared, got %d entries", cacheLen)
		}
	})

	t.Run("no-op when cache disabled", func(t *testing.T) {
		config := &AggregatorConfig{EnableCache: false}
		aggNoCache := NewDefaultEvidenceAggregator(store, config)

		err := aggNoCache.RefreshAll(ctx)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}

func TestCalculateConfidence(t *testing.T) {
	store := NewMockExecutionExperienceStore()
	config := &AggregatorConfig{
		MinSampleCount: 10,
		MaxSampleCount: 1000,
	}
	agg := NewDefaultEvidenceAggregator(store, config)

	t.Run("zero sample count returns zero confidence", func(t *testing.T) {
		confidence := agg.calculateConfidence(0)
		if confidence != 0.0 {
			t.Errorf("expected confidence=0.0, got %.2f", confidence)
		}
	})

	t.Run("small sample count scales confidence down", func(t *testing.T) {
		confidence := agg.calculateConfidence(5)
		// 5 < 10, so confidence = 5/10 * 0.5 = 0.25
		if confidence < 0.2 || confidence > 0.3 {
			t.Errorf("expected confidence~0.25 for sampleCount=5, got %.2f", confidence)
		}
	})

	t.Run("at min threshold gives 0.5 confidence", func(t *testing.T) {
		confidence := agg.calculateConfidence(10)
		// At min threshold, should start at 0.5
		if confidence < 0.5 {
			t.Errorf("expected confidence>=0.5 at min threshold, got %.2f", confidence)
		}
	})

	t.Run("large sample count returns full confidence", func(t *testing.T) {
		confidence := agg.calculateConfidence(1000)
		if confidence != 1.0 {
			t.Errorf("expected confidence=1.0 for sampleCount=1000, got %.2f", confidence)
		}
	})

	t.Run("above max returns full confidence", func(t *testing.T) {
		confidence := agg.calculateConfidence(2000)
		if confidence != 1.0 {
			t.Errorf("expected confidence=1.0 for sampleCount>max, got %.2f", confidence)
		}
	})

	t.Run("linear interpolation between min and max", func(t *testing.T) {
		// At 505 samples (halfway between 10 and 1000), confidence should be ~0.75
		confidence := agg.calculateConfidence(505)
		if confidence < 0.7 || confidence > 0.8 {
			t.Errorf("expected confidence~0.75 for sampleCount=505, got %.2f", confidence)
		}
	})
}

func TestContextCancellation(t *testing.T) {
	store := NewMockExecutionExperienceStore()
	agg := NewDefaultEvidenceAggregator(store, nil)

	t.Run("Aggregate respects context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := agg.Aggregate(ctx, "strategy-1")
		if err == nil {
			t.Error("expected error from cancelled context")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got: %v", err)
		}
	})

	t.Run("AggregateByTaskType respects context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := agg.AggregateByTaskType(ctx, "task_type")
		if err == nil {
			t.Error("expected error from cancelled context")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got: %v", err)
		}
	})

	t.Run("AggregateByTimeWindow respects context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := agg.AggregateByTimeWindow(ctx, "strategy-1", "hourly")
		if err == nil {
			t.Error("expected error from cancelled context")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got: %v", err)
		}
	})

	t.Run("RefreshAll respects context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := agg.RefreshAll(ctx)
		if err == nil {
			t.Error("expected error from cancelled context")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got: %v", err)
		}
	})
}

func TestGetTimeWindow(t *testing.T) {
	store := NewMockExecutionExperienceStore()
	agg := NewDefaultEvidenceAggregator(store, nil)

	t.Run("hourly window calculation", func(t *testing.T) {
		start, end, err := agg.getTimeWindow("hourly")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		duration := end.Sub(start)
		if duration != time.Hour {
			t.Errorf("expected duration=1h, got %v", duration)
		}
	})

	t.Run("daily window calculation", func(t *testing.T) {
		start, end, err := agg.getTimeWindow("daily")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		duration := end.Sub(start)
		if duration != 24*time.Hour {
			t.Errorf("expected duration=24h, got %v", duration)
		}
	})

	t.Run("weekly window calculation", func(t *testing.T) {
		start, end, err := agg.getTimeWindow("weekly")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		duration := end.Sub(start)
		if duration != 7*24*time.Hour {
			t.Errorf("expected duration=7d, got %v", duration)
		}
	})

	t.Run("invalid window returns error", func(t *testing.T) {
		_, _, err := agg.getTimeWindow("monthly")
		if err == nil {
			t.Error("expected error for invalid window")
		}
	})
}

func TestAggregationWithVariousSampleSizes(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	sampleSizes := []int{1, 5, 10, 50, 100, 500, 1000, 2000}

	for _, size := range sampleSizes {
		t.Run(fmt.Sprintf("sample size %d", size), func(t *testing.T) {
			// Create fresh store and aggregator for each test iteration.
			store := NewMockExecutionExperienceStore()
			config := &AggregatorConfig{
				MinSampleCount: 10,
				MaxSampleCount: 1000,
				EnableCache:    false,
			}
			agg := NewDefaultEvidenceAggregator(store, config)

			strategyID := fmt.Sprintf("strategy-%d", size)
			// Create experiences within valid time range (all before now).
			// Use seconds instead of minutes to fit more experiences.
			baseTime := now.Add(-time.Duration(size) * time.Second)
			exps := createTestExperiences(size, strategyID, "task", baseTime)
			store.AddExperiences(exps)

			evidence, err := agg.Aggregate(ctx, strategyID)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if evidence.SampleCount != int64(size) {
				t.Errorf("expected SampleCount=%d, got %d", size, evidence.SampleCount)
			}

			// Verify confidence is within expected range.
			confidence := evidence.Confidence
			if confidence < 0 || confidence > 1 {
				t.Errorf("confidence out of range [0,1]: %.2f", confidence)
			}
		})
	}
}

func TestCacheTTL(t *testing.T) {
	store := NewMockExecutionExperienceStore()
	config := &AggregatorConfig{
		CacheTTLSeconds: 1, // 1 second TTL for testing.
		EnableCache:     true,
	}
	agg := NewDefaultEvidenceAggregator(store, config)
	ctx := context.Background()
	baseTime := time.Now().Add(-time.Hour)

	t.Run("stale cache is recomputed", func(t *testing.T) {
		exps := createTestExperiences(10, "strategy-1", "task", baseTime)
		store.AddExperiences(exps)

		// First call populates cache.
		_, err := agg.Aggregate(ctx, "strategy-1")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		// Wait for cache to expire.
		time.Sleep(2 * time.Second)

		// Modify store.
		store.Clear()
		newExps := createTestExperiences(20, "strategy-1", "task", baseTime)
		store.AddExperiences(newExps)

		// Second call should recompute due to stale cache.
		evidence2, err := agg.Aggregate(ctx, "strategy-1")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}

		// Should get updated evidence.
		if evidence2.SampleCount != 20 {
			t.Errorf("expected recomputed SampleCount=20, got %d", evidence2.SampleCount)
		}
	})
}

func TestThreadSafeCache(t *testing.T) {
	store := NewMockExecutionExperienceStore()
	agg := NewDefaultEvidenceAggregator(store, nil)
	ctx := context.Background()
	baseTime := time.Now().Add(-time.Hour)

	// Add experiences.
	exps := createTestExperiences(100, "strategy-1", "task", baseTime)
	store.AddExperiences(exps)

	// Concurrent aggregation calls.
	var wg sync.WaitGroup
	errCount := 0

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := agg.Aggregate(ctx, "strategy-1")
			if err != nil {
				errCount++
			}
		}()
	}

	wg.Wait()

	if errCount > 0 {
		t.Errorf("expected no errors in concurrent calls, got %d", errCount)
	}
}
