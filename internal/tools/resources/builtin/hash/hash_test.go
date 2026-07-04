package builtin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewHashTool(t *testing.T) {
	tool := NewHashTool()
	require.NotNil(t, tool)
	assert.Equal(t, "hash_tool", tool.Name())
}

func TestHashTool_MD5(t *testing.T) {
	tool := NewHashTool()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "md5",
		"input":     "hello",
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	data := result.Data.(map[string]interface{})
	assert.Equal(t, "5d41402abc4b2a76b9719d911017c592", data["output"])
}

func TestHashTool_SHA1(t *testing.T) {
	tool := NewHashTool()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "sha1",
		"input":     "hello",
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	data := result.Data.(map[string]interface{})
	assert.Equal(t, "aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d", data["output"])
}

func TestHashTool_SHA256(t *testing.T) {
	tool := NewHashTool()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "sha256",
		"input":     "hello",
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	data := result.Data.(map[string]interface{})
	assert.Equal(t, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824", data["output"])
}

func TestHashTool_SHA512(t *testing.T) {
	tool := NewHashTool()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "sha512",
		"input":     "hello",
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	data := result.Data.(map[string]interface{})
	expected := "9b71d224bd62f3785d96d46ad3ea3d73319bfbc2890caadae2dff72519673ca72323c3d99ba5c11d7c7acc6e14b8c5da0c4663475c2e5c3adef46f73bcdec043"
	assert.Equal(t, expected, data["output"])
}

func TestHashTool_Base64Encode(t *testing.T) {
	tool := NewHashTool()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "base64_encode",
		"input":     "hello world",
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	data := result.Data.(map[string]interface{})
	assert.Equal(t, "aGVsbG8gd29ybGQ=", data["output"])
}

func TestHashTool_Base64Decode(t *testing.T) {
	tool := NewHashTool()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "base64_decode",
		"input":     "aGVsbG8gd29ybGQ=",
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	data := result.Data.(map[string]interface{})
	assert.Equal(t, "hello world", data["output"])
}

func TestHashTool_Base64Decode_InvalidInput(t *testing.T) {
	tool := NewHashTool()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "base64_decode",
		"input":     "not-valid-base64!!!",
	})
	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Contains(t, result.Error, "base64 decode failed")
}

func TestHashTool_EmptyInput(t *testing.T) {
	tool := NewHashTool()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "md5",
		"input":     "",
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	data := result.Data.(map[string]interface{})
	assert.Equal(t, "d41d8cd98f00b204e9800998ecf8427e", data["output"]) // MD5 of empty string
}

func TestHashTool_MissingOperation(t *testing.T) {
	tool := NewHashTool()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"input": "test",
	})
	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Contains(t, result.Error, "operation is required")
}

func TestHashTool_MissingInput(t *testing.T) {
	tool := NewHashTool()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "md5",
	})
	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Contains(t, result.Error, "input is required")
}

func TestHashTool_UnknownOperation(t *testing.T) {
	tool := NewHashTool()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "unknown",
		"input":     "test",
	})
	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Contains(t, result.Error, "unsupported operation")
}

func TestHashTool_UnicodeInput(t *testing.T) {
	tool := NewHashTool()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "sha256",
		"input":     "你好世界",
	})
	require.NoError(t, err)
	require.True(t, result.Success)
	data := result.Data.(map[string]interface{})
	output, ok := data["output"].(string)
	require.True(t, ok)
	assert.Len(t, output, 64) // SHA256 hex is always 64 chars
}

func TestHashTool_IsIdempotent(t *testing.T) {
	tool := NewHashTool()
	assert.True(t, tool.IsIdempotent())
}
