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

	"goagentx/internal/agents/base"
	"goagentx/internal/agents/leader"
	"goagentx/internal/agents/sub"
	"goagentx/internal/config"
	"goagentx/internal/core/models"
	"goagentx/internal/llm/output"
	"goagentx/internal/memory"
	"goagentx/internal/observability"
	"goagentx/internal/protocol/ahp"
)

// This is an example demonstrating how to use the framework.
// Users can configure:
// - Number and types of Agents
// - Prompt templates
// - Max retries, max steps, etc.
// All through configuration files.

func main() {
	slog.Info("Starting Style Agent Example")

	// Load configuration from file
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "./examples/simple/config/server.yaml"
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		slog.Error("Failed to load config", "error", err)
		os.Exit(1)
	}

	// Load environment variables
	if err := config.LoadFromEnv(cfg); err != nil {
		slog.Error("Failed to load env config", "error", err)
		os.Exit(1)
	}

	// Initialize components
	components, err := initializeComponents(cfg)
	if err != nil {
		slog.Error("Failed to initialize components", "error", err)
		os.Exit(1)
	}

	// Create Leader Agent with user configuration
	leaderAgent := createLeaderAgent(cfg, components)

	// Create Sub Agents based on user configuration
	subAgents := createSubAgents(cfg, components)

	slog.Info("Initialized Sub Agents", "count", len(subAgents))

	// Start all agents
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := leaderAgent.Start(ctx); err != nil {
		slog.Error("Failed to start leader agent", "error", err)
		os.Exit(1)
	}

	for _, agent := range subAgents {
		if err := agent.Start(ctx); err != nil {
			slog.Warn("Failed to start sub agent", "id", agent.ID(), "error", err)
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

	// Process a sample request
	processSampleRequest(leaderAgent, cfg)

	slog.Info("Example completed successfully")
}

type components struct {
	llmAdapter    output.LLMAdapter
	llmFactory    *output.Factory
	llmConfig     *output.Config
	tracer        observability.Tracer
	messageQueue  *ahp.MessageQueue
	validator     *output.Validator
	template      *output.TemplateEngine
	memoryManager memory.MemoryManager
}

func initializeComponents(cfg *config.Config) (*components, error) {
	// Create LLM adapter based on user configuration
	llmFactory := output.NewFactory()
	llmCfg := &output.Config{
		Provider: cfg.LLM.Provider,
		APIKey:   cfg.LLM.APIKey,
		BaseURL:  cfg.LLM.BaseURL,
		Model:    cfg.LLM.Model,
		Timeout:  cfg.LLM.Timeout,
	}
	llmAdapter, err := llmFactory.Create(cfg.LLM.Provider, llmCfg)
	if err != nil {
		return nil, fmt.Errorf("create LLM adapter: %w", err)
	}

	// Create tracer (NoopTracer for production, LogTracer for development)
	tracer := observability.NewNoopTracer()

	// Create message queue
	messageQueue := ahp.NewMessageQueue("main", &ahp.QueueOptions{MaxSize: 1000})

	// Create validator
	validator := output.NewValidator()

	// Create template engine
	template := output.NewTemplateEngine()

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
		template:      template,
		memoryManager: memoryManager,
	}, nil
}

