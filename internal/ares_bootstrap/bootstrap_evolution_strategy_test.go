package ares_bootstrap

import (
	"context"
	"errors"
	"testing"

	evolution "github.com/Timwood0x10/ares/internal/runtime/ares_evolution"
	"github.com/Timwood0x10/ares/internal/runtime/ares_evolution/mutation"
)

// sentinelStore mimics PGStrategyStore's empty-store contract
// (nil, ErrNoActiveStrategy) while recording SetActive calls.
type sentinelStore struct {
	active  *evolution.Strategy
	setCall int
}

func (s *sentinelStore) GetActive(context.Context) (*evolution.Strategy, error) {
	if s.active == nil {
		return nil, evolution.ErrNoActiveStrategy
	}
	return s.active, nil
}

func (s *sentinelStore) SetActive(_ context.Context, st *evolution.Strategy) error {
	s.active = st
	s.setCall++
	return nil
}

func (s *sentinelStore) GetHistory(context.Context, string, int) ([]*evolution.Strategy, error) {
	return nil, nil
}

var _ evolution.StrategyStore = (*sentinelStore)(nil)

// TestEnsureBootstrapActiveStrategySeedsEmptyStore locks the GA-1 contract:
// an empty store receives the GA base strategy as active so score
// write-back has a target from boot — the defect was that no seed existed
// and every recovery score failed with "no active strategy".
func TestEnsureBootstrapActiveStrategySeedsEmptyStore(t *testing.T) {
	ctx := context.Background()
	base := &mutation.Strategy{
		ID:             "bootstrap-root",
		Params:         map[string]any{"temperature": 0.7},
		PromptTemplate: "you are the root",
	}

	t.Run("memory store empty contract", func(t *testing.T) {
		store := evolution.NewMemoryStrategyStore(0)
		if err := ensureBootstrapActiveStrategy(ctx, store, base); err != nil {
			t.Fatalf("ensureBootstrapActiveStrategy: %v", err)
		}
		got, err := store.GetActive(ctx)
		if err != nil || got == nil {
			t.Fatalf("GetActive = (%v, %v), want seeded strategy", got, err)
		}
		if got.ID != "bootstrap-root" || got.Score != bootstrapStrategyNeutralScore {
			t.Fatalf("seeded = %+v, want bootstrap-root @ %v", got, bootstrapStrategyNeutralScore)
		}
		if got.Params["temperature"] != 0.7 {
			t.Fatalf("seeded params = %v, want base params carried over", got.Params)
		}
	})

	t.Run("pg sentinel empty contract", func(t *testing.T) {
		store := &sentinelStore{}
		if err := ensureBootstrapActiveStrategy(ctx, store, base); err != nil {
			t.Fatalf("ensureBootstrapActiveStrategy: %v", err)
		}
		if store.setCall != 1 || store.active == nil || store.active.ID != "bootstrap-root" {
			t.Fatalf("setCall=%d active=%+v, want one seed of bootstrap-root", store.setCall, store.active)
		}
	})

	t.Run("existing active strategy is never overwritten", func(t *testing.T) {
		store := evolution.NewMemoryStrategyStore(0)
		deployed := &evolution.Strategy{ID: "promoted-candidate", Score: 0.9}
		if err := store.SetActive(ctx, deployed); err != nil {
			t.Fatalf("SetActive: %v", err)
		}
		if err := ensureBootstrapActiveStrategy(ctx, store, base); err != nil {
			t.Fatalf("ensureBootstrapActiveStrategy: %v", err)
		}
		got, _ := store.GetActive(ctx)
		if got == nil || got.ID != "promoted-candidate" {
			t.Fatalf("active = %+v, want the pre-existing promoted-candidate kept", got)
		}
	})

	t.Run("store read failure propagates", func(t *testing.T) {
		broken := &brokenReadStore{}
		if err := ensureBootstrapActiveStrategy(ctx, broken, base); err == nil {
			t.Fatal("read failure must propagate, not silently skip the seed")
		}
	})
}

// brokenReadStore fails every GetActive with a non-sentinel error.
type brokenReadStore struct{ sentinelStore }

func (b *brokenReadStore) GetActive(context.Context) (*evolution.Strategy, error) {
	return nil, errors.New("store offline")
}

// TestEnsureBootstrapActiveStrategyNilArgs pins the nil-safety contract:
// missing store or base is a no-op (defensive callers), never a panic.
func TestEnsureBootstrapActiveStrategyNilArgs(t *testing.T) {
	ctx := context.Background()
	base := &mutation.Strategy{ID: "bootstrap-root"}
	if err := ensureBootstrapActiveStrategy(ctx, nil, base); err != nil {
		t.Fatalf("nil store: %v", err)
	}
	if err := ensureBootstrapActiveStrategy(ctx, evolution.NewMemoryStrategyStore(0), nil); err != nil {
		t.Fatalf("nil base: %v", err)
	}
}
