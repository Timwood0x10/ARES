package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/Timwood0x10/ares/internal/agents"
	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/agents/peer"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/ares_bootstrap"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_protocol/ahp"
	"github.com/Timwood0x10/ares/internal/ares_runtime"
	"github.com/Timwood0x10/ares/internal/llm/output"
	core_tools "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// createAndServeAgents builds and registers the flat peer-agent population with
// the runtime manager. This is the ONLY production serve path (aresos-agentos
// plan C1: leader removed): the configured peers spawn into the Agent Fabric as
// the dynamic population (B1), the scheduler queries the fabric for candidates,
// and the spawn_agent / create_task syscalls are wired into the tool binder for
// autonomous decomposition. The peer kernel is returned so the serve HTTP layer
// can expose the task-submission endpoint (POST /api/tasks → submitPeerTask).
func createAndServeAgents(
	ctx context.Context,
	cfg *ares_config.Config,
	internalReg *core_tools.Registry,
	llmAdapter output.LLMAdapter,
	chatClient sub.ChatClient,
	toolBinder sub.ToolBinder,
	comp *ares_bootstrap.Components,
	mgr *ares_runtime.Manager,
) ([]sub.Agent, *kernelHandle, error) {
	// The Bootstrap experience repo (nil when distillation is not wired) feeds
	// the G1 spawn prior. The StrategySource closes the GA strategy loop: the
	// evolution system deploys the best-evolved strategy into
	// NewEvolution.StrategyStore, and every agent executor reads it via
	// sub.WithStrategySource — without this bridge the deployed strategies
	// were consumed by nothing.
	var strategySrc agents.StrategySource
	if comp.NewEvolution != nil {
		strategySrc = ares_bootstrap.NewStrategySource(comp.NewEvolution.StrategyStore)
		if strategySrc != nil {
			log.Printf("serve: evolution strategy source wired into agents (GA deploy → runtime read)")
		}
	}
	subAgents, peerKernel, err := createPeerAgents(ctx, cfg, llmAdapter, chatClient, toolBinder, comp.EventStore, strategySrc, comp.ExpRepo)
	if err != nil {
		return nil, nil, fmt.Errorf("create peer agents: %w", err)
	}
	// Register agents with the runtime manager.
	for _, sa := range subAgents {
		factory := func() base.Agent { return sa }
		mgr.RegisterAgent(sa, factory)
	}
	log.Printf("serve: %d peer agents registered directly to Kernel", len(subAgents))

	// Live-DAG injection (closes the evolution structure-patch loop): the
	// configured agent population IS the live workflow topology. Register it
	// on the runtime manager and swap it into the evolution executors —
	// without this, workflow/recovery patches mutated the synthetic
	// input→process→output bootstrap DAG forever and "live promotion" was
	// unobservable.
	if comp.NewEvolution != nil {
		liveDAG, dagErr := buildLiveAgentDAG(cfg)
		switch {
		case dagErr == nil:
			mgr.RegisterAgentDAG("agents", liveDAG)
			if err := comp.NewEvolution.UpdateLiveDAG(liveDAG); err != nil {
				log.Printf("serve: live DAG injection failed (evolution keeps placeholder): %v", err)
			} else {
				log.Printf("serve: live agent DAG injected into evolution executors (%d nodes)", len(liveDAG.Steps()))
			}
		case errors.Is(dagErr, errNoLiveAgentDAG):
			log.Printf("serve: no peers configured; evolution keeps placeholder DAG")
		default:
			log.Printf("serve: live agent DAG build failed (evolution keeps placeholder): %v", dagErr)
		}
	}

	return subAgents, peerKernel, nil
}

// buildPeerRegistry registers the peer agents' message senders into a
// peer.Registry so agents can exchange messages directly without routing
// through a privileged orchestrator (primitive 2: peer-to-peer agent
// messaging). Agents that do not expose SendMessage (interface assertion) are
// skipped, not an error.
func buildPeerRegistry(subAgents []sub.Agent) *peer.Registry {
	reg := peer.NewRegistry()
	for _, sa := range subAgents {
		if sender, ok := sa.(interface {
			SendMessage(context.Context, *ahp.AHPMessage) error
		}); ok {
			_ = reg.Register(sa.ID(), sender.SendMessage)
		}
	}
	return reg
}

// setupPeerRegistry builds the peer-to-peer messaging registry. When the
// evolution system is wired, the peer channel is bridged through the
// evolution-aware IPC (v0.3.0 M2-3); otherwise the plain direct peer channel
// is used.
func setupPeerRegistry(
	subAgents []sub.Agent,
	comp *ares_bootstrap.Components,
	kernel *kernelHandle,
) (*peer.Registry, error) {
	var reg *peer.Registry
	switch {
	case comp.NewEvolution != nil:
		bridge, err := wireEvolutionIPC(subAgents, comp.NewEvolution.StrategyStore, comp.Observability.GlobalTracer, kernel)
		if err != nil {
			return nil, fmt.Errorf("wire evolution IPC: %w", err)
		}
		reg = bridge.reg
		log.Printf("peer registry wired through evolution-aware IPC: %d agents registered", len(reg.IDs()))
	default:
		reg = buildPeerRegistry(subAgents)
		log.Printf("peer registry wired: %d agents registered", len(reg.IDs()))
	}
	return reg, nil
}
