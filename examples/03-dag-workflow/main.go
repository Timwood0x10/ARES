// DAG workflow — dynamic orchestration with the peer Runtime (sdk.Graph).
//
// The legacy internal/workflow spec/Runner engine was retired (fusion plan
// Phase B): the SAME four patterns — conditional edge, linear chain,
// fan-out + join, bounded loop — are now expressed with sdk.Graph and run
// through the kernel scheduling path.
//
// Core APIs used (with package paths):
//   - sdk.NewGraph / (*Graph).AddNode / AddEdge / SetRouter — sdk
//   - (*Runtime).RunGraph / GraphResult                     — sdk
//
// Run:
//
//	go run examples/03-dag-workflow/main.go
package main

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/sdk"
)

func main() {
	ctx := context.Background()
	rt := sdk.NewRuntime(sdk.WithOllama("llama3.2"), sdk.WithTrace(false))
	defer rt.Close()

	conditionalEdge(ctx, rt)
	linearChain(ctx, rt)
	fanOutJoin(ctx, rt)
	boundedLoop(ctx, rt)

	fmt.Println("\n✅ All DAG workflow demos completed")
}

// conditionalEdge: an edge whose condition is false kills that branch while
// the sibling continues — data-driven routing at the edge level.
func conditionalEdge(ctx context.Context, rt *sdk.Runtime) {
	fmt.Println("\n═══ Conditional Edge (sdk.Graph) ═══")
	g := sdk.NewGraph("cond").
		AddNode("ingest", func(_ context.Context, st map[string]any) error {
			st["large"] = true
			return nil
		}).
		AddNode("skip-me", func(_ context.Context, _ map[string]any) error {
			st := map[string]any{} // unreachable when the condition is false
			_ = st
			fmt.Println("  [skip-me] should not run")
			return nil
		}).
		AddNode("chunk", func(_ context.Context, st map[string]any) error {
			fmt.Println("  [chunk] large input → split path taken")
			st["chunks"] = 3
			return nil
		}).
		AddNode("report", func(_ context.Context, st map[string]any) error {
			fmt.Printf("  [report] chunks=%v\n", st["chunks"])
			return nil
		}).
		AddEdge("ingest", "skip-me", func(st map[string]any) bool { return !st["large"].(bool) }).
		AddEdge("ingest", "chunk", nil).
		AddEdge("chunk", "report", nil)
	if _, err := rt.RunGraph(ctx, g); err != nil {
		fmt.Println("  error:", err)
	}
}

// linearChain: strict A→B→C ordering through shared state.
func linearChain(ctx context.Context, rt *sdk.Runtime) {
	fmt.Println("\n═══ Linear DAG (sdk.Graph) ═══")
	trace := []string{}
	step := func(id string) func(context.Context, map[string]any) error {
		return func(_ context.Context, st map[string]any) error {
			prev, _ := st["order"].([]string)
			st["order"] = append(prev, id)
			trace = append(trace, id)
			return nil
		}
	}
	g := sdk.NewGraph("chain").
		AddNode("a", step("a")).
		AddNode("b", step("b")).
		AddNode("c", step("c")).
		AddEdge("a", "b", nil).
		AddEdge("b", "c", nil)
	if _, err := rt.RunGraph(ctx, g); err != nil {
		fmt.Println("  error:", err)
	}
	fmt.Println("  order:", trace)
}

// fanOutJoin: one root fans out to parallel branches; a join node runs only
// after ALL branches settle (round barrier semantics).
func fanOutJoin(ctx context.Context, rt *sdk.Runtime) {
	fmt.Println("\n═══ Fan-out + Join (sdk.Graph) ═══")
	var done int
	g := sdk.NewGraph("fanout").
		AddNode("root", func(_ context.Context, _ map[string]any) error { return nil }).
		AddNode("b1", func(_ context.Context, _ map[string]any) error { done++; return nil }).
		AddNode("b2", func(_ context.Context, _ map[string]any) error { done++; return nil }).
		AddNode("b3", func(_ context.Context, _ map[string]any) error { done++; return nil }).
		AddNode("join", func(_ context.Context, _ map[string]any) error {
			fmt.Printf("  [join] all %d branches settled\n", done)
			return nil
		}).
		AddEdge("root", "b1", nil).
		AddEdge("root", "b2", nil).
		AddEdge("root", "b3", nil).
		AddEdge("b1", "join", nil).
		AddEdge("b2", "join", nil).
		AddEdge("b3", "join", nil)
	if _, err := rt.RunGraph(ctx, g); err != nil {
		fmt.Println("  error:", err)
	}
}

// boundedLoop: the router re-enters a DONE node as a loop; MaxIterations and
// the router's own counter bound it, then static edges finish the graph.
func boundedLoop(ctx context.Context, rt *sdk.Runtime) {
	fmt.Println("\n═══ Controlled Loop (sdk.Graph router) ═══")
	g := sdk.NewGraph("loop").
		AddNode("start", func(_ context.Context, st map[string]any) error { st["n"] = 0; return nil }).
		AddNode("iter", func(_ context.Context, st map[string]any) error {
			n, _ := st["n"].(int)
			st["n"] = n + 1
			fmt.Printf("  [iter] pass %d\n", n+1)
			return nil
		}).
		AddNode("done", func(_ context.Context, st map[string]any) error {
			fmt.Printf("  [done] total iterations=%v\n", st["n"])
			return nil
		}).
		AddEdge("start", "iter", nil).
		AddEdge("iter", "done", nil).
		SetRouter(func(_ context.Context, current string, st map[string]any) string {
			if current == "iter" {
				if n, _ := st["n"].(int); n < 3 {
					return "iter"
				}
			}
			return ""
		})
	g.MaxIterations = 8
	if _, err := rt.RunGraph(ctx, g); err != nil {
		fmt.Println("  error:", err)
	}
}
