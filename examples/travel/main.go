package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/template"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/agents/leader"
	"github.com/Timwood0x10/ares/internal/agents/sub"
	"github.com/Timwood0x10/ares/internal/ares_config"
	memory "github.com/Timwood0x10/ares/internal/ares_memory"
	"github.com/Timwood0x10/ares/internal/ares_observability"
	"github.com/Timwood0x10/ares/internal/ares_protocol/ahp"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/llm/output"
)

// Travel Planning Agent Example
// Features:
// - Multi-agent orchestration (destination, food, hotel, itinerary)
// - OpenRouter LLM provider
// - Configurable via YAML

func main() {
	slog.Info("Starting Travel Planning Agent Example")

	// Load configuration from file
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "./examples/travel/ares_config/server.yaml"
	}

	cfg, err := ares_config.Load(configPath)
	if err != nil {
		slog.Error("Failed to load ares_config", "error", err)
		os.Exit(1)
	}

	// Load environment variables
	if err := ares_config.LoadFromEnv(cfg); err != nil {
		slog.Error("Failed to load env ares_config", "error", err)
		os.Exit(1)
	}

	// Initialize components
	components, err := initializeComponents(cfg)
	if err != nil {
		slog.Error("Failed to initialize components", "error", err)
		os.Exit(1)
	}

	// Create Leader Agent
	leaderAgent, err := createLeaderAgent(cfg, components)
	if err != nil {
		slog.Error("Failed to create leader agent", "error", err)
		os.Exit(1)
	}

	// Create Sub Agents
	subAgents := createSubAgents(cfg, components)

	slog.Info("Initialized Travel Agents", "count", len(subAgents))

	// Start all agents
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := leaderAgent.Start(ctx); err != nil {
		slog.Error("Failed to start leader agent", "error", err)
		os.Exit(1)
	}

	for _, agent := range subAgents {
		if err := agent.Start(ctx); err != nil {
			slog.Warn("Failed to start agent", "id", agent.ID(), "error", err)
		}
	}

	// Setup graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		slog.Info("Shutting down")
		cancel()
		time.Sleep(time.Second)
		os.Exit(0)
	}()

	// Process sample requests
	processSampleRequests(leaderAgent, cfg)

	slog.Info("Example completed successfully")
}

type components struct {
	llmAdapter    output.LLMAdapter
	llmFactory    *output.Factory
	llmConfig     *output.Config
	tracer        ares_observability.Tracer
	messageQueue  *ahp.MessageQueue
	validator     *output.Validator
	template      *output.TemplateEngine
	memoryManager memory.MemoryManager
}

func initializeComponents(cfg *ares_config.Config) (*components, error) {
	// Create LLM adapter based on user configuration
	llmFactory := output.NewFactory()
	llmCfg := &output.Config{
		Provider:  cfg.LLM.Provider,
		APIKey:    cfg.LLM.APIKey,
		BaseURL:   cfg.LLM.BaseURL,
		Model:     cfg.LLM.Model,
		Timeout:   cfg.LLM.Timeout,
		MaxTokens: cfg.LLM.MaxTokens,
	}

	llmAdapter, err := llmFactory.Create(cfg.LLM.Provider, llmCfg)
	if err != nil {
		return nil, fmt.Errorf("create LLM adapter: %w", err)
	}

	// Create tracer
	tracer := ares_observability.NewNoopTracer()

	// Create message queue
	messageQueue := ahp.NewMessageQueue("travel-main", &ahp.QueueOptions{MaxSize: 1000})

	// Create validator
	validator := output.NewValidator(output.WithSchemaType(cfg.Validation.SchemaType))

	// Create template engine
	tmpl := output.NewTemplateEngine()

	// Initialize memory manager with default configuration.
	memoryConfig := memory.DefaultMemoryConfig()
	memoryManager, err := memory.NewMemoryManager(memoryConfig)
	if err != nil {
		return nil, fmt.Errorf("create memory manager: %w", err)
	}

	return &components{
		llmAdapter:    llmAdapter,
		llmFactory:    llmFactory,
		llmConfig:     llmCfg,
		tracer:        tracer,
		messageQueue:  messageQueue,
		validator:     validator,
		template:      tmpl,
		memoryManager: memoryManager,
	}, nil
}

