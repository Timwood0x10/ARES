// End-to-End Example: Full Chain Demonstration
//
// This example demonstrates the complete pipeline:
//   Phase 1: Config file startup          -> config.Load + config.LoadFromEnv
//   Phase 2: Agent processes task          -> Leader agent + sub agents
//   Phase 3: Memory distillation           -> TaskResult -> Experience
//   Phase 4: Workflow orchestration        -> DAG-based workflow execution
//   Phase 5: Snapshot & Resurrection       -> State capture + crash recovery
//
// Run: go run examples/end-to-end/main.go

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"time"

	"goagentx/internal/agents/base"
	"goagentx/internal/agents/leader"
	"goagentx/internal/agents/sub"
	"goagentx/internal/config"
	"goagentx/internal/core/models"
	"goagentx/internal/events"
	"goagentx/internal/experience"
	"goagentx/internal/llm/output"
	"goagentx/internal/memory"
	"goagentx/internal/observability"
	"goagentx/internal/plugins/resurrection"
	"goagentx/internal/protocol/ahp"
	"goagentx/internal/workflow/engine"
)

func main() {
	slog.Info("=== End-to-End Example: Full Chain Demonstration ===")
	fmt.Println()

	// ---------------------------------------------------------------
	// Phase 1: Config Startup
	// ---------------------------------------------------------------
	slog.Info("Phase 1: Config file startup")
	slog.Info("----------------------------------------------------")

	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "./examples/end-to-end/config/server.yaml"
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		slog.Error("Failed to load config", "error", err)
		os.Exit(1)
	}

	if err := config.LoadFromEnv(cfg); err != nil {
		slog.Error("Failed to load env config", "error", err)
		os.Exit(1)
	}

	slog.Info("Config loaded", "llm_provider", cfg.LLM.Provider, "llm_model", cfg.LLM.Model)
	if cfg.LLM.APIKey == "" || cfg.LLM.APIKey == "sk-or-v1-xxx" {
		slog.Error("Invalid LLM API key — set LLM_API_KEY environment variable with a real key",
			"api_key_preview", fmt.Sprintf("%s...", cfg.LLM.APIKey[:min(len(cfg.LLM.APIKey), 10)]))
		os.Exit(1)
	}
	slog.Info("Leader agent", "id", cfg.Agents.Leader.ID)
	for _, subCfg := range cfg.Agents.Sub {
		slog.Info("Sub agent", "id", subCfg.ID, "type", subCfg.Type)
	}
	fmt.Println()

	// ---------------------------------------------------------------
	// Phase 2: Agent Processing
	// ---------------------------------------------------------------
	slog.Info("Phase 2: Agent processes task")
	slog.Info("----------------------------------------------------")

	components, err := initializeComponents(cfg)
	if err != nil {
		slog.Error("Failed to initialize components", "error", err)
		os.Exit(1)
	}

	leaderAgent := createLeaderAgent(cfg, components)
	subAgents := createSubAgents(cfg, components)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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

	// Sample task input
	sampleInput := "请分析Python项目中的代码质量，重点关注性能瓶颈和安全隐患"

	result, err := leaderAgent.Process(ctx, sampleInput)
	if err != nil {
		slog.Error("Agent processing error", "error", err)
		// Continue to show distillation and workflow even if agent fails
	}

	var resultStr string
	if result == nil {
		resultStr = "no result"
		slog.Warn("Agent returned nil result")
	} else if recommendResult, ok := result.(*models.RecommendResult); ok {
		resultStr = fmt.Sprintf("Found %d items, reason: %s", len(recommendResult.Items), recommendResult.Reason)
		slog.Info("Agent result", "items", len(recommendResult.Items), "reason", recommendResult.Reason)
		for _, item := range recommendResult.Items {
			slog.Info("  Item", "name", item.Name, "category", item.Category, "score", item.Price)
		}
	} else {
		resultStr = fmt.Sprintf("%v", result)
		slog.Info("Agent result (raw)", "data", resultStr)
	}
	fmt.Println()

	// ---------------------------------------------------------------
	// Phase 3: Memory Distillation
	// ---------------------------------------------------------------
	slog.Info("Phase 3: Memory distillation")
	slog.Info("----------------------------------------------------")

	taskResult := &experience.TaskResult{
		Task:     sampleInput,
		Context:  "Code analysis task in end-to-end demo",
		Result:   resultStr,
		Success:  err == nil,
		AgentID:  cfg.Agents.Leader.ID,
		TenantID: "demo-tenant",
	}

	distilledExp := distillExperience(taskResult)
	if distilledExp != nil {
		slog.Info("Distillation complete", "id", distilledExp.ID, "problem", distilledExp.Problem)
		slog.Info("  Solution", "solution", distilledExp.Solution)
	}

	// Distill a batch of simulated experiences
	batchTasks := []*experience.TaskResult{
		{
			Task:     "优化数据库查询性能",
			Context:  "PostgreSQL slow query optimization",
			Result:   "添加了索引并重写了JOIN查询，查询时间从5s降低到50ms",
			Success:  true,
			AgentID:  "agent-analyzer",
			TenantID: "demo-tenant",
		},
		{
			Task:     "实现用户认证模块",
			Context:  "Web应用JWT认证",
			Result:   "使用JWT + refresh token实现了完整的认证流程",
			Success:  true,
			AgentID:  "agent-recommender",
			TenantID: "demo-tenant",
		},
		{
			Task:     "重构遗留代码",
			Context:  "将单体架构拆分为微服务",
			Result:   "拆分失败，服务间耦合过高",
			Success:  false,
			AgentID:  "agent-analyzer",
			TenantID: "demo-tenant",
		},
	}
	experiences := distillBatchExperiences(batchTasks)
	slog.Info("Batch distillation", "distilled", len(experiences), "total", len(batchTasks))
	fmt.Println()

	// ---------------------------------------------------------------
	// Phase 4: Workflow Orchestration
	// ---------------------------------------------------------------
	slog.Info("Phase 4: Workflow orchestration")
	slog.Info("----------------------------------------------------")

	executeWorkflow(ctx)
	fmt.Println()

	// ---------------------------------------------------------------
	// Phase 5: Snapshot & Resurrection
	// ---------------------------------------------------------------
	slog.Info("Phase 5: Snapshot & Resurrection")
	slog.Info("----------------------------------------------------")

	executeResurrectionDemo(ctx, cfg, components, leaderAgent)
	fmt.Println()

	slog.Info("=== End-to-End Example Completed Successfully ===")
}

