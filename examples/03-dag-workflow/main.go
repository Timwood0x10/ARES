// DAG workflow — demonstrates graph-based workflow with linear and conditional execution.
//
// Run:
//
//	go run examples/03-dag-workflow/main.go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_observability"
	wfgraph "github.com/Timwood0x10/ares/internal/workflow/graph"
	"github.com/Timwood0x10/ares/internal/workflow/graphservice"
)

func main() {
	ctx := context.Background()

	// ── 1. Create graph service ─────────────────────────────────
	svc, err := graphservice.NewService(&graphservice.Config{
		RequestTimeout: 30 * time.Second,
		Tracer:         ares_observability.NewLogTracer(nil),
	})
	if err != nil {
		log.Fatalf("create service: %v", err)
	}

	// ── 2. Linear workflow: validate → enrich → respond ─────────
	fmt.Println("═══ Linear workflow ═══")

	g, err := wfgraph.NewGraph("linear-demo")
	must(err)

	validate, _ := wfgraph.NewFuncNode("validate", func(_ context.Context, s *wfgraph.State) error {
		input, _ := s.Get("input")
		fmt.Printf("  [validate] input=%v\n", input)
		if input == nil || input == "" {
			return fmt.Errorf("empty input")
		}
		s.Set("validated", true)
		return nil
	})

	enrich, _ := wfgraph.NewFuncNode("enrich", func(_ context.Context, s *wfgraph.State) error {
		input, _ := s.Get("input")
		s.Set("enriched", fmt.Sprintf("Processed: %v", input))
		v, _ := s.Get("enriched")
		fmt.Printf("  [enrich] → %v\n", v)
		return nil
	})

	respond, _ := wfgraph.NewFuncNode("respond", func(_ context.Context, s *wfgraph.State) error {
		enriched, _ := s.Get("enriched")
		result := fmt.Sprintf("✅ %v", enriched)
		s.Set("output", result)
		fmt.Printf("  [respond] → %s\n", result)
		return nil
	})

	must2(g.Node("validate", validate))
	must2(g.Node("enrich", enrich))
	must2(g.Node("respond", respond))
	must2(g.Edge("validate", "enrich"))
	must2(g.Edge("enrich", "respond"))
	must2(g.Start("validate"))

	result := mustExec(svc.Execute(ctx, g, &graphservice.ExecuteRequest{
		GraphID: "linear-demo",
		State:   map[string]any{"input": "hello dag"},
	}))
	fmt.Printf("  output: %v\n", result.State["output"])

	// ── 3. Conditional workflow: check → approve|reject ─────────
	fmt.Println("\n═══ Conditional workflow ═══")

	g2, err := wfgraph.NewGraph("conditional-demo")
	must(err)

	check, _ := wfgraph.NewFuncNode("check", func(_ context.Context, s *wfgraph.State) error {
		score, _ := s.Get("score")
		sVal, _ := score.(int)
		fmt.Printf("  [check] score=%d\n", sVal)
		s.Set("approved", sVal >= 70)
		return nil
	})

	approve, _ := wfgraph.NewFuncNode("approve", func(_ context.Context, s *wfgraph.State) error {
		s.Set("output", "✅ Approved")
		fmt.Printf("  [approve] → approved\n")
		return nil
	})

	reject, _ := wfgraph.NewFuncNode("reject", func(_ context.Context, s *wfgraph.State) error {
		s.Set("output", "❌ Rejected")
		fmt.Printf("  [reject] → rejected\n")
		return nil
	})

	must2(g2.Node("check", check))
	must2(g2.Node("approve", approve))
	must2(g2.Node("reject", reject))
	must2(g2.Edge("check", "approve", wfgraph.IfFunc(func(s *wfgraph.State) bool {
		v, _ := s.Get("approved")
		ok, _ := v.(bool)
		return ok
	})))
	must2(g2.Edge("check", "reject"))
	must2(g2.Start("check"))

	for _, score := range []int{85, 30} {
		result2 := mustExec(svc.Execute(ctx, g2, &graphservice.ExecuteRequest{
			GraphID: "conditional-demo",
			State:   map[string]any{"score": score},
		}))
		fmt.Printf("  score=%d → %v\n", score, result2.State["output"])
	}

	fmt.Println("\n✅ DAG workflow demo completed")
}

func must(err error) {
	if err != nil {
		log.Fatalf("graph: %v", err)
	}
}

func must2(_ *wfgraph.Graph, err error) {
	if err != nil {
		log.Fatalf("graph: %v", err)
	}
}

func mustExec(r *graphservice.ExecuteResponse, err error) *graphservice.ExecuteResponse {
	if err != nil {
		log.Fatalf("execute: %v", err)
	}
	return r
}
