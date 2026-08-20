package agentsyscall

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/Timwood0x10/ares/internal/agentfabric"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/taskfabric"
)

// stubExecutor is a minimal Executor for testing.
type stubExecutor struct {
	id  string
	typ models.AgentType
}

func (e *stubExecutor) ID() string             { return e.id }
func (e *stubExecutor) Type() models.AgentType { return e.typ }
func (e *stubExecutor) ExecuteStep(_ context.Context, _ *models.Task) (*StepOutcome, error) {
	return &StepOutcome{Done: true, Result: models.NewTaskResult("t", e.typ)}, nil
}

// stubBinder records bound tools for assertion.
type stubBinder struct {
	mu    sync.Mutex
	tools map[string]func(ctx context.Context, args map[string]any) (any, error)
}

func (b *stubBinder) BindTool(name string, fn func(ctx context.Context, args map[string]any) (any, error)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.tools == nil {
		b.tools = make(map[string]func(ctx context.Context, args map[string]any) (any, error))
	}
	b.tools[name] = fn
}

func (b *stubBinder) call(ctx context.Context, name string, args map[string]any) (any, error) {
	b.mu.Lock()
	fn, ok := b.tools[name]
	b.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("tool %q not bound", name)
	}
	return fn(ctx, args)
}

// TestSpawnAgentCreatesAgentInFabric verifies the spawn_agent syscall creates
// a real agent in the agent fabric with the declared capability and a
// provenance link to the parent.
func TestSpawnAgentCreatesAgentInFabric(t *testing.T) {
	agents := agentfabric.NewFabric()
	kernel := NewKernel(agents, nil, nil, nil)

	result, err := kernel.SpawnAgent(context.Background(), SpawnAgentArgs{
		Capability: "coder",
		ParentID:   "agent-A",
	})
	if err != nil {
		t.Fatalf("SpawnAgent: %v", err)
	}
	if result.AgentID == "" {
		t.Fatal("agent ID must not be empty")
	}
	if result.Capability != "coder" {
		t.Fatalf("capability = %q, want coder", result.Capability)
	}
	if result.Registered {
		t.Fatal("must not be registered without factory")
	}

	agent, err := agents.Get(result.AgentID)
	if err != nil {
		t.Fatalf("Get spawned agent: %v", err)
	}
	if agent.Parent != "agent-A" {
		t.Fatalf("parent = %q, want agent-A", agent.Parent)
	}
	// Provenance link exists.
	kids := agents.Children("agent-A")
	if len(kids) != 1 || kids[0] != result.AgentID {
		t.Fatalf("children = %v, want [%s]", kids, result.AgentID)
	}
}

// TestSpawnAgentRegistersExecutor verifies that when a factory and register
// function are wired, the spawned agent is registered as a scheduler executor.
func TestSpawnAgentRegistersExecutor(t *testing.T) {
	agents := agentfabric.NewFabric()
	var registeredID string
	var registeredExec Executor
	factory := func(agentID, capability string) Executor {
		return &stubExecutor{id: agentID, typ: models.AgentType(capability)}
	}
	register := func(agentID string, executor Executor) {
		registeredID = agentID
		registeredExec = executor
	}
	kernel := NewKernel(agents, nil, factory, register)

	result, err := kernel.SpawnAgent(context.Background(), SpawnAgentArgs{
		Capability: "reviewer",
	})
	if err != nil {
		t.Fatalf("SpawnAgent: %v", err)
	}
	if !result.Registered {
		t.Fatal("must be registered with factory + register")
	}
	if registeredID != result.AgentID {
		t.Fatalf("registered ID = %q, want %q", registeredID, result.AgentID)
	}
	if registeredExec == nil {
		t.Fatal("executor must not be nil")
	}
	if registeredExec.ID() != result.AgentID {
		t.Fatalf("executor ID = %q, want %q", registeredExec.ID(), result.AgentID)
	}
}

// TestSpawnAgentRejectsEmptyCapability verifies the Kernel enforces
// non-empty capability — a spawn without a declared capability is rejected.
func TestSpawnAgentRejectsEmptyCapability(t *testing.T) {
	kernel := NewKernel(agentfabric.NewFabric(), nil, nil, nil)
	_, err := kernel.SpawnAgent(context.Background(), SpawnAgentArgs{})
	if err == nil {
		t.Fatal("must reject empty capability")
	}
}

