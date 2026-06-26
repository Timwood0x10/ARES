package runtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockMemoryPlugin struct {
	name     string
	adviceFn func(ctx context.Context, state RouteState) ([]RouteAdvice, error)
}

func (m *mockMemoryPlugin) Name() string                              { return m.name }
func (m *mockMemoryPlugin) Capabilities() []Capability                { return []Capability{CapMemory} }
func (m *mockMemoryPlugin) Start(_ context.Context, _ EventBus) error { return nil }
func (m *mockMemoryPlugin) Stop(_ context.Context) error              { return nil }
func (m *mockMemoryPlugin) AdviseRoute(ctx context.Context, state RouteState) ([]RouteAdvice, error) {
	if m.adviceFn != nil {
		return m.adviceFn(ctx, state)
	}
	return nil, nil
}

func TestMemoryRouter_Route(t *testing.T) {
	t.Run("returns memory advice when confident", func(t *testing.T) {
		bus := NewPluginBus()
		mem := &mockMemoryPlugin{
			name: "test-memory",
			adviceFn: func(_ context.Context, _ RouteState) ([]RouteAdvice, error) {
				return []RouteAdvice{
					{NextStepID: "step-2", Confidence: 0.8, Reason: "similar past execution"},
				}, nil
			},
		}
		require.NoError(t, bus.Register(mem))
		require.NoError(t, bus.Register(NewMemoryRouter("mr", nil, 0.5)))
		require.NoError(t, bus.Start(context.Background()))

		router, ok := bus.PluginsByCap(CapRouter)[0].(RouterPlugin)
		require.True(t, ok)

		dec, err := router.Route(context.Background(), RouteState{CurrentStepID: "step-1"})
		require.NoError(t, err)
		require.NotNil(t, dec)
		assert.Equal(t, "step-2", dec.NextStepID)
		assert.Equal(t, "memory", dec.Source)
	})

	t.Run("falls back to expression rules when memory confidence too low", func(t *testing.T) {
		bus := NewPluginBus()
		mem := &mockMemoryPlugin{
			adviceFn: func(_ context.Context, _ RouteState) ([]RouteAdvice, error) {
				return []RouteAdvice{
					{NextStepID: "step-2", Confidence: 0.3, Reason: "low confidence"},
				}, nil
			},
		}
		require.NoError(t, bus.Register(mem))
		require.NoError(t, bus.Register(NewMemoryRouter("mr", []RouteRule{
			{FromStepID: "step-1", ToStepID: "step-3", Reason: "default path"},
		}, 0.5)))
		require.NoError(t, bus.Start(context.Background()))

		router, ok := bus.PluginsByCap(CapRouter)[0].(RouterPlugin)
		require.True(t, ok)

		dec, err := router.Route(context.Background(), RouteState{CurrentStepID: "step-1"})
		require.NoError(t, err)
		require.NotNil(t, dec)
		assert.Equal(t, "step-3", dec.NextStepID)
		assert.Equal(t, "expression", dec.Source)
	})

	t.Run("returns nil when no memory plugin available", func(t *testing.T) {
		bus := NewPluginBus()
		require.NoError(t, bus.Register(NewMemoryRouter("mr", nil, 0.5)))
		require.NoError(t, bus.Start(context.Background()))

		router, ok := bus.PluginsByCap(CapRouter)[0].(RouterPlugin)
		require.True(t, ok)

		dec, err := router.Route(context.Background(), RouteState{CurrentStepID: "step-1"})
		require.NoError(t, err)
		assert.Nil(t, dec)
	})

	t.Run("handles empty advice slice", func(t *testing.T) {
		bus := NewPluginBus()
		mem := &mockMemoryPlugin{
			adviceFn: func(_ context.Context, _ RouteState) ([]RouteAdvice, error) {
				return nil, nil
			},
		}
		require.NoError(t, bus.Register(mem))
		require.NoError(t, bus.Register(NewMemoryRouter("mr", nil, 0.5)))
		require.NoError(t, bus.Start(context.Background()))

		router, ok := bus.PluginsByCap(CapRouter)[0].(RouterPlugin)
		require.True(t, ok)

		dec, err := router.Route(context.Background(), RouteState{CurrentStepID: "step-1"})
		require.NoError(t, err)
		assert.Nil(t, dec)
	})
}
