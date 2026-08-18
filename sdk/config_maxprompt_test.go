package sdk

import (
	"os"
	"testing"
)

func TestMaxPromptLengthBridged(t *testing.T) {
	// write a minimal ares.yaml with max_prompt_length, load, and check the
	// option bridge carries it into llmCfg.
	content := `llm:
  provider: openai
  model: test-model
  api_key: test-key
  max_prompt_length: 32768
`
	path := t.TempDir() + "/ares.yaml"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadConfigFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.LLM.MaxPromptLength != 32768 {
		t.Fatalf("yaml parse: want 32768, got %d", cfg.LLM.MaxPromptLength)
	}
	opts, err := cfg.ToOptions()
	if err != nil {
		t.Fatalf("toOptions: %v", err)
	}
	c := defaultConfig()
	for _, o := range opts {
		if err := o(c); err != nil {
			t.Fatalf("apply option: %v", err)
		}
	}
	if c.llmCfg.MaxPromptLength != 32768 {
		t.Fatalf("bridge: want 32768 in llmCfg, got %d", c.llmCfg.MaxPromptLength)
	}
}
