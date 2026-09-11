package agentfabric

// §3.2 (MEDIUM) regressions for the agent fabric:
// CognitionFactory lock discipline, record's agent-state read, the death
// snapshot store bound, and planner growth idempotency across quantum
// re-execution.

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/fabric/planprojection"
	"github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
	llmcore "github.com/Timwood0x10/ares/internal/llmcore"
)

// ── CognitionFactory lock discipline ──

// reentrantFactory re-enters the fabric (Agents needs f.mu) — legal caller
// code that deadlocked when the factory ran under the fabric lock.
func reentrantFactory(f *Fabric) func([]string) Cognition {
	return func([]string) Cognition {
		_ = f.Agents()
		return &countingCognitionAlias{}
	}
}

// countingCognitionAlias avoids clashing with other test helpers.
type countingCognitionAlias struct{}

func (c *countingCognitionAlias) ExecuteStep(_ context.Context, task *models.Task) (*StepOutcome, error) {
	res := models.NewTaskResult(task.TaskID, task.AgentType)
	res.SetSuccess(nil, "ok")
	return &StepOutcome{Done: true, Result: res}, nil
}

// TestSpawnFactoryRunsOutsideFabricLock pins the lock discipline: the
// CognitionFactory is arbitrary caller code and must run BEFORE the fabric
// lock is taken. Pre-fix it ran under f.mu, so any re-entrant fabric call
// (here: Agents()) deadlocked Spawn forever.
func TestSpawnFactoryRunsOutsideFabricLock(t *testing.T) {
	f := NewFabric()
	done := make(chan error, 1)
	go func() {
		_, err := f.Spawn(context.Background(), SpawnSpec{
			Identity:         "reentrant",
			Capabilities:     []string{"code"},
			CognitionFactory: reentrantFactory(f),
		})
		done <- err
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Spawn deadlocked: CognitionFactory ran under the fabric lock")
	}
}

// TestSpawnFactoryPanicLeavesFabricUsable pins the panic path: a factory
// that panics must not leave the fabric mutex locked (pre-fix the panic
// unwound past the manual Unlock, and every later Spawn hung forever).
func TestSpawnFactoryPanicLeavesFabricUsable(t *testing.T) {
	f := NewFabric()
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("the factory panic must propagate to the caller")
			}
		}()
		_, _ = f.Spawn(context.Background(), SpawnSpec{
			Identity:     "boom",
			Capabilities: []string{"code"},
			CognitionFactory: func([]string) Cognition {
				panic("factory exploded")
			},
		})
	}()

	done := make(chan error, 1)
	go func() {
		_, err := f.Spawn(context.Background(), SpawnSpec{
			Identity:     "healthy",
			Capabilities: []string{"code"},
		})
		done <- err
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("fabric is unusable after a factory panic: the mutex was left locked")
	}
}

// ── record's agent-state read ──

// noopSink lets record reach its state read.
type noopSink struct{}

func (noopSink) Emit(_ context.Context, _ AgentEvent) error { return nil }

// TestRecordReadsAgentStateUnderLock pins record's locking: it runs after
// the fabric lock is released, so reading a.State must take the agent's own
// lock. Under -race, the pre-fix unlocked read failed against concurrent
// state transitions. (The writer side uses Suspend/Resume — the remaining
// in-place State writers after SetRunning/SetIdle were removed as dead code.)
func TestRecordReadsAgentStateUnderLock(t *testing.T) {
	ctx := context.Background()
	f := NewFabric().WithEventSink(noopSink{})
	a, err := f.Spawn(ctx, SpawnSpec{Identity: "racy", Capabilities: []string{"code"}})
	require.NoError(t, err)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			f.record(ctx, a, EventAgentSuspended, nil)
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = f.Suspend(ctx, "racy")
			_ = f.Resume(ctx, "racy")
		}
	}()

	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// ── death snapshot store bound ──

