package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/agents/leader"
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
func createAgents(
	cfg *ares_config.Config,
	llmAdapter output.LLMAdapter,
	chatClient sub.ChatClient,
	toolBinder sub.ToolBinder,
	memMgr memory.MemoryManager,
	store ares_events.EventStore,
	feedbackSvc *experience.FeedbackService,
) (leader.Agent, []sub.Agent, error) {
	leaderAgent, err := createLeaderAgent(cfg, llmAdapter, chatClient, toolBinder, memMgr, store, feedbackSvc)
	if err != nil {
		return nil, nil, fmt.Errorf("create leader: %w", err)
	}
	subAgents := createSubAgents(cfg, llmAdapter, chatClient, toolBinder, store)
	return leaderAgent, subAgents, nil
}

func createLeaderAgent(
	cfg *ares_config.Config,
	llmAdapter output.LLMAdapter,
	chatClient sub.ChatClient,
	toolBinder sub.ToolBinder,
	memMgr memory.MemoryManager,
	store ares_events.EventStore,
	feedbackSvc *experience.FeedbackService,
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
			ID:       s.ID,
			Type:     s.Type,
			Triggers: s.Triggers,
		}
	}
	taskPlanner := leader.NewTaskPlannerWithConfig(len(cfg.Agents.Sub), subAgentConfigs)

	agentRegistry := make(map[models.AgentType]string)
	for _, s := range cfg.Agents.Sub {
		agentRegistry[models.AgentType(s.Type)] = s.ID
	}
	taskDispatcher, err := leader.NewTaskDispatcher(
		agentRegistry,
		cfg.Agents.Leader.MaxParallelTasks,
		120,
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create dispatcher: %w", err)
	}

	for _, subCfg := range cfg.Agents.Sub {
		agentType := models.AgentType(subCfg.Type)
		executor := createExecutor(llmAdapter, chatClient, toolBinder, cfg, subCfg)
		if setter, ok := executor.(interface {
			SetEventStore(ares_events.EventStore, string)
		}); ok {
			setter.SetEventStore(store, subCfg.ID)
		}
		taskDispatcher.RegisterExecutor(agentType, executor.Execute)
	}

	resultAggregator := leader.NewResultAggregator(true, 10, leader.SortByNone)
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
	)
}

func createSubAgents(
	cfg *ares_config.Config,
	llmAdapter output.LLMAdapter,
	chatClient sub.ChatClient,
	toolBinder sub.ToolBinder,
	store ares_events.EventStore,
) []sub.Agent {
	agents := make([]sub.Agent, 0, len(cfg.Agents.Sub))

	for _, subCfg := range cfg.Agents.Sub {
		executor := createExecutor(llmAdapter, chatClient, toolBinder, cfg, subCfg)

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
