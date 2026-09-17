// AgentOS capabilities — showcase the full agent operating system features.
//
// Purpose:
//
//	Demonstrate the advanced AgentOS capabilities that go beyond simple
//	agent invocation: GA evolution, knowledge operations, context cleaning,
//	and direct LLM service access. This example shows how ARES works as a
//	complete agent operating system, not just an LLM wrapper.
//
// Learning objectives (what this example teaches you):
//   - How to configure and use GA (Genetic Algorithm) evolution mode.
//   - How to interact with the knowledge store (save, query, search).
//   - How to use the context cleaner to compress conversation history.
//   - How to access the LLM service directly for custom workflows.
//   - How to inspect runtime health with Snapshot.
//
// Core APIs used (package path → symbol):
//   - github.com/Timwood0x10/ares/api.NewRuntime
//   - github.com/Timwood0x10/ares/api.WithEvolution
//   - github.com/Timwood0x10/ares/api.WithKnowledge
//   - github.com/Timwood0x10/ares/api.WithDefaultMemory
//   - github.com/Timwood0x10/ares/api.DefaultDreamCycleConfig
//   - github.com/Timwood0x10/ares/api.ModeGeneticAlgorithm
//   - github.com/Timwood0x10/ares/api.NewContextCleaner
//   - github.com/Timwood0x10/ares/api.DefaultCleanOptions
//   - github.com/Timwood0x10/ares/api.KnowledgeObject
//   - github.com/Timwood0x10/ares/api.(*Runtime).KnowledgeStore
//   - github.com/Timwood0x10/ares/api.(*Runtime).Snapshot
//
// Run:
//
//	go run examples/_fixtures/30-agentos-capabilities/main.go
//
// Expected output:
//
//	=== AgentOS Capabilities Showcase ===
//
//	[1/5] Runtime Health Snapshot
//	   Components: 8 total, 8 ready
//
//	[2/5] Context Cleaner
//	   Original: 6 messages
//	   Cleaned:  4 messages
//
//	[3/5] Knowledge Store Operations
//	   Saved object: fact-001
//	   Query result: 1 objects found
//
//	[4/5] GA Evolution Configuration
//	   Mode: Genetic Algorithm
//	   Population: 20, Elite: 3, Mutation: 0.20
//
//	[5/5] Agent with Full Capabilities
//	   Result: <agent response>
//	   Tool calls: 0 | Tokens: <n>
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Timwood0x10/ares/api"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	fmt.Println("=== AgentOS Capabilities Showcase ===")
	fmt.Println()

	// ── Create Runtime with full capabilities ──
	rt, err := ares.New(
		ares.WithOllama("llama3.2"),
		ares.WithDefaultMemory(),
		ares.WithEvolution(),
		ares.WithKnowledge(),
		ares.WithTrace(false),
	)
	if err != nil {
		return fmt.Errorf("create runtime: %w", err)
	}
	defer rt.Close()

	// ── 1. Runtime Health Snapshot ──
	fmt.Println("[1/5] Runtime Health Snapshot")
	snap := rt.Snapshot()
	fmt.Printf("   Components: %d total, %d ready\n", snap.Summary.Total, snap.Summary.Ready)
	for _, c := range snap.Components {
		fmt.Printf("   - %s: %s\n", c.Name, c.State)
	}
	fmt.Println()

	// ── 2. Context Cleaner ──
	fmt.Println("[2/5] Context Cleaner")
	cleaner := ares.NewContextCleaner()
	messages := []ares.Message{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: "What is the capital of France?"},
		{Role: "assistant", Content: "The capital of France is Paris."},
		{Role: "user", Content: "What about Germany?"},
		{Role: "assistant", Content: "The capital of Germany is Berlin."},
		{Role: "user", Content: "And Italy?"},
	}
	cleaned := cleaner.Clean(messages, ares.DefaultCleanOptions())
	fmt.Printf("   Original: %d messages\n", len(messages))
	fmt.Printf("   Cleaned:  %d messages\n", len(cleaned))
	stats := cleaner.Stats()
	fmt.Printf("   Stats: %d turns processed, %d bytes saved\n", stats.TurnsProcessed, stats.BytesSaved)
	fmt.Println()

	// ── 3. Knowledge Store Operations ──
	fmt.Println("[3/5] Knowledge Store Operations")
	knowledgeStore := rt.KnowledgeStore()
	if knowledgeStore != nil {
		// Save a knowledge object
		obj := &ares.KnowledgeObject{
			ID:        "fact-001",
			Type:      "fact",
			Namespace: "demo",
			Summary:   "Paris is the capital of France",
			Metadata: map[string]any{
				"source":     "geography",
				"confidence": 0.95,
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := knowledgeStore.Save(ctx, obj); err != nil {
			fmt.Printf("   ⚠️  Save failed (expected without DB): %v\n", err)
		} else {
			fmt.Printf("   Saved object: %s\n", obj.ID)
		}

		// Query knowledge objects
		query := ares.KnowledgeQuery{
			Namespace: "demo",
			Limit:     10,
		}
		objects, err := knowledgeStore.Query(ctx, query)
		if err != nil {
			fmt.Printf("   ⚠️  Query failed (expected without DB): %v\n", err)
		} else {
			fmt.Printf("   Query result: %d objects found\n", len(objects))
		}
	} else {
		fmt.Println("   ⚠️  Knowledge store not available (requires PostgreSQL)")
	}
	fmt.Println()

	// ── 4. GA Evolution Configuration ──
	fmt.Println("[4/5] GA Evolution Configuration")
	dreamCfg := ares.DefaultDreamCycleConfig()
	fmt.Printf("   Mode: %s\n", evolutionModeName(dreamCfg.EvolutionMode))
	fmt.Printf("   Population: %d, Elite: %d, Mutation: %.2f\n",
		dreamCfg.PopulationSize, dreamCfg.EliteCount, dreamCfg.MutationRate)
	fmt.Printf("   Selection: %s, Tournament: %d\n",
		dreamCfg.SelectionStrategy, dreamCfg.TournamentSize)
	fmt.Printf("   Crossover: %s, SteadyState: %v\n",
		dreamCfg.CrossoverType, dreamCfg.SteadyState)
	fmt.Println()

	// ── 5. Agent with Full Capabilities ──
	fmt.Println("[5/5] Agent with Full Capabilities")
	agent := rt.NewAgent("showcase-agent",
		ares.WithInstruction("You are a knowledgeable assistant. Answer concisely."),
		ares.WithMaxTokens(256),
		ares.WithTimeout(30*time.Second),
	)

	result, err := agent.Run(ctx, "What is 2+2? Answer with just the number.")
	if err != nil {
		return fmt.Errorf("agent run: %w", err)
	}

	fmt.Printf("   Result: %s\n", truncate(result.Output, 50))
	fmt.Printf("   Tool calls: %d | Tokens: %d\n", result.ToolCalls, result.TokenUsage.Total)
	fmt.Println()

	fmt.Println("=== Showcase Complete ===")
	return nil
}

func evolutionModeName(mode ares.EvolutionMode) string {
	switch mode {
	case ares.ModeEvolutionStrategy:
		return "Evolution Strategy"
	case ares.ModeGeneticAlgorithm:
		return "Genetic Algorithm"
	default:
		return "Unknown"
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
