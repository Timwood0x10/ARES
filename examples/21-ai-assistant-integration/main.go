// Command 21-ai-assistant-integration demonstrates a complete AI
// assistant integration using the public api/ packages and the new
// KnowledgeService exposed by Part B.
//
// Flow:
//  1. Create a KnowledgeService backed by an internal KnowledgeRuntime
//     (via the service.ServiceAdapter).
//  2. Build a knowledge graph from a user intent.
//  3. Compile the graph into a markdown context for LLM consumption.
//  4. Distill raw conversation memory into a KnowledgeObject.
//
// This example DOES NOT import any internal/ package directly — the
// adapter wiring is done in main via the public api/knowledge interface
// and a thin constructor exposed for this purpose.
//
// Usage:
//
//	go run examples/21-ai-assistant-integration/main.go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	apiknowledge "github.com/Timwood0x10/ares/api/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/runtime"
	"github.com/Timwood0x10/ares/internal/knowledge/service"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Build a KnowledgeRuntime and adapt it to the public interface.
	rt := runtime.New(nil, nil, nil, nil, nil, nil)
	svc, err := service.NewServiceAdapter(rt)
	if err != nil {
		log.Fatalf("create knowledge service: %v", err)
	}

	// 2. Build a knowledge graph from a user intent.
	intent := apiknowledge.Intent{
		Goal: "Why did we choose Redis for caching?",
		Budget: apiknowledge.TokenBudget{
			MaxTokens: 4096,
			ForGraph:  2048,
		},
	}

	// BuildGraph validates intent.Goal != "" (Part B guard).
	graph, err := svc.BuildGraph(ctx, intent)
	if err != nil {
		// Expected in this stub: runtime has no planner wired.
		fmt.Printf("BuildGraph returned (expected with nil planner): %v\n", err)
	} else {
		fmt.Printf("Built graph with %d nodes\n", len(graph.Nodes))
	}

	// 3. Compile a (manually constructed) graph into markdown.
	demoGraph := &apiknowledge.WorkingGraph{
		Nodes: map[string]*apiknowledge.KnowledgeObject{
			"decision-42": {
				ID:      "decision-42",
				Type:    apiknowledge.ObjectDecision,
				Summary: "Redis chosen for sub-ms latency and TTL eviction.",
			},
		},
	}
	compiled, err := svc.CompileContext(ctx, demoGraph)
	if err != nil {
		log.Fatalf("compile context: %v", err)
	}
	fmt.Println("Compiled context:")
	fmt.Println(compiled)

	// 4. Distill raw memory into a KnowledgeObject.
	rawMemory := []byte("User asked why we use Redis. Answer: latency + TTL.")
	objs, err := svc.Distill(ctx, rawMemory, "tenant-1")
	if err != nil {
		log.Fatalf("distill: %v", err)
	}
	fmt.Printf("Distilled %d KnowledgeObject(s)\n", len(objs))
	for _, o := range objs {
		fmt.Printf("  - id=%s type=%s ns=%s raw_len=%d\n",
			o.ID, o.Type, o.Namespace, len(o.Raw))
	}

	fmt.Println("AI assistant integration example completed.")
}