// getLLMAdapter returns the appropriate LLM adapter for the given agent config.
// If the agent specifies a model or provider, it creates a new adapter with those values.
// Otherwise, returns the default global adapter.
func getLLMAdapter(comps *components, agentModel string, agentProvider string) output.LLMAdapter {
	// Determine which provider and model to use
	provider := agentProvider
	model := agentModel

	if provider == "" {
		provider = comps.llmConfig.Provider
	}
	if model == "" {
		model = comps.llmConfig.Model
	}

	// If nothing changed from global config, return default adapter
	if provider == comps.llmConfig.Provider && model == comps.llmConfig.Model {
		return comps.llmAdapter
	}

	// Create a new adapter with the specified model and/or provider
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

func createLeaderAgent(cfg *config.Config, comps *components) leader.Agent {
	// Create ProfileParser with user-configured prompts
	profileParser := leader.NewProfileParser(
		comps.llmAdapter,
		comps.template,
		cfg.Prompts.ProfileExtraction,
		comps.validator,
		cfg.Agents.Leader.MaxValidationRetry,
	)

	// Create TaskPlanner
	taskPlanner := leader.NewTaskPlanner(len(cfg.Agents.Sub))

	// Create TaskDispatcher
	agentRegistry := make(map[models.AgentType]string)
	for _, subCfg := range cfg.Agents.Sub {
		agentRegistry[models.AgentType(subCfg.Type)] = subCfg.ID
	}

	taskDispatcher := leader.NewTaskDispatcher(
		agentRegistry,
		cfg.Agents.Leader.MaxParallelTasks,
		cfg.Agents.Leader.MaxSteps,
		nil, // messageSender (not used in simple example)
	)

	// Register executor functions for each sub-agent type
	for _, subCfg := range cfg.Agents.Sub {
		agentType := models.AgentType(subCfg.Type)
		// Get LLM adapter for this agent (uses agent-specific model if configured)
		agentLLM := getLLMAdapter(comps, subCfg.Model, subCfg.Provider)
		executor := sub.NewTaskExecutor(
			nil, // Tool binder (not needed for simple example)
			agentLLM,
			comps.template,
			cfg.Prompts.Recommendation,
			comps.validator,
			subCfg.MaxRetries,
		)
		// Register the executor
		taskDispatcher.RegisterExecutor(agentType, executor.Execute)
	}

	// Create ResultAggregator
	resultAggregator := leader.NewResultAggregator(true, 10, leader.SortByNone)

	// Create LeaderAgent config from user configuration
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

	return leader.New(
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
}

func createSubAgents(cfg *config.Config, comps *components) []sub.Agent {
	agents := make([]sub.Agent, 0, len(cfg.Agents.Sub))

	for _, subCfg := range cfg.Agents.Sub {
		// Create ToolBinder (for future tool integration)
		toolBinder := sub.NewToolBinder()

		// Get LLM adapter for this agent (uses agent-specific model if configured)
		agentLLM := getLLMAdapter(comps, subCfg.Model, subCfg.Provider)

		// Create TaskExecutor with user-configured prompts
		executor := sub.NewTaskExecutor(
			toolBinder,
			agentLLM,
			comps.template,
			cfg.Prompts.Recommendation,
			comps.validator,
			subCfg.MaxRetries,
		)

		// Create heartbeat monitor
		hbMon := ahp.NewHeartbeatMonitor(ahp.DefaultHeartbeatConfig())

		// Create SubAgent config
		subCfgModel := &sub.SubAgentConfig{
			Config: base.Config{
				ID:   subCfg.ID,
				Type: models.AgentType(subCfg.Type),
			},
			EnableTools: false,
		}

		// Create message handler
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

func processSampleRequest(agent leader.Agent, cfg *config.Config) {
	ctx := context.Background()

	// Sample user input
	input := "我想找一些适合日常通勤的衣服，休闲风格，预算500-1000元"

	slog.Info("Processing request", "input", input)

	result, err := agent.Process(ctx, input)
	if err != nil {
		slog.Error("Processing error", "error", err)
		return
	}

	if recommendResult, ok := result.(*models.RecommendResult); ok {
		formatOutput(cfg.Output, recommendResult.Items)
	} else {
		slog.Info("Result", "data", result)
	}
}

// formatOutput formats recommendations according to user configuration.
func formatOutput(outputCfg config.OutputConfig, items []*models.RecommendItem) {
	switch outputCfg.Format {
	case "json":
		// JSON format
		jsonBytes, err := json.MarshalIndent(items, "", "  ")
		if err != nil {
			slog.Error("JSON format error", "error", err)
			return
		}
		fmt.Println(string(jsonBytes))

	case "table":
		// Table format with headers
		fmt.Println("+--------+---------------------------+-------------+---------+")
		fmt.Println("| ID     | Name                      | Category    | Price   |")
		fmt.Println("+--------+---------------------------+-------------+---------+")
		for _, item := range items {
			name := item.Name
			if len(name) > 23 {
				name = name[:20] + "..."
			}
			category := item.Category
			if len(category) > 11 {
				category = category[:8] + "..."
			}
			fmt.Printf("| %-6s | %-25s | %-11s | %7.2f |\n",
				truncate(item.ItemID, 6),
				name,
				category,
				item.Price,
			)
		}
		fmt.Println("+--------+---------------------------+-------------+---------+")

	default:
		// Simple format using templates
		// Render summary
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

		// Render each item
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
