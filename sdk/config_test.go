package sdk

import (
	"os"
	"path/filepath"
	"testing"
)

// tmpConfigFile writes content to a temp YAML file and returns its path.
// The caller is responsible for removing the file via cleanup.
func tmpConfigFile(t *testing.T, content string) (path string, cleanup func()) {
	t.Helper()
	dir := t.TempDir()
	path = filepath.Join(dir, "ares.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("write tmp config: %v", err)
	}
	return path, func() { _ = os.Remove(path) }
}

func TestValidate_NilConfig(t *testing.T) {
	var cfg *ConfigFile
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for nil config")
	}
}

func TestValidate_InvalidTemperature(t *testing.T) {
	cfg := &ConfigFile{
		LLM: LLMFileConfig{Provider: "ollama", Temperature: 3.0},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for temperature out of range")
	}
}

func TestValidate_NegativeDistillationThresholdRejects(t *testing.T) {
	cfg := &ConfigFile{
		Memory: MemoryFileConfig{
			Enabled:               true,
			EnableDistillation:    true,
			DistillationThreshold: -1,
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for negative distillation threshold")
	}
}

func TestValidate_ThresholdZeroFallsBackOK(t *testing.T) {
	cfg := &ConfigFile{
		Memory: MemoryFileConfig{
			Enabled:               true,
			EnableDistillation:    true,
			DistillationThreshold: 0,
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected nil for threshold 0 (fall back to default), got: %v", err)
	}
}

func TestValidate_DistillationDisabledSkipsThreshold(t *testing.T) {
	cfg := &ConfigFile{
		Memory: MemoryFileConfig{
			Enabled:               true,
			EnableDistillation:    false,
			DistillationThreshold: 0,
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected nil when distillation disabled, got: %v", err)
	}
}

func TestValidate_InvalidKnowledgeOverlap(t *testing.T) {
	cfg := &ConfigFile{
		Knowledge: KnowledgeFileConfig{
			ChunkSize:    200,
			ChunkOverlap: 250,
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for chunk_overlap >= chunk_size")
	}
}

func TestValidate_InvalidMinScore(t *testing.T) {
	cfg := &ConfigFile{
		Knowledge: KnowledgeFileConfig{
			ChunkSize: 200,
			MinScore:  1.5,
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for min_score out of [0,1]")
	}
}

func TestValidate_EmptyConfigOK(t *testing.T) {
	cfg := &ConfigFile{}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected nil for empty config, got: %v", err)
	}
}

func TestLoadConfigFile_ValidateRejects(t *testing.T) {
	content := "memory:\n  enabled: true\n  enable_distillation: true\n  distillation_threshold: -5\n"
	path, cleanup := tmpConfigFile(t, content)
	defer cleanup()

	_, err := LoadConfigFile(path)
	if err == nil {
		t.Fatal("expected validation error for negative threshold")
	}
}

func TestLoadConfigFile_FullYaml(t *testing.T) {
	content := `llm:
  provider: ollama
  model: llama3.2:latest
database:
  host: 127.0.0.1
  port: 5433
embedding:
  service_url: http://localhost:8000
  model: qwen3-embedding:0.6b
memory:
  enabled: true
  max_history: 10
  max_sessions: 100
  enable_distillation: true
  distillation_threshold: 3
knowledge:
  chunk_size: 200
  chunk_overlap: 50
  top_k: 10
  min_score: 0.4
`
	path, cleanup := tmpConfigFile(t, content)
	defer cleanup()

	cfg, err := LoadConfigFile(path)
	if err != nil {
		t.Fatalf("LoadConfigFile error: %v", err)
	}
	if cfg.Memory.DistillationThreshold != 3 {
		t.Errorf("threshold = %v, want 3", cfg.Memory.DistillationThreshold)
	}
	if cfg.Database.Host != "127.0.0.1" {
		t.Errorf("db host = %v, want 127.0.0.1", cfg.Database.Host)
	}
	if cfg.Embedding.Model != "qwen3-embedding:0.6b" {
		t.Errorf("embedding model = %v, want qwen3-embedding:0.6b", cfg.Embedding.Model)
	}
	if cfg.Knowledge.ChunkSize != 200 {
		t.Errorf("chunk_size = %v, want 200", cfg.Knowledge.ChunkSize)
	}
}

func TestToOptions_MemoryDistillation(t *testing.T) {
	cfg := &ConfigFile{
		LLM: LLMFileConfig{Provider: "ollama"},
		Memory: MemoryFileConfig{
			Enabled:               true,
			EnableDistillation:    true,
			DistillationThreshold: 5,
		},
	}
	opts, err := cfg.ToOptions()
	if err != nil {
		t.Fatalf("ToOptions error: %v", err)
	}
	if len(opts) < 3 {
		t.Fatalf("expected at least 3 options (llm + memory + distillation), got %d", len(opts))
	}
}

func TestToOptions_DistillationThresholdZeroFallsBack(t *testing.T) {
	cfg := &ConfigFile{
		LLM: LLMFileConfig{Provider: "ollama"},
		Memory: MemoryFileConfig{
			Enabled:               true,
			EnableDistillation:    true,
			DistillationThreshold: 0,
		},
	}
	// ToOptions should succeed: threshold 0 is replaced by default at apply time.
	// Validate is bypassed here (ToOptions does not re-validate); the fallback
	// happens in the WithDistillation option constructor.
	opts, err := cfg.ToOptions()
	if err != nil {
		t.Fatalf("ToOptions error: %v", err)
	}
	_ = opts
}

func TestToOptions_DatabaseAndEmbedding(t *testing.T) {
	cfg := &ConfigFile{
		LLM:       LLMFileConfig{Provider: "ollama"},
		Database:  DatabaseFileConfig{Host: "127.0.0.1", Port: 5433},
		Embedding: EmbeddingFileConfig{ServiceURL: "http://localhost:8000", Model: "qwen3"},
	}
	opts, err := cfg.ToOptions()
	if err != nil {
		t.Fatalf("ToOptions error: %v", err)
	}
	if len(opts) < 3 {
		t.Fatalf("expected at least 3 options (llm + db + embedding), got %d", len(opts))
	}
}

func TestWithMemoryConfig_NegativeRejects(t *testing.T) {
	err := WithMemoryConfig(-1, 0)(&config{})
	if err == nil {
		t.Fatal("expected error for negative maxHistory")
	}
}

func TestWithDistillation_NegativeRejects(t *testing.T) {
	err := WithDistillation(-1)(&config{})
	if err == nil {
		t.Fatal("expected error for negative threshold")
	}
}

func TestWithEmbeddingService_MissingURLRejects(t *testing.T) {
	err := WithEmbeddingService("", "model")(&config{})
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
}

func TestWithPostgres_MissingHostRejects(t *testing.T) {
	err := WithPostgres(DatabaseFileConfig{Host: "", Port: 5433})(&config{})
	if err == nil {
		t.Fatal("expected error for empty host")
	}
}

func TestWithPostgres_InvalidPortRejects(t *testing.T) {
	err := WithPostgres(DatabaseFileConfig{Host: "localhost", Port: 0})(&config{})
	if err == nil {
		t.Fatal("expected error for port 0")
	}
}

func TestWithKnowledgeConfig_InvalidTopKRejects(t *testing.T) {
	err := WithKnowledgeConfig(KnowledgeFileConfig{
		ChunkSize: 200, TopK: 0,
	})(&config{})
	if err == nil {
		t.Fatal("expected error for top_k 0 when chunk_size active")
	}
}

func TestWithKnowledgeConfig_InvalidMinScoreRejects(t *testing.T) {
	err := WithKnowledgeConfig(KnowledgeFileConfig{
		ChunkSize: 200, TopK: 5, MinScore: 1.5,
	})(&config{})
	if err == nil {
		t.Fatal("expected error for min_score 1.5 out of [0,1]")
	}
}

func TestWithKnowledgeConfig_InactiveChunkSizeSkipsChecks(t *testing.T) {
	// When ChunkSize is 0, the section is inactive; TopK/MinScore checks
	// should not fire (mirrors Validate which only checks when ChunkSize > 0).
	err := WithKnowledgeConfig(KnowledgeFileConfig{
		ChunkSize: 0, TopK: 0, MinScore: 1.5,
	})(&config{})
	if err != nil {
		t.Fatalf("expected nil when chunk_size inactive, got: %v", err)
	}
}
