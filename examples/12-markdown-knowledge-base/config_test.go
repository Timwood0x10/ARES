package main

import (
	"os"
	"path/filepath"
	"testing"
)

// writeTempConfig writes content to a temp config file and returns its path.
func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

func TestLoadConfigAppliesDefaults(t *testing.T) {
	path := writeTempConfig(t, "database:\n  password: pw\n")

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Database.Port != 5433 {
		t.Errorf("default port = %d, want 5433", cfg.Database.Port)
	}
	if cfg.Embedding.Dimensions != expectedVectorDim {
		t.Errorf("default dimensions = %d, want %d", cfg.Embedding.Dimensions, expectedVectorDim)
	}
	if cfg.Knowledge.ChunkSize != 1500 {
		t.Errorf("default chunk_size = %d, want 1500", cfg.Knowledge.ChunkSize)
	}
	if cfg.Knowledge.PassagePrefix != "passage:" {
		t.Errorf("default passage_prefix = %q", cfg.Knowledge.PassagePrefix)
	}
}

func TestLoadConfigRejectsBadDimensions(t *testing.T) {
	path := writeTempConfig(t, "embedding:\n  dimensions: 768\n")

	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected error for mismatched dimensions, got nil")
	}
}

func TestLoadConfigRejectsBadOverlap(t *testing.T) {
	path := writeTempConfig(t, "knowledge:\n  chunk_size: 500\n  chunk_overlap: 800\n")

	if _, err := LoadConfig(path); err == nil {
		t.Fatal("expected error for overlap >= chunk_size, got nil")
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	if _, err := LoadConfig(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestLLMEnabled(t *testing.T) {
	cfg := &Config{}
	if cfg.llmEnabled() {
		t.Error("empty config should not enable LLM")
	}
	cfg.LLM.Provider = "ollama"
	cfg.LLM.Model = "llama3.2"
	if !cfg.llmEnabled() {
		t.Error("provider+model should enable LLM")
	}
}
