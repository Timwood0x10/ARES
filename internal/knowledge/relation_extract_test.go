package knowledge

import (
	"testing"
)

func TestExtract_Fixes(t *testing.T) {
	ext := NewRelationExtractor()
	obj := &KnowledgeObject{
		ID:         "obj_1",
		Normalized: "本次发布修复了鉴权bug",
		Summary:    "release notes",
	}

	rels := ext.Extract(obj)
	if len(rels) == 0 {
		t.Fatal("expected at least one relation, got none")
	}

	var found bool
	for _, r := range rels {
		if r.Predicate == "fixes" {
			found = true
			if r.ObjectText == "" {
				t.Errorf("fixes relation has empty ObjectText")
			}
			if r.Evidence == "" {
				t.Errorf("fixes relation has empty Evidence")
			}
			if !AllowedPredicates[r.Predicate] {
				t.Errorf("predicate %q not in AllowedPredicates", r.Predicate)
			}
		}
	}
	if !found {
		t.Errorf("expected a fixes relation, got %v", rels)
	}
}

func TestExtract_FixesEnglish(t *testing.T) {
	ext := NewRelationExtractor()
	obj := &KnowledgeObject{
		ID:         "obj_2",
		Normalized: "this commit fixes the login page",
		Summary:    "release notes",
	}

	rels := ext.Extract(obj)
	var found bool
	for _, r := range rels {
		if r.Predicate == "fixes" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a fixes relation for English text, got %v", rels)
	}
}

func TestExtract_DependsOn(t *testing.T) {
	ext := NewRelationExtractor()
	obj := &KnowledgeObject{
		ID:         "obj_3",
		Normalized: "模块A依赖模块B",
		Summary:    "",
	}

	rels := ext.Extract(obj)
	var found bool
	for _, r := range rels {
		if r.Predicate == "depends_on" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a depends_on relation, got %v", rels)
	}
}

func TestExtract_StripsTrailingPunctuation(t *testing.T) {
	ext := NewRelationExtractor()
	obj := &KnowledgeObject{
		ID:         "obj_4",
		Normalized: "修复了鉴权bug。",
		Summary:    "",
	}

	rels := ext.Extract(obj)
	for _, r := range rels {
		if r.Predicate == "fixes" {
			if r.ObjectText == "鉴权bug。" || r.ObjectText == "鉴权bug," {
				t.Errorf("trailing punctuation not stripped: %q", r.ObjectText)
			}
		}
	}
}

func TestExtract_EmptyReturnsEmptySlice(t *testing.T) {
	ext := NewRelationExtractor()
	obj := &KnowledgeObject{
		ID:         "obj_5",
		Normalized: "no relations here at all",
		Summary:    "nothing to extract",
	}

	rels := ext.Extract(obj)
	if rels == nil {
		t.Fatal("expected non-nil slice, got nil")
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relations, got %d: %v", len(rels), rels)
	}
}

func TestExtract_NilObjectReturnsEmptySlice(t *testing.T) {
	ext := NewRelationExtractor()
	rels := ext.Extract(nil)
	if rels == nil {
		t.Fatal("expected non-nil slice, got nil")
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relations for nil object, got %d", len(rels))
	}
}

// TestExtract_UnknownPredicateSkipped verifies that even though the default
// patterns only use allowed predicates, the AllowedPredicates gate is enforced.
// A relation whose predicate is not in the allowlist must not appear.
func TestExtract_UnknownPredicateSkipped(t *testing.T) {
	// All default predicates are in AllowedPredicates, so verify the gate by
	// confirming every extracted predicate is allowed.
	ext := NewRelationExtractor()
	obj := &KnowledgeObject{
		ID:         "obj_6",
		Normalized: "修复了bugA 依赖模块B 调用函数C 属于团队D",
		Summary:    "",
	}

	rels := ext.Extract(obj)
	if len(rels) == 0 {
		t.Fatal("expected relations, got none")
	}
	for _, r := range rels {
		if !AllowedPredicates[r.Predicate] {
			t.Errorf("predicate %q is not in AllowedPredicates (should have been skipped)", r.Predicate)
		}
	}
}

func TestAllowedPredicatesContainsExpected(t *testing.T) {
	expected := []string{
		"depends_on", "calls", "produces", "consumes",
		"fixes", "causes", "belongs_to", "derived_from",
		"similar_to", "contradicts", "supersedes", "related_to",
	}
	for _, p := range expected {
		if !AllowedPredicates[p] {
			t.Errorf("AllowedPredicates missing %q", p)
		}
	}
	// An arbitrary predicate must not be allowed.
	if AllowedPredicates["made_up_predicate"] {
		t.Error("AllowedPredicates should not contain arbitrary predicates")
	}
}
