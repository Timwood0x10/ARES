// nolint: errcheck // Test code may ignore return values
package leader

import (
	"context"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/base"
	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/events"
	"github.com/Timwood0x10/ares/internal/llm/output"
	"github.com/Timwood0x10/ares/internal/protocol/ahp"
)

func TestProfileParser_Parse(t *testing.T) {
	parser := NewProfileParser(
		nil,                        // llmAdapter
		output.NewTemplateEngine(), // template
		"{{.input}}",               // promptTpl
		output.NewValidator(),      // validator
		3,                          // maxRetries
	)

	tests := []struct {
		name    string
		input   string
		wantErr bool
		checkFn func(*models.UserProfile) bool
	}{
		{
			name:    "parse simple input",
			input:   "I want casual style",
			wantErr: false,
			checkFn: func(p *models.UserProfile) bool {
				// Default profile (when LLM is unavailable) has empty Style
				// but non-nil Preferences map.
				return p != nil && p.Preferences != nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parser.Parse(context.Background(), tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("Parse() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && !tt.checkFn(got) {
				t.Errorf("Parse() check failed")
			}
		})
	}
}

func TestTaskPlanner_Plan(t *testing.T) {
	planner := NewTaskPlanner(3)

	profile := &models.UserProfile{
		Preferences: map[string]any{
			"style": []models.StyleTag{models.StyleTag("casual")},
		},
		Occasions: []models.Occasion{models.Occasion("daily")},
		Budget:    models.NewPriceRange(100, 500),
	}

	tasks, err := planner.Plan(context.Background(), profile, "test input")
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	if len(tasks) == 0 {
		t.Error("Plan() returned empty tasks")
	}

	if len(tasks) > 3 {
		t.Errorf("Plan() returned too many tasks, got %d, want <= 3", len(tasks))
	}
}

func TestTaskPlanner_PlanNilProfile(t *testing.T) {
	planner := NewTaskPlanner(3)

	_, err := planner.Plan(context.Background(), nil, "test input")
	if err == nil {
		t.Error("Plan() should return error for nil profile")
	}
}

func TestResultAggregator_Aggregate(t *testing.T) {
	aggregator := NewResultAggregator(true, 10, SortByNone)

	results := []*models.TaskResult{
		{
			TaskID:    "task_1",
			AgentType: models.AgentTypeTop,
			Success:   true,
			Items: []*models.RecommendItem{
				{
					ItemID:   "item_1",
					Category: "top",
					Name:     "T-Shirt",
					Price:    199.00,
				},
			},
		},
		{
			TaskID:    "task_2",
			AgentType: models.AgentTypeBottom,
			Success:   true,
			Items: []*models.RecommendItem{
				{
					ItemID:   "item_2",
					Category: "bottom",
					Name:     "Jeans",
					Price:    299.00,
				},
			},
		},
	}

	result, err := aggregator.Aggregate(context.Background(), results, nil)
	if err != nil {
		t.Fatalf("Aggregate() error = %v", err)
	}

	if len(result.Items) != 2 {
		t.Errorf("Aggregate() got %d items, want 2", len(result.Items))
	}
}

func TestResultAggregator_AggregateEmpty(t *testing.T) {
	aggregator := NewResultAggregator(false, 10, SortByNone)

	result, err := aggregator.Aggregate(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("Aggregate() error = %v", err)
	}

	if len(result.Items) != 0 {
		t.Errorf("Aggregate() got %d items, want 0", len(result.Items))
	}
}

func TestResultAggregator_Deduplication(t *testing.T) {
	aggregator := NewResultAggregator(true, 10, SortByNone)

	results := []*models.TaskResult{
		{
			TaskID:    "task_1",
			AgentType: models.AgentTypeTop,
			Success:   true,
			Items: []*models.RecommendItem{
				{
					ItemID:   "item_1",
					Category: "top",
					Name:     "T-Shirt",
					Price:    199.00,
				},
			},
		},
		{
			TaskID:    "task_2",
			AgentType: models.AgentTypeBottom,
			Success:   true,
			Items: []*models.RecommendItem{
				{
					ItemID:   "item_1", // Duplicate
					Category: "top",
					Name:     "T-Shirt",
					Price:    199.00,
				},
			},
		},
	}

	result, err := aggregator.Aggregate(context.Background(), results, nil)
	if err != nil {
		t.Fatalf("Aggregate() error = %v", err)
	}

	if len(result.Items) != 1 {
		t.Errorf("Aggregate() got %d items after dedup, want 1", len(result.Items))
	}
}

func TestTaskDispatcher_Dispatch(t *testing.T) {
	registry := map[models.AgentType]string{
		models.AgentTypeTop:    "agent_top",
		models.AgentTypeBottom: "agent_bottom",
	}
	dispatcher := NewTaskDispatcher(registry, 2, 30, nil)

	dispatcher.RegisterExecutor(models.AgentTypeTop, func(ctx context.Context, task *models.Task) (*models.TaskResult, error) {
		result := models.NewTaskResult(task.TaskID, task.AgentType)
		result.SetSuccess([]*models.RecommendItem{{ItemID: "item1", Name: "test item"}}, "ok")
		return result, nil
	})
	dispatcher.RegisterExecutor(models.AgentTypeBottom, func(ctx context.Context, task *models.Task) (*models.TaskResult, error) {
		result := models.NewTaskResult(task.TaskID, task.AgentType)
		result.SetSuccess([]*models.RecommendItem{{ItemID: "item2", Name: "test item"}}, "ok")
		return result, nil
	})

	profile := &models.UserProfile{
		Style:     []models.StyleTag{models.StyleTag("casual")},
		Occasions: []models.Occasion{models.Occasion("daily")},
	}

	tasks := []*models.Task{
		models.NewTask("task_1", models.AgentTypeTop, profile),
		models.NewTask("task_2", models.AgentTypeBottom, profile),
	}

	results, err := dispatcher.Dispatch(context.Background(), tasks)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}

	if len(results) != 2 {
		t.Errorf("Dispatch() got %d results, want 2", len(results))
	}
}

