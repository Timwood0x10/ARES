package builtin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDataValidation_New(t *testing.T) {
	dv := NewDataValidation()
	assert.NotNil(t, dv)
	assert.Equal(t, "data_validation", dv.Name())
}

func TestDataValidation_Execute_MissingOperation(t *testing.T) {
	dv := NewDataValidation()
	result, err := dv.Execute(context.Background(), map[string]interface{}{
		"data": "test",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestDataValidation_Execute_MissingData(t *testing.T) {
	dv := NewDataValidation()
	result, err := dv.Execute(context.Background(), map[string]interface{}{
		"operation": "validate_json",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestDataValidation_Execute_UnsupportedOperation(t *testing.T) {
	dv := NewDataValidation()
	result, err := dv.Execute(context.Background(), map[string]interface{}{
		"operation": "invalid",
		"data":      "test",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestDataValidation_ValidateJSON_Valid(t *testing.T) {
	dv := NewDataValidation()
	result, err := dv.Execute(context.Background(), map[string]interface{}{
		"operation": "validate_json",
		"data":      `{"key": "value"}`,
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.True(t, r["valid"].(bool))
	assert.Equal(t, "object", r["type"])
}

func TestDataValidation_ValidateJSON_ValidArray(t *testing.T) {
	dv := NewDataValidation()
	result, err := dv.Execute(context.Background(), map[string]interface{}{
		"operation": "validate_json",
		"data":      `[1, 2, 3]`,
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.True(t, r["valid"].(bool))
	assert.Equal(t, "array", r["type"])
}

func TestDataValidation_ValidateJSON_ValidPrimitive(t *testing.T) {
	dv := NewDataValidation()
	result, err := dv.Execute(context.Background(), map[string]interface{}{
		"operation": "validate_json",
		"data":      `"hello"`,
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.True(t, r["valid"].(bool))
	assert.Equal(t, "primitive", r["type"])
}

func TestDataValidation_ValidateJSON_Invalid(t *testing.T) {
	dv := NewDataValidation()
	result, err := dv.Execute(context.Background(), map[string]interface{}{
		"operation": "validate_json",
		"data":      "not json",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.False(t, r["valid"].(bool))
}

func TestDataValidation_ValidateEmail_Valid(t *testing.T) {
	dv := NewDataValidation()
	result, err := dv.Execute(context.Background(), map[string]interface{}{
		"operation": "validate_email",
		"data":      "user@example.com",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.True(t, r["valid"].(bool))
}

func TestDataValidation_ValidateEmail_Invalid(t *testing.T) {
	dv := NewDataValidation()
	result, err := dv.Execute(context.Background(), map[string]interface{}{
		"operation": "validate_email",
		"data":      "not-an-email",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.False(t, r["valid"].(bool))
}

func TestDataValidation_ValidateEmail_LocalPartTooLong(t *testing.T) {
	dv := NewDataValidation()
	local := ""
	for i := 0; i < 70; i++ {
		local += "a"
	}
	result, err := dv.Execute(context.Background(), map[string]interface{}{
		"operation": "validate_email",
		"data":      local + "@example.com",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestDataValidation_ValidateEmail_DomainTooLong(t *testing.T) {
	dv := NewDataValidation()
	domain := ""
	for i := 0; i < 260; i++ {
		domain += "a"
	}
	result, err := dv.Execute(context.Background(), map[string]interface{}{
		"operation": "validate_email",
		"data":      "user@" + domain + ".com",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestDataValidation_ValidateEmail_TotalTooLong(t *testing.T) {
	dv := NewDataValidation()
	local := ""
	for i := 0; i < 250; i++ {
		local += "a"
	}
	result, err := dv.Execute(context.Background(), map[string]interface{}{
		"operation": "validate_email",
		"data":      local + "@b.co",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestDataValidation_ValidateURL_Valid(t *testing.T) {
	dv := NewDataValidation()
	result, err := dv.Execute(context.Background(), map[string]interface{}{
		"operation": "validate_url",
		"data":      "https://example.com/path",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.True(t, r["valid"].(bool))
	assert.Equal(t, "https", r["scheme"])
}

func TestDataValidation_ValidateURL_Invalid(t *testing.T) {
	dv := NewDataValidation()
	result, err := dv.Execute(context.Background(), map[string]interface{}{
		"operation": "validate_url",
		"data":      "not a url",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestDataValidation_ValidateURL_NoScheme(t *testing.T) {
	dv := NewDataValidation()
	result, err := dv.Execute(context.Background(), map[string]interface{}{
		"operation": "validate_url",
		"data":      "ftp://example.com",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestDataValidation_ValidateURL_WithPort(t *testing.T) {
	dv := NewDataValidation()
	result, err := dv.Execute(context.Background(), map[string]interface{}{
		"operation": "validate_url",
		"data":      "http://example.com:8080/path",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.Equal(t, "8080", r["port"])
}

func TestDataValidation_ValidateSchema_Valid(t *testing.T) {
	dv := NewDataValidation()
	result, err := dv.Execute(context.Background(), map[string]interface{}{
		"operation": "validate_schema",
		"data":      `{"name": "Alice", "age": 30}`,
		"schema":    `{"required": ["name"], "properties": {"name": {"type": "string"}, "age": {"type": "number"}}}`,
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.True(t, r["valid"].(bool))
}

func TestDataValidation_ValidateSchema_MissingSchema(t *testing.T) {
	dv := NewDataValidation()
	result, err := dv.Execute(context.Background(), map[string]interface{}{
		"operation": "validate_schema",
		"data":      `{"name": "Alice"}`,
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestDataValidation_ValidateSchema_MissingRequired(t *testing.T) {
	dv := NewDataValidation()
	result, err := dv.Execute(context.Background(), map[string]interface{}{
		"operation": "validate_schema",
		"data":      `{"age": 30}`,
		"schema":    `{"required": ["name"], "properties": {"name": {"type": "string"}}}`,
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.False(t, r["valid"].(bool))
}

func TestDataValidation_ValidateSchema_TypeMismatch(t *testing.T) {
	dv := NewDataValidation()
	result, err := dv.Execute(context.Background(), map[string]interface{}{
		"operation": "validate_schema",
		"data":      `{"name": 123}`,
		"schema":    `{"properties": {"name": {"type": "string"}}}`,
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestDataValidation_ValidateSchema_InvalidDataJSON(t *testing.T) {
	dv := NewDataValidation()
	result, err := dv.Execute(context.Background(), map[string]interface{}{
		"operation": "validate_schema",
		"data":      "not json",
		"schema":    `{}`,
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestDataValidation_ValidateSchema_InvalidSchemaJSON(t *testing.T) {
	dv := NewDataValidation()
	result, err := dv.Execute(context.Background(), map[string]interface{}{
		"operation": "validate_schema",
		"data":      `{}`,
		"schema":    "not json",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestDataValidation_ValidateSchema_SchemaNotObject(t *testing.T) {
	dv := NewDataValidation()
	result, err := dv.Execute(context.Background(), map[string]interface{}{
		"operation": "validate_schema",
		"data":      `{}`,
		"schema":    `[]`,
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestDataValidation_ValidateSchema_DataNotObject(t *testing.T) {
	dv := NewDataValidation()
	result, err := dv.Execute(context.Background(), map[string]interface{}{
		"operation": "validate_schema",
		"data":      `[]`,
		"schema":    `{}`,
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestDataValidation_ValidateType(t *testing.T) {
	dv := NewDataValidation()
	assert.True(t, dv.validateType("hello", "string"))
	assert.False(t, dv.validateType(123, "string"))
	assert.True(t, dv.validateType(42, "number"))
	assert.True(t, dv.validateType(42, "integer"))
	assert.True(t, dv.validateType(true, "boolean"))
	assert.True(t, dv.validateType([]interface{}{}, "array"))
	assert.True(t, dv.validateType(map[string]interface{}{}, "object"))
	assert.True(t, dv.validateType("anything", "unknown_type"))
}

func TestDataValidation_IsIdempotent(t *testing.T) {
	dv := NewDataValidation()
	assert.True(t, dv.IsIdempotent())
}
