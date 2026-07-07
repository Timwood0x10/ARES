// Command embedding-mcp provides an MCP server that exposes text embedding
// capabilities via the MCP protocol (stdio transport). It wraps the Python
// embedding service so any MCP client can generate vector embeddings.
//
// Usage:
//
//	embedding-mcp serve                          # stdio (default)
//	embedding-mcp serve --sse :8080              # SSE on port 8080
//	embedding-mcp serve --embedding-url http://localhost:8000  # custom embedding service URL
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_mcp"
	"golang.org/x/sync/errgroup"
)

// EmbeddingRequest matches the Python embedding service POST /embed body.
type EmbeddingRequest struct {
	Text   string `json:"text"`
	Prefix string `json:"prefix,omitempty"`
}

// EmbeddingResponse matches the Python embedding service response.
type EmbeddingResponse struct {
	Embedding []float64 `json:"embedding"`
	Dimension int       `json:"dimension"`
	Cached    bool      `json:"cached"`
}

// BatchEmbeddingRequest matches the Python embedding service POST /embed_batch body.
type BatchEmbeddingRequest struct {
	Texts  []string `json:"texts"`
	Prefix string   `json:"prefix,omitempty"`
}

// BatchEmbeddingResponse matches the Python embedding service response.
type BatchEmbeddingResponse struct {
	Embeddings  [][]float64 `json:"embeddings"`
	Dimension   int         `json:"dimension"`
	CachedCount int         `json:"cached_count"`
}

