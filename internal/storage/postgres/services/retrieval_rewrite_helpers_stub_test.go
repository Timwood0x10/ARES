package services

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

// stubRewriteSvc builds a RetrievalService for pure rewrite/debug helpers:
// those methods only need a logger and never touch repositories.
func stubRewriteSvc() *RetrievalService {
	return &RetrievalService{logger: slog.Default()}
}

// TestRetrievalServiceParseLLMResponse_SplitsTrimsCaps pins the LLM response
// parser: line split, trim, blank-drop, hard cap at 3.
func TestRetrievalServiceParseLLMResponse_SplitsTrimsCaps(t *testing.T) {
	svc := stubRewriteSvc()
	got := svc.parseLLMResponse("  first query  \n\nsecond query\n\n third \nfourth\nfifth\n")
	require.Equal(t, []string{"first query", "second query", "third"}, got)

	require.Empty(t, svc.parseLLMResponse("\n   \n"))
}

// TestRetrievalServiceCalculateSimilarity_JaccardAndIdentity pins the Jaccard
// similarity used by rewrite validation.
func TestRetrievalServiceCalculateSimilarity_JaccardAndIdentity(t *testing.T) {
	svc := stubRewriteSvc()
	cases := []struct {
		s1, s2 string
		want   float64
	}{
		{"deploy service safely", "deploy service safely", 1.0},
		{"alpha beta", "gamma delta", 0.0},
		{"go error handling", "go error recovery", 2.0 / 4.0}, // {go,error} / {go,error,handling,recovery}
		{"", "", 1.0}, // identical short-circuit fires before union-0 branch
		{"onlyleft", "", 0.0},
	}
	for _, tc := range cases {
		require.InDelta(t, tc.want, svc.calculateSimilarity(tc.s1, tc.s2), 1e-9,
			"similarity(%q, %q)", tc.s1, tc.s2)
	}
}

// TestReplaceCaseInsensitive_MultiByteUTF8 pins the rewrite-time replacer:
// case-insensitive, rune-safe across multi-byte content.
func TestReplaceCaseInsensitive_MultiByteUTF8(t *testing.T) {
	require.Equal(t, "deploy Go now", replaceCaseInsensitive("deploy go now", "go", "Go"))
	require.Equal(t, "use 编程 guide", replaceCaseInsensitive("use 编程 guide", "GO", "Go"))
	require.Equal(t, "unchanged", replaceCaseInsensitive("unchanged", "", "x"))
	require.Equal(t, "no match here", replaceCaseInsensitive("no match here", "zz", "yy"))
}

// TestTokenizeAndIsWordChar_RuneClasses pins the tokenizer feeding Jaccard.
func TestTokenizeAndIsWordChar_RuneClasses(t *testing.T) {
	require.Equal(t, []string{"go", "error", "42"}, tokenize("go error 42"))
	require.Equal(t, []string{"trailing"}, tokenize("...trailing"))
	require.Empty(t, tokenize("   "))
	require.True(t, isWordChar('a'))
	require.True(t, isWordChar('Z'))
	require.True(t, isWordChar('7'))
	require.False(t, isWordChar('-'))
	require.False(t, isWordChar('_'))
	require.False(t, isWordChar(' '))
}

// TestRetrievalServiceValidateRewrites_ThreeRules pins the rewrite quality
// gate: similarity floor, 2× length cap, empty-drop.
func TestRetrievalServiceValidateRewrites_ThreeRules(t *testing.T) {
	svc := stubRewriteSvc()
	original := "deploy service safely"
	rewrites := []string{
		"deploy service safely now",         // keeps: similar + within 2×
		"completely unrelated quantum math", // drops: similarity < 0.6
		"",                                  // drops: empty
		"deploy service safely with an extremely long tail that exceeds the length budget by far", // drops: >2×
	}
	got := svc.validateRewrites(original, rewrites)
	require.Equal(t, []string{"deploy service safely now"}, got)
}

// TestRetrievalServiceUniqueRewrites_DedupPreservesOrder pins first-occurrence
// order preservation.
func TestRetrievalServiceUniqueRewrites_DedupPreservesOrder(t *testing.T) {
	svc := stubRewriteSvc()
	got := svc.uniqueRewrites([]string{"b", "a", "b", "c", "a"})
	require.Equal(t, []string{"b", "a", "c"}, got)
}

// TestRetrievalServiceGenerateDebugInfo_PlanAndSignals pins the debug-info
// read model: nil-plan default weights vs plan weights, experience/tool
// signals, breakdown keys, sub-source weights.
func TestRetrievalServiceGenerateDebugInfo_PlanAndSignals(t *testing.T) {
	svc := stubRewriteSvc()

	expResult := &SearchResult{
		ID: "exp-dbg", Score: 0.9, Query: stubQueryLong, QueryWeight: 0.7,
		Source: "experience", SubSource: "keyword",
		Metadata: map[string]any{
			"success": true, "reuse_count": 5,
			"execution_time": 0.5, "lessons": "use rolling updates",
		},
	}
	info := svc.GenerateDebugInfo(expResult, nil)
	require.InDelta(t, 1.2, info.SourceWeight, 1e-9, "nil plan defaults experience to 1.2")
	require.InDelta(t, 0.8, info.SubWeight, 1e-9, "keyword sub-source weight is 0.8")
	require.Equal(t, true, info.Signals["success"])
	require.Equal(t, 5, info.Signals["reuse_count"])
	require.InDelta(t, 0.5, info.Signals["execution_time"].(float64), 1e-9)
	require.Equal(t, "use rolling updates", info.Signals["lessons"])
	require.InDelta(t, 0.7, info.Breakdown["query"], 1e-9)
	require.InDelta(t, 1.2, info.Breakdown["source"], 1e-9)
	require.InDelta(t, 0.8, info.Breakdown["sub_source"], 1e-9)

	plan := &RetrievalPlan{ExperienceWeight: 0.3, ToolsWeight: 0.2, KnowledgeWeight: 0.4}
	infoPlanned := svc.GenerateDebugInfo(expResult, plan)
	require.InDelta(t, 0.3, infoPlanned.SourceWeight, 1e-9)

	toolResult := &SearchResult{
		ID: "tool-dbg", Source: "tool", SubSource: "vector",
		Metadata: map[string]any{"requires_auth": true, "success_rate": 0.4},
	}
	toolInfo := svc.GenerateDebugInfo(toolResult, plan)
	require.InDelta(t, 0.2, toolInfo.SourceWeight, 1e-9)
	require.InDelta(t, 1.0, toolInfo.SubWeight, 1e-9)
	require.Equal(t, true, toolInfo.Signals["requires_auth"])
	require.InDelta(t, 0.4, toolInfo.Signals["success_rate"].(float64), 1e-9)
}

// TestRetrievalServiceBm25Search_EmptyQuery pins the bm25 wrapper's empty-
// query short-circuit.
func TestRetrievalServiceBm25Search_EmptyQueryDirect(t *testing.T) {
	svc := stubRetrievalSvc(nil, nil, nil, nil, stubGuard(t))
	got := svc.bm25Search(context.Background(), &SearchRequest{
		Query: "", Plan: &RetrievalPlan{TopK: 5},
	})
	require.Empty(t, got)
}
