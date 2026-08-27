// Runtime introspection panel demo — real LLM, one-line API.
//
// This is the simplest possible way to run ARES: introspect.NewDashboard
// assembles the whole observable runtime (real LLM agents, kernel scheduler,
// task/agent fabrics, panel collector + HTTP server) behind one handle. The
// demo just declares agents + a workload and calls Run.
//
// Run (from the repo root):
//
//	go run examples/30-introspect-panel-demo/main.go --config configs/ares.local.yaml
//
// Then open http://localhost:5606/introspect in a browser. The panel shows 6
// pages: Overview / Tasks / Agents / Scheduler / Execution / Events, all fed
// by real runtime data (scheduling decisions, task state machines, LLM calls,
// agent lifecycle).
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/Timwood0x10/ares/internal/introspect"
)

func main() {
	cfgPath := "ares.yaml"
	for i := 1; i+1 < len(os.Args); i++ {
		if os.Args[i] == "--config" {
			cfgPath = os.Args[i+1]
		}
	}

	ctx := context.Background()

	// ── Assemble the whole runtime in one call ──
	d, err := introspect.NewDashboard(ctx, introspect.DashboardConfig{
		ConfigPath:    cfgPath,
		Addr:          ":5606",
		MaxConcurrent: 3,
		LeaseTTL:      60 * time.Second,
		Agents: []introspect.AgentSpec{
			{ID: "coder-1", Capabilities: []string{"code"}, Instruction: "You are a code analyst. Use list_files/read_file/web_search to understand the codebase you are asked about. Be factual and reference file paths."},
			{ID: "reviewer-1", Capabilities: []string{"review"}, Instruction: "You are a code reviewer. Analyse the code you are asked about, identify issues, and suggest improvements."},
		},
		Workload: demoWorkload,
	})
	if err != nil {
		log.Fatalf("dashboard: %v", err)
	}

	fmt.Printf("═══ ARES Observatory (Real LLM) ═══\n")
	fmt.Printf("Panel:  http://localhost:5606/introspect\n")
	fmt.Printf("Agents: %v\n", d.Peers())
	fmt.Printf("Run for 120s, or Ctrl+C to stop.\n\n")

	_ = d.Run(ctx)
}

// demoWorkload submits a few real tasks so the panel has something to show.
func demoWorkload(ctx context.Context, d *introspect.Dashboard) {
	tasks := []struct {
		cap string
		in  string
	}{
		{"code", "Examine the taskfabric module: list its files, describe each one's responsibility, and explain how the state machine works."},
		{"review", "Audit the recovery chain in internal/aresrecovery/recovery.go — identify potential issues with the checkpoint restore logic."},
		{"code", "Analyse the kernel scheduler in internal/kernelscheduler/scheduler.go: explain the drain loop and how candidates are scored."},
		{"review", "Review the agent fabric lifecycle in internal/agentfabric/lifecycle.go — assess spawn/kill/suspend/resume safety."},
	}
	for i := 0; i < len(tasks); i++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(8 * time.Second):
		}
		t := tasks[i]
		id, err := d.Submit(t.cap, t.in)
		if err != nil {
			log.Printf("submit %s: %v", t.cap, err)
		} else {
			log.Printf("[workload] submitted %s (%s) → %s", id, t.cap, t.in)
		}
	}
}
