package eval

import (
	"sort"
	"strings"
)

// evalStopwords are filler tokens that carry no extractable meaning on their
// own. They are removed before key comparison so phrasing/casing differences
// do not break precision matching (e.g. "Rust compiles to native code" matches
// "rust compiles native code").
var evalStopwords = map[string]struct{}{
	"a": {}, "an": {}, "of": {}, "to": {}, "in": {}, "on": {}, "for": {},
	"and": {}, "or": {}, "is": {}, "are": {}, "be": {}, "by": {}, "with": {},
	"at": {}, "as": {}, "it": {}, "this": {}, "that": {}, "the": {}, "we": {},
	"you": {}, "i": {}, "he": {}, "she": {}, "they": {}, "was": {}, "were": {},
	// Chinese fillers.
	"的": {}, "了": {}, "是": {}, "在": {}, "和": {}, "与": {}, "也": {}, "就": {},
	"都": {}, "而": {}, "一个": {}, "一种": {}, "这个": {}, "那个": {},
}

// NormalizeKey canonicalizes a knowledge string into a sorted, deduplicated
// token-set key. Two facts expressed differently but meaning the same end up
// with the same key, so precision can be computed by set membership. Empty
// input (or input with no meaningful tokens) yields the empty string, which is
// treated as "unmatchable" by the precision counter.
func NormalizeKey(s string) string {
	if s == "" {
		return ""
	}
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 0x4E00 && r <= 0x9FFF: // CJK Unified Ideographs
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	seen := make(map[string]struct{})
	var out []string
	for _, t := range strings.Fields(b.String()) {
		if _, skip := evalStopwords[t]; skip {
			continue
		}
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	if len(out) == 0 {
		return ""
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}
