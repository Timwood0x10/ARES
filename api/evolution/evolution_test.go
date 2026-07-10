package evolution

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
	"github.com/stretchr/testify/assert"
)

type stubMutator struct{}

func (m *stubMutator) Mutate(_ context.Context, parent *mutation.Strategy, n int) ([]*mutation.Strategy, error) {
	out := make([]*mutation.Strategy, n)
	for i := 0; i < n; i++ {
		out[i] = &mutation.Strategy{ID: parent.ID + "-child", Score: parent.Score, Params: parent.Params}
	}
	return out, nil
}

func TestNewPopulation(t *testing.T) {
	s := &mutation.Strategy{ID: "base", Params: map[string]any{}}
	inner, err := genome.NewPopulation(context.Background(), s, &stubMutator{},
		genome.WithPopulationSize(10),
		genome.WithEliteCount(2),
		genome.WithMutationRate(0.2),
	)
	if err != nil {
		t.Fatalf("NewPopulation: %v", err)
	}
	if inner == nil {
		t.Fatal("expected non-nil population")
	}
	if inner.Size != 10 {
		t.Fatalf("expected size 10, got %d", inner.Size)
	}
}

func TestDefaultPopluationConfigValues(t *testing.T) {
	cfg := DefaultPopulationConfig()
	if cfg.Size != 20 {
		t.Fatalf("expected 20, got %d", cfg.Size)
	}
}

func TestLineageStruct(t *testing.T) {
	l := Lineage{
		ParentID:         "parent",
		ChildID:          "child",
		MutationType:     "crossover",
		WinRate:          0.8,
		ScoreImprovement: 0.15,
	}
	if l.ParentID != "parent" {
		t.Fatal("expected ParentID to be set")
	}
}

func TestNewMutatorReturnsErrNotImplemented(t *testing.T) {
	_, err := NewMutator("test-model", MutationConfig{})
	assert.ErrorIs(t, err, ErrNotImplemented)
}

func TestPromoterEvaluateReturnsErrNotImplemented(t *testing.T) {
	criteria := DefaultPromotionCriteria()
	adapter := NewPromoter(&criteria)
	// adapter is a promoterAdapter with inner=nil, Evaluate returns ErrNotImplemented.
	_, err := adapter.Evaluate(context.Background(), "test-strategy", 0.9, 0.8)
	assert.ErrorIs(t, err, ErrNotImplemented)
}

func TestPromoterDemoteReturnsErrNotImplemented(t *testing.T) {
	criteria := DefaultPromotionCriteria()
	adapter := NewPromoter(&criteria)
	err := adapter.Demote(context.Background(), "test-strategy")
	assert.ErrorIs(t, err, ErrNotImplemented)
}
