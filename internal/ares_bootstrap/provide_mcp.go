// Package ares_bootstrap — MCP provider.
package ares_bootstrap

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/discovery"
	"github.com/Timwood0x10/ares/internal/runtime/protocol/mcp"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// maxUnmatchedHealthWarnings bounds the "discovery reports an MCP server this
// manager does not run" warnings. Discovery only ever reports MCP servers (its
// binary probe drops anything that is not one), so an unmatched verdict is a real
// finding rather than noise — but it must not become a per-cycle log sink in a
// deployment that discovers a fleet this manager was never configured for.
const maxUnmatchedHealthWarnings = 32

// SubscribeMCPHealthToDiscovery wires the discovery engine's health verdicts onto
// the MCP manager's per-server availability cells (defect S-mcp-S4: the engine
// updated svc.Healthy, emitted discovery.health.changed, and nothing consumed it).
//
// The engine's MCPHealthChecker probes each service by connecting and listing its
// tools, so its verdict is the only signal that can see a HALF-DEAD server — one
// that answers a probe but is slow or erroring. The manager's own connection
// looks perfectly healthy in that case, which is why consuming this event is the
// fix rather than a second liveness mechanism of our own.
//
// The join is the subtle part, and the reason this is a function rather than a
// one-line handler: the two sides name the same server in different NAMESPACES.
// Discovery identifies a service by its ENDPOINT (ServiceKey: a command's
// basename, or scheme://host/path for a URL); the manager identifies a server by
// the operator's config name. Feeding the first into the second matched nothing
// in production — every automatically discovered server failed the lookup, and
// the verdict was dropped as "not mine" — so S4 only ever fired for an operator
// who happened to name the config entry exactly like the normalized endpoint.
//
// Two exact joins, in order:
//  1. endpoint: the managed server's own transport endpoint, keyed through the
//     same discovery.ServiceKey the discovered identity was derived with.
//  2. name: an operator who named the config entry after the discovered service
//     gets the join for free, so the direct string match is kept.
//
// A service that joins to neither is REPORTED, once per identity and bounded by
// maxUnmatchedHealthWarnings, instead of being dropped in silence. The manager
// only marks servers it runs — adopting discovered servers is a bigger feature
// (S-mcp-S3 / P2-14) — but the gap must be visible to an operator.
func SubscribeMCPHealthToDiscovery(engine *discovery.Engine, mgr *ares_mcp.MCPManager) {
	if engine == nil || mgr == nil {
		return
	}
	join := &mcpHealthJoin{mgr: mgr, warned: make(map[string]bool)}
	engine.AddHandler(discovery.EventHandlerFunc(join.handle))
}

// handle applies one discovery event: health verdicts about a managed server are
// recorded, anything else is ignored, and a verdict about an MCP server this
// manager does not run is reported (bounded) rather than dropped in silence.
//
// It is a method rather than an inline closure so the wiring is testable without
// standing up an engine, a probe and a provider.
func (j *mcpHealthJoin) handle(evt discovery.Event) {
	if evt.Type != discovery.EventHealthChanged || evt.Service == nil {
		return
	}
	name, ok := j.resolve(evt.Service)
	if !ok {
		j.warnUnmatched(evt.Service)
		return
	}
	if err := j.mgr.SetServerHealth(name, evt.Service.Healthy, evt.Message); err != nil {
		log.Warn("bootstrap: apply MCP server health failed", "server", name, "error", err)
	}
}

// mcpHealthJoin joins discovery's service identities onto managed MCP servers and
// remembers which unmatched services have already been reported.
type mcpHealthJoin struct {
	mgr *ares_mcp.MCPManager
	// mu guards warned and suppressed: the engine may emit from more than one
	// goroutine, and this bookkeeping must not race.
	mu         sync.Mutex
	warned     map[string]bool
	suppressed bool
}

