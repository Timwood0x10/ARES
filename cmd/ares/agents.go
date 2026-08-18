package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/Timwood0x10/ares/internal/agents"
	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/agents/leader"
	"github.com/Timwood0x10/ares/internal/agents/leader/aggregate"
	"github.com/Timwood0x10/ares/internal/agents/peer"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_events"
	experience "github.com/Timwood0x10/ares/internal/ares_experience"
	memory "github.com/Timwood0x10/ares/internal/ares_memory"
	"github.com/Timwood0x10/ares/internal/ares_protocol/ahp"
	"github.com/Timwood0x10/ares/internal/core/models"
	llm "github.com/Timwood0x10/ares/internal/llm"
	"github.com/Timwood0x10/ares/internal/llm/output"
)

// createAgents builds the leader and sub agents with real LLM + tools.
// strategySrc, when non-nil, lets live agents consume the active GA strategy.
func createAgents(
	cfg *ares_config.Config,
	llmAdapter output.LLMAdapter,
	chatClient sub.ChatClient,
	toolBinder sub.ToolBinder,
	memMgr memory.MemoryManager,
	store ares_events.EventStore,
	feedbackSvc *experience.FeedbackService,
	strategySrc agents.StrategySource,
	skillLocator leader.ExperienceLocator,
) (leader.Agent, []sub.Agent, *kernelHandle, error) {
	kernel := &kernelHandle{}
	leaderAgent, err := createLeaderAgent(cfg, llmAdapter, chatClient, toolBinder, memMgr, store, feedbackSvc, strategySrc, skillLocator, kernel)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("create leader: %w", err)
	}
	subAgents := createSubAgents(cfg, llmAdapter, chatClient, toolBinder, store, strategySrc)
	return leaderAgent, subAgents, kernel, nil
}

// buildPeerRegistry registers the leader and sub agents' message senders into
// a peer.Registry so agents can exchange messages directly without routing
// through the leader (primitive 2: peer-to-peer agent messaging). Agents that
// do not expose SendMessage (interface assertion) are skipped, not an error.
func buildPeerRegistry(leaderAgent leader.Agent, subAgents []sub.Agent) *peer.Registry {
	reg := peer.NewRegistry()
	if sender, ok := leaderAgent.(interface {
		SendMessage(context.Context, *ahp.AHPMessage) error
	}); ok {
		_ = reg.Register(leaderAgent.ID(), sender.SendMessage)
	}
	for _, sa := range subAgents {
		if sender, ok := sa.(interface {
			SendMessage(context.Context, *ahp.AHPMessage) error
		}); ok {
			_ = reg.Register(sa.ID(), sender.SendMessage)
		}
	}
	return reg
}

