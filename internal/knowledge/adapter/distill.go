// Package adapter provides bridges between existing ARES subsystems and AKF.
package adapter

import (
	"context"
	"fmt"

	"github.com/Timwood0x10/ares/internal/ares_memory/distillation"
	"github.com/Timwood0x10/ares/internal/knowledge"
)

// ConversationDistiller is the minimal interface for distilling conversations
// into Memory objects. The existing distillation.Distiller implements this.
type ConversationDistiller interface {
	DistillConversation(ctx context.Context, conversationID string, messages []distillation.Message, tenantID, userID string) ([]distillation.Memory, error)
}

// DistillBridge connects the Memory Distillation pipeline to AKF.
// It runs conversations through the existing Distiller, converts the
// resulting Memory objects to KnowledgeObjects, processes them through
// the AKF KnowledgePipeline, and persists them to the KnowledgeStore.
//
// This closes the P6 distillation loop: raw experience → distilled
// Memory → KnowledgeObject → Pipeline → Store.
type DistillBridge struct {
	distiller ConversationDistiller
	pipeline  *knowledge.KnowledgePipeline
	store     knowledge.KnowledgeStore
	namespace string
}

// NewDistillBridge creates a bridge that connects the Memory Distillation
// pipeline to the AKF KnowledgeObject system.
//
// Args:
//   - distiller: existing Memory Distiller that produces Memory objects.
//   - pipeline: AKF KnowledgePipeline (Normalizer → Resolver → Summarizer).
//     Pass nil to skip pipeline processing.
//   - store: AKF KnowledgeStore for persisting the resulting KnowledgeObjects.
//   - namespace: namespace assigned to all produced KnowledgeObjects.
func NewDistillBridge(
	distiller ConversationDistiller,
	pipeline *knowledge.KnowledgePipeline,
	store knowledge.KnowledgeStore,
	namespace string,
) *DistillBridge {
	return &DistillBridge{
		distiller: distiller,
		pipeline:  pipeline,
		store:     store,
		namespace: namespace,
	}
}

// DistillConversation runs a conversation through the full distillation
// pipeline and persists the results as KnowledgeObjects.
//
// Steps:
//  1. Distill: uses the existing Distiller to extract Memories from messages.
//  2. Convert: maps each Memory to a KnowledgeObject via FromMemory.
//  3. Pipeline: runs each KnowledgeObject through Normalizer → Resolver → Summarizer.
//  4. Persist: saves the processed KnowledgeObjects to the Store.
//
// Returns the saved KnowledgeObjects or an error if any step fails.
func (b *DistillBridge) DistillConversation(
	ctx context.Context,
	conversationID string,
	messages []distillation.Message,
	tenantID string,
	userID string,
) ([]*knowledge.KnowledgeObject, error) {
	if b.distiller == nil {
		return nil, fmt.Errorf("distill bridge: distiller is nil")
	}
	if len(messages) == 0 {
		return nil, fmt.Errorf("distill bridge: no messages to distill")
	}

	// Phase 1: run the existing Memory Distiller.
	memories, err := b.distiller.DistillConversation(ctx, conversationID, messages, tenantID, userID)
	if err != nil {
		return nil, fmt.Errorf("distill bridge: distill conversation %q: %w", conversationID, err)
	}
	if len(memories) == 0 {
		return nil, nil
	}

	// Phase 2: convert Memory → KnowledgeObject.
	pointers := make([]*distillation.Memory, len(memories))
	for i := range memories {
		pointers[i] = &memories[i]
	}
	objects := FromMemories(pointers, b.namespace)
	if len(objects) == 0 {
		return nil, nil
	}

	// Phase 3: run through AKF KnowledgePipeline.
	if b.pipeline != nil {
		processed := make([]*knowledge.KnowledgeObject, 0, len(objects))
		for _, obj := range objects {
			result, pErr := b.pipeline.Process(ctx, obj)
			if pErr != nil {
				return nil, fmt.Errorf("distill bridge: pipeline process %q: %w", obj.ID, pErr)
			}
			if result != nil {
				processed = append(processed, result)
			}
		}
		objects = processed
	}

	// Phase 4: persist to KnowledgeStore.
	if b.store != nil {
		if err := b.store.Save(ctx, objects...); err != nil {
			return nil, fmt.Errorf("distill bridge: save to store: %w", err)
		}
	}

	return objects, nil
}
