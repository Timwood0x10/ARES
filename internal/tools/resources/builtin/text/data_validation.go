package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"goagentx/internal/tools/resources/base"
	"goagentx/internal/tools/resources/core"
	"regexp"
	"strings"
)

// DataValidation provides data validation operations.
type DataValidation struct {
	*base.BaseTool
}

// NewDataValidation creates a new DataValidation tool.
func NewDataValidation() *DataValidation {
	params := &core.ParameterSchema{
		Type: "object",
		Properties: map[string]*core.Parameter{
			"operation": {
				Type:        "string",
				Description: "Validation operation to perform (validate_json, validate_email, validate_url, validate_schema)",
				Enum:        []interface{}{"validate_json", "validate_email", "validate_url", "validate_schema"},
			},
			"data": {
				Type:        "string",
				Description: "Data to validate (JSON string, email, or URL)",
			},
			"schema": {
				Type:        "string",
				Description: "JSON schema for validation (required for validate_schema operation)",
			},
		},
		Required: []string{"operation", "data"},
	}

	return &DataValidation{
		BaseTool: base.NewBaseToolWithCapabilities("data_validation", "Validate JSON, email, URL, or schema", core.CategoryData, []core.Capability{core.CapabilityText}, params),
	}
}

// Execute performs the data validation operation.
func (t *DataValidation) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	operation, ok := params["operation"].(string)
	if !ok || operation == "" {
		return core.NewErrorResult("operation is required"), nil
	}

	data, ok := params["data"].(string)
	if !ok || data == "" {
		return core.NewErrorResult("data is required"), nil
	}

	switch operation {
	case "validate_json":
		return t.validateJSON(ctx, data)
	case "validate_email":
		return t.validateEmail(ctx, data)
	case "validate_url":
		return t.validateURL(ctx, data)
	case "validate_schema":
		schema, ok := params["schema"].(string)
		if !ok || schema == "" {
			return core.NewErrorResult("schema is required for validate_schema operation"), nil
		}
		return t.validateSchema(ctx, data, schema)
	default:
		return core.NewErrorResult(fmt.Sprintf("unsupported operation: %s", operation)), nil
	}
}

// validateJSON validates if a string is valid JSON.
func (t *DataValidation) validateJSON(ctx context.Context, data string) (core.Result, error) {
	var js interface{}
	if err := json.Unmarshal([]byte(data), &js); err != nil {
		return core.NewResult(false, map[string]interface{}{
			"valid":  false,
			"reason": fmt.Sprintf("invalid JSON: %v", err),
		}), nil
	}

	// Check if it's an object
	_, isObject := js.(map[string]interface{})
	_, isArray := js.([]interface{})

	var jsonType string
	if isObject {
		jsonType = "object"
	} else if isArray {
		jsonType = "array"
	} else {
		jsonType = "primitive"
	}

	return core.NewResult(true, map[string]interface{}{
		"valid":     true,
		"type":      jsonType,
		"size":      len(data),
		"structure": js,
	}), nil
}

// validateEmail validates if a string is a valid email address.
func (t *DataValidation) validateEmail(ctx context.Context, data string) (core.Result, error) {
	// RFC 5322 compliant email regex (simplified)
	emailRegex := regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

	if !emailRegex.MatchString(data) {
		return core.NewResult(false, map[string]interface{}{
			"valid":  false,
			"reason": "email format is invalid",
			"email":  data,
		}), nil
	}

	// Additional checks
	parts := strings.Split(data, "@")
	if len(parts) != 2 {
		return core.NewResult(false, map[string]interface{}{
			"valid":  false,
			"reason": "invalid email format",
			"email":  data,
		}), nil
	}

	localPart := parts[0]
	domain := parts[1]

	// Check local part length
	if len(localPart) > 64 {
		return core.NewResult(false, map[string]interface{}{
			"valid":  false,
			"reason": "local part exceeds 64 characters",
			"email":  data,
		}), nil
	}

	// Check domain length
	if len(domain) > 255 {
		return core.NewResult(false, map[string]interface{}{
			"valid":  false,
			"reason": "domain exceeds 255 characters",
			"email":  data,
		}), nil
	}

	// Check total length
	if len(data) > 254 {
		return core.NewResult(false, map[string]interface{}{
			"valid":  false,
			"reason": "email exceeds 254 characters",
			"email":  data,
		}), nil
	}

	return core.NewResult(true, map[string]interface{}{
		"valid":      true,
		"email":      data,
		"local_part": localPart,
		"domain":     domain,
	}), nil
}

