// nolint: errcheck // Test code may ignore return values
package sub

import (
	"context"
	"sync"
	"testing"

	"goagent/internal/core/models"
	"goagent/internal/events"
	"goagent/internal/llm/output"
	"goagent/internal/protocol/ahp"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskExecutor_Execute_NilTask_ReturnsError(t *testing.T) {
	executor := NewTaskExecutor(
		nil,                        // toolBinder
		nil,                        // llmAdapter
		output.NewTemplateEngine(), // template
		"{{.category}}",            // promptTpl
		output.NewValidator(),      // validator
		3,                          // maxRetries
	)

	result, err := executor.Execute(context.Background(), nil)
	require.NoError(t, err)
	assert.False(t, result.Success, "Execute() should fail for nil task")
}

func TestTaskExecutor_Execute_NilLLMAdapter_ReturnsFallbackError(t *testing.T) {
	// When llmAdapter is nil, executeByType is called as fallback.
	// executeByType always returns an error since there are no type-specific handlers.
	executor := NewTaskExecutor(
		nil,
		nil,
		output.NewTemplateEngine(),
		"{{.category}}",
		output.NewValidator(),
		3,
	)

	task := models.NewTask("task_1", models.AgentTypeTop, &models.UserProfile{})

	result, err := executor.Execute(context.Background(), task)
	require.NoError(t, err)
	assert.False(t, result.Success, "Execute() should fail when no fallback handler exists")
	assert.Contains(t, result.Error, "no fallback handler")
}

func TestTaskExecutor_Execute_NilProfile_ReturnsFallbackError(t *testing.T) {
	// When task has no UserProfile and no LLM adapter, fallback is used.
	executor := NewTaskExecutor(
		nil,
		nil,
		output.NewTemplateEngine(),
		"{{.category}}",
		output.NewValidator(),
		3,
	)

	task := models.NewTask("task_1", models.AgentTypeTop, nil)

	result, err := executor.Execute(context.Background(), task)
	require.NoError(t, err)
	assert.False(t, result.Success)
	assert.Contains(t, result.Error, "no fallback handler")
}

func TestExecuteByType_UnknownType_ReturnsError(t *testing.T) {
	executor := NewTaskExecutor(
		nil,
		nil,
		output.NewTemplateEngine(),
		"{{.category}}",
		output.NewValidator(),
		3,
	)

	// Use an AgentType that has no handler
	task := models.NewTask("task_test", models.AgentType("unknown_agent_type"), nil)

	result, err := executor.Execute(context.Background(), task)
	require.NoError(t, err)
	assert.False(t, result.Success, "Execute() should fail for unknown agent type")
	assert.Contains(t, result.Error, "no fallback handler",
		"error message should contain 'no fallback handler'")
}

func TestMessageHandler_Handle(t *testing.T) {
	handler := NewMessageHandler("test_agent")

	// Test nil message
	err := handler.Handle(context.Background(), nil)
	if err == nil {
		t.Error("Handle() should return error for nil message")
	}

	// Test valid message
	msg := ahp.NewHeartbeatMessage("test")
	err = handler.Handle(context.Background(), msg)
	if err != nil {
		t.Errorf("Handle() error = %v", err)
	}
}

func TestToolBinder_BindAndCall(t *testing.T) {
	binder := NewToolBinder()

	// Bind a tool
	binder.BindTool("test_tool", func(ctx context.Context, args map[string]any) (any, error) {
		return "test_result", nil
	})

	// Call the tool
	result, err := binder.CallTool(context.Background(), "test_tool", nil)
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}

	if result != "test_result" {
		t.Errorf("CallTool() got %v, want 'test_result'", result)
	}
}

func TestToolBinder_CallNonExistentTool(t *testing.T) {
	binder := NewToolBinder()

	_, err := binder.CallTool(context.Background(), "non_existent", nil)
	if err == nil {
		t.Error("CallTool() should return error for non-existent tool")
	}
}

func TestHeartbeatSender_StartStop(t *testing.T) {
	sender := NewHeartbeatSender("test_agent", 100, nil)

	ctx, cancel := context.WithCancel(context.Background())

	go sender.Start(ctx)

	// Let it run briefly
	cancel()

	sender.Stop()
}