func main() {
	serveCmd := flag.NewFlagSet("serve", flag.ExitOnError)
	sseAddr := serveCmd.String("sse", "", "Enable SSE transport on given address (e.g. :8080)")
	embeddingURL := serveCmd.String("embedding-url", "http://localhost:8000", "Embedding service base URL")

	if len(os.Args) < 2 || os.Args[1] != "serve" {
		fmt.Fprintf(os.Stderr, "Usage: embedding-mcp serve [--sse :8080] [--embedding-url http://localhost:8000]\n")
		os.Exit(1)
	}
	_ = serveCmd.Parse(os.Args[2:])

	client := &http.Client{Timeout: 30 * time.Second}

	// Create MCP server with stdio or SSE transport.
	var transport ares_mcp.ServerTransport
	if *sseAddr != "" {
		transport = ares_mcp.NewSSEServerTransport(*sseAddr)
	} else {
		transport = ares_mcp.NewStdioServerTransport()
	}

	server := ares_mcp.NewMCPServer(
		ares_mcp.Implementation{Name: "embedding-mcp", Version: "1.0.0"},
		transport,
	)

	// Register embed tool.
	embedSchema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"text": {"type": "string", "description": "Text to embed"},
			"prefix": {"type": "string", "description": "Optional prefix: 'query:' for search (default), 'passage:' for documents"}
		},
		"required": ["text"]
	}`)
	if err := server.RegisterTool("embed", "Generate a vector embedding for a single text using the embedding service (e5-large, 1024 dimensions)", embedSchema,
		func(ctx context.Context, args map[string]any) (*ares_mcp.ToolCallResult, error) {
			text, _ := args["text"].(string)
			if text == "" {
				return errorResult("'text' is required"), nil
			}
			prefix, _ := args["prefix"].(string)
			if prefix == "" {
				prefix = "query:"
			}

			reqBody, err := json.Marshal(EmbeddingRequest{Text: text, Prefix: prefix})
			if err != nil {
				return errorResult(fmt.Sprintf("marshal: %v", err)), nil
			}

			resp, err := client.Post(*embeddingURL+"/embed", "application/json", bytes.NewReader(reqBody)) //nolint:noctx
			if err != nil {
				return errorResult(fmt.Sprintf("embedding service unreachable: %v", err)), nil
			}
			defer func() { _ = resp.Body.Close() }()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return errorResult(fmt.Sprintf("read response: %v", err)), nil
			}

			var embedResp EmbeddingResponse
			if err := json.Unmarshal(body, &embedResp); err != nil {
				return errorResult(fmt.Sprintf("parse response: %v", err)), nil
			}

			resultData, _ := json.Marshal(map[string]interface{}{
				"text":      text,
				"dimension": embedResp.Dimension,
				"cached":    embedResp.Cached,
				"vector":    embedResp.Embedding,
			})
			return &ares_mcp.ToolCallResult{
				Content: []ares_mcp.ContentBlock{{Type: "text", Text: string(resultData)}},
			}, nil
		}); err != nil {
		log.Error("failed to register embed tool", "error", err)
		os.Exit(1)
	}

	// Register embed_batch tool.
	batchSchema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"texts": {"type": "array", "items": {"type": "string"}, "description": "Array of texts to embed"},
			"prefix": {"type": "string", "description": "Optional prefix: 'passage:' for documents (default), 'query:' for search"}
		},
		"required": ["texts"]
	}`)
	if err := server.RegisterTool("embed_batch", "Generate vector embeddings for multiple texts in batch", batchSchema,
		func(ctx context.Context, args map[string]any) (*ares_mcp.ToolCallResult, error) {
			textsRaw, ok := args["texts"].([]interface{})
			if !ok || len(textsRaw) == 0 {
				return errorResult("'texts' (array) is required"), nil
			}
			texts := make([]string, len(textsRaw))
			for i, v := range textsRaw {
				texts[i], _ = v.(string)
			}
			prefix, _ := args["prefix"].(string)
			if prefix == "" {
				prefix = "passage:"
			}

			reqBody, err := json.Marshal(BatchEmbeddingRequest{Texts: texts, Prefix: prefix})
			if err != nil {
				return errorResult(fmt.Sprintf("marshal: %v", err)), nil
			}

			resp, err := client.Post(*embeddingURL+"/embed_batch", "application/json", bytes.NewReader(reqBody)) //nolint:noctx
			if err != nil {
				return errorResult(fmt.Sprintf("batch embedding service unreachable: %v", err)), nil
			}
			defer func() { _ = resp.Body.Close() }()

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return errorResult(fmt.Sprintf("read response: %v", err)), nil
			}

			var batchResp BatchEmbeddingResponse
			if err := json.Unmarshal(body, &batchResp); err != nil {
				return errorResult(fmt.Sprintf("parse response: %v", err)), nil
			}

			resultData, _ := json.Marshal(map[string]interface{}{
				"count":     len(batchResp.Embeddings),
				"dimension": batchResp.Dimension,
				"cached":    batchResp.CachedCount,
			})
			return &ares_mcp.ToolCallResult{
				Content: []ares_mcp.ContentBlock{{Type: "text", Text: string(resultData)}},
			}, nil
		}); err != nil {
		log.Error("failed to register embed_batch tool", "error", err)
		os.Exit(1)
	}

	// Register health tool.
	healthSchema := json.RawMessage(`{
		"type": "object",
		"properties": {},
		"required": []
	}`)
	if err := server.RegisterTool("health", "Check if the embedding service is healthy", healthSchema,
		func(ctx context.Context, args map[string]any) (*ares_mcp.ToolCallResult, error) {
			resp, err := client.Get(*embeddingURL + "/health") //nolint:noctx
			if err != nil {
				return errorResult(fmt.Sprintf("embedding service unhealthy: %v", err)), nil
			}
			defer func() { _ = resp.Body.Close() }()

			body, _ := io.ReadAll(resp.Body)
			return &ares_mcp.ToolCallResult{
				Content: []ares_mcp.ContentBlock{{Type: "text", Text: string(body)}},
			}, nil
		}); err != nil {
		log.Error("failed to register health tool", "error", err)
		os.Exit(1)
	}

	// Set up signal handling.
	ctx, cancel := context.WithCancel(context.Background())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	sigEg, sigCtx := errgroup.WithContext(ctx)
	sigEg.Go(func() error {
		select {
		case <-sigCh:
			log.Info("embedding-mcp: shutting down...")
			cancel()
		case <-sigCtx.Done():
		}
		return nil
	})

	log.Info("embedding-mcp: starting", "transport", func() string {
		if *sseAddr != "" {
			return "sse:" + *sseAddr
		}
		return "stdio"
	}(), "embedding_url", *embeddingURL)

	if err := server.Serve(ctx); err != nil {
		cancel()
		log.Error("embedding-mcp: serve error", "error", err)
		os.Exit(1)
	}
	defer cancel()
	_ = sigEg.Wait()
}

func errorResult(msg string) *ares_mcp.ToolCallResult {
	return &ares_mcp.ToolCallResult{
		IsError: true,
		Content: []ares_mcp.ContentBlock{{Type: "text", Text: msg}},
	}
}
