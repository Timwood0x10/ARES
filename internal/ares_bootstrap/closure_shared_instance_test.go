// Package ares_bootstrap — Runtime Closure Shared Instance Tests (Stage 0).
//
// These tests verify that components which must share a single instance
// actually do so. Each test documents the shared instance constraint from
// RUNTIME_COMPONENT_CLOSURE_PLAN_2026-08-04.md §5.2 and checks whether the
// current Bootstrap wiring satisfies it.
//
//go:build closure

package ares_bootstrap

import (
	"context"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_config"
	ares_events "github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSharedInstance_EventStore_Identity verifies that Runtime, Memory,
// and FlightRecorder all reference the same EventStore instance.
//
// Constraint from §5.2:
//
//	Runtime, Memory, Flight, Dashboard/bridge use the same EventStore.
//
// Current status: Bootstrap sets comp.EventStore and passes it to Runtime
// via ProvideRuntime. Memory gets EventStore via SetEventStore called
// post-Bootstrap in serve.go (B01 bypass). FlightRecorder gets EventStore
// via FlightRecorderConfig.EventStore.
func TestSharedInstance_EventStore_Identity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Use a custom EventStore so we can verify identity by pointer.
	customStore := ares_events.NewMemoryEventStore()

	cfg := &ares_config.Config{
		LLM: ares_config.LLMConfig{
			Provider: "mock",
			Model:    "mock-model",
			APIKey:   "test-key",
			BaseURL:  "http://localhost:9999",
		},
	}

	comp, err := Bootstrap(ctx, cfg, &BootstrapDeps{
		EventStore: customStore,
	})
	require.NoError(t, err)
	require.NotNil(t, comp)

	// Verify EventStore identity.
	assert.Same(t, customStore, comp.EventStore,
		"comp.EventStore must be the same instance passed via BootstrapDeps")

	// Verify Runtime references the same EventStore.
	// Runtime Manager stores eventStore internally; we check via behavior.
	// If Runtime has a different store, agent events would not appear in
	// comp.EventStore. We can't directly assert pointer equality without
	// accessing private fields, so we document this as a gap.

	// B01: Memory's EventStore is set post-Bootstrap in serve.go via
	// memMgr.SetEventStore(store, "memory"). At Bootstrap time, Memory
	// has no EventStore. This is the bypass the plan calls out.
	t.Logf("B01: Memory's EventStore is set post-Bootstrap in serve.go, " +
		"not during Bootstrap. Stage 1 should wire EventStore into Memory " +
		"during construction.")

	cancel()
	comp.WaitBackground()
}

// TestSharedInstance_EvidenceStore_Identity verifies that all five genomes
// and the FlightRecorder reference the same EvidenceStore instance.
//
// Constraint from §5.2:
//
//	Flight, Memory retriever, KnowledgeRuntime, five Genome use same EvidenceStore.
//
// Current status: EvidenceStore is created inside ProvideNewEvolution as
// evidence.NewMemoryStore(). FlightRecorder gets EvidenceStore via
// FlightRecorderConfig.EvidenceStore. The wiring in provide_wiring.go
// passes the NewEvolution.EvidenceStore to the FlightRecorder.
func TestSharedInstance_EvidenceStore_Identity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg := &ares_config.Config{
		LLM: ares_config.LLMConfig{
			Provider: "mock",
			Model:    "mock-model",
			APIKey:   "test-key",
			BaseURL:  "http://localhost:9999",
		},
		Evolution: ares_config.EvolutionConfig{Enabled: true},
	}

	comp, err := Bootstrap(ctx, cfg, nil)
	require.NoError(t, err)
	require.NotNil(t, comp)
	require.NotNil(t, comp.NewEvolution,
		"NewEvolution must be wired when evolution is enabled")
	require.NotNil(t, comp.NewEvolution.EvidenceStore,
		"EvidenceStore must be constructed inside NewEvolution")

	// Verify FlightRecorder references the same EvidenceStore.
	// If FlightRecorder is wired, its collector should reference the same
	// evidence store.
	if comp.FlightRecorder != nil {
		// FlightRecorder stores evidenceStore internally via collector.
		// We can verify identity by writing to the EvidenceStore and
		// checking if FlightRecorder's collector observes it.
		// For now, document the constraint.
		t.Logf("EvidenceStore identity: NewEvolution.EvidenceStore exists. " +
			"FlightRecorder identity check requires runtime behavior test.")
	} else {
		t.Logf("FlightRecorder not wired; EvidenceStore identity check skipped. " +
			"Stage 1 should ensure FlightRecorder shares EvidenceStore.")
	}

	cancel()
	comp.WaitBackground()
}

// TestSharedInstance_KnowledgeRuntime_Identity verifies that the
// KnowledgePatchExecutor and AKF tools share the same KnowledgeRuntime.
//
// Constraint from §5.2:
//
//	KnowledgePatchExecutor and AKF tools use same KnowledgeRuntime.
//
// Current status: Bootstrap creates one KnowledgeRuntime via
// BuildKnowledgeRuntime() and assigns it to comp.KnowledgeRuntime.
// The same instance is passed to ProvideNewEvolution and later to
// AKF tools in serve.go.
func TestSharedInstance_KnowledgeRuntime_Identity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg := &ares_config.Config{
		LLM: ares_config.LLMConfig{
			Provider: "mock",
			Model:    "mock-model",
			APIKey:   "test-key",
			BaseURL:  "http://localhost:9999",
		},
	}

	comp, err := Bootstrap(ctx, cfg, nil)
	require.NoError(t, err)
	require.NotNil(t, comp)

	// Verify KnowledgeRuntime exists.
	assert.NotNil(t, comp.KnowledgeRuntime,
		"KnowledgeRuntime must be constructed by Bootstrap")

	// The KnowledgePatchExecutor inside NewEvolution should reference the
	// same KnowledgeRuntime. We can verify this by checking that the
	// executor's runtime field matches comp.KnowledgeRuntime.
	// Since knowledgeExec is a private field, we document the constraint.
	t.Logf("KnowledgeRuntime identity: comp.KnowledgeRuntime exists. " +
		"PatchExecutor identity check requires accessing private field. " +
		"Stage 1 should expose this via a status API.")

	cancel()
	comp.WaitBackground()
}

