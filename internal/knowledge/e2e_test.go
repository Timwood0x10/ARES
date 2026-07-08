package knowledge_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/compiler"
	"github.com/Timwood0x10/ares/internal/knowledge/pipeline"
	"github.com/Timwood0x10/ares/internal/knowledge/planner"
	"github.com/Timwood0x10/ares/internal/knowledge/provider"
	"github.com/Timwood0x10/ares/internal/knowledge/retriever"
	"github.com/Timwood0x10/ares/internal/knowledge/runtime"
)

type e2eProvider struct {
	name    string
	objects []*knowledge.KnowledgeObject
}

func (p *e2eProvider) Name() string                           { return p.name }
func (p *e2eProvider) IntentMatch(_ knowledge.Intent) float64 { return 0.9 }
func (p *e2eProvider) Stream(_ context.Context, _ knowledge.Intent) (<-chan *knowledge.KnowledgeObject, <-chan error) {
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

type e2eQueryPlanner struct{}

func (q *e2eQueryPlanner) PlanQuery(_ context.Context, req planner.KnowledgeRequirement, _, _ string) (*planner.QueryPlan, error) {
	return &planner.QueryPlan{Query: req.Description, QueryType: planner.QuerySQL, MaxResults: req.MaxResults}, nil
}

// TestAKFFullE2E verifies the complete AKF pipeline:
//
//	Query → Planner → SourceDiscovery → Provider(Stream) → Pipeline → Link → Reduce → Compile
func TestAKFFullE2E(t *testing.T) {
	memProvider := &e2eProvider{
		name: "memory",
		objects: []*knowledge.KnowledgeObject{
			{
				ID: "mem:redis1", Type: knowledge.ObjectDecision, Namespace: "memory",
				Summary: "Chose Redis for caching layer", Normalized: "Redis is used as cache",
				Raw: []byte("Decision: Use Redis for caching"), Confidence: 0.9,
				Tags: []string{"redis", "cache", "decision"},
			},
			{
				ID: "mem:pg1", Type: knowledge.ObjectDecision, Namespace: "memory",
				Summary: "Chose PostgreSQL for storage", Normalized: "PostgreSQL is the primary DB",
				Raw: []byte("Decision: Use PostgreSQL for persistence"), Confidence: 0.85,
				Tags: []string{"postgres", "database", "decision"},
			},
		},
	}
	codeProvider := &e2eProvider{
		name: "code",
		objects: []*knowledge.KnowledgeObject{
			{
				ID: "code:redis_cache", Type: knowledge.ObjectCode, Namespace: "code",
				Summary: "Redis cache implementation in Go", Normalized: "cache.NewRedisCache()",
				Confidence: 0.95, Tags: []string{"redis", "golang", "cache"},
			},
		},
	}

	reg := provider.NewProviderRegistry()
	if err := reg.Register(memProvider); err != nil {
		t.Fatalf("register memory: %v", err)
	}
	if err := reg.Register(codeProvider); err != nil {
		t.Fatalf("register code: %v", err)
	}

	pipe := knowledge.NewKnowledgePipeline(
		[]knowledge.Normalizer{&pipeline.DefaultNormalizer{MaxRawBytes: 4096}},
		[]knowledge.EntityMatcher{&pipeline.DefaultEntityMatcher{MatchThreshold: 0.6}},
		[]knowledge.Validator{&pipeline.DefaultValidator{}},
		[]knowledge.Summarizer{&pipeline.DefaultSummarizer{MaxSummaryLen: 100}},
	)

	qp := &e2eQueryPlanner{}
	sd := planner.NewSourceDiscovery(reg, qp)
	pl := planner.NewKnowledgePlanner()

	linkers := []runtime.Linker{&runtime.DefaultLinker{}}
	reducers := []runtime.Reducer{&runtime.DefaultReducer{}}
	rt := runtime.New(pl, sd, reg, pipe, linkers, reducers)

	comp := compiler.NewDefaultCompiler()
	ret := retriever.New(rt, comp)

	budget := knowledge.TokenBudget{
		MaxTokens: 5000,
		ForGraph:  3000,
		Reserved:  2000,
	}

	graph, err := rt.Execute(context.Background(), "Why Redis?", budget, &runtime.Config{
		MaxConcurrentProviders: 2,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(graph.Nodes) == 0 {
		t.Fatal("expected at least 1 node in graph")
	}
	t.Logf("Graph: %d nodes, %d edges", len(graph.Nodes), len(graph.Edges))

	foundRedis := false
	for id := range graph.Nodes {
		if strings.Contains(id, "redis") {
			foundRedis = true
		}
	}
	if !foundRedis {
		t.Error("expected at least one Redis-related node")
	}

	compiled, err := comp.Compile(context.Background(), graph, compiler.CompileConfig{
		Formats: []compiler.Format{compiler.FormatPrompt, compiler.FormatJSON},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if len(compiled.Formats) != 2 {
		t.Errorf("expected 2 formats, got %d", len(compiled.Formats))
	}
	promptContent, ok := compiled.Formats[compiler.FormatPrompt]
	if !ok || promptContent == "" {
		t.Error("expected non-empty Prompt content")
	}
	t.Logf("Compiled: %d tokens", compiled.Metrics.OutputTokens)

	retResult, err := ret.Retrieve(context.Background(), retriever.Query{
		Text: "Why Redis?", MaxResults: 10, MaxTokens: 4000,
		Formats: []compiler.Format{compiler.FormatPrompt},
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if retResult.Context == nil || retResult.Graph == nil {
		t.Fatal("retrieve returned nil context or graph")
	}
	if retResult.Query != "Why Redis?" {
		t.Errorf("expected query 'Why Redis?', got %q", retResult.Query)
	}
	t.Logf("Retrieve: %d nodes, %d tokens", len(retResult.Graph.Nodes), retResult.Context.Metrics.OutputTokens)
}

// TestAKFE2E_PipelineProcessing verifies Normalizer → Summarizer end-to-end.
func TestAKFE2E_PipelineProcessing(t *testing.T) {
	pipe := knowledge.NewKnowledgePipeline(
		[]knowledge.Normalizer{&pipeline.DefaultNormalizer{MaxRawBytes: 1024}},
		nil, nil,
		[]knowledge.Summarizer{&pipeline.DefaultSummarizer{MaxSummaryLen: 30}},
	)

	obj := &knowledge.KnowledgeObject{
		ID:  "test:raw1",
		Raw: []byte("  This   is   a   VERY   long   text  \n\n  \t  indeed!  "),
	}

	result, err := pipe.Process(context.Background(), obj)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if result.Normalized == "" {
		t.Error("Normalized should not be empty")
	}
	if result.Summary == "" {
		t.Error("Summary should not be empty")
	}
	if len(result.Summary) > 33 {
		t.Errorf("Summary too long (%d chars)", len(result.Summary))
	}
}

// TestAKFE2E_ProviderConcurrency verifies concurrent provider loading.
func TestAKFE2E_ProviderConcurrency(t *testing.T) {
	reg := provider.NewProviderRegistry()
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("p%d", i)
		_ = reg.Register(&e2eProvider{
			name: name,
			objects: []*knowledge.KnowledgeObject{
				{ID: name + ":obj1", Summary: name + " object", Confidence: 0.8},
			},
		})
	}

	sd := planner.NewSourceDiscovery(reg, &e2eQueryPlanner{})
	pl := planner.NewKnowledgePlanner()
	rt := runtime.New(pl, sd, reg, nil, nil, nil)

	budget := knowledge.TokenBudget{MaxTokens: 5000, ForGraph: 3000, Reserved: 2000}
	graph, err := rt.Execute(context.Background(), "test", budget, &runtime.Config{
		MaxConcurrentProviders: 5,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(graph.Nodes) < 5 {
		t.Errorf("expected at least 5 nodes, got %d", len(graph.Nodes))
	}
}

// TestAKFE2E_CompileAllFormats verifies every compiler format works.
func TestAKFE2E_CompileAllFormats(t *testing.T) {
	graph := &knowledge.WorkingGraph{
		Nodes: map[string]*knowledge.KnowledgeObject{
			"n1": {ID: "n1", Type: knowledge.ObjectDecision, Summary: "Test", Confidence: 0.9},
		},
		Edges: []knowledge.Relation{},
	}

	comp := compiler.NewDefaultCompiler()
	allFormats := []compiler.Format{
		compiler.FormatPrompt, compiler.FormatMarkdown,
		compiler.FormatJSON, compiler.FormatXML, compiler.FormatToolSchema,
	}

	compiled, err := comp.Compile(context.Background(), graph, compiler.CompileConfig{Formats: allFormats})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	for _, f := range allFormats {
		content, ok := compiled.Formats[f]
		if !ok || content == "" {
			t.Errorf("missing or empty format: %s", f)
		}
	}
}
