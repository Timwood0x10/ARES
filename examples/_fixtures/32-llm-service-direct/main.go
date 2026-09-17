// LLM service direct — access the LLM without the agent loop.
//
// Purpose:
//
//	Demonstrate direct LLM access for custom workflows that don't need the
//	full agent loop. Use LLMService for simple completions, embeddings, or
//	integrating ARES's LLM capabilities into existing applications.
//
// Learning objectives (what this example teaches you):
//   - How to create a standalone LLMService with NewLLMService.
//   - How to use GenerateSimple for quick text completions.
//   - How to use Generate for structured requests with messages.
//   - How to generate embeddings with GenerateEmbedding.
//   - When to use LLMService vs Agent.
//
// Core APIs used (package path → symbol):
//   - github.com/Timwood0x10/ares/api.NewLLMService
//   - github.com/Timwood0x10/ares/api.LLMServiceConfig
//   - github.com/Timwood0x10/ares/api.LLMConfig
//   - github.com/Timwood0x10/ares/api.GenerateRequest
//   - github.com/Timwood0x10/ares/api.LLMMessage
//   - github.com/Timwood0x10/ares/api.EmbeddingRequest
//
// Run:
//
//	go run examples/_fixtures/32-llm-service-direct/main.go
//
// Expected output:
//
//	=== LLM Service Direct Access ===
//
//	[1/3] Simple Completion
//	   Prompt: "What is 2+2?"
//	   Response: "4"
//
//	[2/3] Structured Request
//	   Messages: 2 (system + user)
//	   Response: <completion>
//
//	[3/3] Embeddings
//	   Input: "Hello, world!"
//	   Embedding: [0.123, -0.456, ...] (dimension: 768)
package main

import (
	"context"
	"fmt"
	"os"

	api "github.com/Timwood0x10/ares/api"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	fmt.Println("=== LLM Service Direct Access ===")
	fmt.Println()

	// ── Create LLM Service ──
	llmCfg := &api.LLMConfig{
		Provider: api.LLMProviderOllama,
		Model:    "llama3.2",
	}

	svc, err := api.NewLLMService(&api.LLMServiceConfig{
		LLMConfig: llmCfg,
	})
	if err != nil {
		return fmt.Errorf("create LLM service: %w", err)
	}
	defer svc.Close()

	// ── 1. Simple Completion ──
	fmt.Println("[1/3] Simple Completion")
	prompt := "What is 2+2? Answer with just the number."
	fmt.Printf("   Prompt: %q\n", prompt)

	response, err := svc.GenerateSimple(ctx, prompt)
	if err != nil {
		fmt.Printf("   ⚠️  Failed (expected without Ollama): %v\n", err)
	} else {
		fmt.Printf("   Response: %q\n", response)
	}
	fmt.Println()

	// ── 2. Structured Request ──
	fmt.Println("[2/3] Structured Request")
	messages := []*api.LLMMessage{
		{Role: "system", Content: "You are a helpful assistant. Be concise."},
		{Role: "user", Content: "What is the capital of France?"},
	}

	req := &api.GenerateRequest{
		Messages: messages,
	}

	genResp, err := svc.Generate(ctx, req)
	if err != nil {
		fmt.Printf("   ⚠️  Failed (expected without Ollama): %v\n", err)
	} else {
		fmt.Printf("   Messages: %d (system + user)\n", len(messages))
		fmt.Printf("   Response: %q\n", truncate(genResp.Content, 50))
	}
	fmt.Println()

	// ── 3. Embeddings ──
	fmt.Println("[3/3] Embeddings")
	embedReq := &api.EmbeddingRequest{
		Input: "Hello, world!",
	}

	embedResp, err := svc.GenerateEmbedding(ctx, embedReq)
	if err != nil {
		fmt.Printf("   ⚠️  Failed (expected without embedding service): %v\n", err)
	} else {
		fmt.Printf("   Input: %q\n", embedReq.Input)
		fmt.Printf("   Embedding: [%0.3f, %0.3f, ...] (dimension: %d)\n",
			embedResp.Embedding[0], embedResp.Embedding[1], len(embedResp.Embedding))
	}
	fmt.Println()

	fmt.Println("=== Demo Complete ===")
	fmt.Println()
	fmt.Println("Note: LLMService is ideal for:")
	fmt.Println("  - Simple completions without tool loops")
	fmt.Println("  - Embedding generation for custom RAG pipelines")
	fmt.Println("  - Integrating ARES LLM into existing applications")
	fmt.Println("  - Batch processing without agent overhead")
	return nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
