package agentfabric

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/core/models"
	"github.com/Timwood0x10/ares/internal/fabric/planprojection"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
	"github.com/Timwood0x10/ares/internal/fabric/task/workflow/engine"
	llmcore "github.com/Timwood0x10/ares/internal/llmcore"
)

// depthChat is a scripted ChatClient that returns one tool call per round
// for the first (depth) rounds, then a text answer on round (depth+1). It is
// the benchmark harness: it drives the planner through a configurable number
// of quanta so the benchmark can measure end-to-end latency as a function of
// quantum count.
type depthChat struct {
	mu    sync.Mutex
	calls int
	depth int
}

func (c *depthChat) Chat(_ context.Context, _ []*llmcore.LLMMessage, _ []llmcore.Tool, _ map[string]any) (*llmcore.GenerateResponse, error) {
	c.mu.Lock()
	c.calls++
	round := c.calls
	c.mu.Unlock()

	if round <= c.depth {
		return &llmcore.GenerateResponse{
			ToolCalls: []llmcore.ToolCall{{
				ID:       fmt.Sprintf("tc-%d", round),
				Type:     "function",
				Function: llmcore.FunctionCall{Name: "grep", Arguments: `{"query":"data"}`},
			}},
		}, nil
	}
	return &llmcore.GenerateResponse{Content: "final answer"}, nil
}

// BenchmarkPlanner_QuantumCountVsLatency collects the baseline
// data: end-to-end session execution latency as a function of quantum count
// (tool rounds). Each sub-benchmark runs a session with a different depth
// (1, 2, 4, 8, 16 tool rounds) and reports the wall-clock time.
//
// The benchmark uses a zero-latency mock LLM (depthChat) so the measured
// time is pure fabric overhead — the gap between "LLM responded" and
// "session has an answer node". Production latency = this overhead + LLM
// round-trip time × depth.
//
// Run: go test -bench=BenchmarkPlanner_QuantumCountVsLatency -benchtime=1s ./internal/fabric/agent/
func BenchmarkPlanner_QuantumCountVsLatency(b *testing.B) {
	for _, depth := range []int{1, 2, 4, 8} {
		b.Run(fmt.Sprintf("depth=%d", depth), func(b *testing.B) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			fabric := taskfabric.NewFabric()
			coord := planprojection.NewCompileCoordinator(fabric, nil)
			reg := NewSessionRegistry()
			compileCoord := func(_ context.Context, dag *engine.MutableDAG) (stop func()) {
				return coord.SubscribeGraphEvents(ctx, dag)
			}

			chat := &depthChat{depth: depth}
			planner, err := NewPlannerCognition(PlannerDeps{
				ChatClient: chat,
				ToolBinder: &plannerTestBinder{},
				Sessions:   reg,
				Fabric:     fabric,
			})
			if err != nil {
				b.Fatalf("NewPlannerCognition: %v", err)
			}

			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				b.StopTimer()
				// Fresh session per iteration.
				sessionID := fmt.Sprintf("bench-%d", i)
				g, err := reg.InitSession(sessionID, "find the answer", nil, compileCoord)
				if err != nil {
					b.Fatalf("InitSession: %v", err)
				}
				rootStep := g.DAG().StepIndex()[g.Root()]
				if _, err := fabric.CompileNode(ctx, planprojection.ProjectStep(rootStep)); err != nil {
					b.Fatalf("CompileNode: %v", err)
				}
				driveTaskToCompletedBench(b, ctx, fabric, g.Root(), "find the answer")

				// Drive (depth) plan quanta: each grows a tool node + plan
				// node, the last grows an answer node.
				chat.mu.Lock()
				chat.calls = 0
				chat.mu.Unlock()

				b.StartTimer()
				for round := 0; round < depth; round++ {
					planID := SessionNodeID(sessionID, round, "plan", 0)
					planTask := models.NewTask(planID, models.AgentType("ares/plan"), nil)
					planTask.SessionID = sessionID
					planTask.Payload = map[string]any{
						"input":         "find the answer",
						planMetadataKey: sessionID,
					}
					out, err := planner.ExecuteStep(ctx, planTask)
					if err != nil {
						b.Fatalf("ExecuteStep round %d: %v", round, err)
					}
					_ = out

					// Drive the tool node to completion so the next plan
					// quantum can read its output. The tool is grown at
					// depth (round+1) — see stableRound in planner_cognition.
					toolID := SessionNodeID(sessionID, round+1, "grep", 0)
					b.StopTimer()
					// Node existence is an observation, not measured work:
					// the node is created synchronously by the plan step
					// above, so this returns on its first check — but keep
					// the (pathological) polling out of the timed window so
					// the reported overhead never includes scheduler sleeps.
					waitForTaskExistsBench(b, fabric, toolID, 2*time.Second)
					b.StartTimer()
					driveTaskToCompletedBench(b, ctx, fabric, toolID, "echo(grep,data)")
				}

				// The final plan quantum (round=depth) grows the answer node
				// because chat returns Content (no tool calls) on round > depth.
				finalPlanID := SessionNodeID(sessionID, depth, "plan", 0)
				planTask := models.NewTask(finalPlanID, models.AgentType("ares/plan"), nil)
				planTask.SessionID = sessionID
				planTask.Payload = map[string]any{
					"input":         "find the answer",
					planMetadataKey: sessionID,
				}
				if _, err := planner.ExecuteStep(ctx, planTask); err != nil {
					b.Fatalf("final ExecuteStep: %v", err)
				}
				b.StopTimer()

				// Cleanup for next iteration.
				_ = reg.ReleaseSession(sessionID)
			}
		})
	}
}

// driveTaskToCompletedBench is the benchmark-compatible version of the test helper.
func driveTaskToCompletedBench(tb testing.TB, ctx context.Context, fabric *taskfabric.Fabric, taskID, result string) {
	tb.Helper()
	epoch, err := fabric.Acquire(taskID, "bench-agent", time.Minute)
	if err != nil {
		tb.Fatalf("acquire %s: %v", taskID, err)
	}
	if err := fabric.Start(taskID, "bench-agent", epoch); err != nil {
		tb.Fatalf("start %s: %v", taskID, err)
	}
	if err := fabric.Complete(taskID, "bench-agent", epoch); err != nil {
		tb.Fatalf("complete %s: %v", taskID, err)
	}
}

// waitForTaskExistsBench is the benchmark-compatible version of the test helper.
func waitForTaskExistsBench(tb testing.TB, fabric *taskfabric.Fabric, taskID string, timeout time.Duration) {
	tb.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := fabric.Task(taskID); err == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	tb.Fatalf("task %s did not appear within %s", taskID, timeout)
}
