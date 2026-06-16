// Command mcp-null provides a minimal MCP server for demos that don't need
// external MCP tools. It responds to initialize, tools/list, and tools/call
// with empty/fixed responses, satisfying the service startup requirement.
//
// Usage: mcp-null serve
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
)

// JSON-RPC 2.0 message.
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func main() {
	if len(os.Args) < 2 || os.Args[1] != "serve" {
		fmt.Fprintf(os.Stderr, "Usage: mcp-null serve\n")
		os.Exit(1)
	}
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var msg rpcMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}

		if len(msg.ID) == 0 {
			// Notification, no response needed.
			continue
		}

		var result json.RawMessage

		switch msg.Method {
		case "initialize":
			result = json.RawMessage(`{
				"protocolVersion": "2024-11-05",
				"capabilities": {
					"tools": {}
				},
				"serverInfo": {
					"name": "mcp-null",
					"version": "1.0.0"
				}
			}`)

		case "tools/list":
			result = json.RawMessage(`{
				"tools": [
					{
						"name": "echo",
						"description": "Echoes back the input (no-op for demos)",
						"inputSchema": {
							"type": "object",
							"properties": {
								"message": {"type": "string"}
							},
							"required": ["message"]
						}
					}
				]
			}`)

		case "tools/call":
			result = json.RawMessage(`{
				"content": [
					{
						"type": "text",
						"text": "mcp-null: no-op response"
					}
				]
			}`)

		default:
			writeError(msg.ID, -32601, "Method not found: "+msg.Method)
			continue
		}

		resp := rpcMessage{
			JSONRPC: "2.0",
			ID:      msg.ID,
			Result:  result,
		}
		data, _ := json.Marshal(resp)
		fmt.Println(string(data))
	}

	if err := scanner.Err(); err != nil {
		log.Printf("stdin error: %v", err)
		os.Exit(1)
	}
}

func writeError(id json.RawMessage, code int, message string) {
	resp := rpcMessage{
		JSONRPC: "2.0",
		ID:      id,
		Error: &rpcError{
			Code:    code,
			Message: message,
		},
	}
	data, _ := json.Marshal(resp)
	fmt.Println(string(data))
}
