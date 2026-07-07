package core

import (
	"testing"
)

func TestValidateParams_NilSchema(t *testing.T) {
	err := ValidateParams(nil, map[string]interface{}{"key": "value"})
	if err != nil {
		t.Errorf("nil schema should pass, got: %v", err)
	}
}

func TestValidateParams_EmptySchema(t *testing.T) {
	schema := &ParameterSchema{
		Type:       "object",
		Properties: map[string]*Parameter{},
	}
	err := ValidateParams(schema, map[string]interface{}{"key": "value"})
	if err != nil {
		t.Errorf("empty schema with any params should pass, got: %v", err)
	}
}

func TestValidateParams_RequiredPresent(t *testing.T) {
	schema := &ParameterSchema{
		Type: "object",
		Properties: map[string]*Parameter{
			"name": {Type: "string"},
		},
		Required: []string{"name"},
	}
	err := ValidateParams(schema, map[string]interface{}{"name": "test"})
	if err != nil {
		t.Errorf("required param present should pass, got: %v", err)
	}
}

func TestValidateParams_RequiredMissing(t *testing.T) {
	schema := &ParameterSchema{
		Type: "object",
		Properties: map[string]*Parameter{
			"name": {Type: "string"},
		},
		Required: []string{"name"},
	}
	err := ValidateParams(schema, map[string]interface{}{})
	if err == nil {
		t.Error("required param missing should fail")
	}
}

func TestValidateParams_RequiredNil(t *testing.T) {
	schema := &ParameterSchema{
		Type: "object",
		Properties: map[string]*Parameter{
			"name": {Type: "string"},
		},
		Required: []string{"name"},
	}
	err := ValidateParams(schema, map[string]interface{}{"name": nil})
	if err == nil {
		t.Error("required param nil should fail")
	}
}

func TestValidateParams_TypeStringOK(t *testing.T) {
	schema := &ParameterSchema{
		Type: "object",
		Properties: map[string]*Parameter{
			"expr": {Type: "string"},
		},
	}
	err := ValidateParams(schema, map[string]interface{}{"expr": "1+1"})
	if err != nil {
		t.Errorf("string type should pass, got: %v", err)
	}
}

func TestValidateParams_TypeStringFail(t *testing.T) {
	schema := &ParameterSchema{
		Type: "object",
		Properties: map[string]*Parameter{
			"expr": {Type: "string"},
		},
	}
	err := ValidateParams(schema, map[string]interface{}{"expr": 123})
	if err == nil {
		t.Error("number for string field should fail")
	}
}

func TestValidateParams_TypeNumberOK(t *testing.T) {
	schema := &ParameterSchema{
		Type: "object",
		Properties: map[string]*Parameter{
			"count": {Type: "number"},
		},
	}
	err := ValidateParams(schema, map[string]interface{}{"count": 42.0})
	if err != nil {
		t.Errorf("number type should pass, got: %v", err)
	}
}

func TestValidateParams_TypeIntAsNumber(t *testing.T) {
	schema := &ParameterSchema{
		Type: "object",
		Properties: map[string]*Parameter{
			"count": {Type: "number"},
		},
	}
	err := ValidateParams(schema, map[string]interface{}{"count": 42})
	if err != nil {
		t.Errorf("int should pass as number, got: %v", err)
	}
}

func TestValidateParams_TypeBoolOK(t *testing.T) {
	schema := &ParameterSchema{
		Type: "object",
		Properties: map[string]*Parameter{
			"flag": {Type: "boolean"},
		},
	}
	err := ValidateParams(schema, map[string]interface{}{"flag": true})
	if err != nil {
		t.Errorf("boolean type should pass, got: %v", err)
	}
}

func TestValidateParams_TypeBoolFail(t *testing.T) {
	schema := &ParameterSchema{
		Type: "object",
		Properties: map[string]*Parameter{
			"flag": {Type: "boolean"},
		},
	}
	err := ValidateParams(schema, map[string]interface{}{"flag": "true"})
	if err == nil {
		t.Error("string for boolean field should fail")
	}
}

func TestValidateParams_TypeArrayOK(t *testing.T) {
	schema := &ParameterSchema{
		Type: "object",
		Properties: map[string]*Parameter{
			"items": {Type: "array"},
		},
	}
	err := ValidateParams(schema, map[string]interface{}{"items": []interface{}{1, 2, 3}})
	if err != nil {
		t.Errorf("array type should pass, got: %v", err)
	}
}

func TestValidateParams_TypeArrayFail(t *testing.T) {
	schema := &ParameterSchema{
		Type: "object",
		Properties: map[string]*Parameter{
			"items": {Type: "array"},
		},
	}
	err := ValidateParams(schema, map[string]interface{}{"items": "not-an-array"})
	if err == nil {
		t.Error("string for array field should fail")
	}
}

func TestValidateParams_EnumOK(t *testing.T) {
	schema := &ParameterSchema{
		Type: "object",
		Properties: map[string]*Parameter{
			"op": {
				Type: "string",
				Enum: []interface{}{"upper", "lower", "trim"},
			},
		},
	}
	err := ValidateParams(schema, map[string]interface{}{"op": "upper"})
	if err != nil {
		t.Errorf("valid enum value should pass, got: %v", err)
	}
}

func TestValidateParams_EnumFail(t *testing.T) {
	schema := &ParameterSchema{
		Type: "object",
		Properties: map[string]*Parameter{
			"op": {
				Type: "string",
				Enum: []interface{}{"upper", "lower", "trim"},
			},
		},
	}
	err := ValidateParams(schema, map[string]interface{}{"op": "unknown"})
	if err == nil {
		t.Error("invalid enum value should fail")
	}
}

func TestValidateParams_MultipleErrorsFirstWins(t *testing.T) {
	schema := &ParameterSchema{
		Type: "object",
		Properties: map[string]*Parameter{
			"name":  {Type: "string"},
			"count": {Type: "number"},
			"op": {
				Type: "string",
				Enum: []interface{}{"add"},
			},
		},
		Required: []string{"name"},
	}
	// Missing required: should report "name" first.
	err := ValidateParams(schema, map[string]interface{}{"count": "bad"})
	if err == nil {
		t.Error("missing required should fail")
	}
}

func TestValidateParams_TypeInt64AsInteger(t *testing.T) {
	schema := &ParameterSchema{
		Type: "object",
		Properties: map[string]*Parameter{
			"page": {Type: "integer"},
		},
	}
	err := ValidateParams(schema, map[string]interface{}{"page": int64(5)})
	if err != nil {
		t.Errorf("int64 should pass as integer, got: %v", err)
	}
}

func TestValidateParams_UnknownParamIgnored(t *testing.T) {
	schema := &ParameterSchema{
		Type:       "object",
		Properties: map[string]*Parameter{},
	}
	// Unknown params should be silently ignored.
	err := ValidateParams(schema, map[string]interface{}{"unknown": "value"})
	if err != nil {
		t.Errorf("unknown param should be ignored, got: %v", err)
	}
}

func TestValidateParams_NilValueNonRequired(t *testing.T) {
	schema := &ParameterSchema{
		Type: "object",
		Properties: map[string]*Parameter{
			"optional": {Type: "string"},
		},
	}
	err := ValidateParams(schema, map[string]interface{}{"optional": nil})
	if err != nil {
		t.Errorf("nil value for non-required param should pass, got: %v", err)
	}
}
