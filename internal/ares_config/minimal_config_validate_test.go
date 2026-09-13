package ares_config

import "testing"

// TestNewMinimalConfigPassesValidate locks the invariant that the no-config
// startup path (`ares serve --llm-url …`) produces a configuration that
// satisfies the same validation a config file goes through.
//
// Regression: NewMinimalConfig assembled a default sub-agent population
// without a Timeout, and setDefaults never filled one in, so the resulting
// config failed Validate ("sub-agent 0: timeout must be positive"). Nothing
// caught it because loadServeConfig's minimal branch returns the config
// directly, bypassing Load — and therefore Validate — entirely. A default
// that cannot survive its own validator is a latent startup crash the moment
// anything starts validating that path.
func TestNewMinimalConfigPassesValidate(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		apiKey  string
		model   string
	}{
		{name: "ollama keyless", baseURL: "http://localhost:11434", model: "llama3.2"},
		{name: "openai keyed", baseURL: "https://api.openai.com/v1", apiKey: "sk-test", model: "gpt-4o-mini"},
		{name: "model defaulted", baseURL: "http://localhost:11434"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := NewMinimalConfig(tc.baseURL, tc.apiKey, tc.model)
			if err := cfg.Validate(); err != nil {
				t.Fatalf("minimal config must validate, got %v", err)
			}
		})
	}
}

// TestSetDefaultsFillsSubAgentTimeout locks that an explicitly-authored
// sub-agent entry with no timeout picks up a positive default rather than
// being rejected by Validate. A config file that lists agents but omits
// `timeout` is a reasonable thing to write.
func TestSetDefaultsFillsSubAgentTimeout(t *testing.T) {
	cfg := &Config{
		Agents: AgentsConfig{
			Sub: []SubAgentConfig{{ID: "coder", Type: "coder", Category: "analysis"}},
		},
	}
	cfg.setDefaults()
	if cfg.Agents.Sub[0].Timeout < 1 {
		t.Fatalf("setDefaults must give sub-agents a positive timeout, got %d", cfg.Agents.Sub[0].Timeout)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config must validate after defaults, got %v", err)
	}
}

// TestSetDefaultsPreservesExplicitSubAgentTimeout locks the non-clobbering
// half: a timeout the operator set deliberately must survive defaults.
func TestSetDefaultsPreservesExplicitSubAgentTimeout(t *testing.T) {
	cfg := &Config{
		Agents: AgentsConfig{
			Sub: []SubAgentConfig{{ID: "coder", Type: "coder", Timeout: 45}},
		},
	}
	cfg.setDefaults()
	if cfg.Agents.Sub[0].Timeout != 45 {
		t.Fatalf("setDefaults must not clobber an explicit sub-agent timeout, got %d", cfg.Agents.Sub[0].Timeout)
	}
}
