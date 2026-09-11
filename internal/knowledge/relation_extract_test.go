package knowledge

import (
	"regexp"
	"strings"
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

// TestExtract_EntityBoundTerminators is the regression for the terminator
// matrix: every character the capture class excludes must also be able to
// terminate the capture, otherwise the lazy +? has no valid stop position and
// the whole pattern fails to match. '!' and '?' sat in the exclusion class but
// were dropped from the terminator alternation, so "fixes the auth bug!"
// extracted NOTHING. Cases are pinned through the public Extract contract, not
// the regex internals.
func TestExtract_EntityBoundTerminators(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		// ASCII sentence punctuation must terminate.
		{"ascii_period", "this commit fixes the auth bug.", "the auth bug"},
		{"ascii_period_then_more", "fixes the auth bug. See also foo", "the auth bug"},
		{"ascii_exclamation", "fixes the auth bug!", "the auth bug"},
		{"ascii_question", "fixes the auth bug?", "the auth bug"},
		{"ascii_comma", "fixes the auth bug, then refactors", "the auth bug"},
		{"ascii_semicolon", "fixes the auth bug; also docs", "the auth bug"},
		{"ascii_colon", "fixes the auth bug: a postmortem", "the auth bug"},
		{"ascii_conjunction_and", "fixes A and B", "A"},

		// Full-width CJK punctuation must terminate (the capture class
		// excludes it, so the terminator must accept it).
		{"fullwidth_period", "修复了鉴权bug。", "鉴权bug"},
		{"fullwidth_comma", "修复了 A，B", "A"},
		{"fullwidth_semicolon", "修复了 A；B", "A"},
		{"fullwidth_colon", "修复了 A：B", "A"},
		{"fullwidth_exclamation", "修复了 A！", "A"},
		{"fullwidth_question", "修复了 A？", "A"},
		{"fullwidth_conjunction_he", "修复了 A 和 B", "A"},

		// Dotted identifiers/versions must SURVIVE a '.' that is not a
		// sentence boundary — the whole point of keeping '.' out of the
		// capture class.
		{"dotted_identifier", "this depends on auth.service", "auth.service"},
		{"dotted_version", "depends on v1.2.3. Next", "v1.2.3"},

		// Bare entity with no trailing punctuation still matches via the
		// end-of-text terminator.
		{"no_terminator", "this commit fixes the login page", "the login page"},
	}

	ext := NewRelationExtractor()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			for _, r := range ext.Extract(&KnowledgeObject{ID: "obj", Normalized: tc.text}) {
				if r.Predicate == RelFixes || r.Predicate == RelDependsOn {
					got = r.ObjectText
					break
				}
			}
			if got != tc.want {
				t.Errorf("Extract(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

// TestExtract_CaptureClassNeverExcludesUnterminatableChar guards the invariant
// behind TestExtract_EntityBoundTerminators: no character may appear in the
// capture exclusion class without a matching terminator branch. Implemented
// against entityBound itself so a future edit that reintroduces the asymmetry
// fails here even if the table above is not extended.
func TestExtract_CaptureClassNeverExcludesUnterminatableChar(t *testing.T) {
	// Characters the capture class excludes.
	excluded := []string{"，", "。", "；", "！", "？", ",", ";", "!", "?", "：", ":"}
	// Each must be reachable as a terminator: either directly or as the '.'
	// half of the '\.\s' sentence-boundary branch.
	for _, ch := range excluded {
		pat := `x*` + entityBound
		re, err := regexp.Compile(pat)
		if err != nil {
			t.Fatalf("entityBound does not compile: %v", err)
		}
		m := re.FindStringSubmatch("ab" + ch + "cd")
		if m == nil {
			t.Errorf("entity %q followed by excluded char %q does not match; "+
				"every excluded char must also terminate the capture", "ab", ch)
			continue
		}
		if got := strings.TrimSpace(m[1]); got != "ab" {
			t.Errorf("entity before %q captured as %q, want %q", ch, got, "ab")
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
