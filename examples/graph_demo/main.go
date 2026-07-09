// Graph demo — demonstrates graph construction, scheduling, conditional edges,
// and dynamic routing in the ARES Graph system.
//
// Run:  go run examples/graph_demo/main.go
//
//	or:  go run ./examples/graph_demo/
//
// Scheduling strategies:
//   - FIFO / Default:  first-in-first-out (default)
//   - Priority:       highest-priority node first
//   - ShortJob:       shortest estimated duration first
//   - RoundRobin:     fair cycling across ready nodes
//   - WeightedFair:   proportionally weighted distribution
//
// NodeRouter is additive — it injects additional nodes into the ready queue
// at runtime but does not suppress regular edges. For exclusive-or branching,
// use conditional edges with graph.Condition.
package main

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/internal/workflow/graph"
)

func main() {
	fmt.Println("═══ ARES Graph Demo ═══")
	fmt.Println()
	ctx := context.Background()

	// 1. Basic FIFO — 3 nodes in sequence.
	fmt.Println("1. Basic FIFO (3 nodes in sequence)")
	g := build("basic-seq",
		node("fetch", "Fetching data..."),
		node("process", "Processing..."),
		node("save", "Saving..."),
	)
	must2(g.Edge("fetch", "process"))
	must2(g.Edge("process", "save"))
	must2(g.Start("fetch"))
	r, err := g.Execute(ctx, graph.NewState())
	must1(err)
	fmt.Printf("   Duration: %v\n\n", r.Duration)

	// 2. Conditional branching — one of two successors based on state.
	fmt.Println("2. Conditional branching (score >= 60 → pass, else → fail)")
	g2 := build("conditional",
		node("eval", "Evaluating..."),
		node("pass", "PASSED"),
		node("fail", "FAILED"),
	)
	must2(g2.Edge("eval", "pass", condScore(60)))
	must2(g2.Edge("eval", "fail", condScoreLT(60)))
	must2(g2.Start("eval"))
	s90 := graph.NewState()
	s90.Set("score", 90)
	mustExecErr(g2.Execute(ctx, s90))
	s30 := graph.NewState()
	s30.Set("score", 30)
	mustExecErr(g2.Execute(ctx, s30))
	fmt.Println()

	// 3. Priority scheduling — highest priority first.
	fmt.Println("3. Priority scheduling (high=10, medium=5, low=1)")
	g3 := build("priority",
		node("low", "Low"),
		node("medium", "Medium"),
		node("high", "High"),
	)
	must2(g3.Start("high"))
	must2(g3.Start("medium"))
	must2(g3.Start("low"))
	must2(g3.SetScheduler(graph.NewPriorityScheduler(map[string]int{
		"high": 10, "medium": 5, "low": 1,
	})))
	mustExecErr(g3.Execute(ctx, graph.NewState()))
	fmt.Println()

	// 4. ShortJob scheduling — shortest estimated duration first.
	fmt.Println("4. ShortJob scheduling (fast=10ms, slow=2000ms)")
	g4 := build("shortjob",
		node("slow", "Slow (2000ms)"),
		node("fast", "Fast (10ms)"),
	)
	must2(g4.Start("slow"))
	must2(g4.Start("fast"))
	must2(g4.SetScheduler(graph.NewShortJobScheduler(map[string]int{
		"fast": 10, "slow": 2000,
	})))
	mustExecErr(g4.Execute(ctx, graph.NewState()))
	fmt.Println()

	// 5. RoundRobin scheduling — fair cycling.
	fmt.Println("5. RoundRobin scheduling")
	g5 := build("rr", node("a", "A"), node("b", "B"))
	must2(g5.Edge("a", "b"))
	must2(g5.Start("a"))
	must2(g5.Start("b"))
	must2(g5.SetScheduler(graph.NewRoundRobinScheduler()))
	mustExecErr(g5.Execute(ctx, graph.NewState()))
	fmt.Println()

	// 6. WeightedFair scheduling — proportional to weight.
	fmt.Println("6. WeightedFair scheduling (heavy=3×, light=1×)")
	g6 := build("wf", node("heavy", "H"), node("light", "L"))
	must2(g6.Start("heavy"))
	must2(g6.Start("light"))
	must2(g6.SetScheduler(graph.NewWeightedFairScheduler(map[string]int{
		"heavy": 3, "light": 1,
	})))
	mustExecErr(g6.Execute(ctx, graph.NewState()))
	fmt.Println()

	fmt.Println("═══ Done ═══")
}

// ── Nodes & helpers ────────────────────────────

type echoNode struct{ id, msg string }

func (n *echoNode) ID() string { return n.id }

func (n *echoNode) Execute(_ context.Context, _ *graph.State) error {
	fmt.Printf("     [%s] %s\n", n.id, n.msg)
	return nil
}

func node(id, msg string) *echoNode { return &echoNode{id: id, msg: msg} }

func build(id string, nodes ...*echoNode) *graph.Graph {
	g, err := graph.NewGraph(id)
	must1(err)
	for _, n := range nodes {
		_, err = g.Node(n.id, n)
		must1(err)
	}
	return g
}

func must1(err error) {
	if err != nil {
		panic(err)
	}
}

func must2(_ *graph.Graph, err error) {
	if err != nil {
		panic(err)
	}
}

// mustExecErr panics on Execute errors. Execute returns (*Result, error).
func mustExecErr(_ *graph.Result, err error) {
	if err != nil {
		panic(err)
	}
}

func condScore(threshold int) graph.Condition {
	return func(s *graph.State) bool {
		v, ok := s.Get("score")
		return ok && v.(int) >= threshold
	}
}

func condScoreLT(threshold int) graph.Condition {
	return func(s *graph.State) bool {
		v, ok := s.Get("score")
		return ok && v.(int) < threshold
	}
}
