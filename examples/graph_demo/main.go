// Graph demo — demonstrates graph construction, scheduling, conditional edges,
// and dynamic routing in the ARES Graph system.
//
// Purpose:
//
//	This example teaches the api/graph workflow engine: building a graph of
//	nodes, wiring edges (including conditional branches), and controlling
//	execution order with different scheduling strategies. It runs six small
//	demos: basic FIFO, conditional branching, priority, short-job, round-robin,
//	and weighted-fair scheduling.
//
// Learning objectives:
//   - How to build a graph (graph.NewGraph / (*Graph).Node) and connect nodes
//     with edges (Edge / Start).
//   - How conditional edges (graph.Condition) implement exclusive-or
//     branching on runtime state.
//   - How each scheduler (Priority / ShortJob / RoundRobin / WeightedFair)
//     changes node execution order.
//
// Core APIs (with package paths):
//   - graph.NewGraph / (*Graph).Node / Edge / Start / Execute / SetScheduler
//     (github.com/Timwood0x10/ares/api/graph)
//   - graph.NewState / (*State).Set
//   - graph.NewPriorityScheduler / NewShortJobScheduler / NewRoundRobinScheduler /
//     NewWeightedFairScheduler
//
// Run:
//
//	go run ./examples/graph_demo
//
// Expected output:
//
//	═══ ARES Graph Demo ═══
//	1. Basic FIFO (3 nodes in sequence)
//	     [fetch] Fetching data... ...
//	... six demos with their scheduling results ...
//	═══ Done ═══
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

	"github.com/Timwood0x10/ares/api/graph"
)

func main() {
	fmt.Println("═══ ARES Graph Demo ═══")
	fmt.Println()
	ctx := context.Background()

	// ── Step 1: Basic FIFO — 3 nodes in sequence ──
	// Nodes execute in edge order: fetch → process → save. Start marks the
	// entry node; Execute runs the whole graph.
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

	// ── Step 2: Conditional branching ──
	// Two edges from "eval" each carry a Condition; only the edge whose
	// condition is true fires, implementing if/else on runtime state (score).
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

	// ── Step 3: Priority scheduling ──
	// All three nodes are ready; the priority scheduler picks the highest
	// priority first (high=10 → medium=5 → low=1).
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

	// ── Step 4: ShortJob scheduling ──
	// The scheduler orders ready nodes by estimated duration (fast=10ms
	// before slow=2000ms), minimizing overall completion time.
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

	// ── Step 5: RoundRobin scheduling ──
	// Ready nodes alternate fairly; here "a" and "b" both start ready and
	// cycle through execution.
	fmt.Println("5. RoundRobin scheduling")
	g5 := build("rr", node("a", "A"), node("b", "B"))
	must2(g5.Edge("a", "b"))
	must2(g5.Start("a"))
	must2(g5.Start("b"))
	must2(g5.SetScheduler(graph.NewRoundRobinScheduler()))
	mustExecErr(g5.Execute(ctx, graph.NewState()))
	fmt.Println()

	// ── Step 6: WeightedFair scheduling ──
	// Execution is distributed proportionally to each node's weight
	// (heavy=3× vs light=1×).
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

// echoNode is a minimal graph.Node that prints its message when executed.
type echoNode struct{ id, msg string }

func (n *echoNode) ID() string { return n.id }

func (n *echoNode) Execute(_ context.Context, _ *graph.State) error {
	fmt.Printf("     [%s] %s\n", n.id, n.msg)
	return nil
}

// node builds an echoNode with the given id and message.
func node(id, msg string) *echoNode { return &echoNode{id: id, msg: msg} }

// build creates a graph and registers all nodes into it.
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

// condScore returns a condition that is true when state["score"] >= threshold.
func condScore(threshold int) graph.Condition {
	return func(s *graph.State) bool {
		v, ok := s.Get("score")
		return ok && v.(int) >= threshold
	}
}

// condScoreLT returns a condition that is true when state["score"] < threshold.
func condScoreLT(threshold int) graph.Condition {
	return func(s *graph.State) bool {
		v, ok := s.Get("score")
		return ok && v.(int) < threshold
	}
}