func createLeaderAgent(
	cfg *ares_config.Config,
	llmAdapter output.LLMAdapter,
	chatClient sub.ChatClient,
	toolBinder sub.ToolBinder,
	memMgr memory.MemoryManager,
	store ares_events.EventStore,
	feedbackSvc *experience.FeedbackService,
	strategySrc agents.StrategySource,
	skillLocator leader.ExperienceLocator,
	kernel *kernelHandle,
) (leader.Agent, error) {
	profileParser := leader.NewProfileParser(
		llmAdapter,
		output.NewTemplateEngine(),
		cfg.Prompts.ProfileExtraction,
		output.NewValidator(output.WithSchemaType(cfg.Validation.SchemaType)),
		cfg.Agents.Leader.MaxValidationRetry,
	)

	subAgentConfigs := make([]leader.SubAgentConfig, len(cfg.Agents.Sub))
	for i, s := range cfg.Agents.Sub {
		subAgentConfigs[i] = leader.SubAgentConfig{
			ID:           s.ID,
			Type:         s.Type,
			Triggers:     s.Triggers,
			Dependencies: append([]string(nil), s.Dependencies...),
		}
	}
	// The skill locator pre-fills task.UsedExperienceID with the best-matching
	// skill for the task input, so the design §11 feedback loop can attribute a
	// task outcome to a skill. It is optional: nil leaves UsedExperienceID
	// empty (existing behavior).
	plannerOpts := make([]leader.PlannerOption, 0, 1)
	if skillLocator != nil {
		plannerOpts = append(plannerOpts, leader.WithExperienceLocator(skillLocator))
	}
	taskPlanner := leader.NewTaskPlannerWithConfig(len(cfg.Agents.Sub), subAgentConfigs, plannerOpts...)

	agentRegistry := make(map[models.AgentType]string)
	for _, s := range cfg.Agents.Sub {
		agentRegistry[models.AgentType(s.Type)] = s.ID
	}
	taskDispatcher, err := leader.NewTaskDispatcher(
		agentRegistry,
		cfg.Agents.Leader.MaxParallelTasks,
		120,
		nil,
		leader.WithDispatcherAgentID(cfg.Agents.Leader.ID),
		leader.WithDispatcherEventStore(store),
	)
	if err != nil {
		return nil, fmt.Errorf("create dispatcher: %w", err)
	}

	// Kernel assembly (P4 D4): wrap the legacy dispatcher in the dual-track
	// kernel. The flag starts at PolicyLegacyLeader (safe assembly default) and
	// wireKernelPolicy flips it to PolicyTaskFabric by default at serve startup
	// (only an explicit kernel.policy=legacy keeps the leader path live). Until
	// then the taskfabric new path runs in shadow, scoring every dispatched
	// task without double-executing it.
	subCaps := make([]subAgentCapability, 0, len(cfg.Agents.Sub))
	for _, s := range cfg.Agents.Sub {
		subCaps = append(subCaps, subAgentCapability{ID: s.ID, Type: s.Type})
	}
	kernelDispatcher, kernelFlag := wireKernelDispatcher(taskDispatcher, subCaps)
	taskDispatcher = newKernelTaskDispatcher(kernelDispatcher, store)
	// Expose the assembled kernel to serve so it can flip the policy and start
	// the Task Fabric scheduler per config. The batch adapter is retained so
	// the flip can inject the fabric for result read-back.
	if kernel != nil {
		kernel.dual = kernelDispatcher
		kernel.flag = kernelFlag
		kernel.taskDispatcher = taskDispatcher.(*kernelTaskDispatcher)
	}
	// Kernel shadow mode is live: the Task Fabric path scores every dispatched
	// task (no double execution) and Mismatches() counts divergence vs. the
	// legacy leader path. Flip kernelFlag to PolicyTaskFabric to cut over.
	log.Printf("kernel: dual-track dispatch assembled (policy=%d, shadow=on, candidates=%d)",
		kernelFlag.Active(), len(subCaps))

	resultAggregator := aggregate.NewResultAggregator(true, 10, aggregate.SortByNone)
	hbMon := ahp.NewHeartbeatMonitor(ahp.DefaultHeartbeatConfig())
	msgQueue := ahp.NewMessageQueue(cfg.Agents.Leader.ID, &ahp.QueueOptions{
		MaxSize: 1000, MaxWorkers: 4,
	})

	leaderCfg := &leader.LeaderAgentConfig{
		Config: base.Config{
			ID:   cfg.Agents.Leader.ID,
			Type: models.AgentTypeLeader,
		},
		MaxParallelTasks: cfg.Agents.Leader.MaxParallelTasks,
		MaxSteps:         cfg.Agents.Leader.MaxSteps,
		EnableCache:      cfg.Agents.Leader.EnableCache,
	}

	return leader.New(
		cfg.Agents.Leader.ID,
		profileParser,
		taskPlanner,
		taskDispatcher,
		resultAggregator,
		msgQueue,
		hbMon,
		memMgr,
		leaderCfg,
		leader.WithEventStore(store),
		leader.WithFeedbackService(feedbackSvc),
		leader.WithStrategySource(strategySrc),
	)
}