func getLLMAdapter(comps *components, agentModel string, agentProvider string) output.LLMAdapter {
	provider := agentProvider
	model := agentModel

	if provider == "" {
		provider = comps.llmConfig.Provider
	}
	if model == "" {
		model = comps.llmConfig.Model
	}

	if provider == comps.llmConfig.Provider && model == comps.llmConfig.Model {
		return comps.llmAdapter
	}

	cfg := *comps.llmConfig
	cfg.Model = model
	cfg.Provider = provider
	adapter, err := comps.llmFactory.Create(provider, &cfg)
	if err != nil {
		slog.Warn("Failed to create adapter, using default", "provider", provider, "model", model, "error", err)
		return comps.llmAdapter
	}
	return adapter
}

func createLeaderAgent(cfg *ares_config.Config, comps *components) (leader.Agent, error) {
	// Create ProfileParser - for travel, we parse user preferences
	profileParser := leader.NewProfileParser(
		comps.llmAdapter,
		comps.template,
		cfg.Prompts.ProfileExtraction,
		comps.validator,
		cfg.Agents.Leader.MaxValidationRetry,
	)

	// Create TaskPlanner with sub-agent ares_config for trigger-based task selection
	subAgentConfigs := make([]leader.SubAgentConfig, len(cfg.Agents.Sub))
	for i, sub := range cfg.Agents.Sub {
		subAgentConfigs[i] = leader.SubAgentConfig{
			ID:       sub.ID,
			Type:     sub.Type,
			Triggers: sub.Triggers,
		}
	}
	taskPlanner := leader.NewTaskPlannerWithConfig(len(cfg.Agents.Sub), subAgentConfigs)

	// Create TaskDispatcher
	agentRegistry := make(map[models.AgentType]string)
	for _, subCfg := range cfg.Agents.Sub {
		agentRegistry[models.AgentType(subCfg.Type)] = subCfg.ID
	}

	taskDispatcher, err := leader.NewTaskDispatcher(
		agentRegistry,
		cfg.Agents.Leader.MaxParallelTasks,
		cfg.Agents.Leader.MaxSteps,
		nil, // messageSender
	)
	if err != nil {
		return nil, fmt.Errorf("create leader agent: %w", err)
	}

	// Register executor functions for each sub-agent type
	for _, subCfg := range cfg.Agents.Sub {
		agentType := models.AgentType(subCfg.Type)
		agentLLM := getLLMAdapter(comps, subCfg.Model, subCfg.Provider)
		executor := sub.NewTaskExecutorWithValidation(
			nil,
			agentLLM,
			comps.template,
			cfg.Prompts.Recommendation,
			comps.validator,
			subCfg.MaxRetries,
			cfg.Validation.RetryOnFail,
			cfg.Validation.StrictMode,
		)
		taskDispatcher.RegisterExecutor(agentType, executor.Execute)
	}

	// Create ResultAggregator
	resultAggregator := leader.NewResultAggregator(true, 10, leader.SortByNone)

	// Create LeaderAgent ares_config
	leaderCfg := &leader.LeaderAgentConfig{
		Config: base.Config{
			ID:   cfg.Agents.Leader.ID,
			Type: models.AgentTypeLeader,
		},
		MaxParallelTasks: cfg.Agents.Leader.MaxParallelTasks,
		MaxSteps:         cfg.Agents.Leader.MaxSteps,
		EnableCache:      cfg.Agents.Leader.EnableCache,
	}

	// Create heartbeat monitor
	hbMon := ahp.NewHeartbeatMonitor(ahp.DefaultHeartbeatConfig())

	agent, err := leader.New(
		cfg.Agents.Leader.ID,
		profileParser,
		taskPlanner,
		taskDispatcher,
		resultAggregator,
		comps.messageQueue,
		hbMon,
		comps.memoryManager,
		leaderCfg,
	)
	if err != nil {
		return nil, fmt.Errorf("create leader agent: %w", err)
	}
	return agent, nil
}

