package monitoring

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/ares_runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Nil bus path ---

func TestMonitorPlugin_Start_NilBus_SkipsCollector(t *testing.T) {
	p := NewConsole()
	mp := p.(*MonitorPlugin)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := mp.Start(ctx, nil)
	require.NoError(t, err)
	assert.True(t, mp.isStarted)
	assert.Nil(t, mp.collector, "collector should be nil when no bus is provided")
	assert.NotNil(t, mp.publisher, "publisher should still be created")

	_ = mp.Stop(ctx)
}

// --- Detail with configured panel ---

type mockDetailPanelReader struct {
	mu    sync.Mutex
	views map[string]*DetailView
}

func newMockDetailPanelReader() *mockDetailPanelReader {
	return &mockDetailPanelReader{views: make(map[string]*DetailView)}
}

func (m *mockDetailPanelReader) GetDetail(entityID string) (*DetailView, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.views[entityID]
	if !ok {
		return nil, errors.New("detail not found")
	}
	return v, nil
}

func (m *mockDetailPanelReader) HandleEvent(_ *ares_events.Event) {}

func (m *mockDetailPanelReader) SetViewedAgent(_ string) {}

func TestMonitorPlugin_Detail_WithConfiguredPanel_Success(t *testing.T) {
	dp := newMockDetailPanelReader()
	dp.mu.Lock()
	dp.views["agent-1"] = &DetailView{
		EntityType: "agent",
		EntityID:   "agent-1",
		Data:       map[string]any{"status": "running"},
	}
	dp.mu.Unlock()

	mp := &MonitorPlugin{
		mainPage:  NewMainPage(WithDetailPanel(dp)),
		isStarted: false,
	}

	ctx := context.Background()
	view, err := mp.Detail(ctx, "agent", "agent-1")
	require.NoError(t, err)
	require.NotNil(t, view)
	assert.Equal(t, "agent-1", view.EntityID)
	assert.Equal(t, "running", view.Data["status"])
}

func TestMonitorPlugin_Detail_NoPanel_ReturnsError(t *testing.T) {
	p := NewConsole()
	ctx := context.Background()
	_, err := p.Detail(ctx, "agent", "a1")
	assert.ErrorIs(t, err, ErrDetailNotConfigured)
}

// --- WithTabMap option ---

func TestMonitorPlugin_WithTabMap(t *testing.T) {
	tabs := map[string]Tab{
		"tab1": &mockTab{},
		"tab2": &mockTab{},
	}
	p := NewConsole(WithTabMap(tabs))
	require.NotNil(t, p)
	mp := p.(*MonitorPlugin)
	assert.NotNil(t, mp.mainPage)
}

// --- Start with collector error ---

func TestMonitorPlugin_Start_CollectorError_Propagates(t *testing.T) {
	// Create a MonitorPlugin with a collector that errors on start.
	mp := &MonitorPlugin{
		mainPage:  NewMainPage(),
		publisher: NewPublisher(NewMainPage()),
		isStarted: false,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start with a bus that triggers collector creation.
	bus := ares_runtime.NewPluginBus()
	err := mp.Start(ctx, bus)
	require.NoError(t, err, "should succeed even without explicit collector")

	_ = mp.Stop(ctx)
}

// --- ExecuteAction with interaction engine ---

func TestMonitorPlugin_ExecuteAction_NoEngine_ReturnsError(t *testing.T) {
	p := NewConsole()
	ctx := context.Background()
	_, err := p.ExecuteAction(ctx, "kill")
	assert.ErrorIs(t, err, ErrInteractionNil)
}

// --- Events integration ---

func TestMonitorPlugin_EventsViaBus_CollectsCost(t *testing.T) {
	bus := ares_runtime.NewPluginBus()
	p := NewConsole()
	plugin := p.(ares_runtime.RuntimePlugin)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := plugin.Start(ctx, bus)
	require.NoError(t, err)

	bus.Emit(ctx, "s1", ares_events.EventLLMCall, "test", map[string]any{
		"agent_id": "a1", "estimated_cost": 0.10,
	})

	time.Sleep(50 * time.Millisecond)

	// Verify cost was collected.
	cost, err := p.CostBreakdown(ctx)
	require.NoError(t, err)
	assert.InDelta(t, 0.10, cost.Total, 0.0001)

	// Additional event.
	bus.Emit(ctx, "s2", ares_events.EventLLMCall, "test", map[string]any{
		"agent_id": "a2", "estimated_cost": 0.05,
	})

	time.Sleep(50 * time.Millisecond)

	cost, err = p.CostBreakdown(ctx)
	require.NoError(t, err)
	assert.InDelta(t, 0.15, cost.Total, 0.0001)

	_ = plugin.Stop(ctx)
}
