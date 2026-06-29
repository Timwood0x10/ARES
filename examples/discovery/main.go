// discovery demonstrates the Service Discovery Engine.
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

	engine := discovery.NewEngine("", nil)

	// Event handler — events can be persisted to any DB.
	engine.OnEvent(func(evt discovery.Event) {
		fmt.Printf("  [event] %-25s service=%-30s source=%s\n",
			evt.Type, evt.ServiceID, evt.Source)
	})

	fmt.Println("=== Discovery ===")
	if err := engine.DiscoverNow(ctx); err != nil {
		fmt.Printf("error: %v\n", err)
		return
	}

	services, _ := engine.List(ctx)
	fmt.Printf("\n=== %d Services Found ===\n", len(services))
	for _, svc := range services {
		conf := bestConfidence(svc)
		sources := sourceList(svc)

		fmt.Printf("\n  %s\n", svc.Identity.Name)
		fmt.Printf("    endpoint:    %s\n", svc.Identity.Endpoint)
		fmt.Printf("    confidence:  %d%%\n", conf)
		fmt.Printf("    sources:     %s\n", sources)
		fmt.Printf("    records:     %d\n", len(svc.Records))
		if len(svc.Identity.Tags) > 0 {
			fmt.Printf("    tags:        %v\n", svc.Identity.Tags)
		}
	}
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
