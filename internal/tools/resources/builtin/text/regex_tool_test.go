package builtin

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegexTool_New(t *testing.T) {
	rt := NewRegexTool()
	assert.NotNil(t, rt)
	assert.Equal(t, "regex_tool", rt.Name())
}

func TestRegexTool_Execute_MissingOperation(t *testing.T) {
	rt := NewRegexTool()
	result, err := rt.Execute(context.Background(), map[string]interface{}{
		"text":    "hello world",
		"pattern": "hello",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestRegexTool_Execute_MissingText(t *testing.T) {
	rt := NewRegexTool()
	result, err := rt.Execute(context.Background(), map[string]interface{}{
		"operation": "match",
		"pattern":   "test",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestRegexTool_Execute_MissingPattern(t *testing.T) {
	rt := NewRegexTool()
	result, err := rt.Execute(context.Background(), map[string]interface{}{
		"operation": "match",
		"text":      "hello",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestRegexTool_Execute_InvalidPattern(t *testing.T) {
	rt := NewRegexTool()
	result, err := rt.Execute(context.Background(), map[string]interface{}{
		"operation": "match",
		"text":      "test",
		"pattern":   "[invalid",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestRegexTool_Execute_UnsupportedOperation(t *testing.T) {
	rt := NewRegexTool()
	result, err := rt.Execute(context.Background(), map[string]interface{}{
		"operation": "invalid",
		"text":      "test",
		"pattern":   "test",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestRegexTool_Match_Found(t *testing.T) {
	rt := NewRegexTool()
	result, err := rt.Execute(context.Background(), map[string]interface{}{
		"operation": "match",
		"text":      "hello world hello",
		"pattern":   "hello",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.True(t, r["matched"].(bool))
	assert.Equal(t, 2, r["match_count"])
}

func TestRegexTool_Match_NotFound(t *testing.T) {
	rt := NewRegexTool()
	result, err := rt.Execute(context.Background(), map[string]interface{}{
		"operation": "match",
		"text":      "hello world",
		"pattern":   "zzz",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.False(t, r["matched"].(bool))
}

func TestRegexTool_Match_WithMaxResults(t *testing.T) {
	rt := NewRegexTool()
	result, err := rt.Execute(context.Background(), map[string]interface{}{
		"operation":   "match",
		"text":        "a a a a a",
		"pattern":     "a",
		"max_results": 3,
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.Equal(t, 3, r["match_count"])
}

func TestRegexTool_Match_WithFlags(t *testing.T) {
	rt := NewRegexTool()
	result, err := rt.Execute(context.Background(), map[string]interface{}{
		"operation": "match",
		"text":      "HELLO world",
		"pattern":   "hello",
		"flags":     []interface{}{"i"},
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.True(t, r["matched"].(bool))
}

func TestRegexTool_Extract_Found(t *testing.T) {
	rt := NewRegexTool()
	result, err := rt.Execute(context.Background(), map[string]interface{}{
		"operation": "extract",
		"text":      "name: Alice, age: 30",
		"pattern":   `name: (\w+), age: (\d+)`,
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.True(t, r["matched"].(bool))
	assert.Equal(t, 1, r["match_count"])
}

func TestRegexTool_Extract_NotFound(t *testing.T) {
	rt := NewRegexTool()
	result, err := rt.Execute(context.Background(), map[string]interface{}{
		"operation": "extract",
		"text":      "no match here",
		"pattern":   `(\d+)`,
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.False(t, r["matched"].(bool))
}

func TestRegexTool_Extract_WithMaxResults(t *testing.T) {
	rt := NewRegexTool()
	result, err := rt.Execute(context.Background(), map[string]interface{}{
		"operation":   "extract",
		"text":        "a:1 b:2 c:3",
		"pattern":     `(\w):(\d)`,
		"max_results": 2,
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.Equal(t, 2, r["match_count"])
}

func TestRegexTool_Replace_Found(t *testing.T) {
	rt := NewRegexTool()
	result, err := rt.Execute(context.Background(), map[string]interface{}{
		"operation":   "replace",
		"text":        "hello world",
		"pattern":     "world",
		"replacement": "there",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.Equal(t, "hello there", r["result"])
	assert.Equal(t, 1, r["match_count"])
}

func TestRegexTool_Replace_NotFound(t *testing.T) {
	rt := NewRegexTool()
	result, err := rt.Execute(context.Background(), map[string]interface{}{
		"operation":   "replace",
		"text":        "hello world",
		"pattern":     "zzz",
		"replacement": "x",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.Equal(t, "hello world", r["result"])
	assert.Equal(t, 0, r["match_count"])
}

func TestRegexTool_Replace_MissingReplacement(t *testing.T) {
	rt := NewRegexTool()
	result, err := rt.Execute(context.Background(), map[string]interface{}{
		"operation": "replace",
		"text":      "hello world",
		"pattern":   "world",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestRegexTool_CompileRegex_WithFlags(t *testing.T) {
	rt := NewRegexTool()
	re, err := rt.compileRegex("hello", []string{"i"})
	assert.NoError(t, err)
	assert.True(t, re.MatchString("HELLO"))
}

func TestRegexTool_CompileRegex_NoFlags(t *testing.T) {
	rt := NewRegexTool()
	re, err := rt.compileRegex("hello", nil)
	assert.NoError(t, err)
	assert.False(t, re.MatchString("HELLO"))
}

func TestRegexTool_IsIdempotent(t *testing.T) {
	rt := NewRegexTool()
	assert.True(t, rt.IsIdempotent())
}

// TestRegexTool_EmptyMatchesSkipped is the #62 regression: a pattern that
// matches the empty string (e.g. "a*") yields one empty match per position —
// millions of meaningless entries on large input. Empty matches must be
// skipped in match and extract.
func TestRegexTool_EmptyMatchesSkipped(t *testing.T) {
	rt := NewRegexTool()

	// "a*" over "bbb" matches empty at every position.
	result, err := rt.Execute(context.Background(), map[string]interface{}{
		"operation": "match",
		"text":      "bbb",
		"pattern":   "a*",
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.Equal(t, 0, r["match_count"], "empty matches must be skipped, got %v", r["matches"])
	assert.False(t, r["matched"].(bool), "no non-empty match means matched=false")

	// Mixed: "b*" over "aba" has one non-empty match ("b") plus empties.
	result, err = rt.Execute(context.Background(), map[string]interface{}{
		"operation": "match",
		"text":      "aba",
		"pattern":   "b*",
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	r = result.Data.(map[string]interface{})
	assert.Equal(t, 1, r["match_count"])
	matches := r["matches"].([]map[string]interface{})
	assert.Equal(t, "b", matches[0]["match"])

	// Extract skips empties too.
	result, err = rt.Execute(context.Background(), map[string]interface{}{
		"operation": "extract",
		"text":      "bbb",
		"pattern":   "(a*)",
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	r = result.Data.(map[string]interface{})
	assert.Equal(t, 0, r["match_count"], "extract must skip empty full-matches")
}

// TestRegexTool_MaxResultsCeiling is the #62 regression: max_results=-1
// (unlimited) on a broad pattern over large input produced millions of
// matches and exhausted memory. Regardless of the caller's value, the result
// count is clamped to a hard ceiling.
func TestRegexTool_MaxResultsCeiling(t *testing.T) {
	rt := NewRegexTool()

	// 30k one-char matches; ask for unlimited.
	text := strings.Repeat("ab", 15000)
	result, err := rt.Execute(context.Background(), map[string]interface{}{
		"operation":   "match",
		"text":        text,
		"pattern":     "a",
		"max_results": -1,
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.LessOrEqual(t, r["match_count"].(int), maxRegexResultsCeiling,
		"unlimited max_results must clamp to the ceiling")

	// Absurdly large caps clamp too.
	result, err = rt.Execute(context.Background(), map[string]interface{}{
		"operation":   "match",
		"text":        text,
		"pattern":     "a",
		"max_results": 1 << 30,
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	r = result.Data.(map[string]interface{})
	assert.LessOrEqual(t, r["match_count"].(int), maxRegexResultsCeiling)
}

// TestRegexTool_DefaultMaxResultsApplied: an omitted max_results uses the
// default cap (not unlimited).
func TestRegexTool_DefaultMaxResultsApplied(t *testing.T) {
	rt := NewRegexTool()
	text := strings.Repeat("ab", 3000) // 3000 "a" matches > default cap
	result, err := rt.Execute(context.Background(), map[string]interface{}{
		"operation": "match",
		"text":      text,
		"pattern":   "a",
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.Equal(t, defaultMaxRegexResults, r["match_count"])
}

// TestRegexTool_ReplaceEmptyPatternCountBounded is the #62 companion: the
// pre-replacement match count used FindAllString(text, -1), which
// materializes one entry per position for empty-matching patterns. It must
// count non-empty matches without the unbounded slice.
func TestRegexTool_ReplaceEmptyPatternCountBounded(t *testing.T) {
	rt := NewRegexTool()
	result, err := rt.Execute(context.Background(), map[string]interface{}{
		"operation":   "replace",
		"text":        "hello world",
		"pattern":     "l+",
		"replacement": "-",
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.Equal(t, 2, r["match_count"], "non-empty matches: 'll' and 'l'")
	assert.Equal(t, "he-o wor-d", r["result"])
}
