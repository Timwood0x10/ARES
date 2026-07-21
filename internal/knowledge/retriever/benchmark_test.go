package retriever

import (
	"context"
	"fmt"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/compiler"
	"github.com/Timwood0x10/ares/internal/knowledge/planner"
	"github.com/Timwood0x10/ares/internal/knowledge/provider"
	"github.com/Timwood0x10/ares/internal/knowledge/runtime"
)

// benchProvider streams a fixed set of objects for retrieval benchmarks.
type benchProvider struct {
	name    string
	objects []*knowledge.KnowledgeObject
}

func (p *benchProvider) Name() string                           { return p.name }
func (p *benchProvider) IntentMatch(_ knowledge.Intent) float64 { return 0.9 }
func (p *benchProvider) Stream(_ context.Context, _ knowledge.Intent) (<-chan *knowledge.KnowledgeObject, <-chan error) {
	ch := make(chan *knowledge.KnowledgeObject, len(p.objects))
	errCh := make(chan error, 1)
	go func() {
		defer close(ch)
		defer close(errCh)
		for _, obj := range p.objects {
			ch <- obj
		}
	}()
	return ch, errCh
}

func makeBenchObjects(n int) []*knowledge.KnowledgeObject {
	objs := make([]*knowledge.KnowledgeObject, n)
	types := []knowledge.ObjectType{
		knowledge.ObjectDecision,
		knowledge.ObjectCode,
		knowledge.ObjectArchitecture,
	}
	for i := 0; i < n; i++ {
		objs[i] = &knowledge.KnowledgeObject{
			ID:         fmt.Sprintf("obj-%d", i),
			Type:       types[i%len(types)],
			Summary:    fmt.Sprintf("Benchmark object %d about Redis cache strategy", i),
			Tags:       []string{"cache", "redis", "session"},
			Confidence: 0.9,
		}
	}
	return objs
}

func BenchmarkRetriever_Retrieve(b *testing.B) {
	for _, n := range []int{10, 50, 100, 500} {
		// Build a fresh runtime per size to keep setup out of benchmark timing.
		reg := provider.NewProviderRegistry()
		_ = reg.Register(&benchProvider{
			name:    "bench-memory",
			objects: makeBenchObjects(n),
		})
		sd := planner.NewSourceDiscovery(reg, &testQueryPlanner{})
		p := planner.NewKnowledgePlanner()
		rt := runtime.New(p, sd, reg, nil,
			[]runtime.Linker{&runtime.DefaultLinker{}},
			[]runtime.Reducer{&runtime.DefaultReducer{}},
		)
		comp := compiler.NewDefaultCompiler()
		ret := New(rt, comp)

		b.Run(fmt.Sprintf("objs_%d", n), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := ret.Retrieve(context.Background(), Query{
					Text:       "Why Redis for session caching?",
					MaxResults: 10,
					MaxTokens:  2000,
					Formats:    []compiler.Format{compiler.FormatPrompt},
				})
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkRetriever_RetrieveMultipleFormats(b *testing.B) {
	reg := provider.NewProviderRegistry()
	_ = reg.Register(&benchProvider{
		name:    "bench-memory",
		objects: makeBenchObjects(100),
	})
	sd := planner.NewSourceDiscovery(reg, &testQueryPlanner{})
	p := planner.NewKnowledgePlanner()
	rt := runtime.New(p, sd, reg, nil,
		[]runtime.Linker{&runtime.DefaultLinker{}},
		[]runtime.Reducer{&runtime.DefaultReducer{}},
	)
	comp := compiler.NewDefaultCompiler()
	ret := New(rt, comp)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := ret.Retrieve(context.Background(), Query{
			Text:       "Why Redis for session caching?",
			MaxResults: 10,
			MaxTokens:  2000,
			Formats:    []compiler.Format{compiler.FormatPrompt, compiler.FormatJSON, compiler.FormatMarkdown},
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}
