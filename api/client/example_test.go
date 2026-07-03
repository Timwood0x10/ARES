package client_test

import (
	"context"
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/api/client"
	"github.com/Timwood0x10/ares/api/core"
)

// stubLLM implements core.LLMService for demonstration purposes.
type stubLLM struct{}

func (s *stubLLM) Generate(_ context.Context, _ *core.GenerateRequest) (*core.GenerateResponse, error) {
	return &core.GenerateResponse{Content: "Hello from ARES!"}, nil
}
func (s *stubLLM) GenerateSimple(_ context.Context, prompt string) (string, error) {
	return "Hello, " + prompt, nil
}
func (s *stubLLM) GenerateEmbedding(_ context.Context, _ *core.EmbeddingRequest) (*core.EmbeddingResponse, error) {
	return &core.EmbeddingResponse{Embedding: []float32{0.1, 0.2, 0.3}}, nil
}
func (s *stubLLM) GetConfig() *core.LLMConfig    { return &core.LLMConfig{Provider: "ollama"} }
func (s *stubLLM) IsEnabled() bool               { return true }
func (s *stubLLM) GetProvider() core.LLMProvider { return "ollama" }
func (s *stubLLM) GetModel() string              { return "llama3.2" }

// Example_basic demonstrates creating a client with a pre-built LLM service.
func Example_basic() {
	cl, err := client.NewClient(&client.Config{
		BaseConfig: &core.BaseConfig{RequestTimeout: 30 * time.Second},
		LLM:        &stubLLM{},
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	svc, err := cl.LLM()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	resp, err := svc.GenerateSimple(context.Background(), "ARES")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Println(resp)
	// Output: Hello, ARES
}
