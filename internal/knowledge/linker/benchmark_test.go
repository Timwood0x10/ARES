package linker

import (
	"context"
	"fmt"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// makeObjects generates n KnowledgeObjects with overlapping tags so linkers
// have real edges to produce. Object types are cycled so we get a mix of
// Decision / Code / Memory nodes — the same setup the unit tests use.
func makeObjects(n int) []*knowledge.KnowledgeObject {
	objs := make([]*knowledge.KnowledgeObject, n)
	types := []knowledge.ObjectType{
		knowledge.ObjectDecision,
		knowledge.ObjectCode,
		knowledge.ObjectMemory,
	}
	tagSets := [][]string{
		{"cache", "redis"},
		{"auth", "user"},
		{"queue", "kafka"},
		{"db", "postgres"},
	}
	for i := 0; i < n; i++ {
		objs[i] = &knowledge.KnowledgeObject{
			ID:         fmt.Sprintf("obj-%d", i),
			Type:       types[i%len(types)],
			Summary:    fmt.Sprintf("Object %d: %s context", i, tagSets[i%len(tagSets)][0]),
			Tags:       tagSets[i%len(tagSets)],
			Confidence: 0.9,
		}
	}
	return objs
}

func BenchmarkDecisionLinker(b *testing.B) {
	l := &DecisionLinker{}
	ctx := context.Background()

	for _, n := range []int{10, 50, 100, 500} {
		objs := makeObjects(n)
		b.Run(fmt.Sprintf("objs_%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, err := l.Link(ctx, objs)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkArchitectureLinker(b *testing.B) {
	l := &ArchitectureLinker{}
	ctx := context.Background()

	for _, n := range []int{10, 50, 100, 500} {
		objs := makeObjects(n)
		b.Run(fmt.Sprintf("objs_%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, err := l.Link(ctx, objs)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkTimelineLinker(b *testing.B) {
	l := &TimelineLinker{}
	ctx := context.Background()

	for _, n := range []int{10, 50, 100, 500} {
		objs := makeObjects(n)
		b.Run(fmt.Sprintf("objs_%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, err := l.Link(ctx, objs)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkSimilarityLinker(b *testing.B) {
	l := &SimilarityLinker{}
	ctx := context.Background()

	for _, n := range []int{10, 50, 100, 500} {
		objs := makeObjects(n)
		b.Run(fmt.Sprintf("objs_%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, err := l.Link(ctx, objs)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
