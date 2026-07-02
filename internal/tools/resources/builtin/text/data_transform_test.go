package builtin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDataTransform_New(t *testing.T) {
	dt := NewDataTransform()
	assert.NotNil(t, dt)
	assert.Equal(t, "data_transform", dt.Name())
}

func TestDataTransform_Execute_MissingOperation(t *testing.T) {
	dt := NewDataTransform()
	result, err := dt.Execute(context.Background(), map[string]interface{}{
		"data": "a,b\n1,2",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestDataTransform_Execute_MissingData(t *testing.T) {
	dt := NewDataTransform()
	result, err := dt.Execute(context.Background(), map[string]interface{}{
		"operation": "csv_to_json",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestDataTransform_Execute_UnsupportedOperation(t *testing.T) {
	dt := NewDataTransform()
	result, err := dt.Execute(context.Background(), map[string]interface{}{
		"operation": "invalid",
		"data":      "test",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestDataTransform_CSVToJSON_WithHeader(t *testing.T) {
	dt := NewDataTransform()
	result, err := dt.Execute(context.Background(), map[string]interface{}{
		"operation": "csv_to_json",
		"data":      "name,age\nAlice,30\nBob,25",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	data := result.Data.(map[string]interface{})
	assert.Equal(t, 2, data["row_count"])
}

func TestDataTransform_CSVToJSON_WithoutHeader(t *testing.T) {
	dt := NewDataTransform()
	result, err := dt.Execute(context.Background(), map[string]interface{}{
		"operation":  "csv_to_json",
		"data":       "Alice,30\nBob,25",
		"has_header": false,
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	data := result.Data.(map[string]interface{})
	assert.Equal(t, 2, data["row_count"])
}

func TestDataTransform_CSVToJSON_Empty(t *testing.T) {
	dt := NewDataTransform()
	result, err := dt.Execute(context.Background(), map[string]interface{}{
		"operation": "csv_to_json",
		"data":      "",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestDataTransform_CSVToJSON_EmptyCSVData(t *testing.T) {
	dt := NewDataTransform()
	result, err := dt.Execute(context.Background(), map[string]interface{}{
		"operation": "csv_to_json",
		"data":      "header\n",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
}

func TestDataTransform_CSVToJSON_CustomDelimiter(t *testing.T) {
	dt := NewDataTransform()
	result, err := dt.Execute(context.Background(), map[string]interface{}{
		"operation": "csv_to_json",
		"data":      "name;age\nAlice;30",
		"delimiter": ";",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	data := result.Data.(map[string]interface{})
	assert.Equal(t, 1, data["row_count"])
}

func TestDataTransform_JSONToCSV(t *testing.T) {
	dt := NewDataTransform()
	result, err := dt.Execute(context.Background(), map[string]interface{}{
		"operation": "json_to_csv",
		"data":      `[{"name": "Alice", "age": 30}, {"name": "Bob", "age": 25}]`,
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	data := result.Data.(map[string]interface{})
	assert.Equal(t, 2, data["row_count"])
	assert.Contains(t, data["data"], "Alice")
}

func TestDataTransform_JSONToCSV_InvalidJSON(t *testing.T) {
	dt := NewDataTransform()
	result, err := dt.Execute(context.Background(), map[string]interface{}{
		"operation": "json_to_csv",
		"data":      "not json",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestDataTransform_JSONToCSV_NotArray(t *testing.T) {
	dt := NewDataTransform()
	result, err := dt.Execute(context.Background(), map[string]interface{}{
		"operation": "json_to_csv",
		"data":      `{"key": "value"}`,
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestDataTransform_JSONToCSV_EmptyArray(t *testing.T) {
	dt := NewDataTransform()
	result, err := dt.Execute(context.Background(), map[string]interface{}{
		"operation": "json_to_csv",
		"data":      `[]`,
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
}

func TestDataTransform_JSONToCSV_CustomDelimiter(t *testing.T) {
	dt := NewDataTransform()
	result, err := dt.Execute(context.Background(), map[string]interface{}{
		"operation": "json_to_csv",
		"data":      `[{"name": "Alice"}]`,
		"delimiter": ";",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	data := result.Data.(map[string]interface{})
	assert.Contains(t, data["data"], "Alice")
}

func TestDataTransform_JSONToCSV_EscapeDelimiter(t *testing.T) {
	dt := NewDataTransform()
	result, err := dt.Execute(context.Background(), map[string]interface{}{
		"operation": "json_to_csv",
		"data":      `[{"val": "a,b"}]`,
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
}

func TestDataTransform_FlattenJSON_Simple(t *testing.T) {
	dt := NewDataTransform()
	result, err := dt.Execute(context.Background(), map[string]interface{}{
		"operation": "flatten_json",
		"data":      `{"a": {"b": 1, "c": 2}, "d": 3}`,
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	flattened := result.Data.(map[string]interface{})["data"].(map[string]interface{})
	assert.Equal(t, float64(1), flattened["a.b"])
	assert.Equal(t, float64(2), flattened["a.c"])
	assert.Equal(t, float64(3), flattened["d"])
}

func TestDataTransform_FlattenJSON_InvalidJSON(t *testing.T) {
	dt := NewDataTransform()
	result, err := dt.Execute(context.Background(), map[string]interface{}{
		"operation": "flatten_json",
		"data":      "not json",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestDataTransform_FlattenJSON_CustomSeparator(t *testing.T) {
	dt := NewDataTransform()
	result, err := dt.Execute(context.Background(), map[string]interface{}{
		"operation": "flatten_json",
		"data":      `{"a": {"b": 1}}`,
		"separator": "_",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	flattened := result.Data.(map[string]interface{})["data"].(map[string]interface{})
	assert.Equal(t, float64(1), flattened["a_b"])
}

func TestDataTransform_FlattenJSON_Array(t *testing.T) {
	dt := NewDataTransform()
	result, err := dt.Execute(context.Background(), map[string]interface{}{
		"operation": "flatten_json",
		"data":      `[{"a": 1}, {"a": 2}]`,
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
}

func TestDataTransform_IsIdempotent(t *testing.T) {
	dt := NewDataTransform()
	assert.True(t, dt.IsIdempotent())
}

func TestGetStringWithDefault(t *testing.T) {
	assert.Equal(t, "val", getStringWithDefault(map[string]interface{}{"k": "val"}, "k", "def"))
	assert.Equal(t, "def", getStringWithDefault(map[string]interface{}{}, "k", "def"))
	assert.Equal(t, "def", getStringWithDefault(map[string]interface{}{"k": ""}, "k", "def"))
}

func TestGetBoolWithDefault(t *testing.T) {
	assert.True(t, getBoolWithDefault(map[string]interface{}{"k": true}, "k", false))
	assert.False(t, getBoolWithDefault(map[string]interface{}{}, "k", false))
	assert.True(t, getBoolWithDefault(map[string]interface{}{}, "k", true))
}
