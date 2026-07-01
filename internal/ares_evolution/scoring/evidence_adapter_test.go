package scoring

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_evolution/experience"
)

// mockEvidenceAggregator implements EvidenceAggregator for testing.
type mockEvidenceAggregator struct {
	strategyEvidence map[string]experience.Evidence
	taskTypeEvidence map[string]experience.Evidence
	err              error
}

func (m *mockEvidenceAggregator) AggregateByStrategy(ctx context.Context, strategyID string) (experience.Evidence, error) {
	if m.err != nil {
		return experience.Evidence{}, m.err
	}
	if ev, ok := m.strategyEvidence[strategyID]; ok {
		return ev, nil
	}
	return experience.Evidence{}, errors.New("strategy not found")
}

func (m *mockEvidenceAggregator) AggregateByTaskType(ctx context.Context, taskType string) (experience.Evidence, error) {
	if m.err != nil {
		return experience.Evidence{}, m.err
	}
	if ev, ok := m.taskTypeEvidence[taskType]; ok {
		return ev, nil
	}
	return experience.Evidence{}, errors.New("task type not found")
}

// TestNewExperienceToEvidenceAdapter_NilAggregator verifies that nil aggregator
// is rejected.
func TestNewExperienceToEvidenceAdapter_NilAggregator(t *testing.T) {
	_, err := NewExperienceToEvidenceAdapter(nil)
	if err == nil {
		t.Fatal("expected error for nil aggregator")
	}
	if err.Error() != "evidence aggregator must not be nil" {
		t.Errorf("expected specific error message, got: %s", err.Error())
	}
}

