package pipeline

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// TestMatchTokensAndMatchAgree locks the tokenize-once fast path's
// equivalence contract: MatchTokens (pipeline-fed cache) and the legacy
// Match (self-tokenizing) must resolve identically for the same inputs —
// the cache is an optimization, never a semantic change.
func TestMatchTokensAndMatchAgree(t *testing.T) {
	m := &DefaultEntityMatcher{MatchThreshold: 0.6}
	ctx := context.Background()

	candidates := []*knowledge.KnowledgeObject{
		{ID: "a", Normalized: "redis cache strategy session", Summary: "cache eviction policy"},
		{ID: "b", Normalized: "unrelated database migration", Summary: "schema evolution plan"},
		{ID: "c", Normalized: "redis cache tuning guide", Summary: "cache eviction details"},
	}
	obj := &knowledge.KnowledgeObject{
		ID: "new", Normalized: "redis cache strategy", Summary: "cache eviction",
	}

	legacy, err := m.Match(ctx, obj, candidates)
	if err != nil {
		t.Fatalf("legacy Match: %v", err)
	}

	cache := map[string]map[string]int{}
	objTokens := map[string]int{}
	for _, w := range strings_Fields_norm("redis cache strategy cache eviction") {
		objTokens[w]++
	}
	for _, c := range candidates {
		bag := map[string]int{}
		for _, w := range strings_Fields_norm(c.Normalized + " " + c.Summary) {
			bag[w]++
		}
		cache[c.ID] = bag
	}

	fast, err := m.MatchTokens(ctx, obj, objTokens, candidates, cache)
	if err != nil {
		t.Fatalf("MatchTokens: %v", err)
	}

	if legacy.IsNew != fast.IsNew || legacy.MatchedObjectID != fast.MatchedObjectID {
		t.Fatalf("fast path diverged: legacy=%+v fast=%+v", legacy, fast)
	}
	if legacy.Confidence != fast.Confidence {
		t.Fatalf("confidence diverged: legacy=%v fast=%v", legacy.Confidence, fast.Confidence)
	}
	if fast.IsNew {
		t.Fatal("expected a match: the object overlaps candidate a heavily")
	}
	if fast.MatchedObjectID != legacy.MatchedObjectID {
		t.Fatalf("best match diverged: fast=%q legacy=%q", fast.MatchedObjectID, legacy.MatchedObjectID)
	}
}

// strings_Fields_norm mirrors the matcher's tokenization for building bags
// in tests (split on whitespace, lowercase handled by input).
func strings_Fields_norm(s string) []string {
	out := []string{}
	start := -1
	for i, r := range s {
		if r == ' ' || r == '\t' || r == '\n' {
			if start >= 0 {
				out = append(out, s[start:i])
				start = -1
			}
		} else if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		out = append(out, s[start:])
	}
	return out
}