func createSubAgents(cfg *ares_config.Config, comps *components) []sub.Agent {
	agents := make([]sub.Agent, 0, len(cfg.Agents.Sub))

	for _, subCfg := range cfg.Agents.Sub {
		toolBinder := sub.NewToolBinder()
		agentLLM := getLLMAdapter(comps, subCfg.Model, subCfg.Provider)

		executor := sub.NewTaskExecutorWithValidation(
			toolBinder,
			agentLLM,
			comps.template,
			cfg.Prompts.Recommendation,
			comps.validator,
			subCfg.MaxRetries,
			cfg.Validation.RetryOnFail,
			cfg.Validation.StrictMode,
		)

		hbMon := ahp.NewHeartbeatMonitor(ahp.DefaultHeartbeatConfig())

		subCfgModel := &sub.SubAgentConfig{
			Config: base.Config{
				ID:   subCfg.ID,
				Type: models.AgentType(subCfg.Type),
			},
			EnableTools: false,
		}

		handler := sub.NewMessageHandler(subCfg.ID)

		agent := sub.New(
			subCfg.ID,
			models.AgentType(subCfg.Type),
			executor,
			handler,
			comps.messageQueue,
			hbMon,
			subCfgModel,
		)

		agents = append(agents, agent)
	}

	return agents
}

func processSampleRequests(agent leader.Agent, cfg *ares_config.Config) {
	requests := []string{
		"我想去日本东京旅游，5天4晚，预算10000元，喜欢美食和购物",
		"计划去泰国清迈3天2夜，预算3000元，喜欢自然风光和文化",
	}

	for i, input := range requests {
		slog.Info("Processing request", "request_num", i+1, "input", input)

		ctx := context.Background()
		result, err := agent.Process(ctx, input)
		if err != nil {
			slog.Error("Processing error", "error", err)
			continue
		}

		if recommendResult, ok := result.(*models.RecommendResult); ok {
			formatTravelOutput(cfg.Output, recommendResult.Items)
		} else {
			slog.Info("Result", "data", result)
		}
	}
}

func formatTravelOutput(outputCfg ares_config.OutputConfig, items []*models.RecommendItem) {
	switch outputCfg.Format {
	case "json":
		jsonBytes, err := json.MarshalIndent(items, "", "  ")
		if err != nil {
			slog.Error("JSON format error", "error", err)
			return
		}
		fmt.Println(string(jsonBytes))

	case "table":
		fmt.Println("+------+-------------------------------+-------------+------------+")
		fmt.Println("| ID   | Name                         | Category    | Price      |")
		fmt.Println("+------+-------------------------------+-------------+------------+")
		for _, item := range items {
			name := item.Name
			if len(name) > 27 {
				name = name[:24] + "..."
			}
			category := item.Category
			if len(category) > 11 {
				category = category[:8] + "..."
			}
			fmt.Printf("| %-4s | %-29s | %-11s | %10s |\n",
				truncate(item.ItemID, 4),
				name,
				category,
				fmt.Sprintf("%.0f", item.Price),
			)
		}
		fmt.Println("+------+-------------------------------+-------------+------------+")

	default:
		summaryTmpl, err := template.New("summary").Parse(outputCfg.SummaryTemplate)
		if err != nil {
			slog.Error("Template error", "error", err)
			return
		}
		summaryData := map[string]interface{}{"Count": len(items)}
		var summaryBuf strings.Builder
		if err := summaryTmpl.Execute(&summaryBuf, summaryData); err != nil {
			slog.Error("Template execute error", "error", err)
			return
		}
		slog.Info("Summary", "content", summaryBuf.String())

		itemTmpl, err := template.New("item").Parse(outputCfg.ItemTemplate)
		if err != nil {
			slog.Error("Template error", "error", err)
			return
		}
		for _, item := range items {
			var itemBuf strings.Builder
			if err := itemTmpl.Execute(&itemBuf, item); err != nil {
				continue
			}
			fmt.Println("  - " + itemBuf.String())
		}
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
}
