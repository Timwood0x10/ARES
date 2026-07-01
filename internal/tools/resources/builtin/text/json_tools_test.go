package builtin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestJSONTools_New(t *testing.T) {
	jt := NewJSONTools()
	assert.NotNil(t, jt)
	assert.Equal(t, "json_tools", jt.Name())
}

func TestJSONTools_Execute_MissingOperation(t *testing.T) {
	jt := NewJSONTools()
	result, err := jt.Execute(context.Background(), map[string]interface{}{
		"data": `{"key": "value"}`,
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestJSONTools_Execute_MissingData(t *testing.T) {
	jt := NewJSONTools()
	result, err := jt.Execute(context.Background(), map[string]interface{}{
		"operation": "parse",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestJSONTools_Execute_UnsupportedOperation(t *testing.T) {
	jt := NewJSONTools()
	result, err := jt.Execute(context.Background(), map[string]interface{}{
		"operation": "invalid",
		"data":      "test",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestJSONTools_Parse_Valid(t *testing.T) {
	jt := NewJSONTools()
	result, err := jt.Execute(context.Background(), map[string]interface{}{
		"operation": "parse",
		"data":      `{"key": "value", "num": 42}`,
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
}

func TestJSONTools_Parse_Invalid(t *testing.T) {
	jt := NewJSONTools()
	result, err := jt.Execute(context.Background(), map[string]interface{}{
		"operation": "parse",
		"data":      "not json",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestJSONTools_Extract_SimpleKey(t *testing.T) {
	jt := NewJSONTools()
	result, err := jt.Execute(context.Background(), map[string]interface{}{
		"operation": "extract",
		"data":      `{"user": {"name": "Alice", "age": 30}}`,
		"path":      "user.name",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.Equal(t, "Alice", r["value"])
}

func TestJSONTools_Extract_ArrayIndex(t *testing.T) {
	jt := NewJSONTools()
	result, err := jt.Execute(context.Background(), map[string]interface{}{
		"operation": "extract",
		"data":      `{"items": [{"id": 1}, {"id": 2}]}`,
		"path":      "items[0].id",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.Equal(t, float64(1), r["value"])
}

func TestJSONTools_Extract_MissingPath(t *testing.T) {
	jt := NewJSONTools()
	result, err := jt.Execute(context.Background(), map[string]interface{}{
		"operation": "extract",
		"data":      `{"key": "value"}`,
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestJSONTools_Extract_InvalidJSON(t *testing.T) {
	jt := NewJSONTools()
	result, err := jt.Execute(context.Background(), map[string]interface{}{
		"operation": "extract",
		"data":      "not json",
		"path":      "key",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestJSONTools_Extract_FieldNotFound(t *testing.T) {
	jt := NewJSONTools()
	result, err := jt.Execute(context.Background(), map[string]interface{}{
		"operation": "extract",
		"data":      `{"a": 1}`,
		"path":      "b",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestJSONTools_Extract_NonObjectAccess(t *testing.T) {
	jt := NewJSONTools()
	result, err := jt.Execute(context.Background(), map[string]interface{}{
		"operation": "extract",
		"data":      `["a", "b"]`,
		"path":      "key",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestJSONTools_Extract_OutOfBounds(t *testing.T) {
	jt := NewJSONTools()
	result, err := jt.Execute(context.Background(), map[string]interface{}{
		"operation": "extract",
		"data":      `[1, 2]`,
		"path":      "[5]",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestJSONTools_Extract_InvalidArrayIndex(t *testing.T) {
	jt := NewJSONTools()
	result, err := jt.Execute(context.Background(), map[string]interface{}{
		"operation": "extract",
		"data":      `[1, 2]`,
		"path":      "[abc]",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestJSONTools_Extract_IndexNonArray(t *testing.T) {
	jt := NewJSONTools()
	result, err := jt.Execute(context.Background(), map[string]interface{}{
		"operation": "extract",
		"data":      `{"a": "string"}`,
		"path":      "a[0]",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestJSONTools_Merge_Valid(t *testing.T) {
	jt := NewJSONTools()
	result, err := jt.Execute(context.Background(), map[string]interface{}{
		"operation":  "merge",
		"data":       `{"a": 1, "b": 2}`,
		"merge_data": `{"b": 3, "c": 4}`,
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
}

func TestJSONTools_Merge_MissingMergeData(t *testing.T) {
	jt := NewJSONTools()
	result, err := jt.Execute(context.Background(), map[string]interface{}{
		"operation": "merge",
		"data":      `{}`,
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestJSONTools_Merge_InvalidFirstJSON(t *testing.T) {
	jt := NewJSONTools()
	result, err := jt.Execute(context.Background(), map[string]interface{}{
		"operation":  "merge",
		"data":       "not json",
		"merge_data": `{}`,
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestJSONTools_Merge_InvalidSecondJSON(t *testing.T) {
	jt := NewJSONTools()
	result, err := jt.Execute(context.Background(), map[string]interface{}{
		"operation":  "merge",
		"data":       `{}`,
		"merge_data": "not json",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestJSONTools_Merge_NotObject(t *testing.T) {
	jt := NewJSONTools()
	result, err := jt.Execute(context.Background(), map[string]interface{}{
		"operation":  "merge",
		"data":       `[]`,
		"merge_data": `{}`,
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestJSONTools_Merge_MergeDataNotObject(t *testing.T) {
	jt := NewJSONTools()
	result, err := jt.Execute(context.Background(), map[string]interface{}{
		"operation":  "merge",
		"data":       `{}`,
		"merge_data": `[]`,
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestJSONTools_Merge_DeepMerge(t *testing.T) {
	jt := NewJSONTools()
	result, err := jt.Execute(context.Background(), map[string]interface{}{
		"operation":  "merge",
		"data":       `{"a": {"x": 1}}`,
		"merge_data": `{"a": {"y": 2}}`,
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
}

func TestJSONTools_Pretty_Valid(t *testing.T) {
	jt := NewJSONTools()
	result, err := jt.Execute(context.Background(), map[string]interface{}{
		"operation": "pretty",
		"data":      `{"a": 1, "b": 2}`,
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.Contains(t, r["pretty"], "\"a\": 1")
}

func TestJSONTools_Pretty_InvalidJSON(t *testing.T) {
	jt := NewJSONTools()
	result, err := jt.Execute(context.Background(), map[string]interface{}{
		"operation": "pretty",
		"data":      "not json",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestJSONTools_Pretty_CustomIndent(t *testing.T) {
	jt := NewJSONTools()
	result, err := jt.Execute(context.Background(), map[string]interface{}{
		"operation": "pretty",
		"data":      `{"a": 1}`,
		"indent":    "\t",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
}

func TestJSONTools_IsIdempotent(t *testing.T) {
	jt := NewJSONTools()
	assert.True(t, jt.IsIdempotent())
}

func TestGetInt(t *testing.T) {
	assert.Equal(t, 42, getInt(map[string]interface{}{"k": 42}, "k", 0))
	assert.Equal(t, 42, getInt(map[string]interface{}{"k": float64(42)}, "k", 0))
	assert.Equal(t, 42, getInt(map[string]interface{}{"k": "42"}, "k", 0))
	assert.Equal(t, 10, getInt(map[string]interface{}{}, "k", 10))
	assert.Equal(t, 10, getInt(map[string]interface{}{"k": "abc"}, "k", 10))
}
