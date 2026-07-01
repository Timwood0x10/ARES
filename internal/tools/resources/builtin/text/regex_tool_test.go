package builtin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
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
