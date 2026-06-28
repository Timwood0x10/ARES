package monitoring

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockHub implements WSHub for testing.
type mockHub struct {
	mu       sync.Mutex
	messages []hubMessage
}

type hubMessage struct {
	channel string
	msg     any
}

func newMockHub() *mockHub {
	return &mockHub{}
}

func (h *mockHub) BroadcastToChannel(channel string, msg any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.messages = append(h.messages, hubMessage{channel: channel, msg: msg})
}

func (h *mockHub) Subscribe(channels ...string) <-chan any {
	return make(chan any)
}

func (h *mockHub) Unsubscribe(ch <-chan any) {}

func (h *mockHub) Messages() []hubMessage {
	h.mu.Lock()
	defer h.mu.Unlock()
	cp := make([]hubMessage, len(h.messages))
	copy(cp, h.messages)
	return cp
}

func TestNewPublisher(t *testing.T) {
	t.Run("nil main page", func(t *testing.T) {
		p := NewPublisher(nil)
		assert.Nil(t, p)
	})

	t.Run("defaults", func(t *testing.T) {
		mp := NewMainPage()
		p := NewPublisher(mp)
		require.NotNil(t, p)
		assert.Equal(t, 2*time.Second, p.interval)
		assert.Nil(t, p.hub)
	})

	t.Run("with options", func(t *testing.T) {
		mp := NewMainPage()
		hub := newMockHub()
		p := NewPublisher(mp, WithHub(hub), WithInterval(500*time.Millisecond))
		require.NotNil(t, p)
		assert.Equal(t, hub, p.hub)
		assert.Equal(t, 500*time.Millisecond, p.interval)
	})
}