func createSubAgents(
	cfg *ares_config.Config,
	llmAdapter output.LLMAdapter,
	chatClient sub.ChatClient,
	toolBinder sub.ToolBinder,
	store ares_events.EventStore,
	strategySrc agents.StrategySource,
) []sub.Agent {
	agents := make([]sub.Agent, 0, len(cfg.Agents.Sub))

	for _, subCfg := range cfg.Agents.Sub {
		executor := createExecutor(llmAdapter, chatClient, toolBinder, cfg, subCfg, strategySrc)

		hbMon := ahp.NewHeartbeatMonitor(ahp.DefaultHeartbeatConfig())
		msgQueue := ahp.NewMessageQueue(subCfg.ID, &ahp.QueueOptions{MaxSize: 500})

		subCfgModel := &sub.SubAgentConfig{
			Config: base.Config{
				ID:   subCfg.ID,
				Type: models.AgentType(subCfg.Type),
			},
			EnableTools: true,
		}

		handler := sub.NewMessageHandler(subCfg.ID)

		agent := sub.New(
			subCfg.ID,
			models.AgentType(subCfg.Type),
			executor,
			handler,
			msgQueue,
			hbMon,
			subCfgModel,
			sub.WithEventStore(store),
		)

		agents = append(agents, agent)
	}

	return agents
}

func createExecutor(
	llmAdapter output.LLMAdapter,
	chatClient sub.ChatClient,
	toolBinder sub.ToolBinder,
	cfg *ares_config.Config,
	subCfg ares_config.SubAgentConfig,
	strategySrc agents.StrategySource,
) sub.TaskExecutor {
	return sub.NewTaskExecutorWithValidation(
		toolBinder,
		llmAdapter,
		output.NewTemplateEngine(),
		cfg.Prompts.Recommendation,
		output.NewValidator(output.WithSchemaType(cfg.Validation.SchemaType)),
		subCfg.MaxRetries,
		cfg.Validation.RetryOnFail,
		cfg.Validation.StrictMode,
		sub.WithChatClient(chatClient),
		sub.WithStrategySource(strategySrc),
	)
}

// createChatClient creates a FailoverClient from the LLM config for Chat API support.
func createChatClient(cfg *ares_config.Config) (sub.ChatClient, error) {
	configs := make([]*llm.Config, 0, 1+len(cfg.LLM.Fallbacks))
	configs = append(configs, &llm.Config{
		Provider:  cfg.LLM.Provider,
		APIKey:    cfg.LLM.APIKey,
		BaseURL:   cfg.LLM.BaseURL,
		Model:     cfg.LLM.Model,
		Timeout:   cfg.LLM.Timeout,
		MaxTokens: cfg.LLM.MaxTokens,
	})
	for _, fb := range cfg.LLM.Fallbacks {
		provider := fb.Provider
		if provider == "" {
			provider = "openai"
		}
		configs = append(configs, &llm.Config{
			Provider:  provider,
			APIKey:    fb.APIKey,
			BaseURL:   fb.BaseURL,
			Model:     fb.Model,
			Timeout:   fb.Timeout,
			MaxTokens: fb.MaxTokens,
		})
	}

	timeout := time.Duration(cfg.LLM.Timeout) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}

	rate := cfg.LLM.ScorerAPIRate
	burst := cfg.LLM.ScorerAPIBurst
	return llm.NewFailoverClient(configs, timeout, rate, burst)
}

// submitTasks sends real tasks to the leader agent periodically.
func submitTasks(ctx context.Context, agent leader.Agent) {
	time.Sleep(3 * time.Second)

	tasks := []string{
		"分析这个Go项目的代码架构，找出主要模块和它们之间的依赖关系",
		"Review the error handling patterns in this codebase and suggest improvements",
		"分析这个项目中的并发安全问题，重点关注goroutine和channel的使用",
		"找出代码库中的性能瓶颈，特别是热路径上的复杂度问题",
		"评估这个项目的测试覆盖率，找出缺少测试的关键模块",
	}

	for i := 0; ; i++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		task := tasks[i%len(tasks)]
		log.Printf("submitting task %d: %s", i+1, task)

		result, err := agent.Process(ctx, task)
		if err != nil {
			log.Printf("task %d failed: %v", i+1, err)
		} else if result != nil {
			log.Printf("task %d completed", i+1)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(15 * time.Second):
		}
	}
}
