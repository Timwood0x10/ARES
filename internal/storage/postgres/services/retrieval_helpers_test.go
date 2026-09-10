// Package services — regression tests for the Unicode-safe string helpers.
package services

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestReplaceAllIgnoreCase_UTF8Safe locks REVIEW 2.6#34: the helper operates
// on runes, so multi-byte UTF-8 content (e.g. CJK) that merely CONTAINS an
// ASCII match must never be corrupted. The pre-fix implementation sliced
// byte-by-byte, which produced invalid UTF-8 and mojibake for any query with
// non-ASCII characters.
func TestReplaceAllIgnoreCase_UTF8Safe(t *testing.T) {
	// A CJK query containing a case-mixed contraction: the contraction is
	// replaced and every CJK rune must survive byte-identical.
	in := "数据库 can't 查询"
	out := replaceAllIgnoreCase(in, "CAN'T", "cannot")
	if out != "数据库 cannot 查询" {
		t.Errorf("replacement wrong: %q", out)
	}
	if !utf8.ValidString(out) {
		t.Errorf("output is not valid UTF-8: %q", out)
	}

	// No match: the string must pass through unchanged (the old byte-wise
	// path corrupted CJK even when nothing matched, because it re-encoded
	// continuation bytes individually).
	passthrough := "并行工具调用失败"
	if got := replaceAllIgnoreCase(passthrough, "won't", "will not"); got != passthrough {
		t.Errorf("non-matching input changed: %q", got)
	}

	// Multi-byte match target safety: a rune-sliced window must never match
	// across rune boundaries mid-codepoint.
	if got := replaceAllIgnoreCase("日本語", "本", "x"); got != "日x語" {
		t.Errorf("multi-byte target replacement wrong: %q", got)
	}
}

// TestReplaceAllIgnoreCase_CaseFolding verifies ASCII case-insensitive
// matching still applies with multiple occurrences.
func TestReplaceAllIgnoreCase_CaseFolding(t *testing.T) {
	if got := replaceAllIgnoreCase("It's it's IT'S", "it's", "it is"); got != "it is it is it is" {
		t.Errorf("case-insensitive replacement wrong: %q", got)
	}
	if got := replaceAllIgnoreCase("", "x", "y"); got != "" {
		t.Errorf("empty input changed: %q", got)
	}
	if got := replaceAllIgnoreCase("abc", "", "y"); got != "abc" {
		t.Errorf("empty pattern must be a no-op: %q", got)
	}
}

// TestNormalizeEnglishQuery_CJKContentIsPreserved exercises the production
// caller: normalizing a query that mixes contractions with CJK must only
// touch the contraction.
func TestNormalizeEnglishQuery_CJKContentIsPreserved(t *testing.T) {
	got := normalizeEnglishQuery("用户 don't 需要 API 密钥")
	if !strings.Contains(got, "用户") || !strings.Contains(got, "需要") || !strings.Contains(got, "密钥") {
		t.Errorf("CJK tokens corrupted: %q", got)
	}
	if !strings.Contains(got, "do not") {
		t.Errorf("contraction not expanded: %q", got)
	}
	if !utf8.ValidString(got) {
		t.Errorf("normalized query is not valid UTF-8: %q", got)
	}
}