// TestCreateTaskCreatesTaskInFabric verifies the create_task syscall creates
// a real Task Fabric task in READY state.
func TestCreateTaskCreatesTaskInFabric(t *testing.T) {
	fabric := taskfabric.NewFabric()
	kernel := NewKernel(nil, fabric, nil, nil)

	result, err := kernel.CreateTask(context.Background(), CreateTaskArgs{
		Capability: "coder",
		Payload:    map[string]any{"task_desc": "write tests"},
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if result.TaskID == "" {
		t.Fatal("task ID must not be empty")
	}
	if result.State != string(taskfabric.StateReady) {
		t.Fatalf("state = %q, want READY", result.State)
	}

	task, err := fabric.Task(result.TaskID)
	if err != nil {
		t.Fatalf("Get task: %v", err)
	}
	if task.Capability != "coder" {
		t.Fatalf("capability = %q, want coder", task.Capability)
	}
	if task.State != taskfabric.StateReady {
		t.Fatalf("state = %s, want READY", task.State)
	}
}

// TestCreateTaskRejectsEmptyCapability verifies the Kernel rejects a task
// without a declared capability.
func TestCreateTaskRejectsEmptyCapability(t *testing.T) {
	kernel := NewKernel(nil, taskfabric.NewFabric(), nil, nil)
	_, err := kernel.CreateTask(context.Background(), CreateTaskArgs{})
	if err == nil {
		t.Fatal("must reject empty capability")
	}
}

// TestBindToolsRegistersBothTools verifies BindTools registers spawn_agent
// and create_task on the binder, and the bound functions call the Kernel.
func TestBindToolsRegistersBothTools(t *testing.T) {
	agents := agentfabric.NewFabric()
	fabric := taskfabric.NewFabric()
	kernel := NewKernel(agents, fabric, nil, nil)
	binder := &stubBinder{}

	BindTools(binder, kernel)

	ctx := context.Background()

	// spawn_agent
	spawnResult, err := binder.call(ctx, SpawnAgentTool, map[string]any{
		"capability": "coder",
		"parent_id":  "root",
	})
	if err != nil {
		t.Fatalf("call spawn_agent: %v", err)
	}
	sr, ok := spawnResult.(*SpawnAgentResult)
	if !ok {
		t.Fatalf("spawn result type = %T, want *SpawnAgentResult", spawnResult)
	}
	if sr.Capability != "coder" {
		t.Fatalf("capability = %q, want coder", sr.Capability)
	}

	// create_task
	taskResult, err := binder.call(ctx, CreateTaskTool, map[string]any{
		"capability": "coder",
		"payload":    map[string]any{"task_desc": "review code"},
	})
	if err != nil {
		t.Fatalf("call create_task: %v", err)
	}
	tr, ok := taskResult.(*CreateTaskResult)
	if !ok {
		t.Fatalf("task result type = %T, want *CreateTaskResult", taskResult)
	}
	if tr.TaskID == "" {
		t.Fatal("task ID must not be empty")
	}
}

// TestSpawnedAgentIDsAreUnique verifies multiple spawns produce unique IDs.
func TestSpawnedAgentIDsAreUnique(t *testing.T) {
	kernel := NewKernel(agentfabric.NewFabric(), nil, nil, nil)
	seen := make(map[string]bool)
	for i := 0; i < 20; i++ {
		result, err := kernel.SpawnAgent(context.Background(), SpawnAgentArgs{
			Capability: "coder",
		})
		if err != nil {
			t.Fatalf("spawn %d: %v", i, err)
		}
		if seen[result.AgentID] {
			t.Fatalf("duplicate agent ID: %s", result.AgentID)
		}
		seen[result.AgentID] = true
	}
}

// TestToolSchemasReturnsBoth verifies ToolSchemas returns both tool schemas.
func TestToolSchemasReturnsBoth(t *testing.T) {
	schemas := ToolSchemas()
	if len(schemas) != 2 {
		t.Fatalf("expected 2 schemas, got %d", len(schemas))
	}
	names := make(map[string]bool)
	for _, s := range schemas {
		names[s.Name] = true
		if s.Description == "" {
			t.Fatalf("schema %q has empty description", s.Name)
		}
		if s.Parameters == nil {
			t.Fatalf("schema %q has nil parameters", s.Name)
		}
	}
	if !names[SpawnAgentTool] {
		t.Fatalf("missing %s schema", SpawnAgentTool)
	}
	if !names[CreateTaskTool] {
		t.Fatalf("missing %s schema", CreateTaskTool)
	}
}
