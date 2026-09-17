package ares_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	ares "github.com/Timwood0x10/ares/api"
)

// TestAPI_RuntimeLifecycle verifies Runtime creation and teardown.
func TestAPI_RuntimeLifecycle(t *testing.T) {
	rt := ares.NewRuntime(
		ares.WithOllama("llama3.2"),
		ares.WithoutMemory(),
		ares.WithTrace(false),
	)
	defer rt.Close()

	if rt == nil {
		t.Fatal("NewRuntime returned nil")
	}
	if rt.GetProvider() != "ollama" {
		t.Errorf("provider = %q, want ollama", rt.GetProvider())
	}
	if rt.GetModel() != "llama3.2" {
		t.Errorf("model = %q, want llama3.2", rt.GetModel())
	}
}

// TestAPI_New verifies New returns a Runtime and error.
func TestAPI_New(t *testing.T) {
	rt, err := ares.New(
		ares.WithOllama("llama3.2"),
		ares.WithoutMemory(),
		ares.WithTrace(false),
	)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer rt.Close()
}

// TestAPI_ToolRegistry verifies the tool registry is accessible.
func TestAPI_ToolRegistry(t *testing.T) {
	rt := ares.NewRuntime(
		ares.WithOllama("llama3.2"),
		ares.WithoutMemory(),
		ares.WithTrace(false),
	)
	defer rt.Close()

	reg := rt.ToolRegistry()
	if reg == nil {
		t.Fatal("ToolRegistry() returned nil")
	}
	tools := reg.List()
	if len(tools) == 0 {
		t.Error("expected built-in tools, got 0")
	}
}

// TestAPI_Snapshot verifies runtime introspection.
func TestAPI_Snapshot(t *testing.T) {
	rt := ares.NewRuntime(
		ares.WithOllama("llama3.2"),
		ares.WithoutMemory(),
		ares.WithTrace(false),
	)
	defer rt.Close()

	snap := rt.Snapshot()
	if snap.Summary.Total == 0 {
		t.Error("expected components in snapshot, got 0")
	}
	if snap.TakenAt.IsZero() {
		t.Error("snapshot TakenAt is zero")
	}
}

// TestAPI_AgentCreation verifies agent creation with options.
func TestAPI_AgentCreation(t *testing.T) {
	rt := ares.NewRuntime(
		ares.WithOllama("llama3.2"),
		ares.WithoutMemory(),
		ares.WithTrace(false),
	)
	defer rt.Close()

	agent := rt.NewAgent("test-agent",
		ares.WithInstruction("You are a test agent."),
		ares.WithMaxTokens(1024),
		ares.WithMaxIterations(5),
		ares.WithTimeout(30*time.Second),
	)
	if agent == nil {
		t.Fatal("NewAgent returned nil")
	}
}

// TestAPI_Graph verifies graph construction.
func TestAPI_Graph(t *testing.T) {
	g := ares.NewGraph("test-graph")
	if g == nil {
		t.Fatal("NewGraph returned nil")
	}

	g.AddNode("start", func(ctx context.Context, state map[string]any) error {
		state["step"] = "started"
		return nil
	})
	g.AddNode("end", func(ctx context.Context, state map[string]any) error {
		state["step"] = "ended"
		return nil
	})
	g.AddEdge("start", "end", nil)
}

// TestAPI_ContextCleaner verifies the context cleaner factory.
func TestAPI_ContextCleaner(t *testing.T) {
	cleaner := ares.NewContextCleaner()
	if cleaner == nil {
		t.Fatal("NewContextCleaner returned nil")
	}

	msgs := []ares.Message{
		{Role: "system", Content: "You are helpful."},
		{Role: "user", Content: "Hello, how are you?"},
		{Role: "assistant", Content: "I'm doing well, thanks!"},
	}

	opts := ares.DefaultCleanOptions()
	cleaned := cleaner.Clean(msgs, opts)
	if len(cleaned) == 0 {
		t.Error("Clean returned empty slice")
	}

	stats := cleaner.Stats()
	_ = stats
}

// TestAPI_QualityGateConfig verifies knowledge quality gate config.
func TestAPI_QualityGateConfig(t *testing.T) {
	qg := ares.DefaultQualityGateConfig()
	if qg.MinFinalScore == 0 {
		t.Error("MinFinalScore should not be zero")
	}
	if qg.MaxFactsPerIngest == 0 {
		t.Error("MaxFactsPerIngest should not be zero")
	}
}

