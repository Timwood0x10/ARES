package compiler

import (
	"strings"
	"testing"
)

// TestEstimateContentTokens verifies the CJK-aware token heuristic: ASCII
// contributes ~1 token per 4 chars, non-ASCII (CJK) contributes ~1 token each.
// The previous len(Content)/4 formula counted every CJK char (3 UTF-8 bytes)
// as 0 tokens, so Chinese text never reached the compile threshold.
func TestEstimateContentTokens(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"ascii only", "hello world", 11 / 4}, // 11 ascii chars -> 2
		{"cjk only", "你好世界", 4},               // 4 CJK runes -> 4
		{"mixed", "Hi 世界", 2},                 // "Hi "=3 ascii->0 tokens, "世界"=2 cjk->2 tokens => 2
		{"long cjk", strings.Repeat("中", 100), 100},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := estimateContentTokens(c.in); got != c.want {
				t.Errorf("estimateContentTokens(%q) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}

// TestShouldCompileChineseTrigger verifies that a Chinese conversation reaches
// the compile threshold under the CJK-aware estimate, where the legacy
// len/4 byte heuristic would have undercounted it.
func TestShouldCompileChineseTrigger(t *testing.T) {
	// 120 CJK chars => new estimate 120 tokens; legacy len/4 = 360/4 = 90.
	msgs := []SourceMessage{{Role: "user", Content: strings.Repeat("中文", 60)}}

	// Need 100 tokens (window 200 * threshold 0.5). New estimate (120) triggers;
	// legacy estimate (90) would not.
	if !ShouldCompile(msgs, 200, 0.5) {
		t.Error("ShouldCompile should trigger for 120-CJK-char message at threshold 100, but returned false")
	}

	// Far higher requirement (180 tokens) must not trigger.
	if ShouldCompile(msgs, 200, 0.9) {
		t.Error("ShouldCompile should not trigger below 180-token requirement")
	}
}

// TestShouldCompileASCIIUnchanged confirms the ASCII path is numerically
// identical to the legacy len/4 heuristic, so English behavior is preserved.
func TestShouldCompileASCIIUnchanged(t *testing.T) {
	msgs := []SourceMessage{{Role: "user", Content: strings.Repeat("a", 400)}} // 100 tokens
	if !ShouldCompile(msgs, 200, 0.5) {
		t.Error("ShouldCompile should trigger for 400 ascii chars (100 tokens) at threshold 100")
	}
	// Disable path: zero window or threshold never triggers.
	if ShouldCompile(msgs, 0, 0.5) || ShouldCompile(msgs, 200, 0) {
		t.Error("ShouldCompile must not trigger with zero window or threshold")
	}
}
