package output

import (
	"testing"
)

func TestNewParserCreatesInstance(t *testing.T) {
	p := NewParser()
	if p == nil {
		t.Fatal("NewParser returned nil")
	}
	if !p.fixJSON {
		t.Error("fixJSON should be true by default")
	}
	if p.inputValidator == nil {
		t.Error("inputValidator should not be nil")
	}
}

func TestParserExtractJSON(t *testing.T) {
	p := NewParser()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"json_block", "```json\n{\"key\":\"value\"}\n```", `{"key":"value"}`},
		{"plain_block", "```\n{\"key\":\"value\"}\n```", `{"key":"value"}`},
		{"inline_json", `{"key":"value"}`, `{"key":"value"}`},
		{"no_json", "no json here", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.extractJSON(tt.input)
			if got != tt.want {
				t.Errorf("extractJSON(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParserParseRecommendResultErrors(t *testing.T) {
	p := NewParser()
	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"no_json", "just plain text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := p.ParseRecommendResult(tt.input)
			if err == nil {
				t.Error("expected error for invalid input")
			}
		})
	}
}

func TestParserParseRecommendResultValid(t *testing.T) {
	p := NewParser()
	result, err := p.ParseRecommendResult(`{"recommendations": []}`)
	if err != nil {
		t.Fatalf("ParseRecommendResult: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
}

func TestNewValidatorCreatesInstance(t *testing.T) {
	v := NewValidator()
	if v == nil {
		t.Fatal("NewValidator returned nil")
	}
	if v.schemaType != "default" {
		t.Errorf("schemaType = %q, want default", v.schemaType)
	}
}

func TestNewValidatorWithSchemaTypeOption(t *testing.T) {
	v := NewValidator(WithSchemaType("custom"))
	if v.schemaType != "custom" {
		t.Errorf("schemaType = %q, want custom", v.schemaType)
	}
}

func TestValidatorRegisterCustomValidator(t *testing.T) {
	v := NewValidator()
	called := false
	v.RegisterValidator("my_validator", func(_ interface{}) error {
		called = true
		return nil
	})
	if v.customValidators["my_validator"] == nil {
		t.Fatal("custom validator not registered")
	}
	_ = v.customValidators["my_validator"](nil)
	if !called {
		t.Error("custom validator was not called")
	}
}

func TestValidatorValidateNilSchema(t *testing.T) {
	v := NewValidator()
	if err := v.Validate("data", nil); err != nil {
		t.Errorf("Validate with nil schema: %v", err)
	}
}

func TestValidatorValidateStringType(t *testing.T) {
	v := NewValidator()
	schema := &Schema{Type: "string"}
	tests := []struct {
		name    string
		data    interface{}
		wantErr bool
	}{
		{"valid", "hello", false},
		{"invalid_number", 42, true},
		{"invalid_bool", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.Validate(tt.data, schema)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate(%v, string) err = %v, wantErr %v", tt.data, err, tt.wantErr)
			}
		})
	}
}

func TestValidatorValidateNumberType(t *testing.T) {
	v := NewValidator()
	schema := &Schema{Type: "number"}
	tests := []struct {
		name    string
		data    interface{}
		wantErr bool
	}{
		{"valid_int", 42, false},
		{"valid_float", 3.14, false},
		{"invalid_string", "hello", true},
		{"invalid_bool", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.Validate(tt.data, schema)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate(%v, number) err = %v, wantErr %v", tt.data, err, tt.wantErr)
			}
		})
	}
}

func TestValidatorValidateBooleanType(t *testing.T) {
	v := NewValidator()
	schema := &Schema{Type: "boolean"}
	tests := []struct {
		name    string
		data    interface{}
		wantErr bool
	}{
		{"valid_true", true, false},
		{"valid_false", false, false},
		{"invalid_string", "true", true},
		{"invalid_number", 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.Validate(tt.data, schema)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate(%v, boolean) err = %v, wantErr %v", tt.data, err, tt.wantErr)
			}
		})
	}
}

func TestValidatorValidateArrayType(t *testing.T) {
	v := NewValidator()
	schema := &Schema{Type: "array"}
	tests := []struct {
		name    string
		data    interface{}
		wantErr bool
	}{
		{"valid_slice", []interface{}{1, 2, 3}, false},
		{"valid_empty", []interface{}{}, false},
		{"invalid_string", "not array", true},
		{"invalid_map", map[string]interface{}{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.Validate(tt.data, schema)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate(%v, array) err = %v, wantErr %v", tt.data, err, tt.wantErr)
			}
		})
	}
}

func TestValidatorValidateObjectType(t *testing.T) {
	v := NewValidator()
	schema := &Schema{Type: "object"}
	tests := []struct {
		name    string
		data    interface{}
		wantErr bool
	}{
		{"valid_map", map[string]interface{}{"key": "val"}, false},
		{"invalid_string", "not object", true},
		{"invalid_array", []interface{}{1}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.Validate(tt.data, schema)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate(%v, object) err = %v, wantErr %v", tt.data, err, tt.wantErr)
			}
		})
	}
}

func TestErrInvalidJSONNotNil(t *testing.T) {
	if ErrInvalidJSON == nil {
		t.Error("ErrInvalidJSON should not be nil")
	}
	if ErrInvalidJSON.Error() == "" {
		t.Error("ErrInvalidJSON message should not be empty")
	}
}

func TestNewFactoryCreatesInstance(t *testing.T) {
	f := NewFactory()
	if f == nil {
		t.Fatal("NewFactory returned nil")
	}
}

func TestFactoryCreateUnknownProvider(t *testing.T) {
	f := NewFactory()
	_, err := f.Create("nonexistent_provider", nil)
	if err == nil {
		t.Error("expected error for unknown provider")
	}
}

func TestDefaultTimeoutsPopulated(t *testing.T) {
	if DefaultTimeouts.LLMRequest == 0 {
		t.Error("LLMRequest timeout should not be zero")
	}
	if DefaultTimeouts.LLMStructuredOutput == 0 {
		t.Error("LLMStructuredOutput timeout should not be zero")
	}
	if DefaultTimeouts.DatabaseQuery == 0 {
		t.Error("DatabaseQuery timeout should not be zero")
	}
}

func TestNewTemplateEngineCreatesInstance(t *testing.T) {
	e := NewTemplateEngine()
	if e == nil {
		t.Fatal("NewTemplateEngine returned nil")
	}
}

func TestNewTemplateRegistryCreatesInstance(t *testing.T) {
	r := NewTemplateRegistry()
	if r == nil {
		t.Fatal("NewTemplateRegistry returned nil")
	}
}
