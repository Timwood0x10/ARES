// Package services — regression tests for the precision-path nil-repository
// guards and the query-rewrite gate.
package services

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// TestPrecisionPaths_NilKBRepoDegrades locks S-14: NewRetrievalService does
// not require kbRepo, yet the precision pipeline dereferenced it. Only
// searchKnowledgeVector carried a nil guard, which made the guard asymmetric:
// the vector fallback was safe while the two stages that run BEFORE it were
// not. Precision mode is triggered by every query of ≤10 runes, so this was
// the most common entry point.
//
// The production contract is fail-LOUD (searchPrecision's own guard): a
// missing knowledge base is a misconfiguration, and silently returning "no
// results" would hide it. This test locks that the entry point returns an
// error naming the gap instead of panicking the request goroutine.
func TestPrecisionPaths_NilKBRepoDegrades(t *testing.T) {
	s := &RetrievalService{logger: slog.Default(), kbRepo: nil}
	req := &SearchRequest{Query: "hello", TenantID: "t1", TopK: 5}

	// Must return an error, not panic. A nil-kbRepo dereference panics, which
	// fails this test — that is the pre-fix behaviour.
	_, err := s.searchPrecision(context.Background(), req)
	if err == nil {
		t.Fatal("searchPrecision with nil kbRepo must fail loud, not return empty results")
	}
	if !strings.Contains(err.Error(), "knowledge base") {
		t.Errorf("error should name the missing knowledge base, got: %v", err)
	}
}

// TestBuildQueries_RewriteGate locks S-15: shouldRewriteQuery (which enforces
// the minimum-length rule and the query cache) existed but had no production
// caller, so buildQueries invoked llmBasedRewrite on every Search whenever
// EnableQueryRewrite was set. The cache was written but never read.
//
// The gate must (a) skip the LLM for queries it rejects and (b) record the
// query in the cache so a repeat Search skips the LLM again.
func TestBuildQueries_RewriteGate(t *testing.T) {
	s := &RetrievalService{
		logger:           slog.Default(),
		queryPriority:    DefaultQueryPriorityConfig(),
		queryCache:       make(map[string]time.Time),
		queryCacheMaxLen: 8,
		queryCacheTTL:    time.Minute,
	}
	plan := DefaultRetrievalPlan()
	plan.EnableQueryRewrite = true

	// A short query is rejected by the length rule, so it must never be
	// recorded as rewritten in the cache.
	short := "hi"
	s.buildQueries(context.Background(), short, plan)
	if s.isQueryInCache(short) {
		t.Errorf("short query %q must not be marked rewritten; the length gate did not run", short)
	}

	// A long, complex query passes the gate. With no LLM configured the
	// rewrite yields nothing, but the query must still be recorded so a
	// second identical Search skips the LLM entirely.
	long := "why does the scheduler requeue an expired lease task"
	s.buildQueries(context.Background(), long, plan)
	if !s.isQueryInCache(long) {
		t.Errorf("gated query %q must be recorded in the rewrite cache after buildQueries", long)
	}

	// Now that it is cached, the gate must reject it.
	if s.shouldRewriteQuery(long) {
		t.Errorf("cached query %q must be rejected by shouldRewriteQuery", long)
	}
}
