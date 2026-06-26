package runtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockEvolutionPlugin struct {
	name        string
	recommendFn func(ctx context.Context, state ExecutionState) (*RuntimeRecommendation, error)
}

func (m *mockEvolutionPlugin) Name() string                              { return m.name }
func (m *mockEvolutionPlugin) Capabilities() []Capability                { return []Capability{CapEvolution} }
func (m *mockEvolutionPlugin) Start(_ context.Context, _ EventBus) error { return nil }
func (m *mockEvolutionPlugin) Stop(_ context.Context) error              { return nil }
func (m *mockEvolutionPlugin) Recommend(ctx context.Context, state ExecutionState) (*RuntimeRecommendation, error) {
	if m.recommendFn != nil {
		return m.recommendFn(ctx, state)
	}
	return nil, nil
}
func (m *mockEvolutionPlugin) RecordOutcome(_ context.Context, _ ExecutionOutcome) error { return nil }

func TestEvolutionRouter_Route(t *testing.T) {
	t.Run("uses evolution recommendation when confident", func(t *testing.T) {
		bus := NewPluginBus()
		evo := &mockEvolutionPlugin{
			name: "test-evo",
			recommendFn: func(_ context.Context, _ ExecutionState) (*RuntimeRecommendation, error) {
				return &RuntimeRecommendation{
					PreferredAgent: "expert",
					Confidence:     0.8,
				}, nil
			},
		}
		require.NoError(t, bus.Register(evo))
		require.NoError(t, bus.Register(NewEvolutionRouter("er", []RouteRule{
			{ToStepID: "step-2", Reason: "expert step"},
		})))
		require.NoError(t, bus.Start(context.Background()))

		router, ok := bus.PluginsByCap(CapRouter)[0].(RouterPlugin)
		require.True(t, ok)

		dec, err := router.Route(context.Background(), RouteState{CurrentStepID: "step-1"})
		require.NoError(t, err)
		require.NotNil(t, dec)
		assert.Equal(t, "step-2", dec.NextStepID)
		assert.Equal(t, "evolution", dec.Source)
	})

	t.Run("falls back to expression rules when evolution unavailable", func(t *testing.T) {
		bus := NewPluginBus()
		require.NoError(t, bus.Register(NewEvolutionRouter("er", []RouteRule{
			{FromStepID: "step-1", ToStepID: "step-3", Reason: "fallback"},
		})))
		require.NoError(t, bus.Start(context.Background()))

		router, ok := bus.PluginsByCap(CapRouter)[0].(RouterPlugin)
		require.True(t, ok)

		dec, err := router.Route(context.Background(), RouteState{CurrentStepID: "step-1"})
		require.NoError(t, err)
		assert.Equal(t, "step-3", dec.NextStepID)
		assert.Equal(t, "expression", dec.Source)
	})

	t.Run("returns nil when no rules match and no evolution advice", func(t *testing.T) {
		bus := NewPluginBus()
		require.NoError(t, bus.Register(NewEvolutionRouter("er", nil)))
		require.NoError(t, bus.Start(context.Background()))

		router, ok := bus.PluginsByCap(CapRouter)[0].(RouterPlugin)
		require.True(t, ok)

		dec, err := router.Route(context.Background(), RouteState{CurrentStepID: "step-1"})
		require.NoError(t, err)
		assert.Nil(t, dec)
	})
}
