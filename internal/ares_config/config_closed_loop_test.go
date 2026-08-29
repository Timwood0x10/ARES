// Package ares_config - closed-loop memory and knowledge config tests.
package ares_config

import (
	"testing"
)

// TestSetDefaults_ClosedLoopMemory verifies setDefaults applies the new
// closed-loop memory defaults: MaxHistory=10, and lazy Distillation/RAG
// defaults that only fire when their enable flag is true.
func TestSetDefaults_ClosedLoopMemory(t *testing.T) {
	t.Run("max_history_defaults_to_10", func(t *testing.T) {
		cfg := &Config{}
		cfg.setDefaults()
		if cfg.Memory.MaxHistory != 10 {
			t.Errorf("Memory.MaxHistory default = %d, want 10", cfg.Memory.MaxHistory)
		}
	})

	t.Run("distillation_threshold_default_only_when_enabled", func(t *testing.T) {
		// Disabled: threshold stays zero.
		cfg := &Config{Memory: MemoryConfig{EnableDistillation: boolPtr(false)}}
		cfg.setDefaults()
		if cfg.Memory.DistillationThreshold != 0 {
			t.Errorf("DistillationThreshold should stay 0 when disabled, got %d",
				cfg.Memory.DistillationThreshold)
		}

		// Enabled with zero threshold: defaults to 3.
		cfg = &Config{Memory: MemoryConfig{EnableDistillation: boolPtr(true)}}
		cfg.setDefaults()
		if cfg.Memory.DistillationThreshold != 3 {
			t.Errorf("DistillationThreshold default = %d, want 3", cfg.Memory.DistillationThreshold)
		}

		// Enabled with explicit threshold: preserved.
		cfg = &Config{Memory: MemoryConfig{EnableDistillation: boolPtr(true), DistillationThreshold: 7}}
		cfg.setDefaults()
		if cfg.Memory.DistillationThreshold != 7 {
			t.Errorf("DistillationThreshold should be preserved as 7, got %d",
				cfg.Memory.DistillationThreshold)
		}
	})

	t.Run("rag_defaults_only_when_enabled", func(t *testing.T) {
		// Disabled: TopK/MinScore stay zero.
		cfg := &Config{Memory: MemoryConfig{EnableRAG: false}}
		cfg.setDefaults()
		if cfg.Memory.RAGTopK != 0 || cfg.Memory.RAGMinScore != 0 {
			t.Errorf("RAG fields should stay 0 when disabled, got TopK=%d MinScore=%f",
				cfg.Memory.RAGTopK, cfg.Memory.RAGMinScore)
		}

		// Enabled with zero: defaults to 5 / 0.4.
		cfg = &Config{Memory: MemoryConfig{EnableRAG: true}}
		cfg.setDefaults()
		if cfg.Memory.RAGTopK != 5 {
			t.Errorf("RAGTopK default = %d, want 5", cfg.Memory.RAGTopK)
		}
		if cfg.Memory.RAGMinScore != 0.4 {
			t.Errorf("RAGMinScore default = %f, want 0.4", cfg.Memory.RAGMinScore)
		}

		// Enabled with explicit values: preserved.
		cfg = &Config{Memory: MemoryConfig{EnableRAG: true, RAGTopK: 20, RAGMinScore: 0.7}}
		cfg.setDefaults()
		if cfg.Memory.RAGTopK != 20 || cfg.Memory.RAGMinScore != 0.7 {
			t.Errorf("RAG fields should be preserved, got TopK=%d MinScore=%f",
				cfg.Memory.RAGTopK, cfg.Memory.RAGMinScore)
		}
	})
}