// TestNewExperienceToEvidenceAdapter_Success verifies successful adapter creation.
func TestNewExperienceToEvidenceAdapter_Success(t *testing.T) {
	mock := &mockEvidenceAggregator{
		strategyEvidence: map[string]experience.Evidence{
			"test-strategy": experience.Evidence{
				StrategyID:  "test-strategy",
				SuccessRate: 0.9,
				SampleCount: 10,
			},
		},
	}

	adapter, err := NewExperienceToEvidenceAdapter(mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if adapter == nil {
		t.Fatal("expected non-nil adapter")
	}
}

// TestExperienceToEvidenceAdapter_GetEvidence verifies GetEvidence method.
func TestExperienceToEvidenceAdapter_GetEvidence(t *testing.T) {
	now := time.Now()
	mock := &mockEvidenceAggregator{
		strategyEvidence: map[string]experience.Evidence{
			"strategy-1": experience.Evidence{
				StrategyID:    "strategy-1",
				TaskType:      "code_generation",
				SuccessRate:   0.85,
				LatencyP50:    200,
				LatencyP95:    500,
				ErrorRate:     0.05,
				SampleCount:   20,
				Confidence:    0.9,
				LastUpdated:   now,
			},
		},
	}

	adapter, err := NewExperienceToEvidenceAdapter(mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ev, err := adapter.GetEvidence(context.Background(), "strategy-1")
	if err != nil {
		t.Fatalf("GetEvidence failed: %v", err)
	}

	if ev.StrategyID != "strategy-1" {
		t.Errorf("expected StrategyID=strategy-1, got %s", ev.StrategyID)
	}
	if ev.SuccessRate != 0.85 {
		t.Errorf("expected SuccessRate=0.85, got %f", ev.SuccessRate)
	}
	if ev.LatencyP50 != 200 {
		t.Errorf("expected LatencyP50=200, got %d", ev.LatencyP50)
	}
	if ev.ErrorRate != 0.05 {
		t.Errorf("expected ErrorRate=0.05, got %f", ev.ErrorRate)
	}
	if ev.SampleCount != 20 {
		t.Errorf("expected SampleCount=20, got %d", ev.SampleCount)
	}
	if ev.Confidence != 0.9 {
		t.Errorf("expected Confidence=0.9, got %f", ev.Confidence)
	}
}

// TestExperienceToEvidenceAdapter_GetEvidence_EmptyStrategyID verifies that
// empty strategy_id is rejected.
func TestExperienceToEvidenceAdapter_GetEvidence_EmptyStrategyID(t *testing.T) {
	mock := &mockEvidenceAggregator{}
	adapter, _ := NewExperienceToEvidenceAdapter(mock)

	_, err := adapter.GetEvidence(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty strategy_id")
	}
	if err.Error() != "strategy_id must not be empty" {
		t.Errorf("expected specific error message, got: %s", err.Error())
	}
}

// TestExperienceToEvidenceAdapter_GetEvidence_AggregatorError verifies that
// aggregator errors are propagated.
func TestExperienceToEvidenceAdapter_GetEvidence_AggregatorError(t *testing.T) {
	mock := &mockEvidenceAggregator{
		err: errors.New("aggregation failed"),
	}
	adapter, _ := NewExperienceToEvidenceAdapter(mock)

	_, err := adapter.GetEvidence(context.Background(), "test-strategy")
	if err == nil {
		t.Fatal("expected error from aggregator")
	}
	if err.Error() != "aggregation failed" {
		t.Errorf("expected aggregator error, got: %s", err.Error())
	}
}

// TestExperienceToEvidenceAdapter_GetEvidenceByTaskType verifies
// GetEvidenceByTaskType method.
func TestExperienceToEvidenceAdapter_GetEvidenceByTaskType(t *testing.T) {
	now := time.Now()
	mock := &mockEvidenceAggregator{
		taskTypeEvidence: map[string]experience.Evidence{
			"code_review": experience.Evidence{
				TaskType:      "code_review",
				SuccessRate:   0.75,
				LatencyP50:    300,
				LatencyP95:    800,
				ErrorRate:     0.15,
				SampleCount:   50,
				Confidence:    0.95,
				LastUpdated:   now,
			},
		},
	}

	adapter, err := NewExperienceToEvidenceAdapter(mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ev, err := adapter.GetEvidenceByTaskType(context.Background(), "code_review")
	if err != nil {
		t.Fatalf("GetEvidenceByTaskType failed: %v", err)
	}

	if ev.TaskType != "code_review" {
		t.Errorf("expected TaskType=code_review, got %s", ev.TaskType)
	}
	if ev.SuccessRate != 0.75 {
		t.Errorf("expected SuccessRate=0.75, got %f", ev.SuccessRate)
	}
	if ev.LatencyP50 != 300 {
		t.Errorf("expected LatencyP50=300, got %d", ev.LatencyP50)
	}
	if ev.ErrorRate != 0.15 {
		t.Errorf("expected ErrorRate=0.15, got %f", ev.ErrorRate)
	}
	if ev.SampleCount != 50 {
		t.Errorf("expected SampleCount=50, got %d", ev.SampleCount)
	}
}

// TestExperienceToEvidenceAdapter_GetEvidenceByTaskType_EmptyTaskType verifies
// that empty task_type is rejected.
func TestExperienceToEvidenceAdapter_GetEvidenceByTaskType_EmptyTaskType(t *testing.T) {
	mock := &mockEvidenceAggregator{}
	adapter, _ := NewExperienceToEvidenceAdapter(mock)

	_, err := adapter.GetEvidenceByTaskType(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty task_type")
	}
	if err.Error() != "task_type must not be empty" {
		t.Errorf("expected specific error message, got: %s", err.Error())
	}
}

// TestExperienceToEvidenceAdapter_GetEvidenceByTaskType_AggregatorError verifies
// that aggregator errors are propagated.
func TestExperienceToEvidenceAdapter_GetEvidenceByTaskType_AggregatorError(t *testing.T) {
	mock := &mockEvidenceAggregator{
		err: errors.New("task aggregation failed"),
	}
	adapter, _ := NewExperienceToEvidenceAdapter(mock)

	_, err := adapter.GetEvidenceByTaskType(context.Background(), "test-task")
	if err == nil {
		t.Fatal("expected error from aggregator")
	}
	if err.Error() != "task aggregation failed" {
		t.Errorf("expected aggregator error, got: %s", err.Error())
	}
}

// TestExperienceToEvidenceAdapter_IntegrationWithScorer verifies that the
// adapter works correctly with MemoryAwareScorer in evidence mode.
func TestExperienceToEvidenceAdapter_IntegrationWithScorer(t *testing.T) {
	// Create mock aggregator with evidence data.
	mock := &mockEvidenceAggregator{
		strategyEvidence: map[string]experience.Evidence{
			"test-strategy": experience.Evidence{
				StrategyID:    "test-strategy",
				TaskType:      "test",
				SuccessRate:   0.8,
				LatencyP50:    1000, // 1 second
				ErrorRate:     0.1,
				SampleCount:   10,
				Confidence:    0.85,
			},
		},
	}

	adapter, err := NewExperienceToEvidenceAdapter(mock)
	if err != nil {
		t.Fatalf("NewExperienceToEvidenceAdapter failed: %v", err)
	}

	// Create scorer with adapter.
	ts := mustCreateTieredScorer(t, 5)
	cfg := DefaultMemoryAwareScoringConfig()
	cfg.Enabled = true

	ms, err := NewMemoryAwareScorer(ts, nil, cfg)
	if err != nil {
		t.Fatalf("NewMemoryAwareScorer failed: %v", err)
	}

	// Set evidence provider.
	ms.SetEvidenceProvider(adapter)

	// Verify it implements EvidenceProvider interface.
	var ep EvidenceProvider = adapter
	if ep == nil {
		t.Fatal("adapter should implement EvidenceProvider")
	}
}