// TestAPI_LLMTypes verifies LLM type aliases work.
func TestAPI_LLMTypes(t *testing.T) {
	cfg := &ares.LLMConfig{
		Provider: ares.LLMProviderOpenAI,
		APIKey:   "sk-test",
		Model:    "gpt-4",
		Timeout:  30,
	}
	if cfg.Provider != ares.LLMProviderOpenAI {
		t.Error("LLMProvider constant mismatch")
	}

	base := &ares.BaseConfig{
		RequestTimeout: 30 * time.Second,
		MaxRetries:     3,
	}
	if base.MaxRetries != 3 {
		t.Error("BaseConfig field mismatch")
	}
}

// TestAPI_CleaningModes verifies cleaning mode constants.
func TestAPI_CleaningModes(t *testing.T) {
	modes := []ares.CleaningMode{
		ares.CleaningModeDefault,
		ares.CleaningModeConservative,
		ares.CleaningModeAggressive,
	}
	for i, m := range modes {
		if int(m) != i {
			t.Errorf("CleaningMode[%d] = %d, want %d", i, int(m), i)
		}
	}
}

// TestAPI_FriendlyErr verifies error wrapping.
func TestAPI_FriendlyErr(t *testing.T) {
	orig := fmt.Errorf("connection refused")
	err := ares.FriendlyErr("test", ares.LLMProviderOllama, orig)
	if err == nil {
		t.Fatal("FriendlyErr returned nil")
	}
}

// TestAPI_ConfigFile verifies config file type aliases.
func TestAPI_ConfigFile(t *testing.T) {
	var cfg ares.ConfigFile
	cfg.LLM.Model = "test"
	if cfg.LLM.Model != "test" {
		t.Error("ConfigFile.LLM.Model not accessible")
	}

	var mem ares.MemoryFileConfig
	mem.Enabled = true
	if !mem.Enabled {
		t.Error("MemoryFileConfig.Enabled not accessible")
	}
}

// TestAPI_MCPConn verifies MCP connection type.
func TestAPI_MCPConn(t *testing.T) {
	conn := ares.MCPConn{
		Name:    "test-server",
		Command: "/usr/bin/mcp-server",
		Args:    []string{"--port", "8080"},
	}
	if conn.Name != "test-server" {
		t.Error("MCPConn.Name not accessible")
	}
}

// TestAPI_Task verifies task type.
func TestAPI_Task(t *testing.T) {
	task := ares.Task{
		Capability: "researcher",
		Input:      "Find information about Go",
		Timeout:    30 * time.Second,
	}
	if task.Capability != "researcher" {
		t.Error("Task.Capability not accessible")
	}
}

// TestAPI_ErrorVars verifies error variables are accessible.
func TestAPI_ErrorVars(t *testing.T) {
	if ares.ErrHumanInputUnsupported == nil {
		t.Error("ErrHumanInputUnsupported is nil")
	}
	if ares.ErrDistillDepsMissing == nil {
		t.Error("ErrDistillDepsMissing is nil")
	}
}

// TestAPI_DistillationConfig verifies distillation config types.
func TestAPI_DistillationConfig(t *testing.T) {
	cfg := ares.DefaultDistillationConfig()
	if cfg == nil {
		t.Fatal("DefaultDistillationConfig returned nil")
	}
	if cfg.MinImportance == 0 {
		t.Error("MinImportance should not be zero")
	}
	if cfg.MaxMemoriesPerDistillation == 0 {
		t.Error("MaxMemoriesPerDistillation should not be zero")
	}
}

// TestAPI_Experience verifies experience type.
func TestAPI_Experience(t *testing.T) {
	exp := ares.Experience{
		TenantID: "tenant-1",
		Type:     ares.ExperienceTypeFailure,
		Problem:  "connection timeout",
		Solution: "retry with backoff",
		Score:    0.8,
		Source:   "agent-1",
	}
	if exp.Type != "failure" {
		t.Errorf("Experience.Type = %q, want failure", exp.Type)
	}
	if exp.TenantID != "tenant-1" {
		t.Error("Experience.TenantID not accessible")
	}
}

// TestAPI_DistillationOption verifies WithDistillation option.
func TestAPI_DistillationOption(t *testing.T) {
	rt := ares.NewRuntime(
		ares.WithOllama("llama3.2"),
		ares.WithDefaultMemory(),
		ares.WithDistillation(5),
		ares.WithTrace(false),
	)
	defer rt.Close()
	if rt == nil {
		t.Fatal("NewRuntime with WithDistillation returned nil")
	}
}

