package monitoring

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Sentinel errors for the publisher.
var (
	ErrPublisherNotStarted = errors.New("publisher not started")
)

// WSHub abstracts a WebSocket broadcast hub for pushing real-time updates.
type WSHub interface {
	// BroadcastToChannel sends a message to all subscribers of the given channel.
	BroadcastToChannel(channel string, msg any)
	// Subscribe returns a channel that receives messages for the given channels.
	Subscribe(channels ...string) <-chan any
	// Unsubscribe removes a subscription channel.
	Unsubscribe(ch <-chan any)
}

// SnapshotFunc returns the current console snapshot.
type SnapshotFunc func() ConsoleSnapshot

// Publisher periodically pushes console snapshots to a WSHub and exposes
// HTTP handlers for on-demand queries. All goroutines respect context
// cancellation.
type Publisher struct {
	mu       sync.Mutex
	mainPage *MainPage
	hub      WSHub
	interval time.Duration
	cancel   context.CancelFunc
	running  bool
	done     chan struct{}
}

// PublisherOption configures optional dependencies for Publisher.
type PublisherOption func(*Publisher)

// WithHub sets the WebSocket hub for real-time push.
func WithHub(hub WSHub) PublisherOption {
	return func(p *Publisher) {
		p.hub = hub
	}
}

// WithInterval sets the snapshot push interval. Default is 2 seconds.
func WithInterval(d time.Duration) PublisherOption {
	return func(p *Publisher) {
		p.interval = d
	}
}

// NewPublisher creates a Publisher for the given MainPage.
func NewPublisher(mainPage *MainPage, opts ...PublisherOption) *Publisher {
	if mainPage == nil {
		return nil
	}
	p := &Publisher{
		mainPage: mainPage,
		interval: 2 * time.Second,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Start launches a goroutine that periodically pushes snapshots to the hub.
// The goroutine stops when ctx is cancelled or Stop is called.
func (p *Publisher) Start(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running {
		return
	}

	ctx, cancel := context.WithCancel(ctx)
	p.cancel = cancel
	p.running = true
	p.done = make(chan struct{})

	go p.pushLoop(ctx)
}

// Stop cancels the push goroutine and waits for it to exit.
func (p *Publisher) Stop() {
	p.mu.Lock()
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
	done := p.done
	running := p.running
	p.mu.Unlock()

	if running && done != nil {
		<-done
	}

	p.mu.Lock()
	p.running = false
	p.mu.Unlock()
}

// BroadcastActionResult pushes an action result to the hub.
func (p *Publisher) BroadcastActionResult(result *ActionResult) {
	if result == nil {
		return
	}
	p.mu.Lock()
	hub := p.hub
	p.mu.Unlock()

	if hub != nil {
		hub.BroadcastToChannel("actions", result)
	}
}

// HandleConsole returns the full console snapshot as JSON.
func (p *Publisher) HandleConsole(w http.ResponseWriter, r *http.Request) {
	snap := p.mainPage.Snapshot()
	writeJSON(w, http.StatusOK, snap)
}

// HandleDAG returns the DAG snapshot as JSON.
// Accesses the engine through MainPage's thread-safe accessor.
func (p *Publisher) HandleDAG(w http.ResponseWriter, r *http.Request) {
	engine := p.mainPage.DAGEngine()
	if engine == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "DAG engine not configured"})
		return
	}
	writeJSON(w, http.StatusOK, engine.Snapshot())
}

// HandleCostBar returns the cost bar snapshot as JSON.
func (p *Publisher) HandleCostBar(w http.ResponseWriter, r *http.Request) {
	costBar := p.mainPage.CostBar()
	if costBar == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "cost bar not configured"})
		return
	}
	writeJSON(w, http.StatusOK, costBar.Snapshot())
}

// HandleAgents returns the agent list as JSON.
func (p *Publisher) HandleAgents(w http.ResponseWriter, r *http.Request) {
	snap := p.mainPage.Snapshot()
	writeJSON(w, http.StatusOK, snap.Agents)
}

// HandleAgent returns a single agent's detail as JSON.
// The agent ID is extracted from the URL path: /api/agents/{id}
func (p *Publisher) HandleAgent(w http.ResponseWriter, r *http.Request) {
	agentID := extractPathID(r.URL.Path, "/api/agents/")
	if agentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing agent ID"})
		return
	}

	dp := p.mainPage.DetailPanelReader()
	if dp == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "detail panel not configured"})
		return
	}

	detail, err := dp.GetDetail(agentID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// executeNodeAction dispatches an action to the InteractionEngine.
// Returns 501 if no InteractionEngine is configured.
func (p *Publisher) executeNodeAction(w http.ResponseWriter, r *http.Request, action string) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return
	}
	agentID := extractPathID(r.URL.Path, "/api/agents/")
	if agentID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing agent ID"})
		return
	}

	engine := p.mainPage.DAGEngine()
	if engine == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "DAG engine not configured"})
		return
	}

	// Access the interaction engine through the MainPage's parent plugin.
	// For now, return 501 if no interaction engine is available.
	// The plugin wires this at construction time.
	p.mu.Lock()
	mainPage := p.mainPage
	p.mu.Unlock()

	_ = mainPage
	_ = engine

	// TODO: wire InteractionEngine through MainPage when available.
	writeJSON(w, http.StatusNotImplemented, map[string]any{
		"action":   action,
		"agent_id": agentID,
		"status":   "not_implemented",
		"error":    "interaction engine not wired to publisher",
	})
}

