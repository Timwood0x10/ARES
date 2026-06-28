package monitoring

import (
	"context"
	"log/slog"
	"time"

	"github.com/Timwood0x10/ares/internal/monitoring/dag"
)

// PruneConfig controls the TTL pruning behaviour.
type PruneConfig struct {
	// MaxAgentAge removes dead/completed DAG nodes older than this. Default 24h.
	MaxAgentAge time.Duration
	// MaxEvents keeps at most this many events per event tab. Default 10000.
	MaxEvents int
	// MaxTracesPerAgent keeps at most this many traces per agent. Default 100.
	MaxTracesPerAgent int
	// MaxTimelineLen trims per-node timeline to this length. Default 500.
	MaxTimelineLen int
	// PruneInterval controls how often the pruner runs. Default 5min.
	PruneInterval time.Duration
}

// DAGPrunable abstracts the DAG engine operations needed by the pruner.
type DAGPrunable interface {
	// Nodes returns a map of node ID to current status.
	Nodes() map[string]dag.NodeStatus
	// RemoveNode removes a node and its connected edges.
	RemoveNode(id string) error
	// TrimTimeline keeps at most maxLen timeline events for a node.
	TrimTimeline(id string, maxLen int) error
	// GetNode returns a copy of the node by ID.
	GetNode(id string) (*dag.DAGNode, bool)
}

// TrimableTab extends Tab with the ability to trim its stored data.
type TrimableTab interface {
	Tab
	// Trim retains at most maxLen entries, discarding the oldest.
	Trim(maxLen int)
}

// Pruner periodically removes stale data from the monitoring subsystems.
// All operations are cancelable via context.
type Pruner struct {
	config   PruneConfig
	mainPage *MainPage
	dag      DAGPrunable
	logger   *slog.Logger
	cancel   context.CancelFunc
}

// PrunerOption configures optional dependencies for Pruner.
type PrunerOption func(*Pruner)

// WithPrunerLogger sets a custom logger.
func WithPrunerLogger(logger *slog.Logger) PrunerOption {
	return func(p *Pruner) {
		p.logger = logger
	}
}

// WithPrunerDAG overrides the DAG prunable source.
func WithPrunerDAG(d DAGPrunable) PrunerOption {
	return func(p *Pruner) {
		p.dag = d
	}
}

// NewPruner creates a Pruner for the given MainPage.
func NewPruner(mainPage *MainPage, cfg PruneConfig, opts ...PrunerOption) *Pruner {
	if cfg.MaxAgentAge == 0 {
		cfg.MaxAgentAge = 24 * time.Hour
	}
	if cfg.MaxEvents == 0 {
		cfg.MaxEvents = 10000
	}
	if cfg.MaxTracesPerAgent == 0 {
		cfg.MaxTracesPerAgent = 100
	}
	if cfg.MaxTimelineLen == 0 {
		cfg.MaxTimelineLen = 500
	}
	if cfg.PruneInterval == 0 {
		cfg.PruneInterval = 5 * time.Minute
	}

	p := &Pruner{
		config:   cfg,
		mainPage: mainPage,
		logger:   slog.Default(),
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Start launches the pruning goroutine. It stops when ctx is cancelled or
// Stop is called.
func (p *Pruner) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	p.cancel = cancel

	go p.loop(ctx)
}

// Stop cancels the pruning goroutine.
func (p *Pruner) Stop() {
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
}

// loop runs the prune cycle on a ticker.
func (p *Pruner) loop(ctx context.Context) {
	ticker := time.NewTicker(p.config.PruneInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.prune(ctx)
		}
	}
}

// prune executes a single pruning cycle across all subsystems.
func (p *Pruner) prune(ctx context.Context) {
	p.pruneDAG(ctx)
	p.pruneTabs()
}

// pruneDAG removes old dead/completed nodes and trims timelines.
func (p *Pruner) pruneDAG(ctx context.Context) {
	d := p.dag
	if d == nil && p.mainPage != nil {
		if engine := p.mainPage.DAGEngine(); engine != nil {
			if prunable, ok := engine.(DAGPrunable); ok {
				d = prunable
			}
		}
	}
	if d == nil {
		return
	}

	cutoff := time.Now().Add(-p.config.MaxAgentAge)

	nodes := d.Nodes()
	for id, status := range nodes {
		// Check context cancellation between iterations.
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Trim timeline for all nodes.
		if p.config.MaxTimelineLen > 0 {
			if err := d.TrimTimeline(id, p.config.MaxTimelineLen); err != nil {
				p.logger.Warn("prune: trim timeline failed", "node", id, "error", err)
			}
		}

		// Remove dead/completed nodes older than cutoff.
		if status == dag.StatusDead || status == dag.StatusCompleted {
			node, ok := d.GetNode(id)
			if !ok {
				continue
			}
			if node.UpdatedAt.Before(cutoff) {
				if err := d.RemoveNode(id); err != nil {
					p.logger.Warn("prune: remove node failed", "node", id, "error", err)
				} else {
					p.logger.Debug("prune: removed stale node", "node", id, "status", status)
				}
			}
		}
	}
}

// pruneTabs trims data in TrimableTab instances.
func (p *Pruner) pruneTabs() {
	if p.mainPage == nil {
		return
	}

	p.mainPage.mu.RLock()
	tabs := make(map[string]Tab, len(p.mainPage.tabs))
	for k, v := range p.mainPage.tabs {
		tabs[k] = v
	}
	p.mainPage.mu.RUnlock()

	for name, tab := range tabs {
		if tt, ok := tab.(TrimableTab); ok {
			tt.Trim(p.config.MaxEvents)
			p.logger.Debug("prune: trimmed tab", "tab", name)
		}
	}
}