// TestSetDefaults_Knowledge verifies setDefaults applies Knowledge (AKG)
// defaults only when RetrievalEnabled is true.
func TestSetDefaults_Knowledge(t *testing.T) {
	t.Run("disabled_leaves_zero", func(t *testing.T) {
		cfg := &Config{Knowledge: KnowledgeConfig{RetrievalEnabled: false}}
		cfg.setDefaults()
		if cfg.Knowledge.TopK != 0 || cfg.Knowledge.MinScore != 0 {
			t.Errorf("Knowledge fields should stay 0 when disabled, got TopK=%d MinScore=%f",
				cfg.Knowledge.TopK, cfg.Knowledge.MinScore)
		}
	})

	t.Run("enabled_zero_defaults_to_5_and_0_4", func(t *testing.T) {
		cfg := &Config{Knowledge: KnowledgeConfig{RetrievalEnabled: true}}
		cfg.setDefaults()
		if cfg.Knowledge.TopK != 5 {
			t.Errorf("Knowledge.TopK default = %d, want 5", cfg.Knowledge.TopK)
		}
		if cfg.Knowledge.MinScore != 0.4 {
			t.Errorf("Knowledge.MinScore default = %f, want 0.4", cfg.Knowledge.MinScore)
		}
	})

	t.Run("enabled_explicit_values_preserved", func(t *testing.T) {
		cfg := &Config{Knowledge: KnowledgeConfig{RetrievalEnabled: true, TopK: 15, MinScore: 0.6}}
		cfg.setDefaults()
		if cfg.Knowledge.TopK != 15 || cfg.Knowledge.MinScore != 0.6 {
			t.Errorf("Knowledge fields should be preserved, got TopK=%d MinScore=%f",
				cfg.Knowledge.TopK, cfg.Knowledge.MinScore)
		}
	})
}

// TestValidate_ClosedLoopMemoryAndKnowledge exercises the new validateMemory
// and validateKnowledge branches.
func TestValidate_ClosedLoopMemoryAndKnowledge(t *testing.T) {
	base := func() *Config {
		return &Config{
			Server: ServerConfig{Host: "localhost", Port: 8080},
			LLM: LLMConfig{
				Provider:  "ollama",
				Model:     "llama3",
				Timeout:   60,
				MaxTokens: 4096,
			},
			Agents: AgentsConfig{
				Sub: []SubAgentConfig{},
			},
			Output:     OutputConfig{Format: "simple"},
			Validation: ValidationConfig{MaxRetries: 3},
			Memory: MemoryConfig{
				Archive: ArchiveConfig{Dir: ".context/rounds", MaxRounds: 200},
			},
		}
	}

	t.Run("rag_enabled_valid_passes", func(t *testing.T) {
		cfg := base()
		cfg.Memory.EnableRAG = true
		cfg.Memory.RAGTopK = 5
		cfg.Memory.RAGMinScore = 0.4
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate() unexpected error: %v", err)
		}
	})

	t.Run("rag_enabled_negative_topk_rejected", func(t *testing.T) {
		cfg := base()
		cfg.Memory.EnableRAG = true
		cfg.Memory.RAGTopK = -1
		cfg.Memory.RAGMinScore = 0.4
		if err := cfg.Validate(); err == nil {
			t.Error("Validate() expected error for negative rag_top_k, got nil")
		}
	})

	t.Run("rag_enabled_negative_minscore_rejected", func(t *testing.T) {
		cfg := base()
		cfg.Memory.EnableRAG = true
		cfg.Memory.RAGTopK = 5
		cfg.Memory.RAGMinScore = -0.1
		if err := cfg.Validate(); err == nil {
			t.Error("Validate() expected error for negative rag_min_score, got nil")
		}
	})

	t.Run("knowledge_enabled_valid_passes", func(t *testing.T) {
		cfg := base()
		cfg.Knowledge.RetrievalEnabled = true
		cfg.Knowledge.TopK = 5
		cfg.Knowledge.MinScore = 0.4
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate() unexpected error: %v", err)
		}
	})

	t.Run("knowledge_enabled_negative_topk_rejected", func(t *testing.T) {
		cfg := base()
		cfg.Knowledge.RetrievalEnabled = true
		cfg.Knowledge.TopK = -1
		if err := cfg.Validate(); err == nil {
			t.Error("Validate() expected error for negative knowledge top_k, got nil")
		}
	})

	t.Run("knowledge_enabled_negative_minscore_rejected", func(t *testing.T) {
		cfg := base()
		cfg.Knowledge.RetrievalEnabled = true
		cfg.Knowledge.MinScore = -0.5
		if err := cfg.Validate(); err == nil {
			t.Error("Validate() expected error for negative knowledge min_score, got nil")
		}
	})

	t.Run("memory_max_history_negative_rejected", func(t *testing.T) {
		cfg := base()
		cfg.Memory.MaxHistory = -1
		if err := cfg.Validate(); err == nil {
			t.Error("Validate() expected error for negative memory max_history, got nil")
		}
	})

	t.Run("distillation_threshold_negative_rejected", func(t *testing.T) {
		cfg := base()
		cfg.Memory.DistillationThreshold = -1
		if err := cfg.Validate(); err == nil {
			t.Error("Validate() expected error for negative distillation_threshold, got nil")
		}
	})
}
