package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	evolution "github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
)

// TestStrategyScoreAdapterWriteBackContract locks the zero-LLM feedback
// loop's store contract end to end at the wiring layer: the adapter writes
// the active strategy's score when one exists (GA-1 fix — bootstrap seeds
// the store), and reports the empty-store failure loudly when it does not
// (the pre-fix dogfood shape: every recovery score hit "no active
// strategy" and the GA accumulated nothing).
func TestStrategyScoreAdapterWriteBackContract(t *testing.T) {
	ctx := context.Background()

	t.Run("empty store fails loudly", func(t *testing.T) {
		store := evolution.NewMemoryStrategyStore(0)
		writer := newStrategyScoreAdapter(store)
		if writer == nil {
			t.Fatal("adapter must not be nil for a non-nil store")
		}
		err := writer.WriteActiveScore(ctx, 0.7)
		if err == nil || !strings.Contains(err.Error(), "no active strategy") {
			t.Fatalf("err = %v, want the empty-store failure", err)
		}
	})

	t.Run("seeded store persists the score", func(t *testing.T) {
		store := evolution.NewMemoryStrategyStore(0)
		if err := store.SetActive(ctx, &evolution.Strategy{
			ID:    "bootstrap-root",
			Score: 0.5,
		}); err != nil {
			t.Fatalf("SetActive: %v", err)
		}
		writer := newStrategyScoreAdapter(store)
		if err := writer.WriteActiveScore(ctx, 0.9); err != nil {
			t.Fatalf("WriteActiveScore: %v", err)
		}
		got, err := store.GetActive(ctx)
		if err != nil || got == nil {
			t.Fatalf("GetActive = (%v, %v)", got, err)
		}
		if got.Score != 0.9 {
			t.Fatalf("Score = %v, want 0.9 persisted", got.Score)
		}
		if got.ID != "bootstrap-root" {
			t.Fatalf("ID = %q, write-back must not change which strategy is active", got.ID)
		}
	})

	t.Run("out of range score rejected", func(t *testing.T) {
		store := evolution.NewMemoryStrategyStore(0)
		_ = store.SetActive(ctx, &evolution.Strategy{ID: "s", Score: 0.5})
		writer := newStrategyScoreAdapter(store)
		if err := writer.WriteActiveScore(ctx, 1.5); err == nil {
			t.Fatal("score > 1 must be rejected")
		}
	})

	t.Run("nil store yields nil adapter", func(t *testing.T) {
		if got := newStrategyScoreAdapter(nil); got != nil {
			t.Fatalf("adapter = %v, want nil for nil store", got)
		}
	})

	// Sentinel store: PG reports (nil, ErrNoActiveStrategy) instead of
	// (nil, nil) — the adapter must surface the same "no active" failure.
	t.Run("pg sentinel surfaced as failure", func(t *testing.T) {
		writer := newStrategyScoreAdapter(&pgSentinelStore{})
		err := writer.WriteActiveScore(ctx, 0.4)
		if err == nil {
			t.Fatal("PG empty store must fail the write-back")
		}
		if !strings.Contains(err.Error(), "no active strategy") && !errors.Is(err, evolution.ErrNoActiveStrategy) {
			t.Fatalf("err = %v, want no-active failure", err)
		}
	})
}

// pgSentinelStore returns the PG empty-store shape.
type pgSentinelStore struct{}

func (pgSentinelStore) GetActive(context.Context) (*evolution.Strategy, error) {
	return nil, evolution.ErrNoActiveStrategy
}

func (pgSentinelStore) SetActive(context.Context, *evolution.Strategy) error { return nil }

func (pgSentinelStore) GetHistory(context.Context, string, int) ([]*evolution.Strategy, error) {
	return nil, nil
}

var _ evolution.StrategyStore = pgSentinelStore{}