// TestAPI_LLMMessageTypes verifies LLM message type aliases.
func TestAPI_LLMMessageTypes(t *testing.T) {
	msg := &ares.LLMMessage{
		Role:    "user",
		Content: "hello",
	}
	if msg.Role != "user" {
		t.Error("LLMMessage.Role not accessible")
	}

	req := ares.GenerateRequest{
		Messages: []*ares.LLMMessage{msg},
	}
	if len(req.Messages) != 1 {
		t.Error("GenerateRequest.Messages not accessible")
	}

	resp := ares.GenerateResponse{
		Content:      "hi",
		FinishReason: "stop",
	}
	if resp.Content != "hi" {
		t.Error("GenerateResponse.Content not accessible")
	}
}

// TestAPI_KnowledgeObjectTypes verifies knowledge object type aliases.
func TestAPI_KnowledgeObjectTypes(t *testing.T) {
	obj := ares.KnowledgeObject{
		ID:        "obj-1",
		Type:      "fact",
		Namespace: "default",
		Summary:   "test summary",
	}
	if obj.ID != "obj-1" {
		t.Error("KnowledgeObject.ID not accessible")
	}

	rep := ares.Representation{
		ID:    "rep-1",
		Model: "text-embedding-3-small",
	}
	if rep.Model != "text-embedding-3-small" {
		t.Error("Representation.Model not accessible")
	}

	rel := ares.Relation{
		Predicate: "depends_on",
		ObjectID:  "obj-2",
	}
	if rel.Predicate != "depends_on" {
		t.Error("Relation.Predicate not accessible")
	}
}

// TestAPI_EmbeddingTypes verifies embedding type aliases.
func TestAPI_EmbeddingTypes(t *testing.T) {
	req := ares.EmbeddingRequest{
		Input: "test text",
		Model: "text-embedding-3-small",
	}
	if req.Input != "test text" {
		t.Error("EmbeddingRequest.Input not accessible")
	}

	resp := ares.EmbeddingResponse{
		Embedding: []float32{0.1, 0.2, 0.3},
		Model:     "text-embedding-3-small",
	}
	if len(resp.Embedding) != 3 {
		t.Error("EmbeddingResponse.Embedding not accessible")
	}
}

// TestAPI_ToolRegistryHelpers verifies tool registry factory functions.
func TestAPI_ToolRegistryHelpers(t *testing.T) {
	reg := ares.NewRegistry()
	if reg == nil {
		t.Fatal("NewRegistry returned nil")
	}
	if len(reg.List()) == 0 {
		t.Error("NewRegistry should have built-in tools")
	}

	empty := ares.NewEmptyRegistry()
	if empty == nil {
		t.Fatal("NewEmptyRegistry returned nil")
	}
	if len(empty.List()) != 0 {
		t.Error("NewEmptyRegistry should be empty")
	}
}

// TestAPI_RegisterBuiltinTools verifies built-in tool registration.
func TestAPI_RegisterBuiltinTools(t *testing.T) {
	reg := ares.NewEmptyRegistry()
	err := ares.RegisterBuiltinTools(reg, ares.WithFileSandboxDir("/tmp"))
	if err != nil {
		t.Fatalf("RegisterBuiltinTools error: %v", err)
	}
	if len(reg.List()) == 0 {
		t.Error("RegisterBuiltinTools should register tools")
	}
}

// TestAPI_MCPClientTypes verifies MCP client type aliases.
func TestAPI_MCPClientTypes(t *testing.T) {
	cfg := ares.MCPServerConfig{
		Name:    "test-server",
		Command: "/usr/bin/test",
		Args:    []string{"--port", "8080"},
	}
	if cfg.Name != "test-server" {
		t.Error("MCPServerConfig.Name not accessible")
	}
}

// TestAPI_LLMServiceTypes verifies LLM service type aliases.
func TestAPI_LLMServiceTypes(t *testing.T) {
	cfg := &ares.LLMServiceConfig{
		LLMConfig: &ares.LLMConfig{
			Provider: ares.LLMProviderOllama,
			Model:    "llama3.2",
		},
	}
	if cfg.LLMConfig.Provider != ares.LLMProviderOllama {
		t.Error("LLMServiceConfig.LLMConfig.Provider not accessible")
	}
}

// TestAPI_EvolutionTypes verifies evolution type aliases.
func TestAPI_EvolutionTypes(t *testing.T) {
	cfg := ares.DefaultDreamCycleConfig()
	if cfg.MinTasksBeforeEvolve == 0 {
		t.Error("MinTasksBeforeEvolve should not be zero")
	}
	if cfg.MaxMutations == 0 {
		t.Error("MaxMutations should not be zero")
	}

	// Evolution mode constants
	if ares.ModeEvolutionStrategy != 0 {
		t.Error("ModeEvolutionStrategy should be 0")
	}
	if ares.ModeGeneticAlgorithm != 1 {
		t.Error("ModeGeneticAlgorithm should be 1")
	}
}