// TestSnapshotStoreBounded pins the store cap: killed-and-never-revived
// agents accumulate snapshots forever (Kill removed them from the registry,
// so Retire can never fire), and the rotating spawn/kill cycle therefore
// needs an eviction bound. The oldest deaths go first — recovery prefers
// the freshest cognition anyway.
func TestSnapshotStoreBounded(t *testing.T) {
	s := newSnapshotStore()
	total := maxDeathSnapshots + 500
	base := time.Now().Add(-time.Duration(total) * time.Minute)
	for i := 0; i < total; i++ {
		s.save(fmt.Sprintf("agent-%d", i), AgentSnapshot{
			Cognitive:    CognitiveState{SchemaVersion: CognitiveStateSchemaVersion},
			Capabilities: []string{"code"},
			DiedAt:       base.Add(time.Duration(i) * time.Minute),
		})
	}

	s.mu.RLock()
	size := len(s.byID)
	_, oldestGone := s.byID["agent-0"]
	_, newestKept := s.byID[fmt.Sprintf("agent-%d", total-1)]
	s.mu.RUnlock()

	assert.LessOrEqual(t, size, maxDeathSnapshots,
		"the death snapshot store must be bounded")
	assert.False(t, oldestGone, "the oldest deaths must be evicted first")
	assert.True(t, newestKept, "the newest deaths must be retained")
}

// ── planner growth idempotency ──

// staticToolChat always returns the same single grep tool call, and counts
// how many times the LLM was consulted.
type staticToolChat struct {
	mu    sync.Mutex
	calls int
}

func (c *staticToolChat) Chat(_ context.Context, _ []*llmcore.LLMMessage, _ []llmcore.Tool, _ map[string]any) (*llmcore.GenerateResponse, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return &llmcore.GenerateResponse{
		ToolCalls: []llmcore.ToolCall{{
			ID:       "tc-static",
			Type:     "function",
			Function: llmcore.FunctionCall{Name: "grep", Arguments: `{"query":"same"}`},
		}},
	}, nil
}

// staticAnswerChat always returns a text answer (no tool calls).
type staticAnswerChat struct{}

func (staticAnswerChat) Chat(_ context.Context, _ []*llmcore.LLMMessage, _ []llmcore.Tool, _ map[string]any) (*llmcore.GenerateResponse, error) {
	return &llmcore.GenerateResponse{Content: "final answer"}, nil
}

// newPlannerSession builds the minimal planner harness: session registry +
// L2 graph wired to the incremental compile coordinator.
func newPlannerSession(t *testing.T, sessionID string) (*SessionRegistry, *L2Graph, *taskfabric.Fabric) {
	t.Helper()
	fabric := taskfabric.NewFabric()
	coord := planprojection.NewCompileCoordinator(fabric, nil)
	reg := NewSessionRegistry()
	compileCoord := func(ctx context.Context, dag *engine.MutableDAG) (stop func()) {
		return coord.SubscribeGraphEvents(ctx, dag)
	}
	g, err := reg.InitSession(context.Background(), sessionID, "find the answer", nil, compileCoord)
	require.NoError(t, err)

	// Admit the session root and drive it to COMPLETED so the planner can
	// read the session prompt from its envelope.
	rootStep := g.DAG().StepIndex()[g.Root()]
	_, err = fabric.CompileNode(context.Background(), planprojection.ProjectStep(rootStep))
	require.NoError(t, err)
	driveTaskToCompleted(t, context.Background(), fabric, g.Root(), "find the answer")
	return reg, g, fabric
}

// newPlanTask builds the models.Task the planner executes for a plan node.
func newPlanTask(t *testing.T, sessionID, taskID string) *models.Task {
	t.Helper()
	planTask := models.NewTask(taskID, models.AgentType("ares/plan"), nil)
	planTask.SessionID = sessionID
	planTask.Payload = map[string]any{
		"input":         "find the answer",
		planMetadataKey: sessionID,
	}
	return planTask
}

// TestPlannerReexecutionGrowsNoDuplicateRound pins the full-growth retry: a
// plan quantum re-executed after its round already landed (fabric
// Fail-requeue after the step's side effects, or lease-expiry requeue) must
// derive the SAME growth round and end as a no-op success — not grow a
// whole duplicate round at a shifted depth. Pre-fix the re-execution read
// the ADVANCED PlanDepth and grew a second full round of nodes/tasks.
func TestPlannerReexecutionGrowsNoDuplicateRound(t *testing.T) {
	const sessionID = "idem-full"
	reg, g, fabric := newPlannerSession(t, sessionID)

	chat := &staticToolChat{}
	planner, err := NewPlannerCognition(PlannerDeps{
		ChatClient: chat,
		ToolBinder: &plannerTestBinder{},
		Sessions:   reg,
		Fabric:     fabric,
		Logger:     slog.Default(),
	})
	require.NoError(t, err)

	initialPlanID := SessionNodeID(sessionID, 0, "plan", 0)
	planTask := newPlanTask(t, sessionID, initialPlanID)

	out1, err := planner.ExecuteStep(context.Background(), planTask)
	require.NoError(t, err)
	require.True(t, out1.Done)

	grepID := SessionNodeID(sessionID, 1, "grep", 0)
	waitForTaskExists(t, fabric, grepID, 2*time.Second)
	nodesAfterFirst := g.DAG().NodeCount()
	depthAfterFirst := g.PlanDepth()
	require.Equal(t, 1, depthAfterFirst, "one round must have grown")

	// Re-execute the SAME plan quantum (the retry path). The round is
	// complete: no duplicate nodes, no duplicate tasks, success.
	out2, err := planner.ExecuteStep(context.Background(), planTask)
	require.NoError(t, err, "a re-executed quantum whose round already grew must succeed")
	require.True(t, out2.Done)

	assert.Equal(t, nodesAfterFirst, g.DAG().NodeCount(),
		"re-execution must not grow a duplicate round of nodes")
	assert.Equal(t, 1, g.PlanDepth(), "plan depth must not advance on a re-execution no-op")
}