// validateURL validates if a string is a valid URL.
func (t *DataValidation) validateURL(ctx context.Context, data string) (core.Result, error) {
	// Basic URL regex
	urlRegex := regexp.MustCompile(`^https?://[a-zA-Z0-9.\-]+(:[0-9]+)?(/[a-zA-Z0-9.\-_%~/?#&=]*)?$`)

	if !urlRegex.MatchString(data) {
		return core.NewResult(false, map[string]interface{}{
			"valid":  false,
			"reason": "URL format is invalid or must use http/https",
			"url":    data,
		}), nil
	}

	// Check for valid scheme
	if !strings.HasPrefix(data, "http://") && !strings.HasPrefix(data, "https://") {
		return core.NewResult(false, map[string]interface{}{
			"valid":  false,
			"reason": "URL must use http or https scheme",
			"url":    data,
		}), nil
	}

	// Extract components
	parts := strings.SplitN(data, "://", 2)
	if len(parts) != 2 {
		return core.NewResult(false, map[string]interface{}{
			"valid":  false,
			"reason": "invalid URL format",
			"url":    data,
		}), nil
	}

	scheme := parts[0]
	rest := parts[1]

	// Extract host
	host := rest
	if idx := strings.Index(rest, "/"); idx != -1 {
		host = rest[:idx]
	}

	// Extract port
	port := ""
	if idx := strings.Index(host, ":"); idx != -1 {
		port = host[idx+1:]
		host = host[:idx]
	}

	return core.NewResult(true, map[string]interface{}{
		"valid":  true,
		"url":    data,
		"scheme": scheme,
		"host":   host,
		"port":   port,
	}), nil
}

// validateSchema validates JSON data against a schema.
// NOTE: This is a simplified implementation. For full JSON Schema validation,
// consider using a library like github.com/xeipuuv/gojsonschema.
func (t *DataValidation) validateSchema(ctx context.Context, data, schema string) (core.Result, error) {
	// First validate both are valid JSON
	var dataJSON interface{}
	if err := json.Unmarshal([]byte(data), &dataJSON); err != nil {
		return core.NewErrorResult(fmt.Sprintf("invalid data JSON: %v", err)), nil
	}

	var schemaJSON interface{}
	if err := json.Unmarshal([]byte(schema), &schemaJSON); err != nil {
		return core.NewErrorResult(fmt.Sprintf("invalid schema JSON: %v", err)), nil
	}

	schemaMap, ok := schemaJSON.(map[string]interface{})
	if !ok {
		return core.NewErrorResult("schema must be a JSON object"), nil
	}

	// Check required fields
	required, ok := schemaMap["required"].([]interface{})
	if !ok {
		required = []interface{}{}
	}

	// Check properties
	properties, ok := schemaMap["properties"].(map[string]interface{})
	if !ok {
		properties = make(map[string]interface{})
	}

	// Validate data object
	dataMap, ok := dataJSON.(map[string]interface{})
	if !ok {
		return core.NewErrorResult("data must be a JSON object for schema validation"), nil
	}

	// Check required fields
	var missingFields []string
	for _, req := range required {
		fieldName, ok := req.(string)
		if !ok {
			continue
		}
		if _, exists := dataMap[fieldName]; !exists {
			missingFields = append(missingFields, fieldName)
		}
	}

	// Check data types
	var typeErrors []string
	for field, value := range dataMap {
		if prop, exists := properties[field]; exists {
			propMap, ok := prop.(map[string]interface{})
			if !ok {
				continue
			}

			expectedType, ok := propMap["type"].(string)
			if !ok {
				continue
			}

			if !t.validateType(value, expectedType) {
				typeErrors = append(typeErrors, fmt.Sprintf("field '%s' expected type %s", field, expectedType))
			}
		}
	}

	if len(missingFields) > 0 || len(typeErrors) > 0 {
		return core.NewResult(false, map[string]interface{}{
			"valid":          false,
			"missing_fields": missingFields,
			"type_errors":    typeErrors,
		}), nil
	}

	return core.NewResult(true, map[string]interface{}{
		"valid": true,
	}), nil
}

func (t *DataValidation) IsIdempotent() bool { return true }

// validateType checks if a value matches the expected type.
func (t *DataValidation) validateType(value interface{}, expectedType string) bool {
	switch expectedType {
	case "string":
		_, ok := value.(string)
		return ok
	case "number", "integer":
		switch value.(type) {
		case float64, float32, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			return true
		default:
			return false
		}
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "array":
		_, ok := value.([]interface{})
		return ok
	case "object":
		_, ok := value.(map[string]interface{})
		return ok
	default:
		return true
	}
}