func TestSubAgent_New(t *testing.T) {
	executor := NewTaskExecutor(
		nil,
		nil,
		output.NewTemplateEngine(),
		"{{.category}}",
		output.NewValidator(),
		3,
	)
	handler := NewMessageHandler("sub1")

	agent := New("sub1", models.AgentTypeTop, executor, handler, nil, nil, nil)

	if agent.ID() != "sub1" {
		t.Errorf("expected sub1, got %s", agent.ID())
	}
	if agent.Type() != models.AgentTypeTop {
		t.Errorf("expected AgentTypeTop")
	}
}

func TestSubAgent_DefaultConfig(t *testing.T) {
	cfg := DefaultSubAgentConfig(models.AgentTypeTop)

	if cfg.Type != models.AgentTypeTop {
		t.Errorf("expected AgentTypeTop")
	}
}

func TestSubAgent_StartStop(t *testing.T) {
	executor := NewTaskExecutor(
		nil,
		nil,
		output.NewTemplateEngine(),
		"{{.category}}",
		output.NewValidator(),
		3,
	)
	handler := NewMessageHandler("sub1")

	agent := New("sub1", models.AgentTypeTop, executor, handler, nil, nil, nil)

	// Start
	err := agent.Start(context.Background())
	if err != nil {
		t.Errorf("Start() error = %v", err)
	}

	if agent.Status() != models.AgentStatusReady {
		t.Errorf("expected status Ready after Start")
	}

	// Start again should fail
	err = agent.Start(context.Background())
	if err == nil {
		t.Error("Start() should return error when already started")
	}

	// Stop
	err = agent.Stop(context.Background())
	if err != nil {
		t.Errorf("Stop() error = %v", err)
	}

	if agent.Status() != models.AgentStatusOffline {
		t.Errorf("expected status Offline after Stop")
	}

	// Stop again should fail
	err = agent.Stop(context.Background())
	if err == nil {
		t.Error("Stop() should return error when not running")
	}
}

func TestSubAgent_Process(t *testing.T) {
	executor := NewTaskExecutor(
		nil,
		nil,
		output.NewTemplateEngine(),
		"{{.category}}",
		output.NewValidator(),
		3,
	)
	handler := NewMessageHandler("sub1")

	agent := New("sub1", models.AgentTypeTop, executor, handler, nil, nil, nil)

	// Process without starting should auto-start
	task := models.NewTask("task_1", models.AgentTypeTop, &models.UserProfile{})
	result, err := agent.Process(context.Background(), task)
	if err != nil {
		t.Errorf("Process() error = %v", err)
	}
	_ = result
}

func TestSubAgent_SendReceiveMessage(t *testing.T) {
	executor := NewTaskExecutor(
		nil,
		nil,
		output.NewTemplateEngine(),
		"{{.category}}",
		output.NewValidator(),
		3,
	)
	handler := NewMessageHandler("sub1")
	queue := ahp.NewMessageQueue("sub1", &ahp.QueueOptions{MaxSize: 10})

	sub := &subAgent{
		id:           "sub1",
		agentType:    models.AgentTypeTop,
		status:       models.AgentStatusReady,
		executor:     executor,
		handler:      handler,
		tools:        make(map[string]func(ctx context.Context, args map[string]any) (any, error)),
		messageQueue: queue,
	}

	// Test SendMessage
	msg := ahp.NewMessage(ahp.AHPMethodResult, "sub1", "leader", "task1", "session1")
	err := sub.SendMessage(context.Background(), msg)
	if err != nil {
		t.Errorf("SendMessage() error = %v", err)
	}

	// Test ReceiveMessage
	_, err = sub.ReceiveMessage(context.Background())
	if err != nil {
		t.Errorf("ReceiveMessage() error = %v", err)
	}
}

