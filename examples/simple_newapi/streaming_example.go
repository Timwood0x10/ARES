//go:build ignore

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"

	"github.com/Timwood0x10/ares/api/client"
)

func main() {
	log.Println("=== GoAgent Streaming Example ===")

	// Step 1: Load config and create client
	goagentClient, err := client.NewClientFromDefaultPath()
	if err != nil {
		slog.Error("Failed to create client", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := goagentClient.Close(context.Background()); err != nil {
			slog.Error("Failed to close GoAgent client", "error", err)
		}
	}()

	// Step 2: Create workflow client
	workflowClient, err := client.NewWorkflowClient(goagentClient)
	if err != nil {
		slog.Error("Failed to create workflow client", "error", err)
		os.Exit(1)
	}

	// User query
	userQuery := "Analyze the latest tech trends in AI and cloud computing"
	log.Printf("\n=== User Query ===\n%s\n", userQuery)

	// Step 3: Execute with streaming
	log.Println("\n=== Streaming Execution ===")

	// Create streaming request
	reqBody := map[string]string{
		"query": userQuery,
	}
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		slog.Error("Failed to marshal request", "error", err)
		os.Exit(1)
	}

	// For this example, we'll demonstrate the streaming API pattern
	// In a real scenario, you would connect to a running server
	log.Println("Streaming API endpoint: POST /api/v1/stream")
	log.Println("Request body:", string(jsonData))

	// Demonstrate how to consume SSE events
	log.Println("\n=== How to Consume SSE Events ===")
	log.Println("1. Connect to: POST /api/v1/stream")
	log.Println("2. Set headers: Content-Type: application/json")
	log.Println("3. Send request body with 'query' field")
	log.Println("4. Read SSE events from response body")
	log.Println("")
	log.Println("Event types:")
	log.Println("  - planning: Agent is planning tasks")
	log.Println("  - task_start: A task has started")
	log.Println("  - task_progress: Progress update on a task")
	log.Println("  - task_complete: A task has completed")
	log.Println("  - aggregating: Agent is aggregating results")
	log.Println("  - complete: Processing complete")
	log.Println("  - done: Stream finished")

	// Example of how to read SSE events
	log.Println("\n=== Example SSE Client Code ===")
	fmt.Println(`
// Create HTTP client and send request
resp, err := http.Post("http://localhost:8080/api/v1/stream",
    "application/json", bytes.NewReader(jsonData))
if err != nil {
    log.Fatal(err)
}
defer resp.Body.Close()

// Read SSE events
scanner := bufio.NewScanner(resp.Body)
for scanner.Scan() {
    line := scanner.Text()

    // Parse SSE format
    if strings.HasPrefix(line, "event: ") {
        event := strings.TrimPrefix(line, "event: ")
        scanner.Scan() // Read data line
        data := strings.TrimPrefix(scanner.Text(), "data: ")

        // Handle event
        switch event {
        case "planning":
            fmt.Println("Planning:", data)
        case "task_start":
            fmt.Println("Task started:", data)
        case "task_complete":
            fmt.Println("Task completed:", data)
        case "complete":
            fmt.Println("Result:", data)
        case "done":
            return
        }
    }
}
`)

	// Step 4: Also show non-streaming execution for comparison
	log.Println("\n=== Non-Streaming Execution (for comparison) ===")
	workflowPath := "config/workflow.yaml"

	result, err := workflowClient.ExecuteFromFile(context.Background(), workflowPath, userQuery)
	if err != nil {
		slog.Error("Failed to execute workflow", "error", err)
		os.Exit(1)
	}

	log.Printf("Execution ID: %s", result.ExecutionID)
	log.Printf("Status: %s", result.Status)
	log.Printf("Duration: %v", result.Duration)

	log.Println("\n=== Done! ===")
}

// consumeSSE demonstrates how to consume SSE events from a response.
// This is a helper function showing the pattern.
func consumeSSE(resp *http.Response) {
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		// SSE format: "event: <type>" followed by "data: <json>"
		if len(line) > 7 && line[:7] == "event: " {
			event := line[7:]
			scanner.Scan()
			dataLine := scanner.Text()

			if len(dataLine) > 6 && dataLine[:6] == "data: " {
				data := dataLine[6:]

				var resp struct {
					Event string `json:"event"`
					Data  any    `json:"data"`
					Error string `json:"error,omitempty"`
				}
				if err := json.Unmarshal([]byte(data), &resp); err == nil {
					log.Printf("Event: %s, Data: %v", event, resp.Data)
					if resp.Error != "" {
						log.Printf("Error: %s", resp.Error)
					}
				}
			}
		}
	}
}
