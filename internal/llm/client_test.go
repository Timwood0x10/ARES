// Package llm provides tests for LLM client functionality.
package llm

import (
	"context"
	"testing"
	"time"
)

func TestNewClient(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
	}{
		{
			name: "valid config",
			config: &Config{
				Provider: "ollama",
				BaseURL:  "http://localhost:11434",
				Model:    "llama3",
				Timeout:  30,
			},
			wantErr: false,
		},
		{
			name:    "nil config",
			config:  nil,
			wantErr: true,
		},
		{
			name: "invalid timeout",
			config: &Config{
				Provider: "ollama",
				BaseURL:  "http://localhost:11434",
				Model:    "llama3",
				Timeout:  -1,
			},
			wantErr: false, // Should use default timeout
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := NewClient(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewClient() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && client == nil {
				t.Error("NewClient() returned nil client")
			}
		})
	}
}

func TestClient_IsEnabled(t *testing.T) {
	tests := []struct {
		name     string
		config   *Config
		expected bool
	}{
		{
			name: "OpenRouter with API key",
			config: &Config{
				Provider: "openrouter",
				APIKey:   "test-key",
				BaseURL:  "https://openrouter.ai/api/v1",
				Model:    "minimax/minimax-m2-her",
			},
			expected: true,
		},
		{
			name: "OpenRouter without API key",
			config: &Config{
				Provider: "openrouter",
				BaseURL:  "https://openrouter.ai/api/v1",
				Model:    "minimax/minimax-m2-her",
			},
			expected: false,
		},
		{
			name: "Ollama",
			config: &Config{
				Provider: "ollama",
				BaseURL:  "http://localhost:11434",
				Model:    "llama3",
			},
			expected: true,
		},
		{
			name:     "nil config",
			config:   nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, _ := NewClient(tt.config)
			if got := client.IsEnabled(); got != tt.expected {
				t.Errorf("Client.IsEnabled() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestClient_GetProvider(t *testing.T) {
	config := &Config{
		Provider: "openrouter",
		BaseURL:  "https://openrouter.ai/api/v1",
		Model:    "minimax/minimax-m2-her",
	}

	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	if got := client.GetProvider(); got != "openrouter" {
		t.Errorf("Client.GetProvider() = %v, want openrouter", got)
	}
}

func TestClient_GetModel(t *testing.T) {
	config := &Config{
		Provider: "ollama",
		BaseURL:  "http://localhost:11434",
		Model:    "llama3",
	}

	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	if got := client.GetModel(); got != "llama3" {
		t.Errorf("Client.GetModel() = %v, want llama3", got)
	}
}

func TestClient_Generate(t *testing.T) {
	// Skip test if LLM is not configured
	config := &Config{
		Provider: "ollama",
		BaseURL:  "http://localhost:11434",
		Model:    "llama3",
		Timeout:  10,
	}

	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	// Check if Ollama is running
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if !client.IsEnabled() {
		t.Skip("LLM client is not enabled")
		return
	}

	// Simple generation test
	response, err := client.Generate(ctx, "Say 'hello' in one word.")
	if err != nil {
		t.Logf("LLM generate test skipped (LLM not available): %v", err)
		t.SkipNow()
		return
	}

	if response == "" {
		t.Error("Generate() returned empty response")
	}

	t.Logf("LLM response: %s", response)
}

func TestClient_GenerateWithTimeout(t *testing.T) {
	config := &Config{
		Provider: "ollama",
		BaseURL:  "http://localhost:11434",
		Model:    "llama3",
		Timeout:  1, // Very short timeout
	}

	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	if !client.IsEnabled() {
		t.Skip("LLM client is not enabled")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err = client.Generate(ctx, "This is a very long prompt that should timeout...")
	if err != nil {
		// Expected to timeout
		t.Logf("Timeout test: %v", err)
	}
}

func TestClient_GenerateEmptyPrompt(t *testing.T) {
	config := &Config{
		Provider: "ollama",
		BaseURL:  "http://localhost:11434",
		Model:    "llama3",
		Timeout:  10,
	}

	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	if !client.IsEnabled() {
		t.Skip("LLM client is not enabled")
		return
	}

	ctx := context.Background()
	_, err = client.Generate(ctx, "")
	if err != nil {
		t.Logf("Empty prompt test: %v", err)
	}
}

func TestNewClientWithEmptyBaseURL(t *testing.T) {
	config := &Config{
		Provider: "ollama",
		BaseURL:  "",
		Model:    "llama3",
	}

	client, err := NewClient(config)
	if err != nil {
		t.Logf("Empty base URL test: %v", err)
	}

	if client != nil {
		t.Log("Client created with empty base URL")
	}
}

func TestNewClientWithEmptyModel(t *testing.T) {
	config := &Config{
		Provider: "ollama",
		BaseURL:  "http://localhost:11434",
		Model:    "",
	}

	client, err := NewClient(config)
	if err != nil {
		t.Logf("Empty model test: %v", err)
	}

	if client != nil {
		t.Log("Client created with empty model")
	}
}

func TestNewClientWithEmptyProvider(t *testing.T) {
	config := &Config{
		Provider: "",
		BaseURL:  "http://localhost:11434",
		Model:    "llama3",
	}

	client, err := NewClient(config)
	if err != nil {
		t.Logf("Empty provider test: %v", err)
	}

	if client != nil {
		t.Log("Client created with empty provider")
	}
}
