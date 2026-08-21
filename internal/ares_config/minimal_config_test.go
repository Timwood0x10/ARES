package ares_config

import (
	"os"
	"testing"
)

// TestNewMinimalConfig_OpenAICompatible verifies a minimal config with only an
// LLM endpoint + API key produces a fully-defaulted, runnable config:
// provider is inferred as OpenAI-compatible, memory is enabled (leader
// requirement), and all other subsystems carry defaults.
func TestNewMinimalConfig_OpenAICompatible(t *testing.T) {
	cfg := NewMinimalConfig("https://api.example.com/v1", "sk-test", "")

	if cfg.LLM.Provider != providerOpenAI {
		t.Fatalf("api key must infer openai provider, got %q", cfg.LLM.Provider)
	}
	if cfg.LLM.BaseURL != "https://api.example.com/v1" {
		t.Fatalf("base url must be preserved, got %q", cfg.LLM.BaseURL)
	}
	if cfg.LLM.APIKey != "sk-test" {
		t.Fatalf("api key must be preserved, got %q", cfg.LLM.APIKey)
	}
	if cfg.LLM.Model == "" {
		t.Fatal("model must fall back to a provider default, not stay empty")
	}
	if !cfg.Memory.IsEnabled() {
		t.Fatal("memory must be enabled (kernel scheduler requires a MemoryManager)")
	}
	if cfg.Server.Port != 8080 {
		t.Fatalf("server port must default to 8080, got %d", cfg.Server.Port)
	}
	// Storage defaults to postgres type but stays disabled → Bootstrap uses
	// in-memory storage (no external DB required for a minimal run).
	if cfg.Storage.Enabled {
		t.Fatal("storage must stay disabled in minimal mode (in-memory fallback)")
	}
	// A default agent population is assembled so the runtime can divide tasks
	// even with no config file.
	if len(cfg.Agents.Sub) < 3 {
		t.Fatalf("minimal config must assemble default sub agents, got %d", len(cfg.Agents.Sub))
	}
}

// TestNewMinimalConfig_Ollama verifies the no-API-key case infers the ollama
// provider (local endpoint).
func TestNewMinimalConfig_Ollama(t *testing.T) {
	cfg := NewMinimalConfig("http://localhost:11434", "", "")

	if cfg.LLM.Provider != defaultLLMProvider {
		t.Fatalf("no api key must infer ollama provider, got %q", cfg.LLM.Provider)
	}
	if cfg.LLM.Model != defaultLLMModel {
		t.Fatalf("ollama default model must be %q, got %q", defaultLLMModel, cfg.LLM.Model)
	}
	if !cfg.Memory.IsEnabled() {
		t.Fatal("memory must be enabled")
	}
}

// TestNewMinimalConfig_ExplicitModel verifies an explicit model is honored.
func TestNewMinimalConfig_ExplicitModel(t *testing.T) {
	cfg := NewMinimalConfig("https://api.example.com/v1", "sk-test", "my-model")

	if cfg.LLM.Model != "my-model" {
		t.Fatalf("explicit model must be honored, got %q", cfg.LLM.Model)
	}
}

// TestTinyConfigFileMemoryDefaultsOn verifies the "thin YAML" path: a config
// file that only declares the LLM endpoint (no memory section) loads with
// memory enabled by default — the runtime's automatic assembly means a user
// does not need to write memory.enabled or any other subsystem section.
func TestTinyConfigFileMemoryDefaultsOn(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/tiny.yaml"
	content := "llm:\n  provider: openai\n  base_url: https://api.example.com/v1\n  api_key: sk-test\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load tiny config: %v", err)
	}
	if !cfg.Memory.IsEnabled() {
		t.Fatal("memory must default to enabled in a thin config (leader contract)")
	}
	if cfg.LLM.Provider != "openai" || cfg.LLM.APIKey != "sk-test" {
		t.Fatalf("llm section must be preserved: %+v", cfg.LLM)
	}
	// The serve entry point's memory requirement is satisfied without any
	// memory section in the YAML.
	if cfg.Memory.IsEnabled() != true {
		t.Fatal("IsEnabled must be true after Load")
	}
}

// TestExplicitMemoryDisabled verifies a user can still opt out explicitly:
// `memory.enabled: false` is honored and IsEnabled returns false.
func TestExplicitMemoryDisabled(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/memoff.yaml"
	content := "llm:\n  provider: ollama\nmemory:\n  enabled: false\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Memory.IsEnabled() {
		t.Fatal("explicit memory.enabled: false must be honored")
	}
}
