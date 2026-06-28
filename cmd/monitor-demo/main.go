package main

import (
	"context"
	"fmt"
	"log"

	"github.com/Timwood0x10/ares/internal/monitoring"
)

func main() {
	// Create the monitoring plugin
	plugin := monitoring.NewConsole().(*monitoring.MonitorPlugin)

	// Start without EventBus (no real-time events, just HTTP API)
	ctx := context.Background()
	if err := plugin.Start(ctx, nil); err != nil {
		log.Printf("Warning: Start with nil bus: %v", err)
	}

	// Create Gin HTTP server
	server := monitoring.NewHTTPServer(plugin)

	// Start the server
	addr := ":9090"
	fmt.Println("=== ARES Console ===")
	fmt.Printf("Listening on %s\n\n", addr)
	fmt.Println("Endpoints:")
	fmt.Println("  GET  /api/console        - Full snapshot")
	fmt.Println("  GET  /api/console/dag    - DAG topology")
	fmt.Println("  GET  /api/agents         - List agents")
	fmt.Println("  GET  /api/agents/:id     - Agent detail")
	fmt.Println("  POST /api/agents/:id/kill - Kill agent")
	fmt.Println("  GET  /api/mcp/tools      - MCP tools")
	fmt.Println("  GET  /api/cost           - Cost breakdown")
	fmt.Println("  GET  /api/subscribe      - SSE stream")
	fmt.Println()

	if err := server.Run(addr); err != nil {
		log.Fatal(err)
	}
}