// resolve maps a discovered service onto a managed server name.
//
// The key table is rebuilt per event rather than cached: the MCP config hot
// reloads, and a stale table would silently stop matching after a reload — the
// same class of silent mismatch this function exists to remove. Building it costs
// one pass over the configured servers, and health events only fire on flips.
func (j *mcpHealthJoin) resolve(svc *discovery.DiscoveredService) (string, bool) {
	byEndpoint := make(map[string]string)
	names := make(map[string]bool)
	for _, ep := range j.mgr.ServerEndpoints() {
		names[ep.Name] = true
		for _, endpoint := range []string{ep.Command, ep.URL} {
			if endpoint == "" {
				continue
			}
			if key := discovery.ServiceKey(endpoint); key != "" {
				byEndpoint[key] = ep.Name
			}
		}
	}
	// Every name discovery might have used for this service, each resolved
	// through the canonical key so a URL or path form still joins.
	lookups := []string{svc.Identity.ID, svc.Identity.Name}
	if svc.Identity.Endpoint != "" {
		lookups = append(lookups, discovery.ServiceKey(svc.Identity.Endpoint))
	}
	for _, lookup := range lookups {
		if lookup == "" {
			continue
		}
		if name, ok := byEndpoint[lookup]; ok {
			return name, true
		}
		if names[lookup] {
			return lookup, true
		}
	}
	return "", false
}

// warnUnmatched reports a discovered MCP server this manager does not run, once
// per identity, and stops after maxUnmatchedHealthWarnings identities (saying so
// once, so the silence that follows is itself explained).
func (j *mcpHealthJoin) warnUnmatched(svc *discovery.DiscoveredService) {
	key := svc.Identity.ID
	if key == "" {
		key = svc.Identity.Name
	}
	j.mu.Lock()
	switch {
	case j.warned[key]:
		j.mu.Unlock()
		return
	case len(j.warned) >= maxUnmatchedHealthWarnings:
		first := !j.suppressed
		j.suppressed = true
		j.mu.Unlock()
		if first {
			log.Warn("bootstrap: too many MCP servers discovered that this manager does not run; further unmatched health verdicts are suppressed",
				"limit", maxUnmatchedHealthWarnings)
		}
		return
	}
	j.warned[key] = true
	j.mu.Unlock()
	log.Warn("bootstrap: discovery reports an MCP server this manager does not run, so its health cannot be applied",
		"discovered", key, "hint", "configure the server (its health then matches by endpoint), or ignore this if it is intentionally unmanaged")
}

// mapMCPServerConfig converts ares_config MCPServerEntry to ares_mcp MCPServerConfig.
func mapMCPServerConfig(cfg ares_config.MCPConfig) *ares_mcp.MCPManagerConfig {
	if len(cfg.Servers) == 0 {
		return nil
	}
	mgrCfg := &ares_mcp.MCPManagerConfig{
		Servers: make([]ares_mcp.MCPServerConfig, 0, len(cfg.Servers)),
	}
	for _, s := range cfg.Servers {
		sc := ares_mcp.MCPServerConfig{
			Name:      s.Name,
			Enabled:   s.Enabled,
			AutoStart: s.AutoStart,
			Timeout:   time.Duration(s.Timeout) * time.Second,
			Transport: ares_mcp.TransportConfig{
				Type: s.Transport.Type,
			},
		}
		switch s.Transport.Type {
		case "stdio":
			if s.Transport.Stdio != nil {
				sc.Transport.Stdio = &ares_mcp.StdioConfig{
					Command: s.Transport.Stdio.Command,
					Args:    s.Transport.Stdio.Args,
					Env:     s.Transport.Stdio.Env,
					WorkDir: s.Transport.Stdio.WorkDir,
				}
			}
		case "sse":
			if s.Transport.SSE != nil {
				sc.Transport.SSE = &ares_mcp.SSEConfig{
					URL:     s.Transport.SSE.URL,
					Headers: s.Transport.SSE.Headers,
					Timeout: time.Duration(s.Transport.SSE.Timeout) * time.Second,
				}
			}
		}
		mgrCfg.Servers = append(mgrCfg.Servers, sc)
	}
	return mgrCfg
}

func ProvideMCP(ctx context.Context, cfg ares_config.MCPConfig) (*ares_mcp.MCPManager, error) {
	if len(cfg.Servers) == 0 {
		// No MCP servers configured is valid for minimal configs
		registry := core.NewRegistry()
		return ares_mcp.NewMCPManager(&ares_mcp.MCPManagerConfig{}, registry)
	}
	registry := core.NewRegistry()
	mgrCfg := mapMCPServerConfig(cfg)
	mcpMgr, err := ares_mcp.NewMCPManager(mgrCfg, registry)
	if err != nil {
		return nil, fmt.Errorf("bootstrap: MCP manager: %w", err)
	}
	if err := mcpMgr.Start(ctx); err != nil {
		return nil, fmt.Errorf("bootstrap: MCP manager start: %w", err)
	}
	return mcpMgr, nil
}
