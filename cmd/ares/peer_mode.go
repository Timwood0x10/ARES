package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/Timwood0x10/ares/internal/agentfabric"
	"github.com/Timwood0x10/ares/internal/agents"
	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/agentsyscall"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/aresrecovery"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/llm/output"
	"github.com/Timwood0x10/ares/internal/taskfabric"
)

// createPeerAgents builds a set of peer agents WITHOUT a Leader. Each agent
// registers directly with the Kernel scheduler via the Task Fabric. This is
// the W2 "Leader OFF" startup mode (aresos-plan.md §6.3.6): a group of
// equal agents competes for tasks via capability-based scheduling, with no
// privileged orchestrator.
//
// The spawn_agent / create_task syscalls are wired into the shared ToolBinder
// so every agent can autonomously decide to decompose work and spawn peers.
// The Kernel enforces quota/capability validation on every spawn.
func createPeerAgents(
	ctx context.Context,
	cfg *ares_config.Config,
	llmAdapter output.LLMAdapter,
	chatClient sub.ChatClient,
	toolBinder sub.ToolBinder,
	store ares_events.EventStore,
	strategySrc agents.StrategySource,
) ([]sub.Agent, *kernelHandle, error) {
	kernel := &kernelHandle{}

	// Build sub-agents from config (same as the leader path — each agent
	// gets the full LLM + tool stack).
	subAgents := createSubAgents(cfg, llmAdapter, chatClient, toolBinder, store, strategySrc)
	if len(subAgents) == 0 {
		return nil, nil, fmt.Errorf("peer mode: no sub-agents configured")
	}

	// Assemble the Kernel: Task Fabric + Agent Fabric + scheduler. This
	// mirrors flipKernelToTaskFabric but runs directly at startup (no
	// legacy path to flip from).
	kernel.fabric = taskfabric.NewFabric()
	if store != nil {
		kernel.fabric = kernel.fabric.WithEventStore(store)
	}
	kernel.executors = make(map[string]CapabilityExecutor, len(subAgents))
	for _, a := range subAgents {
		if a != nil {
			kernel.executors[a.ID()] = a // sub.Agent satisfies CapabilityExecutor
		}
	}

	// Build the candidate list for the fabric dispatcher.
	subCaps := make([]subAgentCapability, 0, len(cfg.Agents.Sub))
	for _, s := range cfg.Agents.Sub {
		subCaps = append(subCaps, subAgentCapability{ID: s.ID, Type: s.Type})
	}

	// Assemble the dual-track kernel with the fabric path as the active
	// path (no legacy leader dispatcher — a nil legacy dispatcher is fine
	// because the flag starts at PolicyTaskFabric).
	kernelDispatcher, kernelFlag := wireKernelDispatcher(nil, subCaps)
	kernel.dual = kernelDispatcher
	kernel.flag = kernelFlag

	// One shared load tracker for the scheduler.
	tracker := newLoadTracker()
	kernel.tracker = tracker

	// Enable real Task Fabric execution (not shadow mode).
	enableKernelExecution(kernel.dual, kernel.fabric)
	kernelFlag.Set(0) // PolicyTaskFabric

	// Start the scheduler.
	sched := NewKernelScheduler(kernel.fabric, kernel.executors, tracker)
	if store != nil {
		sched.WithEventStore(store)
	}
	sched.WithMaxConcurrent(0)
	kernel.scheduler = sched
	kernel.flipped = true

	// W4 evolution feedback loop: record execution outcomes per agent +
	// capability, and periodically push the derived confidence back into the
	// tracker so the next Schedule prefers historically-successful executors.
	attribution := aresrecovery.NewExecutionAttribution()
	sched.WithAttribution(attribution)
	feedback := aresrecovery.NewEvolutionFeedbackAdapter(attribution, tracker)
	go aresrecovery.RunEvolutionFeedbackLoop(ctx, feedback, 10*time.Second)

	// Assemble the Lifecycle pillar (agentfabric + aresrecovery).
	agents := agentfabric.NewFabric()
	if len(cfg.Kernel.Resources) > 0 {
		agents = agents.WithResourceBudget(cfg.Kernel.Resources)
	}
	kernel.agents = agents

	policy := aresrecovery.DefaultRestartPolicy()
	if cfg.Kernel.MaxRestarts > 0 {
		policy.MaxRestarts = cfg.Kernel.MaxRestarts
	}
	kernel.recovery = aresrecovery.New(kernel.fabric, agents, policy)
	sched.WithGovernance(agents)

	// Wire the spawn_agent / create_task syscalls into the shared ToolBinder.
	// Every agent's LLM executor sees these tools alongside the built-in
	// tools, so it can autonomously decide to spawn peers and create tasks.
	kernelSyscall := agentsyscall.NewKernel(
		agents,
		kernel.fabric,
		func(agentID, capability string) agentsyscall.Executor {
			agent := newPeerExecutor(agentID, models.AgentType(capability), llmAdapter, chatClient, toolBinder, cfg, strategySrc)
			return &peerExecutorAdapter{agent: agent}
		},
		func(agentID string, executor agentsyscall.Executor) {
			// Adapt agentsyscall.Executor to CapabilityExecutor for the scheduler.
			// The peerExecutorAdapter wraps sub.Agent and satisfies both
			// agentsyscall.Executor and CapabilityExecutor.
			if se, ok := executor.(*peerExecutorAdapter); ok {
				sched.RegisterExecutor(agentID, se.agent)
			}
		},
	)
	agentsyscall.BindTools(toolBinder, kernelSyscall)
	log.Printf("peer mode: spawn_agent / create_task syscalls wired into tool binder")

	// Inject agent priorities into the tracker.
	for _, sub := range cfg.Agents.Sub {
		if sub.Priority > 0 {
			tracker.SetPriority(sub.ID, sub.Priority)
		}
	}

	// Start the scheduler and recovery loop. The recovery loop wires a REAL
	// executor factory (newPeerExecutor — full sub.Agent with LLM + tools) and
	// binds each replacement to exactly the task it was spawned for
	// (RegisterExecutorForTask), so a dead agent's task is resumed by a real
	// cognitive process — not a canned-success stub, and never at the expense
	// of a brand-new task.
	go sched.Run(ctx)
	go runKernelRecoveryLoop(ctx, store, kernel.recovery, parseKernelLoopConfig(cfg),
		func(taskID, agentID string, executor CapabilityExecutor) {
			sched.RegisterExecutorForTask(taskID, agentID, executor)
		},
		func(agentID, capability string) CapabilityExecutor {
			return newPeerExecutor(agentID, models.AgentType(capability), llmAdapter, chatClient, toolBinder, cfg, strategySrc)
		},
		sched.HasCapableExecutor,
	)

	log.Printf("peer mode: %d peer agents registered, Kernel scheduler started (no leader)", len(subAgents))
	return subAgents, kernel, nil
}