func TestTaskDispatcher_DispatchEmpty(t *testing.T) {
	registry := map[models.AgentType]string{}
	dispatcher := NewTaskDispatcher(registry, 2, 30, nil)

	_, err := dispatcher.Dispatch(context.Background(), nil)
	if err == nil {
		t.Error("Dispatch() should return error for empty tasks")
	}
}

func TestLeaderAgent_New(t *testing.T) {
	parser := NewProfileParser(
		nil,
		output.NewTemplateEngine(),
		"{{.input}}",
		output.NewValidator(),
		3,
	)
	planner := NewTaskPlanner(3)
	registry := map[models.AgentType]string{
		models.AgentTypeTop: "agent_top",
	}
	dispatcher := NewTaskDispatcher(registry, 2, 30, nil)
	aggregator := NewResultAggregator(true, 10, SortByNone)

	agent := New("leader1", parser, planner, dispatcher, aggregator, nil, nil, nil, nil)

	if agent.ID() != "leader1" {
		t.Errorf("expected leader1, got %s", agent.ID())
	}
	if agent.Type() != models.AgentTypeLeader {
		t.Errorf("expected AgentTypeLeader")
	}
}

func TestLeaderAgent_DefaultConfig(t *testing.T) {
	cfg := DefaultLeaderAgentConfig()

	if cfg.Type != models.AgentTypeLeader {
		t.Errorf("expected AgentTypeLeader")
	}
	if cfg.MaxParallelTasks != 10 {
		t.Errorf("expected MaxParallelTasks 10")
	}
}

