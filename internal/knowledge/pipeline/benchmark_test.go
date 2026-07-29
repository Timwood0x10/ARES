package pipeline

import (
	"context"
	"fmt"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// makeRawObject builds a KnowledgeObject whose Raw field contains realistic
// noisy text that the normalizer must clean (control chars, excess whitespace,
// null bytes).
func makeRawObject(i int) *knowledge.KnowledgeObject {
	raw := fmt.Sprintf("  \tHello\tWorld!  \n\nThis is object %d.\x00\x00  ", i)
	return &knowledge.KnowledgeObject{
		ID:   fmt.Sprintf("obj-%d", i),
		Type: knowledge.ObjectDecision,
		Raw:  []byte(raw),
	}
}

func BenchmarkDefaultNormalizer_Normalize(b *testing.B) {
	n := &DefaultNormalizer{}
	ctx := context.Background()

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		obj := makeRawObject(i)
		_, err := n.Normalize(ctx, obj)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDefaultNormalizer_AlreadyNormalized(b *testing.B) {
	n := &DefaultNormalizer{}
	ctx := context.Background()

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		obj := &knowledge.KnowledgeObject{
			ID:         fmt.Sprintf("obj-%d", i),
			Normalized: "already clean text",
		}
		_, err := n.Normalize(ctx, obj)
		if err != nil {
			b.Fatal(err)
		}
	}
}
