package ares_bootstrap

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWireKnowledgeCompilerDisabled verifies the opt-in contract: when
// KnowledgeCompiler.Enabled is false (the default), wiring is a no-op and the
// Components.KnowledgeCompiler field stays nil, preserving prior behavior.
func TestWireKnowledgeCompilerDisabled(t *testing.T) {
	comp := &Components{}
	cfg := &ares_config.Config{} // Enabled defaults to false

	wireKnowledgeCompiler(context.Background(), cfg, comp)

	assert.Nil(t, comp.KnowledgeCompiler, "disabled compiler must not wire")
}

// TestWireKnowledgeCompilerNilCfg verifies the nil-cfg guard does not panic.
func TestWireKnowledgeCompilerNilCfg(t *testing.T) {
	comp := &Components{}
	assert.NotPanics(t, func() {
		wireKnowledgeCompiler(context.Background(), nil, comp)
	})
	assert.Nil(t, comp.KnowledgeCompiler)
}

// TestWireKnowledgeCompilerEnabled verifies that an enabled, valid config wires
// a non-nil Pipeline and Lifecycle. The wired pipeline is zero-LLM and reuses
// the AKG extractor + distillation classifier/scorer + knowledge graph builder.
func TestWireKnowledgeCompilerEnabled(t *testing.T) {
	comp := &Components{}
	cfg := &ares_config.Config{
		KnowledgeCompiler: ares_config.KnowledgeCompilerConfig{
			Enabled:             true,
			MaxNodes:            500,
			PromptMaxTokens:     2048,
			AKGMaxFacts:         100,
			MinConfidence:       0.3,
			AKGMinConfidence:    0.4,
			DistillMinScore:     0.4,
			WindowSize:          128000,
			Threshold:           0.7,
			DistillAfterCompile: true,
		},
	}

	wireKnowledgeCompiler(context.Background(), cfg, comp)

	require.NotNil(t, comp.KnowledgeCompiler, "enabled compiler must wire")
	assert.NotNil(t, comp.KnowledgeCompiler.Pipeline, "Pipeline must be wired")
	assert.NotNil(t, comp.KnowledgeCompiler.Lifecycle, "Lifecycle must be wired")
}

// TestWireKnowledgeCompilerEnabledViaBootstrap confirms the wiring is reachable
// end-to-end through the public Bootstrap entry point with a minimal config.
func TestWireKnowledgeCompilerEnabledViaBootstrap(t *testing.T) {
	ctx := context.Background()
	cfg := &ares_config.Config{
		LLM: ares_config.LLMConfig{
			Provider: "ollama",
			Model:    "llama3.2",
			Timeout:  60,
		},
		KnowledgeCompiler: ares_config.KnowledgeCompilerConfig{
			Enabled:             true,
			MaxNodes:            100,
			PromptMaxTokens:     1024,
			WindowSize:          32000,
			Threshold:           0.7,
			DistillAfterCompile: true,
		},
	}

	comp, err := Bootstrap(ctx, cfg, nil)
	require.NoError(t, err)
	require.NotNil(t, comp)
	assert.NotNil(t, comp.KnowledgeCompiler, "Bootstrap must wire the knowledge compiler when enabled")
	assert.NotNil(t, comp.KnowledgeCompiler.Pipeline)
}

// TestBootstrapKnowledgeCompilerDisabledByDefault confirms the pipeline is NOT
// wired when the config omits KnowledgeCompiler (the default opt-in behavior),
// so existing deployments are unaffected.
func TestBootstrapKnowledgeCompilerDisabledByDefault(t *testing.T) {
	ctx := context.Background()
	cfg := &ares_config.Config{
		LLM: ares_config.LLMConfig{
			Provider: "ollama",
			Model:    "llama3.2",
			Timeout:  60,
		},
	}

	comp, err := Bootstrap(ctx, cfg, nil)
	require.NoError(t, err)
	require.NotNil(t, comp)
	assert.Nil(t, comp.KnowledgeCompiler, "compiler must be nil when not enabled")
}
