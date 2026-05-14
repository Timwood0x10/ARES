package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Timwood0x10/goagent/internal/agents/base"
	"github.com/Timwood0x10/goagent/internal/agents/leader"
	"github.com/Timwood0x10/goagent/internal/agents/sub"
	"github.com/Timwood0x10/goagent/internal/config"
	"github.com/Timwood0x10/goagent/internal/core/models"
	"github.com/Timwood0x10/goagent/internal/llm/output"
	"github.com/Timwood0x10/goagent/internal/memory"
	"github.com/Timwood0x10/goagent/internal/protocol/ahp"
)

/*
DevAgent - Developer Assistant with Multi-Agent Orchestration
Features: Code generation, review, testing, documentation with DAG orchestration

Logging Strategy:
- Use slog (structured logging) for system-level events, errors, and debugging
- Use fmt.Printf for interactive CLI output (user-facing messages)
- This separation ensures proper log formatting while maintaining CLI usability
*/
func main() {
	// Enable debug logging
	opts := &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}
	handler := slog.NewTextHandler(os.Stdout, opts)
	logger := slog.New(handler)
	slog.SetDefault(logger)

	slog.Info("Starting DevAgent - Developer Assistant")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Load configuration
	cfg, err := loadConfig()
	if err != nil {
		slog.Error("Failed to load config", "error", err)
		os.Exit(1)
	}

	// Initialize components
	comps, err := initializeComponents(ctx, cfg)
	if err != nil {
		slog.Error("Failed to initialize components", "error", err)
		os.Exit(1)
	}

	// Create agents
	leaderAgent := createLeaderAgent(cfg, comps)
	subAgents := createSubAgents(cfg, comps)

	slog.Info("Initialized DevAgents", "count", len(subAgents))

	// Start agents
	if err := startAgents(ctx, leaderAgent, subAgents); err != nil {
		slog.Error("Failed to start agents", "error", err)
		os.Exit(1)
	}

	// Setup graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		slog.Info("Shutting down...")
		cancel()
		time.Sleep(time.Second)
		os.Exit(0)
	}()

	// Start interactive CLI
	runInteractiveCLI(ctx, leaderAgent, comps)
}

type components struct {
	llmAdapter    output.LLMAdapter
	llmFactory    *output.Factory
	llmConfig     *output.Config
	messageQueue  *ahp.MessageQueue
	validator     *output.Validator
	template      *output.TemplateEngine
	memoryManager memory.MemoryManager
}

func loadConfig() (*config.Config, error) {
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "./config/server.yaml"
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	if err := config.LoadFromEnv(cfg); err != nil {
		return nil, fmt.Errorf("load env config: %w", err)
	}

	return cfg, nil
}

