package main

import (
	"context"
	"fmt"
	"log"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/agents/leader"
	"github.com/Timwood0x10/ares/internal/agents/peer"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/ares_bootstrap"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_runtime"
	"github.com/Timwood0x10/ares/internal/llm/output"
	core_tools "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// createAndServeAgents builds and registers agents with the runtime manager,
// choosing between the Leader ON (legacy compat) and Leader OFF (W2 Peer Agent)
// paths based on the kernel config. Extracted from runServe to keep its
// cyclomatic complexity within gocyclo's 30 limit.
//
// W2: Leader OFF mode — when kernel.leader_enabled is false, skip the leader
// entirely and run purely on the Peer Agent path: the configured sub-agents
// spawn into the Agent Fabric as the dynamic population (C1), the scheduler
// queries the fabric for candidates (B1), and the spawn_agent / create_task
// syscalls are wired into the tool binder for autonomous decomposition. The
// leader path is retained only behind kernel.leader_enabled=true (legacy
// gray-scaling) — see Deprecated createAndRegisterServeAgents.
func createAndServeAgents(
	ctx context.Context,
	cfg *ares_config.Config,
	internalReg *core_tools.Registry,
	llmAdapter output.LLMAdapter,
	chatClient sub.ChatClient,
	toolBinder sub.ToolBinder,
	comp *ares_bootstrap.Components,
	mgr *ares_runtime.Manager,
) (leader.Agent, []sub.Agent, *kernelHandle, error) {
	if cfg.Kernel.IsLeaderEnabled() {
		leaderAgent, subAgents, err := createAndRegisterServeAgents(ctx, cfg, internalReg, llmAdapter, chatClient, toolBinder, comp, mgr)
		return leaderAgent, subAgents, nil, err
	}

	// W2 Peer Agent mode: no leader, agents register directly to Kernel. The
	// Bootstrap experience repo (nil when distillation is not wired) feeds the
	// G1 spawn prior. The peer kernel is returned so the serve HTTP layer can
	// expose the task-submission endpoint (POST /api/tasks → submitPeerTask).
	subAgents, peerKernel, err := createPeerAgents(ctx, cfg, llmAdapter, chatClient, toolBinder, comp.EventStore, nil, comp.ExpRepo)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create peer agents: %w", err)
	}
	// Register agents with the runtime manager.
	for _, sa := range subAgents {
		factory := func() base.Agent { return sa }
		mgr.RegisterAgent(sa, factory)
	}
	log.Printf("serve: Leader OFF mode — %d peer agents registered directly to Kernel", len(subAgents))
	return nil, subAgents, peerKernel, nil
}

// setupPeerRegistry builds the peer-to-peer messaging registry. When the
// evolution system is wired, the peer channel is bridged through the
// evolution-aware IPC (v0.3.0 M2-3); otherwise the plain direct peer channel
// is used. In Leader OFF mode the registry is built from sub-agents only.
// Extracted from runServe to keep its cyclomatic complexity within limits.
func setupPeerRegistry(
	leaderAgent leader.Agent,
	subAgents []sub.Agent,
	comp *ares_bootstrap.Components,
) (*peer.Registry, error) {
	var reg *peer.Registry
	switch {
	case leaderAgent != nil && comp.NewEvolution != nil:
		bridge, err := wireEvolutionIPC(leaderAgent, subAgents, comp.NewEvolution.StrategyStore, comp.Observability.GlobalTracer)
		if err != nil {
			return nil, fmt.Errorf("wire evolution IPC: %w", err)
		}
		reg = bridge.reg
		log.Printf("peer registry wired through evolution-aware IPC: %d agents registered", len(reg.IDs()))
	case leaderAgent != nil:
		reg = buildPeerRegistry(leaderAgent, subAgents)
		log.Printf("peer registry wired: %d agents registered", len(reg.IDs()))
	default:
		// W2 Leader OFF: build peer registry from sub-agents only.
		reg = buildPeerRegistry(nil, subAgents)
		log.Printf("peer registry wired (leader OFF): %d agents registered", len(reg.IDs()))
	}
	if leaderAgent != nil {
		if leaderWithPeer, ok := leaderAgent.(interface {
			SetPeerRegistry(*peer.Registry)
		}); ok {
			leaderWithPeer.SetPeerRegistry(reg)
		}
	}
	return reg, nil
}
