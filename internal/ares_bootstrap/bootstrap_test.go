package ares_bootstrap

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_config"
	ares_memory "github.com/Timwood0x10/ares/internal/runtime/memory"
)

func TestProvideRuntime(t *testing.T) {
	rt, err := ProvideRuntime(nil)
	require.NoError(t, err)
	require.NotNil(t, rt)
}

func TestProvideMemory_DefaultConfig(t *testing.T) {
	mem, err := ProvideMemory(nil)
	require.NoError(t, err)
	require.NotNil(t, mem)
}

func TestProvideMemory_CustomConfig(t *testing.T) {
	cfg := ares_memory.DefaultMemoryConfig()
	mem, err := ProvideMemory(cfg)
	require.NoError(t, err)
	require.NotNil(t, mem)
}

// TestWireMemoryPlumbsConfigFields locks the serve-side config plumbing:
// wireMemory must copy the YAML memory knobs into the runtime MemoryConfig
// instead of silently discarding them (only RAG fields used to be copied,
// so an explicit memory.max_history never reached the manager).
func TestWireMemoryPlumbsConfigFields(t *testing.T) {
	cfg := &ares_config.Config{
		Memory: ares_config.MemoryConfig{
			Enabled:               boolPtr(true),
			MaxHistory:            25,
			SessionMemory:         ares_config.SessionConfig{MaxHistory: 40},
			DistillationThreshold: 6,
			EnableRAG:             true,
			RAGTopK:               7,
			RAGMinScore:           0.6,
		},
	}
	mem, err := wireMemory(cfg, nil)
	require.NoError(t, err)
	require.NotNil(t, mem)

	store, ok := mem.(ares_memory.MemoryConfigStore)
	require.True(t, ok, "memory manager must expose MemoryConfigStore")
	store.Lock()
	runtimeCfg := store.GetConfig()
	require.NotNil(t, runtimeCfg)
	maxHistory := runtimeCfg.MaxHistory
	sessionMaxHistory := runtimeCfg.SessionMaxHistory
	distillThreshold := runtimeCfg.DistillationThreshold
	enableRAG := runtimeCfg.EnableRAG
	ragTopK := runtimeCfg.RAGTopK
	ragMinScore := runtimeCfg.RAGMinScore
	store.Unlock()

	require.Equal(t, 25, maxHistory, "YAML memory.max_history must reach the runtime config")
	require.Equal(t, 40, sessionMaxHistory, "YAML memory.session.max_history must reach the runtime config")
	require.Equal(t, 6, distillThreshold, "YAML memory.distillation_threshold must reach the runtime config")
	require.True(t, enableRAG, "YAML memory.enable_rag must reach the runtime config")
	require.Equal(t, 7, ragTopK, "YAML memory.rag_top_k must reach the runtime config")
	require.InDelta(t, 0.6, ragMinScore, 1e-9, "YAML memory.rag_min_score must reach the runtime config")
}

// TestWireMemoryDisabledReturnsNil pins the documented disabled contract:
// (nil, nil) when cfg.Memory.IsEnabled() is false.
func TestWireMemoryDisabledReturnsNil(t *testing.T) {
	cfg := &ares_config.Config{
		Memory: ares_config.MemoryConfig{Enabled: boolPtr(false)},
	}
	mem, err := wireMemory(cfg, nil)
	require.NoError(t, err)
	require.Nil(t, mem, "disabled memory must yield (nil, nil)")
}

// TestWireMemoryClampsSessionCapToReadWindow pins the store-cap floor: a
// session.max_history below max_history must be raised to the read window,
// otherwise BuildContext is silently truncated under the configured depth.
func TestWireMemoryClampsSessionCapToReadWindow(t *testing.T) {
	cfg := &ares_config.Config{
		Memory: ares_config.MemoryConfig{
			Enabled:       boolPtr(true),
			MaxHistory:    100,
			SessionMemory: ares_config.SessionConfig{MaxHistory: 50},
		},
	}
	mem, err := wireMemory(cfg, nil)
	require.NoError(t, err)
	require.NotNil(t, mem)

	store, ok := mem.(ares_memory.MemoryConfigStore)
	require.True(t, ok)
	store.Lock()
	runtimeCfg := store.GetConfig()
	require.NotNil(t, runtimeCfg)
	sessionCap := runtimeCfg.SessionMaxHistory
	maxHistory := runtimeCfg.MaxHistory
	store.Unlock()

	require.Equal(t, 100, maxHistory)
	require.Equal(t, 100, sessionCap,
		"session store cap must clamp up to the read-side max_history")
}

func TestBootstrap_WithMinimalConfig(t *testing.T) {
	ctx := context.Background()
	cfg := &ares_config.Config{
		LLM: ares_config.LLMConfig{
			Provider: "mock",
			Model:    "mock-model",
			APIKey:   "test-key",
			BaseURL:  "http://localhost:9999",
		},
		Memory: ares_config.MemoryConfig{Enabled: boolPtr(true)},
	}
	comp, err := Bootstrap(ctx, cfg, nil)
	require.NoError(t, err)
	require.NotNil(t, comp)
	assert.NotNil(t, comp.EventStore)
	assert.NotNil(t, comp.Runtime)
	assert.NotNil(t, comp.Memory)
	assert.NotNil(t, comp.LLM)
	assert.NotNil(t, comp.Dashboard)
}

func TestBootstrap_WithDeps(t *testing.T) {
	ctx := context.Background()
	cfg := &ares_config.Config{
		Memory: ares_config.MemoryConfig{Enabled: boolPtr(true)},
	}
	comp, err := Bootstrap(ctx, cfg, &BootstrapDeps{
		LLMClient: &mockLLMClient{},
	})
	require.NoError(t, err)
	require.NotNil(t, comp)
	assert.NotNil(t, comp.LLM)
}

// boolPtr returns a pointer to a bool literal for *bool config fields.
func boolPtr(b bool) *bool { return &b }

// mockLLMClient is a minimal mock for eval.LLMClient.
type mockLLMClient struct{}

func (m *mockLLMClient) Generate(ctx context.Context, prompt string) (string, error) {
	return "mock response", nil
}

func (m *mockLLMClient) Close() error {
	return nil
}