func TestSubAgent_Heartbeat(t *testing.T) {
	executor := NewTaskExecutor(
		nil,
		nil,
		output.NewTemplateEngine(),
		"{{.category}}",
		output.NewValidator(),
		3,
	)
	handler := NewMessageHandler("sub1")
	hbMon := ahp.NewHeartbeatMonitor(ahp.DefaultHeartbeatConfig())

	sub := &subAgent{
		id:           "sub1",
		agentType:    models.AgentTypeTop,
		status:       models.AgentStatusReady,
		executor:     executor,
		handler:      handler,
		tools:        make(map[string]func(ctx context.Context, args map[string]any) (any, error)),
		heartbeatMon: hbMon,
	}

	err := sub.Heartbeat(context.Background())
	if err != nil {
		t.Errorf("Heartbeat() error = %v", err)
	}

	if !sub.IsAlive() {
		t.Error("IsAlive() should return true after heartbeat")
	}
}

func TestSubAgent_Execute(t *testing.T) {
	executor := NewTaskExecutor(
		nil,
		nil,
		output.NewTemplateEngine(),
		"{{.category}}",
		output.NewValidator(),
		3,
	)
	handler := NewMessageHandler("sub1")

	agent := New("sub1", models.AgentTypeTop, executor, handler, nil, nil, nil)

	task := models.NewTask("task_1", models.AgentTypeTop, &models.UserProfile{})
	result, err := agent.Execute(context.Background(), task)
	if err != nil {
		t.Errorf("Execute() error = %v", err)
	}
	if result == nil {
		t.Error("Execute() should return result")
	}
}

func TestToolBinder_ListTools(t *testing.T) {
	binder := NewToolBinder()

	binder.BindTool("tool1", func(ctx context.Context, args map[string]any) (any, error) {
		return nil, nil
	})
	binder.BindTool("tool2", func(ctx context.Context, args map[string]any) (any, error) {
		return nil, nil
	})

	// ListTools is not implemented, so just test that tools can be bound and called
	result, err := binder.CallTool(context.Background(), "tool1", nil)
	if err != nil {
		t.Errorf("CallTool() error = %v", err)
	}
	if result != nil {
		t.Errorf("CallTool() got %v, want nil", result)
	}
}

func TestMessageHandler_HandleTaskMessage(t *testing.T) {
	handler := NewMessageHandler("test_agent")

	// Create a task message
	msg := ahp.NewTaskMessage("leader", "test_agent", "task1", "session1", map[string]any{"key": "value"})

	// Handle the task message - will fail since executor is nil
	err := handler.Handle(context.Background(), msg)
	// Error expected since there's no executor
	_ = err
}

func TestMessageHandler_HandleAckMessage(t *testing.T) {
	handler := NewMessageHandler("test_agent")

	// Create an ACK message
	msg := ahp.NewACKMessage("test_agent", "leader", "task1", "session1")

	// Handle the ACK message
	err := handler.Handle(context.Background(), msg)
	if err != nil {
		t.Errorf("Handle() error = %v", err)
	}
}

// --- StatefulAgent implementation tests ---

func TestSubAgent_ImplementsStatefulAgent(t *testing.T) {
	// Compile-time check is enforced by the package-level var declaration.
	// This test verifies the interface at runtime as well.
	executor := NewTaskExecutor(nil, nil, output.NewTemplateEngine(),
		"{{.category}}", output.NewValidator(), 3)
	handler := NewMessageHandler("sub1")

	agent := New("sub1", models.AgentTypeTop, executor, handler, nil, nil, nil)

	_, ok := agent.(interface {
		RestoreState(map[string]any) error
		ReplayEvents([]*events.Event) error
		Snapshot() (map[string]any, error)
	})
	assert.True(t, ok, "subAgent should implement StatefulAgent methods")
}

func TestSubAgent_RestoreState_NilState(t *testing.T) {
	executor := NewTaskExecutor(nil, nil, output.NewTemplateEngine(),
		"{{.category}}", output.NewValidator(), 3)
	handler := NewMessageHandler("sub1")

	agent := New("sub1", models.AgentTypeTop, executor, handler, nil, nil, nil)
	a := agent.(*subAgent)

	err := a.RestoreState(nil)
	assert.NoError(t, err, "RestoreState with nil should not error")
	assert.Equal(t, models.AgentStatusOffline, a.Status())
}

