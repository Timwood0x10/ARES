package main

import (
	"errors"
	"fmt"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/workflow/engine"
)

// errNoLiveAgentDAG is returned when no peers are configured: the caller
// keeps the bootstrap placeholder rather than injecting an empty graph.
var errNoLiveAgentDAG = errors.New("no peer agents configured for a live DAG")

// buildLiveAgentDAG materializes the configured agent population as a real
// MutableDAG: one node per peer (AgentType = primary capability), dependency
// edges from the legacy agents.sub entries' Dependencies when present.
//
// This is the live topology the evolution system's structure patches act on.
// Historically serve never called UpdateLiveDAG, so workflow/recovery
// patches mutated the synthetic input→process→output bootstrap DAG forever —
// the "live runtime" promotion affected nothing observable. The returned DAG
// is registered on the runtime manager AND injected into the evolution
// executors so graph/recovery patches land on the agent graph actually shown
// in the runtime snapshot.
//
// Returns (nil, errNoLiveAgentDAG) when no peers are configured — the caller
// matches on that sentinel and keeps the bootstrap placeholder rather than
// injecting an empty graph.
func buildLiveAgentDAG(cfg *ares_config.Config) (*engine.MutableDAG, error) {
	peers := normalizedPeers(cfg)
	if len(peers) == 0 {
		return nil, errNoLiveAgentDAG
	}

	// Legacy sub entries may declare Dependencies between agents; carry them
	// over so pre-C1 configs keep their declared topology.
	legacyDeps := make(map[string][]string, len(cfg.Agents.Sub))
	for _, s := range cfg.Agents.Sub {
		if len(s.Dependencies) > 0 {
			legacyDeps[s.ID] = append([]string(nil), s.Dependencies...)
		}
	}

	steps := make([]*engine.Step, 0, len(peers))
	seen := make(map[string]bool, len(peers))
	for _, p := range peers {
		if p.ID == "" || seen[p.ID] {
			continue // defensive: NewMutableDAG rejects duplicate ids anyway
		}
		seen[p.ID] = true
		typ := ""
		if len(p.Capabilities) > 0 {
			typ = p.Capabilities[0]
		}
		step := &engine.Step{
			ID:        p.ID,
			Name:      p.ID,
			AgentType: typ,
			Input:     fmt.Sprintf("capability:%s", typ),
		}
		if deps, ok := legacyDeps[p.ID]; ok {
			step.DependsOn = deps
		}
		steps = append(steps, step)
	}
	if len(steps) == 0 {
		return nil, errNoLiveAgentDAG
	}

	dag, err := engine.NewMutableDAG(steps)
	if err != nil {
		return nil, fmt.Errorf("build live agent DAG: %w", err)
	}
	return dag, nil
}
