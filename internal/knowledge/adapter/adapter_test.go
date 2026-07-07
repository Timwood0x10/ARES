package adapter

import (
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_memory/distillation"
	"github.com/Timwood0x10/ares/internal/knowledge"
)

func TestFromMemory(t *testing.T) {
	m := &distillation.Memory{
		ID:          "mem_abc",
		Type:        distillation.MemoryKnowledge,
		Content:     "Key insight about caching strategies",
		Importance:  85,
		CreatedAt:   time.Now(),
	}

	obj := FromMemory(m, "default")
	if obj == nil {
		t.Fatal("expected non-nil KnowledgeObject")
	}

	expectedID := "mem_mem_abc"
	if obj.ID != expectedID {
		t.Errorf("expected ID '%s', got '%s'", expectedID, obj.ID)
	}
	if obj.Type != knowledge.ObjectMemory {
		t.Errorf("expected ObjectMemory, got %s", obj.Type)
	}
	if obj.Confidence != 0.85 {
		t.Errorf("expected confidence 0.85, got %f", obj.Confidence)
	}
	if obj.Summary != "Key insight about caching strategies" {
		t.Errorf("unexpected summary: %s", obj.Summary)
	}
}

func TestFromMemoryNil(t *testing.T) {
	obj := FromMemory(nil, "test")
	if obj != nil {
		t.Error("expected nil for nil input")
	}
}

func TestFromMemoryProfile(t *testing.T) {
	m := &distillation.Memory{
		ID:      "user_001",
		Type:    distillation.MemoryProfile,
		Content: "User prefers Python over Go for data processing",
	}

	obj := FromMemory(m, "users")
	if obj.Type != knowledge.ObjectUser {
		t.Errorf("expected ObjectUser for profile memory, got %s", obj.Type)
	}
}

func TestFromMemoryTruncation(t *testing.T) {
	longContent := string(make([]byte, 300))
	for i := range longContent {
		longContent = "This is a very long memory content that should be truncated when converted to a KnowledgeObject summary field to keep token usage low for LLM consumption " + string(rune('A'+i%26))
	}

	m := &distillation.Memory{
		ID:      "long",
		Type:    distillation.MemoryKnowledge,
		Content: longContent,
	}

	obj := FromMemory(m, "test")
	if len(obj.Summary) > 210 {
		t.Errorf("expected truncated summary, got %d chars", len(obj.Summary))
	}
}

func TestFromMemories(t *testing.T) {
	memories := []*distillation.Memory{
		{ID: "m1", Type: distillation.MemoryKnowledge, Content: "first"},
		{ID: "m2", Type: distillation.MemoryPreference, Content: "second"},
		{ID: "m3", Type: distillation.MemoryInteraction, Content: "third"},
	}

	objects := FromMemories(memories, "ns")
	if len(objects) != 3 {
		t.Errorf("expected 3 objects, got %d", len(objects))
	}
}

func TestFromMemoriesWithNil(t *testing.T) {
	memories := []*distillation.Memory{
		{ID: "m1", Type: distillation.MemoryKnowledge, Content: "valid"},
		nil,
		{ID: "m2", Type: distillation.MemoryKnowledge, Content: "valid"},
	}

	objects := FromMemories(memories, "ns")
	if len(objects) != 2 {
		t.Errorf("expected 2 objects (nil skipped), got %d", len(objects))
	}
}