func initializeComponents(ctx context.Context, cfg *config.Config) (*components, error) {
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

	messageQueue := ahp.NewMessageQueue("devagent-main", &ahp.QueueOptions{MaxSize: 1000})
	validator := output.NewValidator(output.WithSchemaType(cfg.Validation.SchemaType))
	tmpl := output.NewTemplateEngine()

	// Initialize memory manager with default configuration.
	memoryConfig := memory.DefaultMemoryConfig()
	memoryManager, err := memory.NewMemoryManager(memoryConfig)
	if err != nil {
		return nil, fmt.Errorf("create memory manager: %w", err)
	}

	// Start memory manager.
	if err := memoryManager.Start(ctx); err != nil {
		return nil, fmt.Errorf("start memory manager: %w", err)
	}

	return &components{
		llmAdapter:    llmAdapter,
		llmFactory:    llmFactory,
		llmConfig:     llmCfg,
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

func createLeaderAgent(cfg *config.Config, comps *components) leader.Agent {
	profileParser := leader.NewProfileParser(
		comps.llmAdapter,
		comps.template,
		cfg.Prompts.ProfileExtraction,
		comps.validator,
		cfg.Agents.Leader.MaxValidationRetry,
	)

	subAgentConfigs := make([]leader.SubAgentConfig, len(cfg.Agents.Sub))
	for i, sub := range cfg.Agents.Sub {
		subAgentConfigs[i] = leader.SubAgentConfig{
			ID:       sub.ID,
			Type:     sub.Type,
			Triggers: sub.Triggers,
		}
	}

	taskPlanner := leader.NewTaskPlannerWithConfig(len(cfg.Agents.Sub), subAgentConfigs)

	agentRegistry := make(map[models.AgentType]string)
	for _, subCfg := range cfg.Agents.Sub {
		agentRegistry[models.AgentType(subCfg.Type)] = subCfg.ID
	}

	taskDispatcher := leader.NewTaskDispatcher(
		agentRegistry,
		cfg.Agents.Leader.MaxParallelTasks,
		cfg.Agents.Leader.MaxSteps,
		nil,
	)

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

	resultAggregator := leader.NewResultAggregator(true, 10, leader.SortByNone)
	hbMon := ahp.NewHeartbeatMonitor(ahp.DefaultHeartbeatConfig())

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
		comps.messageQueue,
		hbMon,
		comps.memoryManager,
		leaderCfg,
	)
}

func createSubAgents(cfg *config.Config, comps *components) []sub.Agent {
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

func startAgents(ctx context.Context, leaderAgent leader.Agent, subAgents []sub.Agent) error {
	if err := leaderAgent.Start(ctx); err != nil {
		return fmt.Errorf("start leader agent: %w", err)
	}

	for _, agent := range subAgents {
		if err := agent.Start(ctx); err != nil {
			slog.Warn("Failed to start agent", "id", agent.ID(), "error", err)
		}
	}

	return nil
}

func runInteractiveCLI(ctx context.Context, agent leader.Agent, comps *components) {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("╔══════════════════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                           DevAgent v0.0.1                                 ║")
	fmt.Println("║                  Developer Assistant with Multi-Agent                  ║")
	fmt.Println("║              Code • Review • Test • Documentation                   ║")
	fmt.Println("╚══════════════════════════════════════════════════════════════════════════════╝")
	fmt.Println()

	fmt.Println("Available commands:")
	fmt.Println("  'exit' or 'quit' - Exit the assistant")
	fmt.Println("  'help' - Show help information")
	fmt.Println("  Or simply describe your development task...")
	fmt.Println()

	for {
		select {
		case <-ctx.Done():
			return
		default:
			fmt.Print("DevAgent> ")
			input, err := reader.ReadString('\n')
			if err != nil {
				slog.Error("Failed to read input", "error", err)
				continue
			}

			input = strings.TrimSpace(input)
			if input == "" {
				continue
			}

			if input == "exit" || input == "quit" {
				fmt.Println("Goodbye!")
				return
			}

			if input == "help" {
				showHelp()
				continue
			}

			if err := processUserInput(ctx, agent, comps, input); err != nil {
				slog.Error("Failed to process input", "error", err)
			}
		}
	}
}

func showHelp() {
	fmt.Println("DevAgent Help:")
	fmt.Println("  This assistant helps you with development tasks including:")
	fmt.Println("  - Code generation")
	fmt.Println("  - Code review")
	fmt.Println("  - Test generation")
	fmt.Println("  - Documentation")
	fmt.Println()
	fmt.Println("  Examples:")
	fmt.Println("    'Create a REST API for user management in Python'")
	fmt.Println("    'Implement a binary search algorithm in Go'")
	fmt.Println("    'Write unit tests for a sorting function'")
	fmt.Println("    'Generate documentation for a data processing pipeline'")
	fmt.Println()
}

func processUserInput(ctx context.Context, agent leader.Agent, comps *components, input string) error {
	fmt.Println()
	fmt.Printf("Processing: %s\n", input)
	fmt.Println(strings.Repeat("-", 50))

	startTime := time.Now()
	result, err := agent.Process(ctx, input)
	duration := time.Since(startTime)

	if err != nil {
		slog.Error("Processing error", "error", err)
		fmt.Printf("Error: %v\n\n", err)
		return fmt.Errorf("process input: %w", err)
	}

	if recommendResult, ok := result.(*models.RecommendResult); ok {
		filesCreated := generateFiles(recommendResult.Items)

		if len(filesCreated) > 0 {
			fmt.Printf("\n✅ Successfully created %d file(s):\n", len(filesCreated))
			for _, file := range filesCreated {
				fmt.Printf("   📄 %s\n", file)
			}
			fmt.Println()
		}

		formatOutput(recommendResult.Items)
	} else {
		slog.Info("Result", "data", result)
		fmt.Printf("Result: %v\n\n", result)
	}

	fmt.Printf("Completed in %v\n\n", duration)
	return nil
}

func generateFiles(items []*models.RecommendItem) []string {
	var createdFiles []string

	slog.Debug("Starting file generation", "total_items", len(items))

	for _, item := range items {
		var content string
		var filename string

		slog.Debug("Processing item", "item_id", item.ItemID, "name", item.Name, "category", item.Category)

		// Prefer Content field, then try to extract from Metadata
		if item.Content != "" {
			content = item.Content
			slog.Debug("Found content in Content field", "item_id", item.ItemID, "content_length", len(content))
		} else if item.Metadata != nil {
			if contentVal, ok := item.Metadata["content"]; ok {
				content = fmt.Sprintf("%v", contentVal)
				slog.Debug("Found content in metadata", "item_id", item.ItemID, "content_length", len(content))
			} else {
				slog.Warn("No content key in metadata", "item_id", item.ItemID)
			}
		} else {
			slog.Warn("Item has no metadata", "item_id", item.ItemID)
		}

		if content == "" {
			slog.Warn("No content found for item", "item_id", item.ItemID)
			slog.Debug("Item full data for debugging", "item", fmt.Sprintf("%+v", item))
			continue
		}

		// Determine filename based on category and name
		switch item.Category {
		case "code":
			// Determine file extension based on content
			ext := detectFileExtension(content)
			filename = generateFilename(item.Name, ext)
		case "test":
			// Determine file extension based on content
			ext := detectFileExtension(content)
			filename = fmt.Sprintf("test_%s.%s", sanitizeFilename(item.Name), ext)
		case "docs":
			if strings.Contains(item.Name, "README") {
				filename = "README.md"
			} else {
				filename = fmt.Sprintf("%s.md", sanitizeFilename(item.Name))
			}
		case "review":
			filename = fmt.Sprintf("REVIEW_%s.md", sanitizeFilename(item.Name))
		default:
			filename = fmt.Sprintf("%s.txt", sanitizeFilename(item.Name))
		}

		// Write file
		if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
			slog.Error("Failed to write file", "filename", filename, "error", err)
			fmt.Printf("❌ Failed to create file: %s\n", filename)
			continue
		}

		createdFiles = append(createdFiles, filename)
		slog.Info("Created file", "filename", filename, "category", item.Category)
	}

	return createdFiles
}

func generateFilename(name string, extension string) string {
	sanitized := sanitizeFilename(name)

	// Remove common prefixes
	sanitized = strings.TrimPrefix(sanitized, "binary_search_")
	sanitized = strings.TrimPrefix(sanitized, "test_")
	sanitized = strings.TrimPrefix(sanitized, "unit_test_")

	// If name is empty or too short, use a default
	if len(sanitized) < 3 {
		sanitized = "main"
	}

	return fmt.Sprintf("%s.%s", sanitized, extension)
}

func sanitizeFilename(name string) string {
	// Remove common suffixes and prefixes
	name = strings.TrimSpace(name)
	name = strings.ToLower(name)

	// Replace spaces and special chars with underscores
	replacer := strings.NewReplacer(
		" ", "_",
		"-", "_",
		".", "_",
		",", "_",
		"(", "",
		")", "",
		"[", "",
		"]", "",
		"{", "",
		"}", "",
	)
	name = replacer.Replace(name)

	// Remove multiple consecutive underscores
	for strings.Contains(name, "__") {
		name = strings.ReplaceAll(name, "__", "_")
	}

	// Remove leading/trailing underscores
	name = strings.Trim(name, "_")

	// Limit length
	if len(name) > 50 {
		name = name[:50]
	}

	return name
}

func formatOutput(items []*models.RecommendItem) {
	if len(items) == 0 {
		fmt.Println("No results generated.")
		return
	}

	fmt.Printf("Generated %d item(s):\n\n", len(items))

	for i, item := range items {
		fmt.Printf("[%d] %s\n", i+1, item.Name)
		fmt.Printf("    Type: %s\n", item.Category)
		if item.Brand != "" {
			fmt.Printf("    Brand: %s\n", item.Brand)
		}
		fmt.Printf("    Description: %s\n", item.Description)
		fmt.Printf("    Price: %.2f\n", item.Price)

		// Extract additional info from metadata
		if item.Metadata != nil {
			if language, ok := item.Metadata["language"]; ok {
				fmt.Printf("    Language: %v\n", language)
			}
			if qualityScore, ok := item.Metadata["quality_score"]; ok {
				fmt.Printf("    Quality Score: %.1f/100\n", qualityScore)
			}
			if content, ok := item.Metadata["content"]; ok {
				fmt.Printf("\n    Content:\n")
				fmt.Println(formatContent(fmt.Sprintf("%v", content)))
			}
		}

		fmt.Println()
	}

	if len(items) > 1 {
		fmt.Println(strings.Repeat("-", 50))
		fmt.Printf("Summary: %d items generated\n", len(items))
	}
}

func formatContent(content string) string {
	if content == "" {
		return ""
	}

	lines := strings.Split(content, "\n")
	var formatted strings.Builder

	for _, line := range lines {
		formatted.WriteString("    ")
		formatted.WriteString(line)
		formatted.WriteString("\n")
	}

	return formatted.String()
}

// detectFileExtension  check code to determine file type based on content
func detectFileExtension(content string) string {
	content = strings.TrimSpace(content)

	// check  Go code
	if strings.Contains(content, "package ") &&
		(strings.Contains(content, "import \"") || strings.Contains(content, "import (")) {
		return "go"
	}

	// check  Python code
	if strings.Contains(content, "import ") && !strings.Contains(content, "package ") {
		// Python's import keyword
		if strings.Contains(content, "def ") || strings.Contains(content, "class ") {
			return "py"
		}
	}

	// check JavaScript/TypeScript
	if strings.Contains(content, "function ") || strings.Contains(content, "const ") || strings.Contains(content, "let ") {
		if strings.Contains(content, "interface ") {
			return "ts"
		}
		return "js"
	}

	// check Java
	if strings.Contains(content, "public class ") || strings.Contains(content, "public static void main") {
		return "java"
	}

	// check C/C++
	if strings.Contains(content, "#include <") || strings.Contains(content, "#include \"") {
		if strings.Contains(content, "class ") && strings.Contains(content, "public:") {
			return "cpp"
		}
		return "c"
	}

	// check Rust
	if strings.Contains(content, "fn main()") || strings.Contains(content, "use ") {
		return "rs"
	}

	// default to txt
	return "txt"
}
