package services

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	memembed "github.com/Timwood0x10/ares/internal/runtime/memory/embedding"
	"github.com/Timwood0x10/ares/internal/storage/postgres"
	postgresembedding "github.com/Timwood0x10/ares/internal/storage/postgres/embedding"
)

// fakePipeline is a memembed.EmbeddingPipeline returning canned vectors.
type fakePipeline struct{}

func (fakePipeline) BuildSpec(kind memembed.EmbeddingKind, payload any) (memembed.EmbeddingSpec, error) {
	return memembed.BuildMemoryQuerySpec(payload.(string), "fake-model", 1, 0), nil
}

func (fakePipeline) Embed(_ context.Context, _ memembed.EmbeddingSpec) ([]float64, error) {
	return []float64{0.1, 0.2}, nil
}

func (fakePipeline) Model() string { return "fake-model" }

// TestEmbeddingCacheIsLRUNotFIFO locks REVIEW 3.7: a cache hit did not
// refresh recency, so the "LRU" cache was actually FIFO — with a full cache,
// a hot entry (repeatedly hit) was evicted as eagerly as a cold one. After
// the fix, hits move the key to the back of the access list and eviction
// removes the truly least-recently-used entry.
func TestEmbeddingCacheIsLRUNotFIFO(t *testing.T) {
	svc := &RetrievalService{
		logger:                   slog.Default(),
		embeddingCache:           map[string][]float64{},
		embeddingCacheSizeLimit:  2,
		embeddingCacheAccessList: make([]string, 0, 4),
		pipeline:                 fakePipeline{},
	}
	ctx := context.Background()

	// Fill the cache to its limit: a (oldest), b.
	if got := svc.getEmbeddingCached(ctx, "a"); len(got) != 2 {
		t.Fatalf("embed a: %v", got)
	}
	if got := svc.getEmbeddingCached(ctx, "b"); len(got) != 2 {
		t.Fatalf("embed b: %v", got)
	}

	// Touch "a" so it becomes most recently used.
	if got := svc.getEmbeddingCached(ctx, "a"); len(got) != 2 {
		t.Fatalf("re-hit a: %v", got)
	}

	// Insert "c": the cache is at its limit, so exactly one entry must be
	// evicted. LRU says "b" (least recently used); FIFO would evict "a".
	if got := svc.getEmbeddingCached(ctx, "c"); len(got) != 2 {
		t.Fatalf("embed c: %v", got)
	}

	svc.embeddingCacheMu.RLock()
	defer svc.embeddingCacheMu.RUnlock()
	if _, ok := svc.embeddingCache["a"]; !ok {
		t.Error("hot entry 'a' was evicted — cache is FIFO, not LRU")
	}
	if _, ok := svc.embeddingCache["b"]; ok {
		t.Error("cold entry 'b' should have been evicted as least recently used")
	}
	if _, ok := svc.embeddingCache["c"]; !ok {
		t.Error("new entry 'c' missing from cache")
	}
}

// TestEmbeddingFailureOpensCircuitBreaker locks REVIEW 3.7: searchSingleQuery
// recorded an embedding SUCCESS even when getEmbeddingCached returned nothing
// (the embedding call failed), so the breaker never opened and a dead
// embedding service was retried on every search forever. After the fix an
// empty embedding counts as a failure, and enough consecutive failures open
// the breaker.
func TestEmbeddingFailureOpensCircuitBreaker(t *testing.T) {
	// Unreachable endpoint: every Embed call fails fast (connection refused).
	deadClient := postgresembedding.NewEmbeddingClient("http://127.0.0.1:1", "fake-model", nil, 500*time.Millisecond)
	svc := &RetrievalService{
		logger:                   slog.Default(),
		embeddingCache:           map[string][]float64{},
		embeddingCacheSizeLimit:  10,
		embeddingCacheAccessList: make([]string, 0, 10),
		embeddingClient:          deadClient,
		retrievalGuard:           postgres.NewRetrievalGuard(1000, 3, time.Minute, time.Second),
	}

	req := &SearchRequest{
		Query:    "anything",
		TenantID: "tenant-1",
		// No keyword search, no vector sources: the embedding call is the
		// only thing this exercise drives.
		Plan: &RetrievalPlan{},
	}

	for i := 0; i < 3; i++ {
		svc.searchSingleQuery(context.Background(), WeightedQuery{Query: "q", Weight: 1}, req)
	}

	if err := svc.retrievalGuard.CheckEmbeddingCircuitBreaker(); err == nil {
		t.Fatal("circuit breaker must open after repeated embedding failures; " +
			"embedding failures were being recorded as successes")
	}
}

// TestLoadSynonymRulesRejectsSiblingDirectoryPrefix locks the REVIEW 3.7
// path-check fix: the allowed-directory guard must require a separator
// boundary, not a bare string prefix — /x/synonyms-evil next to an allowed
// /x/synonyms used to pass HasPrefix and let the sibling's YAML be loaded.
func TestLoadSynonymRulesRejectsSiblingDirectoryPrefix(t *testing.T) {
	base := t.TempDir()
	allowed := filepath.Join(base, "synonyms")
	sibling := filepath.Join(base, "synonyms-evil")
	for _, dir := range []string{allowed, sibling} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	evilRule := map[string]any{"evil-marker": []string{"pwned"}}
	data, err := yaml.Marshal(evilRule)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sibling, "synonyms.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	goodRule := map[string]any{"good-marker": []string{"fine"}}
	data, err = yaml.Marshal(goodRule)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(allowed, "synonyms.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	SetAllowedSynonymDir(allowed)
	defer SetAllowedSynonymDir("")

	// Sibling path sharing the prefix must be rejected → default rules.
	t.Setenv("SYNONYM_CONFIG_PATH", filepath.Join(sibling, "synonyms.yaml"))
	rules := loadSynonymRules()
	if _, ok := rules["evil-marker"]; ok {
		t.Fatal("sibling directory with shared prefix must be rejected")
	}
	if _, ok := rules["how to"]; !ok {
		t.Fatalf("default rules must be used when the path is rejected; got %v", rules)
	}

	// A path inside the allowed dir must be loaded.
	t.Setenv("SYNONYM_CONFIG_PATH", filepath.Join(allowed, "synonyms.yaml"))
	rules = loadSynonymRules()
	if _, ok := rules["good-marker"]; !ok {
		t.Fatalf("allowed path must be loaded; got %v", rules)
	}
}