func TestSubAgent_RestoreState_ValidStatus(t *testing.T) {
	executor := NewTaskExecutor(nil, nil, output.NewTemplateEngine(),
		"{{.category}}", output.NewValidator(), 3)
	handler := NewMessageHandler("sub1")

	agent := New("sub1", models.AgentTypeTop, executor, handler, nil, nil, nil)
	a := agent.(*subAgent)

	err := a.RestoreState(map[string]any{
		"status": string(models.AgentStatusReady),
	})
	assert.NoError(t, err)
	assert.Equal(t, models.AgentStatusReady, a.Status())
}

func TestSubAgent_RestoreState_EmptyStatusIgnored(t *testing.T) {
	executor := NewTaskExecutor(nil, nil, output.NewTemplateEngine(),
		"{{.category}}", output.NewValidator(), 3)
	handler := NewMessageHandler("sub1")

	agent := New("sub1", models.AgentTypeTop, executor, handler, nil, nil, nil)
	a := agent.(*subAgent)

	err := a.RestoreState(map[string]any{
		"status": "",
	})
	assert.NoError(t, err)
	assert.Equal(t, models.AgentStatusOffline, a.Status(),
		"empty status should not overwrite current status")
}

func TestSubAgent_RestoreState_IgnoresNonStringStatus(t *testing.T) {
	executor := NewTaskExecutor(nil, nil, output.NewTemplateEngine(),
		"{{.category}}", output.NewValidator(), 3)
	handler := NewMessageHandler("sub1")

	agent := New("sub1", models.AgentTypeTop, executor, handler, nil, nil, nil)
	a := agent.(*subAgent)

	err := a.RestoreState(map[string]any{
		"status": 12345, // not a string
	})
	assert.NoError(t, err)
	assert.Equal(t, models.AgentStatusOffline, a.Status(),
		"non-string status should be ignored")
}

func TestSubAgent_RestoreState_IgnoresExtraKeys(t *testing.T) {
	executor := NewTaskExecutor(nil, nil, output.NewTemplateEngine(),
		"{{.category}}", output.NewValidator(), 3)
	handler := NewMessageHandler("sub1")

	agent := New("sub1", models.AgentTypeTop, executor, handler, nil, nil, nil)
	a := agent.(*subAgent)

	err := a.RestoreState(map[string]any{
		"status":      string(models.AgentStatusBusy),
		"unknown_key": "value",
	})
	assert.NoError(t, err)
	assert.Equal(t, models.AgentStatusBusy, a.Status())
}

func TestSubAgent_RestoreState_EmptyMap(t *testing.T) {
	executor := NewTaskExecutor(nil, nil, output.NewTemplateEngine(),
		"{{.category}}", output.NewValidator(), 3)
	handler := NewMessageHandler("sub1")

	agent := New("sub1", models.AgentTypeTop, executor, handler, nil, nil, nil)
	a := agent.(*subAgent)

	err := a.RestoreState(map[string]any{})
	assert.NoError(t, err)
	assert.Equal(t, models.AgentStatusOffline, a.Status())
}

func TestSubAgent_ReplayEvents_Empty(t *testing.T) {
	executor := NewTaskExecutor(nil, nil, output.NewTemplateEngine(),
		"{{.category}}", output.NewValidator(), 3)
	handler := NewMessageHandler("sub1")

	agent := New("sub1", models.AgentTypeTop, executor, handler, nil, nil, nil)
	a := agent.(*subAgent)

	err := a.ReplayEvents(nil)
	assert.NoError(t, err, "ReplayEvents with nil should not error")

	err = a.ReplayEvents([]*events.Event{})
	assert.NoError(t, err, "ReplayEvents with empty slice should not error")
}

func TestSubAgent_ReplayEvents_NilEventSkipped(t *testing.T) {
	executor := NewTaskExecutor(nil, nil, output.NewTemplateEngine(),
		"{{.category}}", output.NewValidator(), 3)
	handler := NewMessageHandler("sub1")

	agent := New("sub1", models.AgentTypeTop, executor, handler, nil, nil, nil)
	a := agent.(*subAgent)

	err := a.ReplayEvents([]*events.Event{nil, nil})
	assert.NoError(t, err, "nil events should be skipped without panic")
}

