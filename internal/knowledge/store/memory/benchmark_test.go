package memorystore

import (
	"context"
	"fmt"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// makeObject builds a single KnowledgeObject with a realistic payload.
func makeObject(i int) *knowledge.KnowledgeObject {
	return &knowledge.KnowledgeObject{
		ID:         fmt.Sprintf("obj-%d", i),
		Type:       knowledge.ObjectDecision,
		Summary:    fmt.Sprintf("Decision %d: use Redis for session caching", i),
		Tags:       []string{"cache", "redis", "session"},
		Confidence: 0.9,
		Normalized: fmt.Sprintf("normalized text for object %d", i),
		Raw:        []byte(fmt.Sprintf("raw bytes for object %d with some content", i)),
	}
}

func BenchmarkStore_Save(b *testing.B) {
	s := New()
	ctx := context.Background()

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		obj := makeObject(i)
		_ = s.Save(ctx, obj)
	}
}

func BenchmarkStore_SaveBatch10(b *testing.B) {
	ctx := context.Background()

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s := New()
		objs := make([]*knowledge.KnowledgeObject, 10)
		for j := 0; j < 10; j++ {
			objs[j] = makeObject(i*10 + j)
		}
		_ = s.Save(ctx, objs...)
	}
}

func BenchmarkStore_SaveBatch100(b *testing.B) {
	ctx := context.Background()

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s := New()
		objs := make([]*knowledge.KnowledgeObject, 100)
		for j := 0; j < 100; j++ {
			objs[j] = makeObject(i*100 + j)
		}
		_ = s.Save(ctx, objs...)
	}
}

func BenchmarkStore_Get(b *testing.B) {
	s := New()
	ctx := context.Background()

	// Pre-populate 1000 objects.
	for i := 0; i < 1000; i++ {
		_ = s.Save(ctx, makeObject(i))
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.Get(ctx, fmt.Sprintf("obj-%d", i%1000))
	}
}

func BenchmarkStore_QueryByType(b *testing.B) {
	s := New()
	ctx := context.Background()

	// Pre-populate 500 objects cycling through types.
	types := []knowledge.ObjectType{
		knowledge.ObjectDecision,
		knowledge.ObjectCode,
		knowledge.ObjectMemory,
	}
	for i := 0; i < 500; i++ {
		obj := makeObject(i)
		obj.Type = types[i%len(types)]
		_ = s.Save(ctx, obj)
	}

	q := knowledge.Query{Types: []knowledge.ObjectType{knowledge.ObjectDecision}, Limit: 50}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.Query(ctx, q)
	}
}

func BenchmarkStore_Search(b *testing.B) {
	s := New()
	ctx := context.Background()

	// Pre-populate 500 objects with searchable text.
	for i := 0; i < 500; i++ {
		obj := makeObject(i)
		obj.Summary = fmt.Sprintf("Redis cache strategy %d", i)
		_ = s.Save(ctx, obj)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.Search(ctx, "Redis cache", "text-embedding-3-small", 10)
	}
}

func BenchmarkStore_Delete(b *testing.B) {
	ctx := context.Background()

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s := New()
		_ = s.Save(ctx, makeObject(0))
		_ = s.Delete(ctx, "obj-0")
	}
}
