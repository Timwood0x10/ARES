package ares_config

import "testing"

// TestKnowledgeCompilerDefaults verifies that setDefaults populates the
// KnowledgeCompiler zero-values with the documented defaults. The defaults
// mirror compiler.DefaultPipelineConfig / compiler.DefaultLifecycleConfig so
// the wired pipeline behaves identically to the library defaults.
func TestKnowledgeCompilerDefaults(t *testing.T) {
	cfg := &Config{}
	cfg.setDefaults()

	kc := cfg.KnowledgeCompiler
	if kc.MaxNodes != 500 {
		t.Errorf("MaxNodes default = %d, want 500", kc.MaxNodes)
	}
	if kc.PromptMaxTokens != 8000 {
		t.Errorf("PromptMaxTokens default = %d, want 8000", kc.PromptMaxTokens)
	}
	if kc.AKGMaxFacts != 200 {
		t.Errorf("AKGMaxFacts default = %d, want 200", kc.AKGMaxFacts)
	}
	if kc.MinConfidence != 0.3 {
		t.Errorf("MinConfidence default = %v, want 0.3", kc.MinConfidence)
	}
	if kc.AKGMinConfidence != 0.4 {
		t.Errorf("AKGMinConfidence default = %v, want 0.4", kc.AKGMinConfidence)
	}
	if kc.DistillMinScore != 0.4 {
		t.Errorf("DistillMinScore default = %v, want 0.4", kc.DistillMinScore)
	}
	if kc.WindowSize != 128000 {
		t.Errorf("WindowSize default = %d, want 128000", kc.WindowSize)
	}
	if kc.Threshold != 0.7 {
		t.Errorf("Threshold default = %v, want 0.7", kc.Threshold)
	}
	if kc.Enabled {
		t.Error("Enabled default = true, want false (opt-in)")
	}
}

// TestValidateKnowledgeCompilerDisabled covers the disabled no-op branch: when
// Enabled is false, validation must pass even with otherwise-invalid values.
func TestValidateKnowledgeCompilerDisabled(t *testing.T) {
	cfg := &Config{
		KnowledgeCompiler: KnowledgeCompilerConfig{
			Enabled:   false,
			Threshold: 5.0, // would be invalid if enabled
		},
	}
	if err := cfg.validateKnowledgeCompiler(); err != nil {
		t.Errorf("validateKnowledgeCompiler() disabled = %v, want nil", err)
	}
}

// TestValidateKnowledgeCompilerEnabled is a table-driven test covering every
// validation branch when the pipeline is enabled.
func TestValidateKnowledgeCompilerEnabled(t *testing.T) {
	base := func() KnowledgeCompilerConfig {
		return KnowledgeCompilerConfig{
			Enabled:          true,
			Threshold:        0.7,
			MinConfidence:    0.3,
			AKGMinConfidence: 0.4,
			DistillMinScore:  0.4,
			MaxNodes:         500,
			PromptMaxTokens:  8000,
			AKGMaxFacts:      200,
			WindowSize:       128000,
		}
	}

	tests := []struct {
		name    string
		mutate  func(kc *KnowledgeCompilerConfig)
		wantErr bool
	}{
		{name: "valid", mutate: func(kc *KnowledgeCompilerConfig) {}, wantErr: false},
		{name: "threshold too high", mutate: func(kc *KnowledgeCompilerConfig) { kc.Threshold = 1.5 }, wantErr: true},
		{name: "threshold negative", mutate: func(kc *KnowledgeCompilerConfig) { kc.Threshold = -0.1 }, wantErr: true},
		{name: "threshold boundary one", mutate: func(kc *KnowledgeCompilerConfig) { kc.Threshold = 1.0 }, wantErr: false},
		{name: "min_confidence too high", mutate: func(kc *KnowledgeCompilerConfig) { kc.MinConfidence = 2 }, wantErr: true},
		{name: "akg_min_confidence negative", mutate: func(kc *KnowledgeCompilerConfig) { kc.AKGMinConfidence = -1 }, wantErr: true},
		{name: "distill_min_score too high", mutate: func(kc *KnowledgeCompilerConfig) { kc.DistillMinScore = 1.2 }, wantErr: true},
		{name: "max_nodes negative", mutate: func(kc *KnowledgeCompilerConfig) { kc.MaxNodes = -1 }, wantErr: true},
		{name: "prompt_max_tokens negative", mutate: func(kc *KnowledgeCompilerConfig) { kc.PromptMaxTokens = -10 }, wantErr: true},
		{name: "akg_max_facts negative", mutate: func(kc *KnowledgeCompilerConfig) { kc.AKGMaxFacts = -5 }, wantErr: true},
		{name: "window_size negative", mutate: func(kc *KnowledgeCompilerConfig) { kc.WindowSize = -1 }, wantErr: true},
		{name: "zero max_nodes allowed", mutate: func(kc *KnowledgeCompilerConfig) { kc.MaxNodes = 0 }, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kc := base()
			tt.mutate(&kc)
			cfg := &Config{KnowledgeCompiler: kc}
			err := cfg.validateKnowledgeCompiler()
			if tt.wantErr && err == nil {
				t.Errorf("validateKnowledgeCompiler() %s = nil, want error", tt.name)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("validateKnowledgeCompiler() %s = %v, want nil", tt.name, err)
			}
		})
	}
}

// TestValidateKnowledgeCompilerViaValidate confirms the validateKnowledgeCompiler
// hook is reachable through the top-level Validate entry point.
func TestValidateKnowledgeCompilerViaValidate(t *testing.T) {
	cfg := &Config{
		KnowledgeCompiler: KnowledgeCompilerConfig{
			Enabled:   true,
			Threshold: 2.0, // invalid
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("Validate() expected error for invalid knowledge_compiler threshold, got nil")
	}
}