// HandleKillAgent handles a POST request to kill an agent.
func (p *Publisher) HandleKillAgent(w http.ResponseWriter, r *http.Request) {
	p.executeNodeAction(w, r, "kill")
}

// HandleResumeAgent handles a POST request to resume an agent.
func (p *Publisher) HandleResumeAgent(w http.ResponseWriter, r *http.Request) {
	p.executeNodeAction(w, r, "resume")
}

// HandleRetryAgent handles a POST request to retry an agent.
func (p *Publisher) HandleRetryAgent(w http.ResponseWriter, r *http.Request) {
	p.executeNodeAction(w, r, "retry")
}

// HandleTab returns a tab snapshot by name.
// The tab name is extracted from the URL path: /api/tabs/{name}
func (p *Publisher) HandleTab(w http.ResponseWriter, r *http.Request) {
	tabName := extractPathID(r.URL.Path, "/api/tabs/")
	if tabName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing tab name"})
		return
	}

	tab, ok := p.mainPage.GetTab(tabName)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("tab %q not found", tabName)})
		return
	}
	writeJSON(w, http.StatusOK, tab.Snapshot())
}

// HandleCost returns the full cost breakdown as JSON.
func (p *Publisher) HandleCost(w http.ResponseWriter, r *http.Request) {
	snap := p.mainPage.Snapshot()
	writeJSON(w, http.StatusOK, snap.Cost)
}

// HandleTrace returns trace spans by trace ID.
// The trace ID is extracted from the URL path: /api/traces/{id}
func (p *Publisher) HandleTrace(w http.ResponseWriter, r *http.Request) {
	traceID := extractPathID(r.URL.Path, "/api/traces/")
	if traceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing trace ID"})
		return
	}
	// Trace data is not yet wired; return placeholder.
	writeJSON(w, http.StatusOK, map[string]any{
		"trace_id": traceID,
		"spans":    []any{},
	})
}

// RegisterRoutes registers all HTTP routes on the given mux.
func (p *Publisher) RegisterRoutes(mux *http.ServeMux) {
	if mux == nil {
		return
	}
	mux.HandleFunc("/api/console", p.HandleConsole)
	mux.HandleFunc("/api/dag", p.HandleDAG)
	mux.HandleFunc("/api/cost-bar", p.HandleCostBar)
	mux.HandleFunc("/api/agents/", p.routeAgent)
	mux.HandleFunc("/api/tabs/", p.HandleTab)
	mux.HandleFunc("/api/cost", p.HandleCost)
	mux.HandleFunc("/api/traces/", p.HandleTrace)
}

// routeAgent dispatches agent sub-routes based on the HTTP method and path suffix.
func (p *Publisher) routeAgent(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	if strings.HasSuffix(path, "/kill") {
		p.HandleKillAgent(w, r)
		return
	}
	if strings.HasSuffix(path, "/resume") {
		p.HandleResumeAgent(w, r)
		return
	}
	if strings.HasSuffix(path, "/retry") {
		p.HandleRetryAgent(w, r)
		return
	}

	// Default: single agent detail.
	p.HandleAgent(w, r)
}

// pushLoop periodically pushes snapshots to the hub until ctx is done.
func (p *Publisher) pushLoop(ctx context.Context) {
	defer p.signalDone()

	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.pushSnapshot()
		}
	}
}

// signalDone closes the done channel to unblock Stop.
func (p *Publisher) signalDone() {
	p.mu.Lock()
	done := p.done
	p.mu.Unlock()

	if done != nil {
		close(done)
	}
}

// pushSnapshot sends the current snapshot to the hub if available.
func (p *Publisher) pushSnapshot() {
	p.mu.Lock()
	hub := p.hub
	p.mu.Unlock()

	if hub == nil {
		return
	}

	snap := p.mainPage.Snapshot()
	hub.BroadcastToChannel("console", ConsoleUpdate{
		Type:      "snapshot",
		Payload:   snap,
		Timestamp: time.Now(),
	})
}

// extractPathID extracts the ID portion from a URL path given a prefix.
// For example, extractPathID("/api/agents/a1", "/api/agents/") returns "a1".
func extractPathID(path, prefix string) string {
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	id := strings.TrimPrefix(path, prefix)
	// Strip trailing action suffixes like /kill, /resume, /retry.
	for _, suffix := range []string{"/kill", "/resume", "/retry"} {
		id = strings.TrimSuffix(id, suffix)
	}
	return strings.Trim(id, "/")
}

// writeJSON marshals v as JSON and writes it to the response.
// If encoding fails, it logs the error and writes a 500 response.
func writeJSON(w http.ResponseWriter, status int, v any) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		slog.Error("writeJSON: encode failed", "error", err, "status", status)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal encoding error"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}
