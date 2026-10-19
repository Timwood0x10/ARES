// Package tokencount estimates token cost without a tokenizer.
//
// It lives in a leaf package because two layers need the same numbers (the prompt budget in
// `memory` and the per-session storage cap in `memory/context`), and the second cannot import
// the first without a cycle. Duplicating the rates would let the two drift apart silently —
// the storage cap and the prompt budget would then disagree about how big a message is.
package tokencount

import (
	"math"
	"unicode"
)

// Per-character rates, from DeepSeek's published estimate: a CJK character costs ~0.6 token
// and a non-CJK character ~0.3. Per-character rather than runes/3 because the two classes
// differ by 2x and mixed prompts are common — one rate would under-count Chinese or
// over-count code.
const (
	TokensPerCJKChar   = 0.6
	TokensPerOtherChar = 0.3
)

// Text returns the estimated token cost of s.
//
// It is an ESTIMATE, not a tokenizer: no BPE merges, no model-specific vocabulary. That is a
// deliberate trade — the number decides how much history to drop, and a cheap estimate that is
// right within a few percent beats an exact count that is not available here.
func Text(s string) float64 {
	var cjk, other int
	for _, r := range s {
		if IsCJK(r) {
			cjk++
			continue
		}
		other++
	}
	return float64(cjk)*TokensPerCJKChar + float64(other)*TokensPerOtherChar
}

// Rounded returns Text(s) rounded up to whole tokens.
func Rounded(s string) int {
	return int(math.Ceil(Text(s)))
}

// IsCJK reports whether a rune belongs to a script billed at the CJK rate.
func IsCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hangul, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r)
}