func TestPublisher_StartStop(t *testing.T) {
	mp := NewMainPage()
	hub := newMockHub()
	p := NewPublisher(mp, WithHub(hub), WithInterval(50*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p.Start(ctx)

	// Double start should be a no-op.
	p.Start(ctx)

	// Wait for at least one push cycle.
	time.Sleep(100 * time.Millisecond)

	p.Stop()

	msgs := hub.Messages()
	assert.GreaterOrEqual(t, len(msgs), 1)
	assert.Equal(t, "console", msgs[0].channel)
}

func TestPublisher_StopWithoutStart(t *testing.T) {
	mp := NewMainPage()
	p := NewPublisher(mp)
	p.Stop()
	// Should not panic.
}

func TestPublisher_BroadcastActionResult(t *testing.T) {
	mp := NewMainPage()
	hub := newMockHub()
	p := NewPublisher(mp, WithHub(hub))

	result := &ActionResult{
		ActionID: "act-1",
		Success:  true,
		Message:  "done",
	}
	p.BroadcastActionResult(result)

	msgs := hub.Messages()
	require.Len(t, msgs, 1)
	assert.Equal(t, "actions", msgs[0].channel)
}

func TestPublisher_BroadcastActionResult_Nil(t *testing.T) {
	mp := NewMainPage()
	hub := newMockHub()
	p := NewPublisher(mp, WithHub(hub))

	p.BroadcastActionResult(nil)
	assert.Empty(t, hub.Messages())
}

func TestPublisher_BroadcastActionResult_NoHub(t *testing.T) {
	mp := NewMainPage()
	p := NewPublisher(mp)

	p.BroadcastActionResult(&ActionResult{ActionID: "a1"})
	// Should not panic.
}

func TestPublisher_HandleConsole(t *testing.T) {
	mp := NewMainPage()
	p := NewPublisher(mp)

	req := httptest.NewRequest(http.MethodGet, "/api/console", nil)
	w := httptest.NewRecorder()
	p.HandleConsole(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var snap ConsoleSnapshot
	err := json.NewDecoder(w.Body).Decode(&snap)
	require.NoError(t, err)
	assert.False(t, snap.UpdateTime.IsZero())
}

func TestPublisher_HandleDAG(t *testing.T) {
	mp := NewMainPage()
	p := NewPublisher(mp)

	req := httptest.NewRequest(http.MethodGet, "/api/dag", nil)
	w := httptest.NewRecorder()
	p.HandleDAG(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestPublisher_HandleCostBar(t *testing.T) {
	mp := NewMainPage()
	p := NewPublisher(mp)

	req := httptest.NewRequest(http.MethodGet, "/api/cost-bar", nil)
	w := httptest.NewRecorder()
	p.HandleCostBar(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestPublisher_HandleAgents(t *testing.T) {
	mp := NewMainPage()
	p := NewPublisher(mp)

	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	w := httptest.NewRecorder()
	p.HandleAgents(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestPublisher_HandleAgent(t *testing.T) {
	t.Run("missing agent ID", func(t *testing.T) {
		mp := NewMainPage()
		p := NewPublisher(mp)

		req := httptest.NewRequest(http.MethodGet, "/api/agents/", nil)
		w := httptest.NewRecorder()
		p.HandleAgent(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("agent not found", func(t *testing.T) {
		tracker := newMockTracker()
		dp := NewDetailPanel(tracker)
		mp := NewMainPage(WithDetailPanel(dp))
		p := NewPublisher(mp)

		req := httptest.NewRequest(http.MethodGet, "/api/agents/missing", nil)
		w := httptest.NewRecorder()
		// Override path for handler.
		req.URL.Path = "/api/agents/missing"
		p.HandleAgent(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("no detail panel", func(t *testing.T) {
		mp := NewMainPage()
		p := NewPublisher(mp)

		req := httptest.NewRequest(http.MethodGet, "/api/agents/a1", nil)
		w := httptest.NewRecorder()
		p.HandleAgent(w, req)

		assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	})
}

func TestPublisher_HandleKillAgent(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		mp := NewMainPage()
		p := NewPublisher(mp)

		req := httptest.NewRequest(http.MethodPost, "/api/agents/a1/kill", nil)
		w := httptest.NewRecorder()
		p.HandleKillAgent(w, req)

		assert.Equal(t, http.StatusNotImplemented, w.Code)

		var resp map[string]any
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.Equal(t, "kill", resp["action"])
	})

	t.Run("wrong method", func(t *testing.T) {
		mp := NewMainPage()
		p := NewPublisher(mp)

		req := httptest.NewRequest(http.MethodGet, "/api/agents/a1/kill", nil)
		w := httptest.NewRecorder()
		p.HandleKillAgent(w, req)

		assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
	})
}

func TestPublisher_HandleResumeAgent(t *testing.T) {
	mp := NewMainPage()
	p := NewPublisher(mp)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/a1/resume", nil)
	w := httptest.NewRecorder()
	p.HandleResumeAgent(w, req)

	assert.Equal(t, http.StatusNotImplemented, w.Code)
}

func TestPublisher_HandleRetryAgent(t *testing.T) {
	mp := NewMainPage()
	p := NewPublisher(mp)

	req := httptest.NewRequest(http.MethodPost, "/api/agents/a1/retry", nil)
	w := httptest.NewRecorder()
	p.HandleRetryAgent(w, req)

	assert.Equal(t, http.StatusNotImplemented, w.Code)
}

func TestPublisher_HandleTab(t *testing.T) {
	t.Run("existing tab", func(t *testing.T) {
		tab := newMockTab("events", "Events")
		tab.snapValue = map[string]string{"status": "ok"}
		mp := NewMainPage(WithTabs(map[string]Tab{"events": tab}))
		p := NewPublisher(mp)

		req := httptest.NewRequest(http.MethodGet, "/api/tabs/events", nil)
		w := httptest.NewRecorder()
		p.HandleTab(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("missing tab", func(t *testing.T) {
		mp := NewMainPage()
		p := NewPublisher(mp)

		req := httptest.NewRequest(http.MethodGet, "/api/tabs/missing", nil)
		w := httptest.NewRecorder()
		p.HandleTab(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("empty tab name", func(t *testing.T) {
		mp := NewMainPage()
		p := NewPublisher(mp)

		req := httptest.NewRequest(http.MethodGet, "/api/tabs/", nil)
		w := httptest.NewRecorder()
		p.HandleTab(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestPublisher_HandleCost(t *testing.T) {
	mp := NewMainPage()
	p := NewPublisher(mp)

	req := httptest.NewRequest(http.MethodGet, "/api/cost", nil)
	w := httptest.NewRecorder()
	p.HandleCost(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestPublisher_HandleTrace(t *testing.T) {
	t.Run("valid trace ID", func(t *testing.T) {
		mp := NewMainPage()
		p := NewPublisher(mp)

		req := httptest.NewRequest(http.MethodGet, "/api/traces/trace-1", nil)
		w := httptest.NewRecorder()
		p.HandleTrace(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("missing trace ID", func(t *testing.T) {
		mp := NewMainPage()
		p := NewPublisher(mp)

		req := httptest.NewRequest(http.MethodGet, "/api/traces/", nil)
		w := httptest.NewRecorder()
		p.HandleTrace(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestPublisher_RegisterRoutes(t *testing.T) {
	mp := NewMainPage()
	p := NewPublisher(mp)

	mux := http.NewServeMux()
	p.RegisterRoutes(mux)

	// Verify that routes are registered by making requests.
	tests := []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodGet, "/api/console", http.StatusOK},
		{http.MethodGet, "/api/dag", http.StatusOK},
		{http.MethodGet, "/api/cost-bar", http.StatusOK},
		{http.MethodGet, "/api/agents/", http.StatusBadRequest},
		{http.MethodGet, "/api/tabs/", http.StatusBadRequest},
		{http.MethodGet, "/api/cost", http.StatusOK},
		{http.MethodGet, "/api/traces/", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			assert.Equal(t, tt.want, w.Code)
		})
	}
}

func TestPublisher_RegisterRoutes_NilMux(t *testing.T) {
	mp := NewMainPage()
	p := NewPublisher(mp)
	p.RegisterRoutes(nil)
	// Should not panic.
}

func TestPublisher_PushLoop_ContextCancel(t *testing.T) {
	mp := NewMainPage()
	hub := newMockHub()
	p := NewPublisher(mp, WithHub(hub), WithInterval(10*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	p.Start(ctx)

	time.Sleep(50 * time.Millisecond)
	cancel()
	time.Sleep(30 * time.Millisecond)

	// Stop waits for the goroutine to exit.
	p.Stop()

	msgs := hub.Messages()
	assert.GreaterOrEqual(t, len(msgs), 1)
}

func TestExtractPathID(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		prefix string
		want   string
	}{
		{"simple", "/api/agents/a1", "/api/agents/", "a1"},
		{"with trailing slash", "/api/agents/a1/", "/api/agents/", "a1"},
		{"with kill suffix", "/api/agents/a1/kill", "/api/agents/", "a1"},
		{"with resume suffix", "/api/agents/a1/resume", "/api/agents/", "a1"},
		{"empty after prefix", "/api/agents/", "/api/agents/", ""},
		{"no match", "/api/other/x", "/api/agents/", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractPathID(tt.path, tt.prefix)
			assert.Equal(t, tt.want, got)
		})
	}
}