// newPeerExecutor creates a full sub.Agent executor for a dynamically spawned
// peer agent. The executor carries the same LLM + tool stack as the configured
// agents, so a spawned agent is a real cognitive process — not a stub.
func newPeerExecutor(
	agentID string,
	capability models.AgentType,
	llmAdapter output.LLMAdapter,
	chatClient sub.ChatClient,
	toolBinder sub.ToolBinder,
	cfg *ares_config.Config,
	strategySrc agents.StrategySource,
) sub.Agent {
	// Build a sub-agent config from the capability.
	subCfg := ares_config.SubAgentConfig{
		ID:         agentID,
		Type:       string(capability),
		MaxRetries: 3,
		Timeout:    60,
	}
	executor := createExecutor(llmAdapter, chatClient, toolBinder, cfg, subCfg, strategySrc)
	handler := sub.NewMessageHandler(agentID)
	agent := sub.New(
		agentID,
		capability,
		executor,
		handler,
		nil,
		nil,
		&sub.SubAgentConfig{
			Config: base.Config{
				ID:   agentID,
				Type: capability,
			},
			EnableTools: true,
		},
	)
	return agent
}

// submitPeerTask creates a task directly in the Task Fabric for the peer-agent
// runtime (no leader dispatch). This is the entry point for user-submitted
// work in Leader OFF mode: the task enters READY and the Kernel scheduler
// picks it up via the normal Schedule → Acquire → RunQuantum path.
//
// TODO(tech-debt): wire this to the HTTP serve endpoint so /api/tasks in
// Leader OFF mode submits directly to the fabric instead of going through
// the leader. Currently the leader path's submitTasks covers the autopilot
// demo; the peer-mode HTTP endpoint will be added when the serve API is
// extended for peer-agent mode.
func submitPeerTask(ctx context.Context, kernel *kernelHandle, capability string, payload map[string]any) (string, error) {
	if kernel == nil || kernel.fabric == nil {
		return "", fmt.Errorf("peer mode: kernel fabric not wired")
	}
	kernel.tracker.mu.Lock()
	kernel.tracker.done["__seq"]++
	seq := kernel.tracker.done["__seq"]
	kernel.tracker.mu.Unlock()
	taskID := fmt.Sprintf("peer-task-%s-%d", capability, seq)

	task := &taskfabric.Task{
		ID:          taskID,
		Capability:  capability,
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 2},
		Checkpoint: &taskfabric.CheckpointEnvelope{
			Payload: payload,
		},
	}
	if err := kernel.fabric.Create(task); err != nil {
		return "", fmt.Errorf("peer mode: create task: %w", err)
	}
	log.Printf("peer mode: submitted task %q (%s) → READY", taskID, capability)
	return taskID, nil
}

// _ ensures submitPeerTask is retained as the peer-mode task submission API
// even before its HTTP endpoint is wired (staticcheck U1000).
var _ = submitPeerTask

// peerExecutorAdapter wraps a sub.Agent to satisfy the agentsyscall.Executor
// interface. The adapter translates sub.StepOutcome to agentsyscall.StepOutcome
// so the syscall package stays decoupled from the sub package (code_rules_v2
// §5.2: interface at the consumer).
type peerExecutorAdapter struct {
	agent sub.Agent
}

func (a *peerExecutorAdapter) ID() string             { return a.agent.ID() }
func (a *peerExecutorAdapter) Type() models.AgentType { return a.agent.Type() }
func (a *peerExecutorAdapter) ExecuteStep(ctx context.Context, task *models.Task) (*agentsyscall.StepOutcome, error) {
	out, err := a.agent.ExecuteStep(ctx, task)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return &agentsyscall.StepOutcome{}, nil
	}
	return &agentsyscall.StepOutcome{
		Done:       out.Done,
		Checkpoint: out.Checkpoint,
		Result:     out.Result,
	}, nil
}