func TestSubAgent_ReplayEvents_TaskCompleted(t *testing.T) {
	executor := NewTaskExecutor(nil, nil, output.NewTemplateEngine(),
		"{{.category}}", output.NewValidator(), 3)
	handler := NewMessageHandler("sub1")

	agent := New("sub1", models.AgentTypeTop, executor, handler, nil, nil, nil)
	a := agent.(*subAgent)

	evts := []*events.Event{
		{
			Type: events.EventTaskCompleted,
			Payload: map[string]any{
				"task_id": "task-1",
			},
		},
		{
			Type: events.EventTaskCompleted,
			Payload: map[string]any{
				"task_id": "task-2",
			},
		},
	}

	err := a.ReplayEvents(evts)
	assert.NoError(t, err, "ReplayEvents should succeed for task completion events")
}

func TestSubAgent_ReplayEvents_UnknownEventTypeIgnored(t *testing.T) {
	executor := NewTaskExecutor(nil, nil, output.NewTemplateEngine(),
		"{{.category}}", output.NewValidator(), 3)
	handler := NewMessageHandler("sub1")

	agent := New("sub1", models.AgentTypeTop, executor, handler, nil, nil, nil)
	a := agent.(*subAgent)

	evts := []*events.Event{
		{
			Type:    events.EventAgentStarted,
			Payload: map[string]any{},
		},
		{
			Type: events.EventTaskCreated,
			Payload: map[string]any{
				"task_id": "task-1",
			},
		},
	}

	err := a.ReplayEvents(evts)
	assert.NoError(t, err, "unknown event types should be silently ignored")
}

func TestSubAgent_Snapshot_OfflineStatus(t *testing.T) {
	executor := NewTaskExecutor(nil, nil, output.NewTemplateEngine(),
		"{{.category}}", output.NewValidator(), 3)
	handler := NewMessageHandler("sub1")

	agent := New("sub1", models.AgentTypeTop, executor, handler, nil, nil, nil)
	a := agent.(*subAgent)

	snap, err := a.Snapshot()
	require.NoError(t, err)
	assert.Equal(t, "sub1", snap["agent_id"])
	assert.Equal(t, string(models.AgentStatusOffline), snap["status"])
}

func TestSubAgent_Snapshot_ReadyStatus(t *testing.T) {
	executor := NewTaskExecutor(nil, nil, output.NewTemplateEngine(),
		"{{.category}}", output.NewValidator(), 3)
	handler := NewMessageHandler("sub1")

	agent := New("sub1", models.AgentTypeTop, executor, handler, nil, nil, nil)
	a := agent.(*subAgent)

	_ = a.Start(context.Background())

	snap, err := a.Snapshot()
	require.NoError(t, err)
	assert.Equal(t, "sub1", snap["agent_id"])
	assert.Equal(t, string(models.AgentStatusReady), snap["status"])
}

func TestSubAgent_Snapshot_ReturnsCopy(t *testing.T) {
	executor := NewTaskExecutor(nil, nil, output.NewTemplateEngine(),
		"{{.category}}", output.NewValidator(), 3)
	handler := NewMessageHandler("sub1")

	agent := New("sub1", models.AgentTypeTop, executor, handler, nil, nil, nil)
	a := agent.(*subAgent)

	snap1, _ := a.Snapshot()
	snap2, _ := a.Snapshot()

	// Mutate snap1 and verify snap2 is unaffected.
	snap1["status"] = "mutated"
	assert.NotEqual(t, snap1["status"], snap2["status"],
		"Snapshot should return independent copies")
}

func TestSubAgent_WithEventStore(t *testing.T) {
	store := events.NewMemoryEventStore()
	executor := NewTaskExecutor(nil, nil, output.NewTemplateEngine(),
		"{{.category}}", output.NewValidator(), 3)
	handler := NewMessageHandler("sub1")

	agent := New("sub1", models.AgentTypeTop, executor, handler, nil, nil, nil,
		WithEventStore(store))
	a := agent.(*subAgent)

	assert.NotNil(t, a.eventStore, "WithEventStore should set eventStore")
}

