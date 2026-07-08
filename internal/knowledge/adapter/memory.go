// Package adapter provides adapters between existing ARES subsystems and AKF.
package adapter

import (
	"fmt"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_memory/distillation"
	"github.com/Timwood0x10/ares/internal/knowledge"
)

// FromMemory converts a distillation.Memory into a KnowledgeObject.
// This is the bridge between the existing Memory Distillation pipeline
// and the AKF KnowledgeObject model.
func FromMemory(m *distillation.Memory, ns string) *knowledge.KnowledgeObject {
	if m == nil {
		return nil
	}

	objType := memoryTypeToObjectType(m.Type)
	summary := m.Content
	if len(summary) > 200 {
		summary = summary[:200] + "..."
	}

	return &knowledge.KnowledgeObject{
		ID:         fmt.Sprintf("mem_%s", m.ID),
		Type:       objType,
		Namespace:  ns,
		Summary:    summary,
		Confidence: clampConfidence(float64(m.Importance) / 100.0),
		CreatedAt:  m.CreatedAt,
		UpdatedAt:  time.Now(),
	}
}

// FromMemories converts a slice of distillation.Memory into KnowledgeObjects.
func FromMemories(memories []*distillation.Memory, ns string) []*knowledge.KnowledgeObject {
	objects := make([]*knowledge.KnowledgeObject, 0, len(memories))
	for _, m := range memories {
		if obj := FromMemory(m, ns); obj != nil {
			objects = append(objects, obj)
		}
	}
	return objects
}

func memoryTypeToObjectType(mt distillation.MemoryType) knowledge.ObjectType {
	switch mt {
	case distillation.MemoryKnowledge:
		return knowledge.ObjectMemory
	case distillation.MemoryPreference:
		return knowledge.ObjectMemory
	case distillation.MemoryInteraction:
		return knowledge.ObjectMemory
	case distillation.MemoryProfile:
		return knowledge.ObjectUser
	default:
		return knowledge.ObjectMemory
	}
}

// clampConfidence clamps a confidence score to the [0, 1] range.
func clampConfidence(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