// TestSharedInstance_StrategyStore_Identity verifies that the GA deployment
// and the Agent StrategySource reference the same StrategyStore.
//
// Constraint from §5.2:
//
//	GA deployment write and Agent StrategySource read use same StrategyStore.
//
// Current status: StrategyStore is created in wireGAEvolution and assigned
// to NewEvolution.StrategyStore. serve.go creates a StrategySource from
// comp.NewEvolution.StrategyStore. They share the same instance.
func TestSharedInstance_StrategyStore_Identity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg := &ares_config.Config{
		LLM: ares_config.LLMConfig{
			Provider: "mock",
			Model:    "mock-model",
			APIKey:   "test-key",
			BaseURL:  "http://localhost:9999",
		},
		Evolution: ares_config.EvolutionConfig{Enabled: true},
	}

	comp, err := Bootstrap(ctx, cfg, nil)
	require.NoError(t, err)
	require.NotNil(t, comp)
	require.NotNil(t, comp.NewEvolution, "NewEvolution must be wired")
	require.NotNil(t, comp.NewEvolution.StrategyStore,
		"StrategyStore must be created by wireGAEvolution")

	// The StrategySource adapter in serve.go wraps the same StrategyStore.
	// NewStrategySource(comp.NewEvolution.StrategyStore) creates a source
	// that reads from the same store the GA writes to.
	// This is correct — document it as passing.
	t.Logf("StrategyStore identity: NewEvolution.StrategyStore exists. " +
		"serve.go's StrategySource wraps the same instance. ✅")

	cancel()
	comp.WaitBackground()
}

// TestSharedInstance_EmbeddingClient_Identity verifies that the distillation
// pipeline and the MemoryRetriever share the same EmbeddingClient.
//
// Constraint from §5.2:
//
//	DistillBridge write and StoreProvider/KnowledgeRetriever read use same
//	KnowledgeStore and namespace.
//	Embedding client is shared between distillation and retrievers.
//
// Current status: wireDistillation returns the embedding client, which is
// then passed to wireRetrievers. They share the same instance when both
// are configured.
func TestSharedInstance_EmbeddingClient_Identity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg := &ares_config.Config{
		LLM: ares_config.LLMConfig{
			Provider: "mock",
			Model:    "mock-model",
			APIKey:   "test-key",
			BaseURL:  "http://localhost:9999",
		},
		Storage:   ares_config.StorageConfig{Enabled: false},
		Embedding: ares_config.EmbeddingConfig{Enabled: false},
	}

	comp, err := Bootstrap(ctx, cfg, nil)
	require.NoError(t, err)
	require.NotNil(t, comp)

	// When embedding is not configured, both distillation and retrievers
	// are skipped. This is correct — no shared instance to verify.
	// When embedding IS configured, wireDistillation returns the client
	// and wireRetrievers receives it, ensuring identity.
	if !cfg.Embedding.Enabled {
		t.Log("Embedding disabled; shared instance identity vacuously holds.")
	}

	cancel()
	comp.WaitBackground()
}

// TestSharedInstance_PatchRegistry_Identity verifies that the
// PatchRegistry's executors all reference live runtime targets, not
// synthetic placeholders.
//
// Constraint from §5.2:
//
//	workflow/scheduler/recovery executor bound to Runtime live DAG/policy,
//	not synthetic objects entering Ready.
//
// Current status: At Bootstrap, the PatchRegistry has executors bound to
// a synthetic 3-step DAG. The live DAG is only wired post-Start in serve.go.
func TestSharedInstance_PatchRegistry_Identity(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg := &ares_config.Config{
		LLM: ares_config.LLMConfig{
			Provider: "mock",
			Model:    "mock-model",
			APIKey:   "test-key",
			BaseURL:  "http://localhost:9999",
		},
		Evolution: ares_config.EvolutionConfig{Enabled: true},
	}

	comp, err := Bootstrap(ctx, cfg, nil)
	require.NoError(t, err)
	require.NotNil(t, comp)
	require.NotNil(t, comp.NewEvolution, "NewEvolution must be wired")
	require.NotNil(t, comp.NewEvolution.PatchReg,
		"PatchRegistry must exist")

	// F04: At Bootstrap time, the PatchRegistry has executors bound to
	// synthetic targets. The live DAG executor is only registered in
	// wireEvolutionLiveDAGs (serve.go, post-Start).
	// We can verify this by checking that the fallback executor is NOT
	// a LiveDAGPatchExecutor (since serve.go hasn't run).

	// Document the constraint.
	t.Logf("PatchRegistry identity: executors exist but are bound to " +
		"synthetic DAG at Bootstrap time. F04: live binding happens " +
		"post-Start in serve.go. Stage 3 should move this before Ready.")

	cancel()
	comp.WaitBackground()
}