// TestPlannerPartialGrowthRetryCompletesRound pins the partial-growth
// retry: a quantum that crashed after growing the tool node but before the
// plan node leaves deterministic IDs behind; the re-executed quantum must
// skip the existing node, finish the round, and succeed. Pre-fix the retry
// hit the duplicate node ID and the plan task failed permanently.
func TestPlannerPartialGrowthRetryCompletesRound(t *testing.T) {
	const sessionID = "idem-partial"
	reg, g, fabric := newPlannerSession(t, sessionID)

	chat := &staticToolChat{}
	planner, err := NewPlannerCognition(PlannerDeps{
		ChatClient: chat,
		ToolBinder: &plannerTestBinder{},
		Sessions:   reg,
		Fabric:     fabric,
		Logger:     slog.Default(),
	})
	require.NoError(t, err)

	// Simulate the crashed first execution: only the tool node landed
	// (deterministic round-1 ID, predecessor = root, exactly what
	// growToolNodes would have written).
	grepID := SessionNodeID(sessionID, 1, "grep", 0)
	require.NoError(t, g.AddToolNode(context.Background(), grepID, "grep",
		map[string]any{"query": "same", planMetadataKey: sessionID}, g.Root()))
	waitForTaskExists(t, fabric, grepID, 2*time.Second)

	initialPlanID := SessionNodeID(sessionID, 0, "plan", 0)
	planTask := newPlanTask(t, sessionID, initialPlanID)
	out, err := planner.ExecuteStep(context.Background(), planTask)
	require.NoError(t, err, "the retry must complete the partially-grown round")
	require.True(t, out.Done)

	planID := SessionNodeID(sessionID, 1, "plan", 0)
	assert.True(t, g.HasNode(planID), "the round's plan node must now exist")
	waitForTaskExists(t, fabric, planID, 2*time.Second)
	assert.Equal(t, 1, g.PlanDepth())
}

// TestPlannerAnswerReexecutionIsIdempotent pins the answer-node retry: an
// answer grown by an earlier execution of the same quantum is skipped
// (answer growth does not advance PlanDepth, so the ID is stable) instead
// of failing the retried quantum on the duplicate ID.
func TestPlannerAnswerReexecutionIsIdempotent(t *testing.T) {
	const sessionID = "idem-answer"
	reg, g, fabric := newPlannerSession(t, sessionID)

	planner, err := NewPlannerCognition(PlannerDeps{
		ChatClient: staticAnswerChat{},
		ToolBinder: &plannerTestBinder{},
		Sessions:   reg,
		Fabric:     fabric,
		Logger:     slog.Default(),
	})
	require.NoError(t, err)

	initialPlanID := SessionNodeID(sessionID, 0, "plan", 0)
	planTask := newPlanTask(t, sessionID, initialPlanID)
	out1, err := planner.ExecuteStep(context.Background(), planTask)
	require.NoError(t, err)
	require.True(t, out1.Done)

	answerID := SessionNodeID(sessionID, 1, "answer", 0)
	waitForTaskExists(t, fabric, answerID, 2*time.Second)
	nodesAfterFirst := g.DAG().NodeCount()

	out2, err := planner.ExecuteStep(context.Background(), planTask)
	require.NoError(t, err, "a re-executed quantum whose answer already grew must succeed")
	require.True(t, out2.Done)
	assert.Equal(t, nodesAfterFirst, g.DAG().NodeCount())
}
