// Package memory — post-construction distillation engine injection.
package memory

import (
	"fmt"

	apiembed "github.com/Timwood0x10/ares/internal/embedding"
	"github.com/Timwood0x10/ares/internal/errors"
	"github.com/Timwood0x10/ares/internal/runtime/memory/distillation"
	memembed "github.com/Timwood0x10/ares/internal/runtime/memory/embedding"
)

// SetDistillationEngine attaches the distillation engine (embedding pipeline
// + experience repository) to a manager built by NewMemoryManager, which
// constructs session/task memory only. Serve bootstrap assembles memory
// before the embedding client and experience repo exist (assembleCore runs
// ahead of assembleExperience), so the engine is injected later through this
// seam — the same pattern as SetRetrievers / SetSkillsRegistry.
//
// Must be called before concurrent traffic reaches the distillation-backed
// methods (StoreDistilledTask / SearchSimilarTasks): bootstrap injects during
// single-threaded wiring, before serve starts admitting submissions.
// Re-injection is rejected — the distiller snapshots its components at
// construction, and swapping them mid-flight would split write and read paths.
//
// Args:
//
//	embedder - embedding service for vector generation.
//	expRepo  - experience repository for storage and retrieval.
//
// Returns:
//
//	error - nil on success; a descriptive error when either argument is nil,
//	        the embedding pipeline cannot be constructed, or an engine is
//	        already attached.
func (m *memoryManager) SetDistillationEngine(embedder apiembed.EmbeddingService, expRepo distillation.ExperienceRepository) error {
	if embedder == nil || expRepo == nil {
		return errors.New("memory: distillation engine requires non-nil embedder and experience repository")
	}
	pipeline, err := memembed.NewEmbeddingPipeline(embedder)
	if err != nil {
		return fmt.Errorf("memory: create embedding pipeline: %w", err)
	}
	// Honor the configured round gate: 0 keeps the distiller's ungated
	// default, a positive value arms DistillationThreshold. Snapshot under
	// RLock before taking the write lock below.
	m.mu.RLock()
	threshold := 0
	if m.config != nil {
		threshold = m.config.DistillationThreshold
	}
	m.mu.RUnlock()
	distillCfg := distillation.DefaultDistillationConfig()
	if threshold > 0 {
		distillCfg.DistillationThreshold = threshold
	}
	distiller := distillation.NewDistiller(distillCfg, embedder, expRepo)
	distiller.SetEmbeddingPipeline(pipeline)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.distiller != nil || m.pipeline != nil || m.expRepo != nil {
		return errors.New("memory: distillation engine already initialized")
	}
	m.distiller = distiller
	m.embedder = embedder
	m.pipeline = pipeline
	m.expRepo = expRepo
	m.distillConfig = distillCfg
	return nil
}
