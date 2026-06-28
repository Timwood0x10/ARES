package monitoring

import (
	"context"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/ares_runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCollector(t *testing.T) {
	t.Run("nil bus", func(t *testing.T) {
		mp := NewMainPage()
		c := NewCollector(nil, mp)
		assert.Nil(t, c)
	})

	t.Run("nil main page", func(t *testing.T) {
		bus := ares_runtime.NewPluginBus()
		c := NewCollector(bus, nil)
		assert.Nil(t, c)
	})

	t.Run("valid", func(t *testing.T) {
		bus := ares_runtime.NewPluginBus()
		mp := NewMainPage()
		c := NewCollector(bus, mp)
		require.NotNil(t, c)
		assert.Equal(t, bus, c.bus)
		assert.Equal(t, mp, c.mainPage)
	})

	t.Run("with logger", func(t *testing.T) {
		bus := ares_runtime.NewPluginBus()
		mp := NewMainPage()
		c := NewCollector(bus, mp, WithCollectorLogger(nil))
		require.NotNil(t, c)
	})
}

func TestCollector_StartStop(t *testing.T) {
	bus := ares_runtime.NewPluginBus()
	mp := NewMainPage()
	c := NewCollector(bus, mp)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := c.Start(ctx)
	require.NoError(t, err)

	c.Stop()
}

func TestCollector_DispatchesEvents(t *testing.T) {
	bus := ares_runtime.NewPluginBus()
	mp := NewMainPage()
	c := NewCollector(bus, mp)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := c.Start(ctx)
	require.NoError(t, err)

	// Emit an event via the bus.
	bus.Emit(ctx, "s1", ares_events.EventAgentStarted, "test", map[string]any{
		"agent_id": "a1",
		"name":     "worker",
	})

	// Give the goroutine time to process.
	time.Sleep(50 * time.Millisecond)

	// Verify the DAG received the event.
	node, ok := mp.engine.GetNode("a1")
	require.True(t, ok)
	assert.Equal(t, "worker", node.Name)

	c.Stop()
}

func TestCollector_StopIdempotent(t *testing.T) {
	bus := ares_runtime.NewPluginBus()
	mp := NewMainPage()
	c := NewCollector(bus, mp)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := c.Start(ctx)
	require.NoError(t, err)

	c.Stop()
	c.Stop()
	// Should not panic.
}

func TestCollector_ContextCancel(t *testing.T) {
	bus := ares_runtime.NewPluginBus()
	mp := NewMainPage()
	c := NewCollector(bus, mp)

	ctx, cancel := context.WithCancel(context.Background())

	err := c.Start(ctx)
	require.NoError(t, err)

	cancel()
	time.Sleep(50 * time.Millisecond)

	// Collector should be stopped via context cancellation.
}

func TestCollector_DispatchesToCostBar(t *testing.T) {
	bus := ares_runtime.NewPluginBus()
	mp := NewMainPage()
	c := NewCollector(bus, mp)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := c.Start(ctx)
	require.NoError(t, err)

	bus.Emit(ctx, "s1", ares_events.EventLLMCall, "test", map[string]any{
		"agent_id":       "a1",
		"estimated_cost": 0.05,
	})

	time.Sleep(50 * time.Millisecond)

	snap := mp.costBar.Snapshot()
	assert.InDelta(t, 0.05, snap.Total, 0.0001)

	c.Stop()
}
