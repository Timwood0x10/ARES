// mcp-registry demonstrates MCP service discovery and lifecycle management.
//
// Shows: active discovery, passive registration, tag management, listing.
//
// Run: go run ./examples/mcp-registry
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/Timwood0x10/ares/api/discovery"
)

func main() {
	ctx := context.Background()

	store := discovery.NewMemoryStore()
	engine := discovery.NewEngine(discovery.EngineConfig{
		ProjectDir: ".",
		Store:      store,
	})

	engine.OnEvent(func(evt discovery.Event) {
		fmt.Printf("  [event] %-25s %s\n", evt.Type, evt.ServiceID)
	})

	// ── 1. Active Discovery ───────────────────────────────
	fmt.Println("=== Phase 1: Discovery ===")
	if err := engine.DiscoverNow(ctx); err != nil {
		log.Printf("discovery: %v", err)
	}
	services, err := engine.List(ctx)
	if err != nil {
		log.Fatalf("list: %v", err)
	}
	fmt.Printf("  Discovered %d MCP server(s)\n", len(services))
	for _, svc := range services {
		fmt.Printf("    %s — %s\n", svc.Identity.Name, svc.Identity.Endpoint)
	}

	// ── 2. Passive Registration ───────────────────────────
	fmt.Println("\n=== Phase 2: Registration ===")
	mockServices := []discovery.RegisterRequest{
		{
			Name:     "codegraph",
			Endpoint: "codegraph serve --mcp",
			Tags:     []string{"capability:code", "type:query"},
		},
		{
			Name:     "postgres-mcp",
			Endpoint: "postgres-mcp --url postgres://localhost/mydb",
			Tags:     []string{"capability:database", "type:query"},
		},
		{
			Name:     "web-search-mcp",
			Endpoint: "web-search-mcp serve",
			Tags:     []string{"capability:search", "type:query"},
		},
		{
			Name:     "file-manager",
			Endpoint: "file-manager serve",
			Tags:     []string{"capability:filesystem", "type:action"},
		},
	}
	for _, reg := range mockServices {
		if err := engine.Register(ctx, reg); err != nil {
			fmt.Printf("  ✗ register %s: %v\n", reg.Name, err)
		} else {
			fmt.Printf("  ✓ Registered %s\n", reg.Name)
		}
	}

	// ── 3. List All Services ──────────────────────────────
	fmt.Println("\n=== Phase 3: All Services ===")
	all, err := engine.List(ctx)
	if err != nil {
		log.Fatalf("list: %v", err)
	}
	for _, svc := range all {
		fmt.Printf("  %s:\n", svc.Identity.Name)
		fmt.Printf("    endpoint:  %s\n", svc.Identity.Endpoint)
		if len(svc.Identity.Tags) > 0 {
			fmt.Printf("    tags:      %v\n", svc.Identity.Tags)
		}
	}

	// ── 4. Tag Management ─────────────────────────────────
	fmt.Println("\n=== Phase 4: Tag Management ===")
	if err := engine.UpdateTags(ctx, "codegraph", discovery.UpdateTagsRequest{
		Add: []string{"domain:source-code"},
	}); err != nil {
		fmt.Printf("  ✗ update tags: %v\n", err)
	} else {
		fmt.Println("  ✓ Updated tags on codegraph")
	}

	// ── 5. Cleanup ────────────────────────────────────────
	fmt.Println("\n=== Phase 5: Cleanup ===")
	if err := engine.Unregister(ctx, "file-manager"); err != nil {
		fmt.Printf("  ✗ unregister: %v\n", err)
	} else {
		fmt.Println("  ✓ Unregistered file-manager")
	}

	services, err = engine.List(ctx)
	if err != nil {
		log.Fatalf("list: %v", err)
	}
	fmt.Printf("  Remaining: %d services\n", len(services))
}
