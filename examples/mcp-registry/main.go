// mcp-registry demonstrates the full MCP service lifecycle:
//
//	Discovery → Tagging → Scoring → Routing
//
// Run: go run ./examples/mcp-registry
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/api/mcp"
)

func main() {
	ctx := context.Background()

	// ── 1. Setup ──────────────────────────────────────────
	store := mcp.NewMemoryStore()
	discovery := mcp.NewDiscoveryService(store, mcp.DiscoveryConfig{
		ProjectDir: ".", // scan .claude/settings.json in current dir
	})
	router := mcp.NewRouter(store, mcp.DefaultScoreConfig())

	// ── 2. Discovery ──────────────────────────────────────
	fmt.Println("=== Phase 1: Discovery ===")
	services, _ := discovery.DiscoverNow(ctx)
	fmt.Printf("  Discovered %d MCP server(s)\n", len(services))
	for _, svc := range services {
		fmt.Printf("    %s — %s\n", svc.Name, svc.Endpoint)
	}

	// Also register some mock services for demo
	registerMockServices(ctx, store)

	// ── 3. Tagging (auto-applied during discovery) ────────
	fmt.Println("\n=== Phase 2: Tagging ===")
	all, _ := store.List(ctx)
	for _, svc := range all {
		fmt.Printf("  %s:\n", svc.Name)
		for level, tags := range svc.Tags {
			fmt.Printf("    %s: %v\n", level, tags)
		}
	}

	// ── 4. Scoring ────────────────────────────────────────
	fmt.Println("\n=== Phase 3: Scoring ===")
	requirements := map[string][]string{
		"category": {"code"},
		"type":     {"query"},
	}
	fmt.Printf("  Requirements: %v\n", requirements)
	for _, svc := range all {
		score := mcp.Score(svc, requirements, mcp.DefaultScoreConfig())
		fmt.Printf("    %-30s score=%.1f\n", svc.Name, score)
	}

	// ── 5. Routing ────────────────────────────────────────
	fmt.Println("\n=== Phase 4: Routing ===")
	results, _ := router.Route(ctx, mcp.RouteRequest{
		Tags: requirements,
		TopN: 3,
	})
	for i, r := range results {
		fmt.Printf("  #%d %-30s score=%.1f  (%s)\n", i+1, r.Service.Name, r.Score, r.Reason)
	}

	// ── 6. Update stats (simulate usage) ──────────────────
	fmt.Println("\n=== Phase 5: Stats Update ===")
	store.UpdateStats(ctx, "codebase-memory-mcp", true, 50*time.Millisecond)
	store.UpdateStats(ctx, "codebase-memory-mcp", true, 30*time.Millisecond)
	store.UpdateStats(ctx, "codebase-memory-mcp", false, 200*time.Millisecond)
	svc, _ := store.Get(ctx, "codebase-memory-mcp")
	fmt.Printf("  %s: calls=%d errors=%d success_rate=%.2f avg_latency=%v\n",
		svc.Name, svc.CallCount, svc.ErrorCount, svc.SuccessRate, svc.AvgLatency)

	// Re-route after stats update
	results, _ = router.Route(ctx, mcp.RouteRequest{Tags: requirements, TopN: 3})
	fmt.Println("\n  Re-routing after stats update:")
	for i, r := range results {
		fmt.Printf("  #%d %-30s score=%.1f\n", i+1, r.Service.Name, r.Score)
	}
}

func registerMockServices(ctx context.Context, store *mcp.MemoryStore) {
	mockServices := []*mcp.MCPService{
		{
			ID:          "codegraph",
			Name:        "codegraph",
			Description: "Code analysis and graph queries for codebases",
			Endpoint:    "codegraph serve --mcp",
			Available:   true,
			SuccessRate: 0.98,
			AvgLatency:  20 * time.Millisecond,
			CallCount:   100,
		},
		{
			ID:          "postgres-mcp",
			Name:        "postgres-mcp",
			Description: "PostgreSQL database query and management",
			Endpoint:    "postgres-mcp --url postgres://localhost/mydb",
			Available:   true,
			SuccessRate: 0.99,
			AvgLatency:  10 * time.Millisecond,
			CallCount:   500,
		},
		{
			ID:          "web-search-mcp",
			Name:        "web-search-mcp",
			Description: "Web search and content extraction",
			Endpoint:    "web-search-mcp serve",
			Available:   true,
			SuccessRate: 0.85,
			AvgLatency:  200 * time.Millisecond,
			CallCount:   50,
		},
		{
			ID:          "file-manager",
			Name:        "file-manager",
			Description: "File system operations: read, write, list files",
			Endpoint:    "file-manager serve",
			Available:   true,
			SuccessRate: 0.99,
			AvgLatency:  5 * time.Millisecond,
			CallCount:   200,
			Favorite:    true,
		},
	}
	for _, svc := range mockServices {
		svc.LastSeen = time.Now()
		svc.Tags = make(map[string][]string)
		mcp.AutoTag(svc)
		_ = store.Save(ctx, svc)
	}
}
