package main

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/ares_runtime"
	"github.com/Timwood0x10/ares/internal/monitoring"
	"github.com/Timwood0x10/ares/internal/monitoring/data"
	"github.com/Timwood0x10/ares/internal/monitoring/tabs"
	"github.com/spf13/cobra"
)

var demoCmd = &cobra.Command{
	Use:   "demo",
	Short: "Start console demo with simulated workload",
	Long: `Starts the ARES monitoring console with simulated agent activity.
No LLM, MCP, or real agents needed — perfect for UI development.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDemo()
	},
}

var (
	demoAddr string
)

func init() {
	rootCmd.AddCommand(demoCmd)
	demoCmd.Flags().StringVarP(&demoAddr, "addr", "a", ":9090", "Listen address")
}

func runDemo() error {
	bus := ares_runtime.NewPluginBus()
	tracker := data.NewAgentTracker()
	linker := data.NewTraceLinker()
	tabMap := map[string]monitoring.Tab{
		"events":    tabs.NewEventTab(),
		"memory":    tabs.NewMemoryTab(),
		"evolution": tabs.NewEvolutionTab(),
		"arena":     tabs.NewArenaTab(),
		"workflow":  tabs.NewWorkflowTab(),
		"mcp":       tabs.NewMCPTab(),
		"llm":       tabs.NewLLMTab(),
	}
	plugin := monitoring.NewConsole(
		monitoring.WithAgentTracker(tracker),
		monitoring.WithTraceLinkerOption(linker),
		monitoring.WithTabMap(tabMap),
	).(*monitoring.MonitorPlugin)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := plugin.Start(ctx, bus); err != nil {
		return fmt.Errorf("start plugin: %w", err)
	}

	go simulateWorkload(ctx, bus)

	server := monitoring.NewHTTPServer(plugin)

	fmt.Println("=== ARES Console Demo ===")
	fmt.Printf("Console: http://localhost%s/console/\n", demoAddr)
	fmt.Println("Simulating agents, LLM calls, tools, tasks...")
	fmt.Println()

	if err := server.Run(demoAddr); err != nil {
		log.Fatal(err)
	}
	return nil
}

// simulateWorkload emits a stream of realistic events.
func simulateWorkload(ctx context.Context, bus ares_runtime.EventBus) {
	agents := []struct {
		id    string
		name  string
		role  string
		model string
	}{
		{"leader-1", "leader", "orchestrator", "llama3.2"},
		{"coder-a", "coder-alpha", "coder", "openai/gpt-3.5-turbo"},
		{"coder-b", "coder-beta", "coder", "openai/gpt-3.5-turbo"},
		{"reviewer-1", "reviewer", "reviewer", "llama3.2"},
		{"researcher-1", "researcher", "research", "openai/gpt-3.5-turbo"},
	}

	parents := map[string]string{
		"leader-1":     "",
		"coder-a":      "leader-1",
		"coder-b":      "leader-1",
		"reviewer-1":   "leader-1",
		"researcher-1": "",
	}
	for i, a := range agents {
		time.Sleep(time.Duration(500+i*300) * time.Millisecond)
		payload := map[string]any{
			"agent_id":   a.id,
			"name":       a.name,
			"role":       a.role,
			"model_name": a.model,
		}
		if p := parents[a.id]; p != "" {
			payload["parent_id"] = p
		}
		bus.Emit(ctx, a.id, ares_events.EventAgentStarted, a.id, payload)
	}

	taskID := 0
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		agent := agents[rand.Intn(len(agents))]
		action := rand.Intn(5)

		switch action {
		case 0:
			inTokens := int64(200 + rand.Intn(2000))
			outTokens := int64(50 + rand.Intn(800))
			cost := float64(inTokens)*0.000003 + float64(outTokens)*0.000015
			bus.Emit(ctx, agent.id, ares_events.EventLLMCall, agent.id, map[string]any{
				"agent_id":       agent.id,
				"model":          agent.model,
				"input_tokens":   inTokens,
				"output_tokens":  outTokens,
				"estimated_cost": cost,
				"duration":       time.Duration(500+rand.Intn(3000)) * time.Millisecond,
			})

		case 1:
			tools := []string{"codegraph_files", "grep", "read_file", "write_file", "bash", "web_search"}
			tool := tools[rand.Intn(len(tools))]
			bus.Emit(ctx, agent.id, ares_events.EventToolCallStarted, agent.id, map[string]any{
				"agent_id":  agent.id,
				"tool_name": tool,
			})
			time.Sleep(time.Duration(50+rand.Intn(500)) * time.Millisecond)
			bus.Emit(ctx, agent.id, ares_events.EventToolCallCompleted, agent.id, map[string]any{
				"agent_id":  agent.id,
				"tool_name": tool,
				"duration":  time.Duration(50+rand.Intn(500)) * time.Millisecond,
			})

		case 2:
			taskID++
			tid := fmt.Sprintf("task-%d", taskID)
			taskNames := []string{"analyze code", "fix bug", "write tests", "refactor module", "review PR", "deploy service", "build graph", "run evaluation"}
			taskName := taskNames[rand.Intn(len(taskNames))]
			bus.Emit(ctx, agent.id, ares_events.EventTaskCreated, agent.id, map[string]any{
				"task_id":  tid,
				"agent_id": agent.id,
				"name":     taskName,
			})
			go func(aid, tid string, fail bool) {
				time.Sleep(time.Duration(1+rand.Intn(5)) * time.Second)
				if fail {
					bus.Emit(ctx, aid, ares_events.EventTaskFailed, aid, map[string]any{
						"task_id":  tid,
						"agent_id": aid,
						"error":    "timeout exceeded",
					})
				} else {
					bus.Emit(ctx, aid, ares_events.EventTaskCompleted, aid, map[string]any{
						"task_id":  tid,
						"agent_id": aid,
					})
				}
			}(agent.id, tid, rand.Float64() < 0.15)

		case 3:
			bus.Emit(ctx, agent.id, ares_events.EventMemoryDistilled, agent.id, map[string]any{
				"agent_id":  agent.id,
				"content":   fmt.Sprintf("Learned pattern: %s approach works best for %s tasks", agent.role, agent.name),
				"relevance": 0.5 + rand.Float64()*0.5,
			})

		case 4:
			if rand.Float64() < 0.05 {
				bus.Emit(ctx, agent.id, ares_events.EventFailoverTriggered, agent.id, map[string]any{
					"agent_id":   agent.id,
					"fault_type": "crash",
				})
				time.Sleep(time.Duration(500+rand.Intn(1500)) * time.Millisecond)
				bus.Emit(ctx, agent.id, ares_events.EventFailoverCompleted, agent.id, map[string]any{
					"agent_id": agent.id,
					"status":   "completed",
				})
			}
		}

		time.Sleep(time.Duration(300+rand.Intn(1200)) * time.Millisecond)
	}
}
