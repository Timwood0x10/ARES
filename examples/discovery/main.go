// discovery demonstrates the Service Discovery Engine.
//
// Shows: active discovery, passive registration, tag management, health check.
//
// Run: go run ./examples/discovery
package main

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/api/discovery"
)

func main() {
	ctx := context.Background()

	// ── Create engine with pluggable store ────────────────
	// Users can pass their own Store (SQLite, Postgres, etc.)
	// Default: in-memory.
	engine := discovery.NewEngine(discovery.EngineConfig{
		ProjectDir: "",
		Store:      nil, // Uses MemoryStore by default.
	})

	// Events → can be persisted to any DB.
	engine.OnEvent(func(evt discovery.Event) {
		fmt.Printf("  [event] %-25s %s\n", evt.Type, evt.ServiceID)
	})

	// ── 1. Active Discovery ───────────────────────────────
	fmt.Println("=== 1. Active Discovery ===")
	_ = engine.DiscoverNow(ctx)

	services, _ := engine.List(ctx)
	fmt.Printf("  Found %d services\n", len(services))

	// ── 2. Passive Registration ───────────────────────────
	fmt.Println("\n=== 2. Passive Registration ===")
	err := engine.Register(ctx, discovery.RegisterRequest{
		Name:     "my-custom-mcp",
		Endpoint: "/usr/local/bin/my-custom-mcp",
		Tags:     []string{"capability:analytics", "domain:business"},
		Metadata: map[string]string{"team": "platform", "env": "prod"},
	})
	if err != nil {
		fmt.Printf("  register error: %v\n", err)
	} else {
		fmt.Println("  ✓ Registered my-custom-mcp")
	}

	// ── 3. Tag Management ─────────────────────────────────
	fmt.Println("\n=== 3. Tag Management ===")
	err = engine.UpdateTags(ctx, "my-custom-mcp", discovery.UpdateTagsRequest{
		Add:    []string{"capability:export", "priority:high"},
		Remove: []string{"domain:business"},
	})
	if err != nil {
		fmt.Printf("  update tags error: %v\n", err)
	} else {
		fmt.Println("  ✓ Updated tags on my-custom-mcp")
	}

	// ── 4. Health Check ───────────────────────────────────
	fmt.Println("\n=== 4. Health Check ===")
	_ = engine.CheckHealth(ctx)

	// ── 5. List All Services ──────────────────────────────
	fmt.Println("\n=== 5. All Services ===")
	services, _ = engine.List(ctx)
	for _, svc := range services {
		conf := bestConfidence(svc)
		healthIcon := "✗"
		healthMsg := "unchecked"
		if svc.CheckedAt != nil {
			if svc.Healthy {
				healthIcon = "✓"
			}
			healthMsg = svc.HealthMsg
		}

		fmt.Printf("\n  %s %s\n", healthIcon, svc.Identity.Name)
		fmt.Printf("    endpoint:    %s\n", svc.Identity.Endpoint)
		fmt.Printf("    confidence:  %d%%\n", conf)
		fmt.Printf("    sources:     %s\n", sourceList(svc))
		fmt.Printf("    health:      %s\n", healthMsg)
		if len(svc.Identity.Tags) > 0 {
			fmt.Printf("    tags:        %v\n", svc.Identity.Tags)
		}
		if len(svc.Identity.Metadata) > 0 {
			fmt.Printf("    metadata:    %v\n", svc.Identity.Metadata)
		}
	}

	// ── 6. Unregister ─────────────────────────────────────
	fmt.Println("\n=== 6. Unregister ===")
	_ = engine.Unregister(ctx, "my-custom-mcp")
	fmt.Println("  ✓ Unregistered my-custom-mcp")

	services, _ = engine.List(ctx)
	fmt.Printf("  Remaining: %d services\n", len(services))
}

func bestConfidence(svc *discovery.DiscoveredService) discovery.Confidence {
	var best discovery.Confidence
	for _, r := range svc.Records {
		if r.Confidence > best {
			best = r.Confidence
		}
	}
	return best
}

func sourceList(svc *discovery.DiscoveredService) string {
	sources := make([]string, 0, len(svc.Records))
	seen := make(map[string]bool)
	for _, r := range svc.Records {
		if !seen[r.Source] {
			seen[r.Source] = true
			sources = append(sources, fmt.Sprintf("%s(%d%%)", r.Source, r.Confidence))
		}
	}
	result := ""
	for i, s := range sources {
		if i > 0 {
			result += ", "
		}
		result += s
	}
	return result
}