func TestSubAgent_EmitEvent_WithStore(t *testing.T) {
	store := events.NewMemoryEventStore()
	executor := NewTaskExecutor(nil, nil, output.NewTemplateEngine(),
		"{{.category}}", output.NewValidator(), 3)
	handler := NewMessageHandler("sub1")

	agent := New("sub1", models.AgentTypeTop, executor, handler, nil, nil, nil,
		WithEventStore(store))
	a := agent.(*subAgent)

	a.emitEvent(context.Background(), events.EventTaskCompleted, map[string]any{
		"task_id": "task-1",
	})

	// Verify the event was stored.
	evts, err := store.Read(context.Background(), "sub1", events.ReadOptions{})
	require.NoError(t, err)
	require.Len(t, evts, 1)
	assert.Equal(t, events.EventTaskCompleted, evts[0].Type)
	assert.Equal(t, "task-1", evts[0].Payload["task_id"])
	assert.Equal(t, "sub1", evts[0].StreamID)
}

func TestSubAgent_EmitEvent_NilStore(t *testing.T) {
	executor := NewTaskExecutor(nil, nil, output.NewTemplateEngine(),
		"{{.category}}", output.NewValidator(), 3)
	handler := NewMessageHandler("sub1")

	agent := New("sub1", models.AgentTypeTop, executor, handler, nil, nil, nil)
	a := agent.(*subAgent)

	// Should not panic when eventStore is nil.
	a.emitEvent(context.Background(), events.EventTaskCompleted, map[string]any{
		"task_id": "task-1",
	})
}

func TestSubAgent_EmitEvent_NilPayload(t *testing.T) {
	store := events.NewMemoryEventStore()
	executor := NewTaskExecutor(nil, nil, output.NewTemplateEngine(),
		"{{.category}}", output.NewValidator(), 3)
	handler := NewMessageHandler("sub1")

	agent := New("sub1", models.AgentTypeTop, executor, handler, nil, nil, nil,
		WithEventStore(store))
	a := agent.(*subAgent)

	// Should handle nil payload without panic.
	a.emitEvent(context.Background(), events.EventAgentStarted, nil)

	evts, err := store.Read(context.Background(), "sub1", events.ReadOptions{})
	require.NoError(t, err)
	require.Len(t, evts, 1)
	assert.Equal(t, events.EventAgentStarted, evts[0].Type)
	assert.Nil(t, evts[0].Payload)
}

func TestSubAgent_RestoreAndSnapshot_Roundtrip(t *testing.T) {
	executor := NewTaskExecutor(nil, nil, output.NewTemplateEngine(),
		"{{.category}}", output.NewValidator(), 3)
	handler := NewMessageHandler("sub1")

	agent := New("sub1", models.AgentTypeTop, executor, handler, nil, nil, nil)
	a := agent.(*subAgent)

	// Restore state.
	err := a.RestoreState(map[string]any{
		"status": string(models.AgentStatusBusy),
	})
	require.NoError(t, err)

	// Take snapshot and verify roundtrip.
	snap, err := a.Snapshot()
	require.NoError(t, err)
	assert.Equal(t, string(models.AgentStatusBusy), snap["status"])
	assert.Equal(t, "sub1", snap["agent_id"])
}

func TestSubAgent_StatefulAgent_ConcurrentAccess(t *testing.T) {
	store := events.NewMemoryEventStore()
	executor := NewTaskExecutor(nil, nil, output.NewTemplateEngine(),
		"{{.category}}", output.NewValidator(), 3)
	handler := NewMessageHandler("sub1")

	agent := New("sub1", models.AgentTypeTop, executor, handler, nil, nil, nil,
		WithEventStore(store))
	a := agent.(*subAgent)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			_ = a.RestoreState(map[string]any{
				"status": string(models.AgentStatusReady),
			})
		}()
		go func() {
			defer wg.Done()
			_, _ = a.Snapshot()
		}()
		go func() {
			defer wg.Done()
			a.emitEvent(context.Background(), events.EventTaskCompleted, map[string]any{
				"task_id": "task-concurrent",
			})
		}()
	}
	wg.Wait()
}

// nolint: errcheck // Test code may ignore return values
