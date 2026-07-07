package builtin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewStringUtils(t *testing.T) {
	tool := NewStringUtils()
	require.NotNil(t, tool)
	assert.Equal(t, "string_utils", tool.Name())
}

// ── Basic operations ─────────────────────────────────

func TestStringUtils_Upper(t *testing.T) {
	tool := NewStringUtils()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "upper", "input": "hello",
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, "HELLO", result.Data.(map[string]interface{})["output"])
}

func TestStringUtils_Lower(t *testing.T) {
	tool := NewStringUtils()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "lower", "input": "HELLO",
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, "hello", result.Data.(map[string]interface{})["output"])
}

func TestStringUtils_Trim(t *testing.T) {
	tool := NewStringUtils()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "trim", "input": "  hello world  ",
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, "hello world", result.Data.(map[string]interface{})["output"])
}

func TestStringUtils_Length(t *testing.T) {
	tool := NewStringUtils()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "length", "input": "hello",
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, 5, result.Data.(map[string]interface{})["output"])
}

func TestStringUtils_Reverse(t *testing.T) {
	tool := NewStringUtils()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "reverse", "input": "hello",
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, "olleh", result.Data.(map[string]interface{})["output"])
}

func TestStringUtils_Split(t *testing.T) {
	tool := NewStringUtils()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "split", "input": "a,b,c", "delimiter": ",",
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	data := result.Data.(map[string]interface{})
	assert.Equal(t, 3, data["count"])
}

func TestStringUtils_Join(t *testing.T) {
	tool := NewStringUtils()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "join", "input": "", "join_items": "a,b,c", "delimiter": ",",
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, "a,b,c", result.Data.(map[string]interface{})["output"])
}

func TestStringUtils_Substring(t *testing.T) {
	tool := NewStringUtils()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "substring", "input": "hello world", "start": float64(0), "end": float64(5),
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, "hello", result.Data.(map[string]interface{})["output"])
}

func TestStringUtils_Replace(t *testing.T) {
	tool := NewStringUtils()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "replace", "input": "hello world", "old": "world", "new": "go",
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, "hello go", result.Data.(map[string]interface{})["output"])
}

// ── Unicode / multi-byte ───────────────────────────

func TestStringUtils_UnicodeLength(t *testing.T) {
	tool := NewStringUtils()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "length", "input": "你好世界",
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	// Length should be 4 (4 Chinese characters), not 12 (UTF-8 bytes)
	assert.Equal(t, 4, result.Data.(map[string]interface{})["output"])
}

func TestStringUtils_UnicodeReverse(t *testing.T) {
	tool := NewStringUtils()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "reverse", "input": "你好",
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, "好你", result.Data.(map[string]interface{})["output"])
}

func TestStringUtils_UnicodeSubstring(t *testing.T) {
	tool := NewStringUtils()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "substring", "input": "你好世界", "start": float64(1), "end": float64(3),
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, "好世", result.Data.(map[string]interface{})["output"])
}

func TestStringUtils_UnicodeUpper(t *testing.T) {
	tool := NewStringUtils()
	ctx := context.Background()

	// Turkish i → İ (special case, but Go's ToUpper handles it)
	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "upper", "input": "istanbul",
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, "ISTANBUL", result.Data.(map[string]interface{})["output"])
}

// ── Edge cases ─────────────────────────────────────

func TestStringUtils_EmptyInput(t *testing.T) {
	tool := NewStringUtils()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "length", "input": "",
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, 0, result.Data.(map[string]interface{})["output"])
}

func TestStringUtils_TrimEmpty(t *testing.T) {
	tool := NewStringUtils()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "trim", "input": "",
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, "", result.Data.(map[string]interface{})["output"])
}

func TestStringUtils_SplitEmptyDelimiter(t *testing.T) {
	tool := NewStringUtils()
	ctx := context.Background()

	// Default delimiter is comma
	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "split", "input": "x,y,z",
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, 3, result.Data.(map[string]interface{})["count"])
}

func TestStringUtils_SplitWhitespace(t *testing.T) {
	tool := NewStringUtils()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "split", "input": "a b c", "delimiter": " ",
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, 3, result.Data.(map[string]interface{})["count"])
}

func TestStringUtils_SubstringOutOfRange(t *testing.T) {
	tool := NewStringUtils()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "substring", "input": "hi", "start": float64(0), "end": float64(10),
	})
	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Contains(t, result.Error, "invalid substring range")
}

func TestStringUtils_SubstringNegativeStart(t *testing.T) {
	tool := NewStringUtils()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "substring", "input": "hello", "start": float64(-1), "end": float64(3),
	})
	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Contains(t, result.Error, "invalid substring range")
}

func TestStringUtils_SubstringStartGtEnd(t *testing.T) {
	tool := NewStringUtils()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "substring", "input": "hello", "start": float64(3), "end": float64(1),
	})
	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Contains(t, result.Error, "invalid substring range")
}

func TestStringUtils_ReplaceNoMatch(t *testing.T) {
	tool := NewStringUtils()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "replace", "input": "hello", "old": "xyz", "new": "abc",
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	assert.Equal(t, "hello", result.Data.(map[string]interface{})["output"])
}

func TestStringUtils_ReplaceMissingOld(t *testing.T) {
	tool := NewStringUtils()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "replace", "input": "hello", "new": "abc",
	})
	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Contains(t, result.Error, "old string is required")
}

func TestStringUtils_JoinMissingItems(t *testing.T) {
	tool := NewStringUtils()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "join", "input": "",
	})
	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Contains(t, result.Error, "join_items is required")
}

func TestStringUtils_MissingOperation(t *testing.T) {
	tool := NewStringUtils()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{"input": "test"})
	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Contains(t, result.Error, "operation is required")
}

func TestStringUtils_UnknownOperation(t *testing.T) {
	tool := NewStringUtils()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "unknown", "input": "test",
	})
	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Contains(t, result.Error, "unsupported operation")
}

func TestStringUtils_IsIdempotent(t *testing.T) {
	tool := NewStringUtils()
	assert.True(t, tool.IsIdempotent())
}