func TestLeaderAgent_StartStop(t *testing.T) {
	parser := NewProfileParser(
		nil,
		output.NewTemplateEngine(),
		"{{.input}}",
		output.NewValidator(),
		3,
	)
	planner := NewTaskPlanner(3)
	registry := map[models.AgentType]string{}
	dispatcher := NewTaskDispatcher(registry, 2, 30, nil)
	aggregator := NewResultAggregator(true, 10, SortByNone)

	agent := New("leader1", parser, planner, dispatcher, aggregator, nil, nil, nil, nil)

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

func TestLeaderAgent_Process(t *testing.T) {
	parser := NewProfileParser(
		nil,
		output.NewTemplateEngine(),
		"{{.input}}",
		output.NewValidator(),
		3,
	)
	planner := NewTaskPlanner(3)
	registry := map[models.AgentType]string{
		models.AgentTypeTop: "agent_top",
	}
	dispatcher := NewTaskDispatcher(registry, 2, 30, nil)
	dispatcher.RegisterExecutor(models.AgentTypeTop, func(ctx context.Context, task *models.Task) (*models.TaskResult, error) {
		result := models.NewTaskResult(task.TaskID, task.AgentType)
		result.SetSuccess([]*models.RecommendItem{{ItemID: "item1", Name: "test item"}}, "ok")
		return result, nil
	})
	aggregator := NewResultAggregator(true, 10, SortByNone)

	agent := New("leader1", parser, planner, dispatcher, aggregator, nil, nil, nil, nil)

	// Process without starting should auto-start
	result, err := agent.Process(context.Background(), "I want casual style")
	if err != nil {
		t.Errorf("Process() error = %v", err)
	}
	_ = result
}

func TestLeaderAgent_ProcessNotReady(t *testing.T) {
	parser := NewProfileParser(
		nil,
		output.NewTemplateEngine(),
		"{{.input}}",
		output.NewValidator(),
		3,
	)
	planner := NewTaskPlanner(3)
	registry := map[models.AgentType]string{
		models.AgentTypeTop: "agent_top",
	}
	dispatcher := NewTaskDispatcher(registry, 2, 30, nil)
	dispatcher.RegisterExecutor(models.AgentTypeTop, func(ctx context.Context, task *models.Task) (*models.TaskResult, error) {
		result := models.NewTaskResult(task.TaskID, task.AgentType)
		result.SetSuccess([]*models.RecommendItem{{ItemID: "item1", Name: "test item"}}, "ok")
		return result, nil
	})
	aggregator := NewResultAggregator(true, 10, SortByNone)

	agent := New("leader1", parser, planner, dispatcher, aggregator, nil, nil, nil, nil)

	// Start then set to busy
	if err := agent.Start(context.Background()); err != nil {
		t.Errorf("Start() error = %v", err)
	}
	// Note: can't easily set to busy without proper implementation

	// Process after stop should auto-start
	if err := agent.Stop(context.Background()); err != nil {
		t.Errorf("Stop() error = %v", err)
	}
	result, err := agent.Process(context.Background(), "test")
	if err != nil {
		t.Errorf("Process() error = %v", err)
	}
	_ = result
}

func TestLeaderAgent_SendReceiveMessage(t *testing.T) {
	parser := NewProfileParser(
		nil,
		output.NewTemplateEngine(),
		"{{.input}}",
		output.NewValidator(),
		3,
	)
	planner := NewTaskPlanner(3)
	registry := map[models.AgentType]string{}
	dispatcher := NewTaskDispatcher(registry, 2, 30, nil)
	aggregator := NewResultAggregator(true, 10, SortByNone)
	queue := ahp.NewMessageQueue("leader1", &ahp.QueueOptions{MaxSize: 10})

	// Create using the concrete type
	leader := &leaderAgent{
		id:           "leader1",
		agentType:    models.AgentTypeLeader,
		status:       models.AgentStatusReady,
		config:       DefaultLeaderAgentConfig(),
		parser:       parser,
		planner:      planner,
		dispatcher:   dispatcher,
		aggregator:   aggregator,
		messageQueue: queue,
	}

	// Test SendMessage
	msg := &ahp.AHPMessage{
		Method:      ahp.AHPMethodTask,
		AgentID:     "leader1",
		TargetAgent: "sub1",
		TaskID:      "task1",
		SessionID:   "session1",
	}
	err := leader.SendMessage(context.Background(), msg)
	if err != nil {
		t.Errorf("SendMessage() error = %v", err)
	}

	// Test ReceiveMessage
	_, err = leader.ReceiveMessage(context.Background())
	if err != nil {
		t.Errorf("ReceiveMessage() error = %v", err)
	}
}

func TestLeaderAgent_Heartbeat(t *testing.T) {
	parser := NewProfileParser(
		nil,
		output.NewTemplateEngine(),
		"{{.input}}",
		output.NewValidator(),
		3,
	)
	planner := NewTaskPlanner(3)
	registry := map[models.AgentType]string{}
	dispatcher := NewTaskDispatcher(registry, 2, 30, nil)
	aggregator := NewResultAggregator(true, 10, SortByNone)
	hbMon := ahp.NewHeartbeatMonitor(ahp.DefaultHeartbeatConfig())

	leader := &leaderAgent{
		id:           "leader1",
		agentType:    models.AgentTypeLeader,
		status:       models.AgentStatusReady,
		config:       DefaultLeaderAgentConfig(),
		parser:       parser,
		planner:      planner,
		dispatcher:   dispatcher,
		aggregator:   aggregator,
		heartbeatMon: hbMon,
	}

	err := leader.Heartbeat(context.Background())
	if err != nil {
		t.Errorf("Heartbeat() error = %v", err)
	}

	if !leader.IsAlive() {
		t.Error("IsAlive() should return true after heartbeat")
	}
}

// --- RestoreState tests ---

func TestRestoreState_NilState(t *testing.T) {
	parser := NewProfileParser(nil, output.NewTemplateEngine(), "{{.input}}", output.NewValidator(), 3)
	planner := NewTaskPlanner(3)
	dispatcher := NewTaskDispatcher(map[models.AgentType]string{}, 2, 30, nil)
	aggregator := NewResultAggregator(true, 10, SortByNone)

	agent := New("leader1", parser, planner, dispatcher, aggregator, nil, nil, nil, nil)
	sa, ok := agent.(base.StatefulAgent)
	if !ok {
		t.Fatal("agent does not implement StatefulAgent")
	}

	err := sa.RestoreState(nil)
	if err != nil {
		t.Errorf("RestoreState(nil) should return nil error, got %v", err)
	}
}

func TestRestoreState_EmptyState(t *testing.T) {
	parser := NewProfileParser(nil, output.NewTemplateEngine(), "{{.input}}", output.NewValidator(), 3)
	planner := NewTaskPlanner(3)
	dispatcher := NewTaskDispatcher(map[models.AgentType]string{}, 2, 30, nil)
	aggregator := NewResultAggregator(true, 10, SortByNone)

	agent := New("leader1", parser, planner, dispatcher, aggregator, nil, nil, nil, nil)
	sa, ok := agent.(base.StatefulAgent)
	if !ok {
		t.Fatal("agent does not implement StatefulAgent")
	}

	err := sa.RestoreState(map[string]any{})
	if err != nil {
		t.Errorf("RestoreState(empty) should return nil error, got %v", err)
	}
}

func TestRestoreState_WithSessionID(t *testing.T) {
	parser := NewProfileParser(nil, output.NewTemplateEngine(), "{{.input}}", output.NewValidator(), 3)
	planner := NewTaskPlanner(3)
	dispatcher := NewTaskDispatcher(map[models.AgentType]string{}, 2, 30, nil)
	aggregator := NewResultAggregator(true, 10, SortByNone)

	agent := New("leader1", parser, planner, dispatcher, aggregator, nil, nil, nil, nil)
	sa, ok := agent.(base.StatefulAgent)
	if !ok {
		t.Fatal("agent does not implement StatefulAgent")
	}

	err := sa.RestoreState(map[string]any{
		"session_id": "session-abc",
	})
	if err != nil {
		t.Errorf("RestoreState should return nil error, got %v", err)
	}

	// Verify sessionID was restored by reading it from the concrete type.
	la := agent.(*leaderAgent)
	la.mu.RLock()
	sid := la.sessionID
	la.mu.RUnlock()
	if sid != "session-abc" {
		t.Errorf("expected sessionID 'session-abc', got '%s'", sid)
	}
}

func TestRestoreState_InvalidSessionIDType(t *testing.T) {
	parser := NewProfileParser(nil, output.NewTemplateEngine(), "{{.input}}", output.NewValidator(), 3)
	planner := NewTaskPlanner(3)
	dispatcher := NewTaskDispatcher(map[models.AgentType]string{}, 2, 30, nil)
	aggregator := NewResultAggregator(true, 10, SortByNone)

	agent := New("leader1", parser, planner, dispatcher, aggregator, nil, nil, nil, nil)
	sa, ok := agent.(base.StatefulAgent)
	if !ok {
		t.Fatal("agent does not implement StatefulAgent")
	}

	// Non-string session_id should be silently ignored.
	err := sa.RestoreState(map[string]any{
		"session_id": 12345,
	})
	if err != nil {
		t.Errorf("RestoreState should return nil error, got %v", err)
	}

	la := agent.(*leaderAgent)
	la.mu.RLock()
	sid := la.sessionID
	la.mu.RUnlock()
	if sid != "" {
		t.Errorf("expected empty sessionID, got '%s'", sid)
	}
}

// nolint: errcheck // Test code may ignore return values

// --- Snapshot / Restore / ReplayEvents tests ---

func newTestLeaderAgent(t *testing.T) (*leaderAgent, base.StatefulAgent) {
	t.Helper()
	parser := NewProfileParser(nil, output.NewTemplateEngine(), "{{.input}}", output.NewValidator(), 3)
	planner := NewTaskPlanner(3)
	dispatcher := NewTaskDispatcher(map[models.AgentType]string{}, 2, 30, nil)
	aggregator := NewResultAggregator(true, 10, SortByNone)

	agent := New("test-snapshot-agent", parser, planner, dispatcher, aggregator, nil, nil, nil, nil)
	sa, ok := agent.(base.StatefulAgent)
	if !ok {
		t.Fatal("agent does not implement StatefulAgent")
	}
	return agent.(*leaderAgent), sa
}

// TestSnapshot_BasicFields verifies that Snapshot returns all expected fields.
func TestSnapshot_BasicFields(t *testing.T) {
	_, sa := newTestLeaderAgent(t)

	snap, err := sa.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if snap == nil {
		t.Fatal("Snapshot() returned nil")
	}

	// Verify required fields exist.
	requiredFields := []string{
		"session_id", "agent_id", "status",
		"last_task_id", "conversation_summary", "snapshot_version",
	}
	for _, field := range requiredFields {
		if _, ok := snap[field]; !ok {
			t.Errorf("Snapshot missing required field: %s", field)
		}
	}

	// Verify agent_id matches.
	if snap["agent_id"] != "test-snapshot-agent" {
		t.Errorf("Snapshot agent_id = %q, want %q", snap["agent_id"], "test-snapshot-agent")
	}

	// Verify snapshot_version.
	if snap["snapshot_version"] != 1 {
		t.Errorf("Snapshot version = %d, want 1", snap["snapshot_version"])
	}
}

// TestSnapshot_WithState verifies that Snapshot reflects current state.
func TestSnapshot_WithState(t *testing.T) {
	_, sa := newTestLeaderAgent(t)

	// Set state via RestoreState first.
	_ = sa.RestoreState(map[string]any{
		"session_id":           "sess-123",
		"last_task_id":         "task-456",
		"conversation_summary": "test summary",
	})

	snap, _ := sa.Snapshot()

	if snap["session_id"] != "sess-123" {
		t.Errorf("Snapshot session_id = %q, want %q", snap["session_id"], "sess-123")
	}
	if snap["last_task_id"] != "task-456" {
		t.Errorf("Snapshot last_task_id = %q, want %q", snap["last_task_id"], "task-456")
	}
	if snap["conversation_summary"] != "test summary" {
		t.Errorf("Snapshot conversation_summary = %q, want %q", snap["conversation_summary"], "test summary")
	}
}

// TestRestoreState_FullState verifies all restorable fields.
func TestRestoreState_FullState(t *testing.T) {
	_, sa := newTestLeaderAgent(t)

	now := time.Now()
	state := map[string]any{
		"session_id":            "sess-full",
		"last_task_id":          "task-full",
		"agent_status":          "busy",
		"conversation_summary":  "full restore test",
		"last_interaction_time": now.Format(time.RFC3339),
	}

	err := sa.RestoreState(state)
	if err != nil {
		t.Fatalf("RestoreState error = %v", err)
	}

	// We can't easily verify internal state without accessing concrete type,
	// but we can verify no error and that subsequent Snapshot reflects it.
	snap, _ := sa.Snapshot()
	if snap["session_id"] != "sess-full" {
		t.Errorf("session_id after restore = %q, want %q", snap["session_id"], "sess-full")
	}
	if snap["last_task_id"] != "task-full" {
		t.Errorf("last_task_id after restore = %q, want %q", snap["last_task_id"], "task-full")
	}
	if snap["status"] != "busy" {
		t.Errorf("status after restore = %q, want %q", snap["status"], "busy")
	}
}

// TestRestoreState_InvalidStatus verifies invalid status is silently skipped.
func TestRestoreState_InvalidStatus(t *testing.T) {
	_, sa := newTestLeaderAgent(t)

	err := sa.RestoreState(map[string]any{
		"session_id":   "sess-test",
		"agent_status": "invalid_status",
	})
	if err != nil {
		t.Fatalf("RestoreState error = %v", err)
	}

	// Status should remain offline (default), not changed to invalid.
	snap, _ := sa.Snapshot()
	if snap["status"] == "invalid_status" {
		t.Error("invalid status should not be restored")
	}
}

// TestReplayEvents_SessionCreated verifies session restoration from events.
func TestReplayEvents_SessionCreated(t *testing.T) {
	_, sa := newTestLeaderAgent(t)

	evts := []*events.Event{
		{
			Type:    events.EventSessionCreated,
			Payload: map[string]any{"session_id": "replay-sess", "user_id": "user-1"},
		},
	}

	err := sa.ReplayEvents(evts)
	if err != nil {
		t.Fatalf("ReplayEvents error = %v", err)
	}

	snap, _ := sa.Snapshot()
	if snap["session_id"] != "replay-sess" {
		t.Errorf("session_id after replay = %q, want %q", snap["session_id"], "replay-sess")
	}
}

// TestReplayEvents_TaskCreated verifies task ID restoration from events.
func TestReplayEvents_TaskCreated(t *testing.T) {
	_, sa := newTestLeaderAgent(t)

	evts := []*events.Event{
		{
			Type:    events.EventSessionCreated,
			Payload: map[string]any{"session_id": "replay-sess"},
		},
		{
			Type:    events.EventTaskCreated,
			Payload: map[string]any{"task_id": "replay-task", "session_id": "replay-sess"},
		},
	}

	err := sa.ReplayEvents(evts)
	if err != nil {
		t.Fatalf("ReplayEvents error = %v", err)
	}

	snap, _ := sa.Snapshot()
	if snap["last_task_id"] != "replay-task" {
		t.Errorf("last_task_id after replay = %q, want %q", snap["last_task_id"], "replay-task")
	}
}

// TestReplayEvents_MessageAdded verifies message count tracking from events.
func TestReplayEvents_MessageAdded(t *testing.T) {
	_, sa := newTestLeaderAgent(t)

	evts := []*events.Event{
		{
			Type:    events.EventMessageAdded,
			Payload: map[string]any{"role": "user", "session_id": "sess-1"},
		},
		{
			Type:    events.EventMessageAdded,
			Payload: map[string]any{"role": "assistant", "session_id": "sess-1"},
		},
	}

	err := sa.ReplayEvents(evts)
	if err != nil {
		t.Fatalf("ReplayEvents error = %v", err)
	}

	snap, _ := sa.Snapshot()
	summary, _ := snap["conversation_summary"].(string)
	if summary == "" {
		t.Error("conversation_summary should be set after MessageAdded replay")
	}
}

// TestReplayEvents_AgentStartedStopped verifies status transitions from events.
func TestReplayEvents_AgentStartedStopped(t *testing.T) {
	_, sa := newTestLeaderAgent(t)

	evts := []*events.Event{
		{Type: events.EventAgentStarted, Payload: map[string]any{}},
		{Type: events.EventAgentStopped, Payload: map[string]any{}},
	}

	err := sa.ReplayEvents(evts)
	if err != nil {
		t.Fatalf("ReplayEvents error = %v", err)
	}

	// Last event is Stopped, so status should be offline.
	snap, _ := sa.Snapshot()
	if snap["status"] != string(models.AgentStatusOffline) {
		t.Errorf("status after stopped replay = %q, want %q", snap["status"], models.AgentStatusOffline)
	}
}

// TestReplayEvents_NilAndEmpty verifies safe handling of edge cases.
func TestReplayEvents_NilAndEmpty(t *testing.T) {
	_, sa := newTestLeaderAgent(t)

	// Nil events.
	if err := sa.ReplayEvents(nil); err != nil {
		t.Errorf("ReplayEvents(nil) error = %v", err)
	}

	// Empty events.
	if err := sa.ReplayEvents([]*events.Event{}); err != nil {
		t.Errorf("ReplayEvents(empty) error = %v", err)
	}

	// Events with nil entry.
	if err := sa.ReplayEvents([]*events.Event{{Type: events.EventSessionCreated, Payload: nil}, nil}); err != nil {
		t.Errorf("ReplayEvents(with nil) error = %v", err)
	}
}

// TestSnapshotRestore_RoundTrip verifies full Snapshot → Restore cycle.
func TestSnapshotRestore_RoundTrip(t *testing.T) {
	_, sa := newTestLeaderAgent(t)

	// Set initial state.
	_ = sa.RestoreState(map[string]any{
		"session_id":           "roundtrip-sess",
		"last_task_id":         "roundtrip-task",
		"conversation_summary": "roundtrip summary",
	})

	// Take snapshot.
	snap, err := sa.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot error = %v", err)
	}

	// Create new agent and restore from snapshot.
	_, sa2 := newTestLeaderAgent(t)
	err = sa2.RestoreState(snap)
	if err != nil {
		t.Fatalf("RestoreState from snapshot error = %v", err)
	}

	// Verify state matches.
	snap2, _ := sa2.Snapshot()
	if snap2["session_id"] != snap["session_id"] {
		t.Errorf("session_id mismatch: got %q, want %q", snap2["session_id"], snap["session_id"])
	}
	if snap2["last_task_id"] != snap["last_task_id"] {
		t.Errorf("last_task_id mismatch: got %q, want %q", snap2["last_task_id"], snap["last_task_id"])
	}
	if snap2["conversation_summary"] != snap["conversation_summary"] {
		t.Errorf("conversation_summary mismatch: got %q, want %q", snap2["conversation_summary"], snap["conversation_summary"])
	}
}
