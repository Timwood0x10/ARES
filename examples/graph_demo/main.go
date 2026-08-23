// Graph demo — demonstrates the three M1 collaboration patterns using the
// unified sdk.Graph API (docs/design/sdk-graph-v030.md).
//
// This example replaces the old api/graph demo, showing that ONE Graph API
// covers delegate / pipeline / orchestrate without three separate APIs.
//
// Run:
//
//	go run ./examples/graph_demo
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/sdk"
)

func main() {
	fmt.Println("═══ ARES SDK Graph Demo ═══")
	fmt.Println()

	rt := sdk.NewRuntime(sdk.WithOllama("llama3.2"), sdk.WithoutMemory(), sdk.WithTrace(false))
	defer rt.Close()
	ctx := context.Background()

	// ── 1. Delegate Mode ──
	// Leader delegates to specialists, then aggregates results.
	// Graph: leader → specialist-a, specialist-b → aggregate
	fmt.Println("1. Delegate Mode (leader → specialists → aggregate)")
	delegateDemo(ctx, rt)
	fmt.Println()

	// ── 2. Pipeline Mode ──
	// A → B → C serial chain with state propagation.
	fmt.Println("2. Pipeline Mode (fetch → transform → store)")
	pipelineDemo(ctx, rt)
	fmt.Println()

	// ── 3. Orchestration Mode ──
	// Coordinator fans out to parallel workers, join node aggregates.
	fmt.Println("3. Orchestration Mode (coordinator → workers → join)")
	orchestrationDemo(ctx, rt)
	fmt.Println()

	fmt.Println("═══ Done ═══")
}

// delegateDemo shows leader → specialists → aggregate: the leader sets the
// task, two specialists process in parallel, and the aggregate node collects
// their outputs.
func delegateDemo(ctx context.Context, rt *sdk.Runtime) {
	g := sdk.NewGraph("delegate")
	g.AddNode("leader", func(_ context.Context, state map[string]any) error {
		state["task"] = "analyze codebase"
		fmt.Printf("     [leader] delegating: %s\n", state["task"])
		return nil
	})
	g.AddNode("specialist-a", func(_ context.Context, state map[string]any) error {
		fmt.Println("     [specialist-a] checking architecture")
		state["result-a"] = "arch: clean"
		return nil
	})
	g.AddNode("specialist-b", func(_ context.Context, state map[string]any) error {
		fmt.Println("     [specialist-b] checking tests")
		state["result-b"] = "tests: 80% coverage"
		return nil
	})
	g.AddNode("aggregate", func(_ context.Context, state map[string]any) error {
		a, _ := state["result-a"].(string)
		b, _ := state["result-b"].(string)
		fmt.Printf("     [aggregate] %s | %s\n", a, b)
		return nil
	})
	g.AddEdge("leader", "specialist-a", nil)
	g.AddEdge("leader", "specialist-b", nil)
	g.AddEdge("specialist-a", "aggregate", nil)
	g.AddEdge("specialist-b", "aggregate", nil)

	if _, err := rt.RunGraph(ctx, g); err != nil {
		fmt.Printf("     error: %v\n", err)
	}
}

// pipelineDemo shows a serial A → B → C chain: each stage reads the previous
// stage's output from the shared state.
func pipelineDemo(ctx context.Context, rt *sdk.Runtime) {
	g := sdk.NewGraph("pipeline")
	g.AddNode("fetch", func(_ context.Context, state map[string]any) error {
		fmt.Println("     [fetch] raw data acquired")
		state["data"] = "raw:hello,world"
		return nil
	})
	g.AddNode("transform", func(_ context.Context, state map[string]any) error {
		data, _ := state["data"].(string)
		fmt.Printf("     [transform] processing: %s\n", data)
		state["data"] = "transformed:HELLO,WORLD"
		return nil
	})
	g.AddNode("store", func(_ context.Context, state map[string]any) error {
		data, _ := state["data"].(string)
		fmt.Printf("     [store] saved: %s\n", data)
		return nil
	})
	g.AddEdge("fetch", "transform", nil)
	g.AddEdge("transform", "store", nil)

	if _, err := rt.RunGraph(ctx, g); err != nil {
		fmt.Printf("     error: %v\n", err)
	}
}

// orchestrationDemo shows a coordinator → parallel workers → join pattern:
// workers execute concurrently and the join node waits for all.
func orchestrationDemo(ctx context.Context, rt *sdk.Runtime) {
	g := sdk.NewGraph("orchestrate")
	g.AddNode("coordinator", func(_ context.Context, state map[string]any) error {
		fmt.Println("     [coordinator] dispatching to workers")
		state["tasks"] = 3
		return nil
	})
	g.AddNode("worker-1", func(_ context.Context, state map[string]any) error {
		time.Sleep(10 * time.Millisecond)
		fmt.Println("     [worker-1] done")
		state["w1"] = "result-1"
		return nil
	})
	g.AddNode("worker-2", func(_ context.Context, state map[string]any) error {
		time.Sleep(10 * time.Millisecond)
		fmt.Println("     [worker-2] done")
		state["w2"] = "result-2"
		return nil
	})
	g.AddNode("worker-3", func(_ context.Context, state map[string]any) error {
		time.Sleep(10 * time.Millisecond)
		fmt.Println("     [worker-3] done")
		state["w3"] = "result-3"
		return nil
	})
	g.AddNode("join", func(_ context.Context, state map[string]any) error {
		fmt.Printf("     [join] collected: %s, %s, %s\n",
			state["w1"], state["w2"], state["w3"])
		return nil
	})
	g.AddEdge("coordinator", "worker-1", nil)
	g.AddEdge("coordinator", "worker-2", nil)
	g.AddEdge("coordinator", "worker-3", nil)
	g.AddEdge("worker-1", "join", nil)
	g.AddEdge("worker-2", "join", nil)
	g.AddEdge("worker-3", "join", nil)

	if _, err := rt.RunGraph(ctx, g); err != nil {
		fmt.Printf("     error: %v\n", err)
	}
}