// -----------------------------------------------------------------------
// Component initialization (following the travel example pattern)
// -----------------------------------------------------------------------

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

	tracer := observability.NewNoopTracer()
	messageQueue := ahp.NewMessageQueue("e2e-main", &ahp.QueueOptions{MaxSize: 1000})
	validator := output.NewValidator(output.WithSchemaType(cfg.Validation.SchemaType))
	tmpl := output.NewTemplateEngine()

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

func getLLMAdapter(comps *components, agentModel, agentProvider string) output.LLMAdapter {
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

// -----------------------------------------------------------------------
// Agent creation (following the travel example pattern)
// -----------------------------------------------------------------------

// createExecutorForSubAgent builds a TaskExecutorWithValidation for a given
// sub-agent config. This is the single source of truth for executor creation,
// used by both createLeaderAgent (for dispatcher registration) and createSubAgents.
func createExecutorForSubAgent(comps *components, cfg *config.Config, subCfg config.SubAgentConfig) sub.TaskExecutor {
	agentLLM := getLLMAdapter(comps, subCfg.Model, subCfg.Provider)
	return sub.NewTaskExecutorWithValidation(
		nil, // toolBinder: nil for local execution mode
		agentLLM,
		comps.template,
		cfg.Prompts.Recommendation,
		comps.validator,
		subCfg.MaxRetries,
		cfg.Validation.RetryOnFail,
		cfg.Validation.StrictMode,
	)
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
		30,  // timeout per step in seconds (not MaxSteps — that is task step count)
		nil, // MessageSender is nil because we use RegisterExecutor mode:
		//     tasks are dispatched locally via executor funcs, never sent remotely.
	)

	for _, subCfg := range cfg.Agents.Sub {
		agentType := models.AgentType(subCfg.Type)
		executor := createExecutorForSubAgent(comps, cfg, subCfg)
		taskDispatcher.RegisterExecutor(agentType, executor.Execute)
	}

	resultAggregator := leader.NewResultAggregator(true, 10, leader.SortByNone)

	leaderCfg := &leader.LeaderAgentConfig{
		Config: base.Config{
			ID:   cfg.Agents.Leader.ID,
			Type: models.AgentTypeLeader,
		},
		MaxParallelTasks: cfg.Agents.Leader.MaxParallelTasks,
		MaxSteps:         cfg.Agents.Leader.MaxSteps,
		EnableCache:      cfg.Agents.Leader.EnableCache,
	}

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
		executor := createExecutorForSubAgent(comps, cfg, subCfg)

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

// -----------------------------------------------------------------------
// Phase 3: Memory Distillation (in-memory implementation)
// -----------------------------------------------------------------------

// InMemoryExperienceStore is a simple in-memory store for distilled experiences.
// In production, this would be DistillationService backed by Postgres + pgvector.
type InMemoryExperienceStore struct {
	experiences []*experience.Experience
}

// shouldDistill checks if a task result is worth distilling (mirrors the real
// experience.ShouldDistill logic).
func shouldDistill(task *experience.TaskResult) bool {
	if task == nil {
		return false
	}
	if !task.Success {
		return false
	}
	if len(task.Task) < 10 {
		return false
	}
	if len(task.Result) < 20 {
		return false
	}
	return true
}

// distillExperience converts a TaskResult into an Experience.
// In production, this would call DistillationService.Distill() which:
//  1. Calls LLM to extract Problem/Solution/Constraints
//  2. Generates embedding via EmbeddingClient
//  3. Persists to database via ExperienceRepositoryInterface
func distillExperience(task *experience.TaskResult) *experience.Experience {
	if !shouldDistill(task) {
		slog.Warn("Task does not meet distillation criteria",
			"success", task.Success,
			"task_len", len(task.Task),
			"result_len", len(task.Result))
		return nil
	}

	exp := &experience.Experience{
		ID:          fmt.Sprintf("exp-%d", time.Now().UnixNano()),
		TenantID:    task.TenantID,
		Type:        "success",
		Problem:     extractProblem(task.Task, task.Context),
		Solution:    extractSolution(task.Result),
		Constraints: "N/A",
		Score:       1.0,
		Success:     task.Success,
		AgentID:     task.AgentID,
		UsageCount:  0,
		CreatedAt:   time.Now(),
	}

	slog.Info("Distilled experience",
		"id", exp.ID,
		"problem", truncate(exp.Problem, 60),
		"solution", truncate(exp.Solution, 60))
	return exp
}

// distillBatchExperiences distills multiple task results.
// Mirrors the real DistillationService.DistillBatch flow.
func distillBatchExperiences(tasks []*experience.TaskResult) []*experience.Experience {
	store := &InMemoryExperienceStore{}
	for _, task := range tasks {
		exp := distillExperience(task)
		if exp != nil {
			store.experiences = append(store.experiences, exp)
		}
	}

	// Show ranking if we have multiple experiences (mirrors RankingService)
	if len(store.experiences) > 1 {
		slog.Info("Experience ranking:")
		for i, exp := range store.experiences {
			recency := fmt.Sprintf("%.2f", time.Since(exp.CreatedAt).Hours()/24.0)
			slog.Info(fmt.Sprintf("  #%d", i+1),
				"id", exp.ID,
				"problem", truncate(exp.Problem, 40),
				"score", fmt.Sprintf("%.2f", exp.Score),
				"usage", exp.UsageCount,
				"age_hours", recency)
		}
	}

	return store.experiences
}

func extractProblem(task, context string) string {
	// In production, this is done via LLM (DistillationService.extractExperience).
	return fmt.Sprintf("Task: %s | Context: %s", task, context)
}

func extractSolution(result string) string {
	// In production, this is done via LLM (DistillationService.extractExperience).
	return result
}

func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// getMapKeys returns sorted keys of a map[string]any for diagnostic logging.
func getMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// -----------------------------------------------------------------------
// Phase 4: Workflow Orchestration
// -----------------------------------------------------------------------

// mockAnalyzerAgent is a simple mock agent for workflow demonstration.
// It implements the base.Agent interface by embedding the base skeleton.
type mockAnalyzerAgent struct {
	id   string
	role string
}

func (a *mockAnalyzerAgent) ID() string                      { return a.id }
func (a *mockAnalyzerAgent) Type() models.AgentType          { return models.AgentType(a.role) }
func (a *mockAnalyzerAgent) Status() models.AgentStatus      { return models.AgentStatusReady }
func (a *mockAnalyzerAgent) Start(ctx context.Context) error { return nil }
func (a *mockAnalyzerAgent) Stop(ctx context.Context) error  { return nil }
func (a *mockAnalyzerAgent) Events() <-chan base.AgentEvent  { return nil }
func (a *mockAnalyzerAgent) ProcessStream(ctx context.Context, input any) (<-chan base.AgentEvent, error) {
	ch := make(chan base.AgentEvent)
	close(ch)
	return ch, nil
}
func (a *mockAnalyzerAgent) Process(ctx context.Context, input any) (any, error) {
	inputStr := fmt.Sprintf("%v", input)
	slog.Info("Workflow agent processing", "agent", a.id, "input_len", len(inputStr))

	// NOTE: This mock returns *models.RecommendResult because
	// workflow/engine/registry.go AgentExecutor.Execute() specifically
	// type-asserts to *models.RecommendResult and extracts Items[0].Description.
	// If the executor's output handling changes, this mock must be updated.
	return &models.RecommendResult{
		SessionID: a.id,
		Items: []*models.RecommendItem{
			{
				ItemID:      fmt.Sprintf("result-%s", a.id),
				Name:        fmt.Sprintf("Analysis from %s", a.id),
				Description: fmt.Sprintf("Processed by %s: %s", a.id, truncate(inputStr, 100)),
				Category:    a.role,
				Price:       95.0,
			},
		},
		Reason:    fmt.Sprintf("Automated analysis by %s", a.id),
		CreatedAt: time.Now(),
	}, nil
}

func initWorkflowRegistry() *engine.AgentRegistry {
	registry := engine.NewAgentRegistry()

	// Register the "analyzer" agent type
	_ = registry.Register("analyzer", func(ctx context.Context, config interface{}) (base.Agent, error) {
		return &mockAnalyzerAgent{id: "wf-analyzer", role: "analyzer"}, nil
	})

	// Register the "recommender" agent type
	_ = registry.Register("recommender", func(ctx context.Context, config interface{}) (base.Agent, error) {
		return &mockAnalyzerAgent{id: "wf-recommender", role: "recommender"}, nil
	})

	return registry
}

func executeWorkflow(ctx context.Context) {
	// Load workflow definition from YAML file.
	// This mirrors the real workflow.NewYAMLFileLoader usage in production.
	loader := engine.NewYAMLFileLoader()
	workflow, err := loader.Load(ctx, "./examples/end-to-end/config/workflow.yaml")
	if err != nil {
		slog.Error("Failed to load workflow", "error", err)
		return
	}

	slog.Info("Workflow loaded", "id", workflow.ID, "name", workflow.Name, "steps", len(workflow.Steps))
	for _, step := range workflow.Steps {
		deps := step.DependsOn
		if deps == nil {
			deps = []string{}
		}
		slog.Info("  Step", "id", step.ID, "type", step.AgentType, "depends_on", deps)
	}

	// Verify DAG is valid (no cycles)
	dag, err := engine.NewDAG(workflow.Steps)
	if err != nil {
		slog.Error("Invalid DAG", "error", err)
		return
	}
	order, _ := dag.GetExecutionOrder()
	slog.Info("Execution order", "order", order)

	// Register agents and execute.
	registry := initWorkflowRegistry()
	executor := engine.NewExecutor(registry)

	workflowCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	start := time.Now()
	result, err := executor.Execute(workflowCtx, workflow, "请分析微服务架构的性能瓶颈和优化方案")
	duration := time.Since(start)

	if err != nil {
		slog.Error("Workflow execution failed", "error", err)
		return
	}

	slog.Info("Workflow completed",
		"status", result.Status,
		"duration", duration,
		"steps", len(result.Steps))

	for _, stepResult := range result.Steps {
		slog.Info(fmt.Sprintf("  Step %s (%s)", stepResult.StepID, stepResult.Name),
			"status", stepResult.Status,
			"output", truncate(stepResult.Output, 80),
			"duration", stepResult.Duration)
	}
}

// -----------------------------------------------------------------------
// Phase 5: Snapshot & Resurrection Demonstration
// -----------------------------------------------------------------------

// executeResurrectionDemo demonstrates the full snapshot/resurrection lifecycle:
//   - Capture a running agent's state via Snapshot()
//   - Simulate agent crash by stopping it
//   - Create a fresh agent instance via factory
//   - Restore state from saved snapshot
//   - Replay events for incremental recovery
//   - Verify the restored agent can continue processing with recovered session
func executeResurrectionDemo(ctx context.Context, cfg *config.Config, comps *components, originalAgent leader.Agent) {
	// executeResurrectionDemo demonstrates the full snapshot/resurrection lifecycle.
	// NOTE: This is a simplified demo. The factory creates new dependency instances
	// (message queue, memory manager) rather than sharing the original agent's
	// dependencies. In production, the factory should reuse shared infrastructure
	// (e.g., same MemoryManager, same MessageQueue) so the restored agent can
	// continue conversations in the same context as the original.

	// 5.1 Capture snapshot of the running leader agent.
	slog.Info("5.1 Capture snapshot of running leader agent")

	statefulAgent, ok := originalAgent.(base.StatefulAgent)
	if !ok {
		slog.Error("Leader agent does not implement StatefulAgent interface",
			"agent_id", originalAgent.ID())
		return
	}

	snapshot, err := statefulAgent.Snapshot()
	if err != nil {
		slog.Error("Failed to capture snapshot", "error", err)
		return
	}

	snapshotJSON, _ := json.MarshalIndent(snapshot, "", "  ")
	slog.Info("Snapshot captured successfully",
		"agent_id", originalAgent.ID(),
		"snapshot", string(snapshotJSON))

	// Persist snapshot to MemorySnapshotStore for resurrection demo.
	snapStore := resurrection.NewMemorySnapshotStore()
	if err := snapStore.Save(ctx, originalAgent.ID(), snapshot); err != nil {
		slog.Error("Failed to persist snapshot", "error", err)
		return
	}
	slog.Info("Snapshot persisted to MemorySnapshotStore",
		"agent_id", originalAgent.ID())

	// Record the original session ID for later verification.
	originalSessionID := ""
	if sid, ok := snapshot["session_id"].(string); ok && sid != "" {
		originalSessionID = sid
	} else {
		slog.Warn("Snapshot does not contain a valid 'session_id' (string) key",
			"keys", getMapKeys(snapshot),
			"note", "continuity check will be skipped")
	}
	slog.Info("Original session ID recorded", "session_id", originalSessionID)

	// 5.2 Simulate agent crash by stopping the agent.
	slog.Info("5.2 Simulate agent crash (stop agent)")

	if err := originalAgent.Stop(ctx); err != nil {
		slog.Warn("Error stopping agent during crash simulation", "error", err)
	}
	slog.Info("Agent stopped (crash simulated)",
		"agent_id", originalAgent.ID(),
		"status", originalAgent.Status())

	// 5.3 Create new agent instance via factory.
	// TODO: For shared state recovery, pass the original `comps` (which contains
	// the same MemoryManager and MessageQueue) so the restored agent can access
	// previously stored memories and messages. Currently createLeaderAgent creates
	// fresh instances, which is sufficient for demo purposes but limits continuity.
	slog.Info("5.3 Create new agent via factory")

	newAgent := createLeaderAgent(cfg, comps)
	slog.Info("New agent created via factory",
		"new_agent_id", newAgent.ID(),
		"type", newAgent.Type())

	// 5.4 Restore state from snapshot.
	slog.Info("5.4 Restore state from snapshot")

	loadedSnapshot, err := snapStore.Load(ctx, originalAgent.ID())
	if err != nil {
		slog.Error("Failed to load snapshot from store", "error", err)
		return
	}
	if loadedSnapshot == nil {
		slog.Error("No snapshot found in store")
		return
	}

	newStatefulAgent, ok := newAgent.(base.StatefulAgent)
	if !ok {
		slog.Error("New agent does not implement StatefulAgent interface")
		return
	}

	if err := newStatefulAgent.RestoreState(loadedSnapshot); err != nil {
		slog.Error("Failed to restore state from snapshot", "error", err)
		return
	}
	slog.Info("State restored from snapshot successfully",
		"agent_id", newAgent.ID(),
		"restored_session_id", loadedSnapshot["session_id"])

	// 5.5 Replay events for incremental recovery.
	slog.Info("5.5 Replay events for incremental recovery")

	// Build synthetic events that would have been emitted during the
	// original agent's lifecycle. In production these come from EventStore.
	replayEvents := []*events.Event{
		{
			ID:       "evt-session-created-001",
			StreamID: originalAgent.ID(),
			Type:     events.EventSessionCreated,
			Payload: map[string]any{
				"session_id": originalSessionID,
				"user_id":    "demo-user",
			},
			Version:   1,
			Timestamp: time.Now().Add(-2 * time.Minute),
		},
		{
			ID:       "evt-message-added-001",
			StreamID: originalAgent.ID(),
			Type:     events.EventMessageAdded,
			Payload: map[string]any{
				"session_id": originalSessionID,
				"role":       "user",
			},
			Version:   2,
			Timestamp: time.Now().Add(-1 * time.Minute),
		},
	}

	if err := newStatefulAgent.ReplayEvents(replayEvents); err != nil {
		slog.Error("Failed to replay events", "error", err)
		return
	}
	slog.Info("Events replayed successfully",
		"event_count", len(replayEvents),
		"agent_id", newAgent.ID())

	// 5.6 Verify restored agent processes new task with recovered session.
	slog.Info("5.6 Verify restored agent processes new task with recovered session")

	resCtx, resCancel := context.WithTimeout(ctx, 30*time.Second)
	defer resCancel()

	if err := newAgent.Start(resCtx); err != nil {
		slog.Error("Failed to start restored agent", "error", err)
		return
	}
	slog.Info("Restored agent started",
		"agent_id", newAgent.ID(),
		"status", newAgent.Status())

	followUpTask := "基于之前的分析，请进一步优化数据库索引策略"
	slog.Info("Processing follow-up task on restored agent",
		"task", truncate(followUpTask, 60))

	restoreResult, err := newAgent.Process(resCtx, followUpTask)
	if err != nil {
		slog.Error("Restored agent processing failed", "error", err)
		return
	}

	var restoreResultStr string
	if recommendResult, ok := restoreResult.(*models.RecommendResult); ok {
		slog.Info("Restored agent result",
			"items", len(recommendResult.Items),
			"reason", recommendResult.Reason,
			"session_id", recommendResult.SessionID)
	} else if restoreResult != nil {
		restoreResultStr = fmt.Sprintf("%v", restoreResult)
		slog.Info("Restored agent result (raw)", "data", restoreResultStr)
	}

	// 5.7 Compare results to confirm state continuity.
	slog.Info("5.7 Compare results to confirm state continuity")

	slog.Info("=== Resurrection Verification Summary ===")
	slog.Info("  Original Agent ID:", "id", originalAgent.ID())
	slog.Info("  Restored Agent ID:", "id", newAgent.ID())
	slog.Info("  Original Session ID:", "session_id", originalSessionID)

	// Verify A: Snapshot field-level restoration.
	restoredSnapshot, snapErr := newStatefulAgent.Snapshot()
	fieldRestorationOK := false
	if snapErr == nil && restoredSnapshot != nil {
		restoredSID, _ := restoredSnapshot["session_id"].(string)
		if originalSessionID != "" && restoredSID == originalSessionID {
			fieldRestorationOK = true
			slog.Info("✓ Snapshot field restoration verified",
				"session_id", restoredSID,
				"snapshot_keys", getMapKeys(restoredSnapshot))
		} else {
			slog.Warn("✗ Snapshot session_id mismatch after restore",
				"original", originalSessionID,
				"restored", restoredSID)
		}
	} else {
		slog.Warn("Could not capture post-restore snapshot for field verification",
			"error", snapErr)
	}

	// Verify B: Result-level session continuity (best-effort).
	resultContinuity := false
	if recommendResult, ok := restoreResult.(*models.RecommendResult); ok {
		if originalSessionID != "" && recommendResult.SessionID == originalSessionID {
			resultContinuity = true
		}
	}

	if fieldRestorationOK || resultContinuity {
		slog.Info("✓ State continuity verified",
			"field_restoration", fieldRestorationOK,
			"result_continuity", resultContinuity,
			"status", "PASS")
	} else {
		slog.Info("✗ State continuity could not be verified",
			"original_session", originalSessionID,
			"note", "in-memory agents may generate new sessions — this is expected in demo mode")
	}

	slog.Info("Phase 5 completed: Snapshot & Resurrection demonstration finished")
}

func init() {
	// Set JSON format for structured logging in the demo
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))
}
