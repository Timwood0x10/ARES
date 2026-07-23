// Package compiler — tests for Chinese-aware extraction and sentence splitting.
package compiler

import (
	"context"
	"strings"
	"testing"
	"time"
)

// chineseSample is a realistic Chinese technical discussion used to validate
// that extraction yields meaningful (non-fragment) knowledge for CJK input.
const chineseSample = "我们决定采用Rust作为进化系统的主语言。" +
	"对话压缩模块实现了zero-LLM的知识提取，替代了旧有的LLM蒸馏方案。" +
	"我们必须保证中文对话也能被正确提取。" +
	"不过Rust的编译速度比Go慢，代价是构建时间变长。" +
	"知识图谱用于存储蒸馏后的记忆节点。" +
	"待确认：是否需要中文NER支持。"

// TestExtractChineseConversation validates Phase 1 gating: Chinese input must
// produce entities, facts, at least one decision, and multi-word objects.
func TestExtractChineseConversation(t *testing.T) {
	extractor := NewAKGExtractor()
	messages := []SourceMessage{
		{ID: "m1", Role: "user", Content: chineseSample, Timestamp: time.Now()},
	}

	entities, facts, err := extractor.Extract(context.Background(), messages)
	if err != nil {
		t.Fatalf("extract returned error: %v", err)
	}

	// Gate 1: a meaningful number of entities is extracted.
	if len(entities) < 5 {
		t.Errorf("expected at least 5 entities for Chinese input, got %d: %+v", len(entities), entityNames(entities))
	}

	// Gate 2: at least one Chinese concept term is recognized.
	if !containsEntity(entities, "知识图谱") {
		t.Errorf("expected Chinese concept '知识图谱' to be extracted, got %+v", entityNames(entities))
	}

	// Gate 3: at least one decision node is detected.
	hasDecision := false
	for _, e := range entities {
		if strings.HasPrefix(e.Type, "decision_") {
			hasDecision = true
			break
		}
	}
	if !hasDecision {
		t.Errorf("expected at least one decision entity, got %+v", entityNames(entities))
	}

	// Gate 4: facts are extracted with multi-word (non-truncated) objects.
	if len(facts) < 2 {
		t.Errorf("expected at least 2 facts for Chinese input, got %d", len(facts))
	}
	foundMultiWord := false
	for _, f := range facts {
		if f.Subject == "对话压缩模块" && strings.Contains(f.Object, "知识提取") {
			foundMultiWord = true
		}
	}
	if !foundMultiWord {
		t.Errorf("expected a fact with subject '对话压缩模块' and a multi-word object, got %+v", facts)
	}
}

// TestExtractTripleMultiWordObject is an English regression ensuring the object
// of a triple is kept whole up to the clause boundary, not truncated to the
// first token.
func TestExtractTripleMultiWordObject(t *testing.T) {
	sentence := "ARES uses Patch for runtime updates, but sacrifices latency."
	triple := extractTriple(sentence)
	if triple == nil {
		t.Fatal("expected a triple to be extracted")
	}
	if triple.subject != "ARES" {
		t.Errorf("unexpected subject: %q", triple.subject)
	}
	if triple.object != "Patch for runtime updates" {
		t.Errorf("object should be multi-word, got %q", triple.object)
	}
}

// TestSplitSentencesDecimalAndAbbreviation validates that splitSentences does
// not break on decimal points, version numbers, or dotted abbreviations.
func TestSplitSentencesDecimalAndAbbreviation(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantCount   int
		mustContain []string
	}{
		{
			name:        "decimal and version numbers stay intact",
			input:       "See v0.2.7 and 3.14. Done.",
			wantCount:   2,
			mustContain: []string{"v0.2.7", "3.14"},
		},
		{
			name:        "dotted abbreviation stays intact",
			input:       "Check e.g. the lib. Done.",
			wantCount:   2,
			mustContain: []string{"e.g."},
		},
		{
			name:      "Chinese punctuation splits sentences",
			input:     "我们决定采用Rust。对话压缩实现了zero-LLM提取。",
			wantCount: 2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := splitSentences(tc.input)
			if len(got) != tc.wantCount {
				t.Errorf("expected %d sentences, got %d: %+v", tc.wantCount, len(got), got)
			}
			joined := strings.Join(got, " ")
			for _, want := range tc.mustContain {
				if !strings.Contains(joined, want) {
					t.Errorf("expected split result to contain %q, got %+v", want, got)
				}
			}
		})
	}
}

// TestExtractQuotedChineseTerms validates that Chinese spans enclosed in
// inline backticks, ASCII double quotes, and corner brackets are extracted as
// high-confidence entities.
func TestExtractQuotedChineseTerms(t *testing.T) {
	content := "我们用`中文抽取`和\"知识抽取\"以及「实体识别」来构建图谱。"
	entities := extractQuotedTermEntities(content, "m1", make(map[string]bool))
	for _, want := range []string{"中文抽取", "知识抽取", "实体识别"} {
		if !containsEntity(entities, want) {
			t.Errorf("expected quoted term %q to be extracted, got %+v", want, entityNames(entities))
		}
	}
}

// entityNames returns the names of the given entities for debug output.
func entityNames(entities []ExtractedEntity) []string {
	names := make([]string, 0, len(entities))
	for _, e := range entities {
		names = append(names, e.Name)
	}
	return names
}

// containsEntity reports whether entities contains an entity with the given name.
func containsEntity(entities []ExtractedEntity, name string) bool {
	for _, e := range entities {
		if e.Name == name {
			return true
		}
	}
	return false
}
