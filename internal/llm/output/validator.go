package output

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"sync"

	"goagentx/internal/core/models"
)

// Validator validates data against schemas.
type Validator struct {
	mu               sync.RWMutex
	customValidators map[string]ValidatorFunc
	schemaType       string   // Schema type for validation (e.g., "default", "travel", "custom")
	regexCache       sync.Map // Cache compiled regex patterns
}

// ValidatorFunc is a custom validation function.
type ValidatorFunc func(interface{}) error

// ValidatorOption is a functional option for Validator.
type ValidatorOption func(*Validator)

// WithSchemaType sets the schema type for validation.
func WithSchemaType(schemaType string) ValidatorOption {
	return func(v *Validator) {
		v.schemaType = schemaType
	}
}

// NewValidator creates a new Validator.
func NewValidator(opts ...ValidatorOption) *Validator {
	v := &Validator{
		customValidators: make(map[string]ValidatorFunc),
		schemaType:       "default", // default schema type
	}

	// Apply options
	for _, opt := range opts {
		opt(v)
	}

	v.registerDefaults()

	return v
}

// registerDefaults registers built-in validators.
func (v *Validator) registerDefaults() {
	v.RegisterValidator("string", v.validateString)
	v.RegisterValidator("number", v.validateNumber)
	v.RegisterValidator("integer", v.validateInteger)
	v.RegisterValidator("boolean", v.validateBoolean)
	v.RegisterValidator("array", v.validateArray)
	v.RegisterValidator("object", v.validateObject)
}

// RegisterValidator registers a custom validator.
func (v *Validator) RegisterValidator(name string, fn ValidatorFunc) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.customValidators[name] = fn
}

// Validate validates data against a schema.
func (v *Validator) Validate(data interface{}, schema *Schema) error {
	if schema == nil {
		return nil
	}

	return v.validateValue(data, schema, "root")
}

func (v *Validator) validateValue(data interface{}, schema *Schema, path string) error {
	// Handle null
	if data == nil {
		if schema.Nullable {
			return nil
		}
		return fmt.Errorf("%s: value is null", path)
	}

	// Type validation
	if schema.Type != "" {
		if err := v.validateType(data, schema.Type, path); err != nil {
			return err
		}
	}

	// Enum validation
	if len(schema.Enum) > 0 {
		if err := v.validateEnum(data, schema.Enum, path); err != nil {
			return err
		}
	}

	// String-specific validations
	if str, ok := data.(string); ok {
		if schema.MinLength != nil && len(str) < *schema.MinLength {
			return fmt.Errorf("%s: length %d is less than minimum %d", path, len(str), *schema.MinLength)
		}
		if schema.MaxLength != nil && len(str) > *schema.MaxLength {
			return fmt.Errorf("%s: length %d exceeds maximum %d", path, len(str), *schema.MaxLength)
		}
		if schema.Pattern != "" {
			// Use cached regex pattern for better performance.
			// regexp.Compile is used instead of MustCompile to avoid panic on invalid patterns.
			if re, ok := v.regexCache.Load(schema.Pattern); ok {
				regex := re.(*regexp.Regexp)
				if !regex.MatchString(str) {
					return fmt.Errorf("%s: does not match pattern %s", path, schema.Pattern)
				}
			} else {
				compiled, err := regexp.Compile(schema.Pattern)
				if err != nil {
					return fmt.Errorf("%s: invalid pattern %q: %w", path, schema.Pattern, err)
				}
				actual, _ := v.regexCache.LoadOrStore(schema.Pattern, compiled)
				regex := actual.(*regexp.Regexp)
				if !regex.MatchString(str) {
					return fmt.Errorf("%s: does not match pattern %s", path, schema.Pattern)
				}
			}
		}
	}

	// Number-specific validations
	if num, ok := toFloat64(data); ok {
		if schema.Minimum != nil && num < *schema.Minimum {
			return fmt.Errorf("%s: value %f is less than minimum %f", path, num, *schema.Minimum)
		}
		if schema.Maximum != nil && num > *schema.Maximum {
			return fmt.Errorf("%s: value %f exceeds maximum %f", path, num, *schema.Maximum)
		}
	}

	// Array validation
	if arr, ok := data.([]interface{}); ok {
		if schema.MinItems != nil && len(arr) < *schema.MinItems {
			return fmt.Errorf("%s: item count %d is less than minimum %d", path, len(arr), *schema.MinItems)
		}
		if schema.MaxItems != nil && len(arr) > *schema.MaxItems {
			return fmt.Errorf("%s: item count %d exceeds maximum %d", path, len(arr), *schema.MaxItems)
		}
		if schema.Items != nil {
			for i, item := range arr {
				if err := v.validateValue(item, schema.Items, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	}

	// Object validation
	if obj, ok := data.(map[string]interface{}); ok {
		// Required fields
		for _, required := range schema.Required {
			if _, exists := obj[required]; !exists {
				return fmt.Errorf("%s: missing required field %s", path, required)
			}
		}
		// Properties validation
		if schema.Properties != nil {
			for propName, propSchema := range schema.Properties {
				if val, exists := obj[propName]; exists {
					if err := v.validateValue(val, propSchema, fmt.Sprintf("%s.%s", path, propName)); err != nil {
						return err
					}
				}
			}
		}
	}

	// Custom validator
	if schema.Type != "" {
		v.mu.RLock()
		fn, exists := v.customValidators[schema.Type]
		v.mu.RUnlock()
		if exists {
			if err := fn(data); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		}
	}

	return nil
}

func (v *Validator) validateType(data interface{}, expectedType string, path string) error {
	switch expectedType {
	case "string":
		_, ok := data.(string)
		if !ok {
			return fmt.Errorf("%s: expected string, got %T", path, data)
		}
	case "number":
		if _, ok := toFloat64(data); !ok {
			return fmt.Errorf("%s: expected number, got %T", path, data)
		}
	case "integer":
		if _, ok := toInt64(data); !ok {
			return fmt.Errorf("%s: expected integer, got %T", path, data)
		}
	case "boolean":
		_, ok := data.(bool)
		if !ok {
			return fmt.Errorf("%s: expected boolean, got %T", path, data)
		}
	case "array":
		_, ok := data.([]interface{})
		if !ok {
			return fmt.Errorf("%s: expected array, got %T", path, data)
		}
	case "object":
		_, ok := data.(map[string]interface{})
		if !ok {
			return fmt.Errorf("%s: expected object, got %T", path, data)
		}
	}
	return nil
}

func (v *Validator) validateEnum(value interface{}, enum []interface{}, path string) error {
	for _, e := range enum {
		if reflect.DeepEqual(value, e) {
			return nil
		}
	}
	return fmt.Errorf("%s: value %v is not in enum %v", path, value, enum)
}

func (v *Validator) validateString(value interface{}) error {
	_, ok := value.(string)
	if !ok {
		return errors.New("expected string")
	}
	return nil
}

func (v *Validator) validateNumber(value interface{}) error {
	_, ok := toFloat64(value)
	if !ok {
		return errors.New("expected number")
	}
	return nil
}

func (v *Validator) validateInteger(value interface{}) error {
	_, ok := toInt64(value)
	if !ok {
		return errors.New("expected integer")
	}
	return nil
}

func (v *Validator) validateBoolean(value interface{}) error {
	_, ok := value.(bool)
	if !ok {
		return errors.New("expected boolean")
	}
	return nil
}

func (v *Validator) validateArray(value interface{}) error {
	_, ok := value.([]interface{})
	if !ok {
		return errors.New("expected array")
	}
	return nil
}

func (v *Validator) validateObject(value interface{}) error {
	_, ok := value.(map[string]interface{})
	if !ok {
		return errors.New("expected object")
	}
	return nil
}

// ValidateRecommendResult validates RecommendResult against schema.
func (v *Validator) ValidateRecommendResult(result *models.RecommendResult) error {
	if result == nil {
		return errors.New("result is nil")
	}

	// Convert RecommendResult items to []interface{} for validation
	itemsInterface := make([]interface{}, len(result.Items))
	for i, item := range result.Items {
		if item == nil {
			return fmt.Errorf("items[%d] is nil", i)
		}
		// Convert AgentPreferences []string to []interface{}
		agentPreferencesInterface := make([]interface{}, len(item.AgentPreferences))
		for j, s := range item.AgentPreferences {
			agentPreferencesInterface[j] = string(s)
		}
		// Convert Colors []string to []interface{}
		colorsInterface := make([]interface{}, len(item.Colors))
		for j, c := range item.Colors {
			colorsInterface[j] = c
		}

		itemsInterface[i] = map[string]interface{}{
			"item_id":           item.ItemID,
			"name":              item.Name,
			"category":          item.Category,
			"description":       item.Description,
			"price":             item.Price,
			"url":               item.URL,
			"image_url":         item.ImageURL,
			"agent_preferences": agentPreferencesInterface,
			"colors":            colorsInterface,
			"match_reason":      item.MatchReason,
			"brand":             item.Brand,
			"metadata":          item.Metadata,
		}
	}

	// Convert RecommendResult to map[string]interface{} for validation
	resultMap := map[string]interface{}{
		"session_id":  result.SessionID,
		"user_id":     result.UserID,
		"items":       itemsInterface,
		"reason":      result.Reason,
		"total_price": result.TotalPrice,
		"match_score": result.MatchScore,
		"occasion":    result.Occasion,
		"season":      result.Season,
		"metadata":    result.Metadata,
	}

	schema := v.getSchema()
	return v.Validate(resultMap, schema)
}

// getSchema returns the appropriate schema based on schemaType.
func (v *Validator) getSchema() *Schema {
	switch v.schemaType {
	case "travel":
		return GetTravelResultSchema()
	case "default":
		return GetRecommendResultSchema()
	default:
		return GetRecommendResultSchema()
	}
}

// GetTravelResultSchema returns the schema for travel recommendation results.
func GetTravelResultSchema() *Schema {
	return &Schema{
		Type: "object",
		Properties: map[string]*Schema{
			"session_id": {
				Type: "string",
			},
			"user_id": {
				Type: "string",
			},
			"items": {
				Type:     "array",
				MinItems: pointerToInt(1),
				Items:    GetTravelItemSchema(),
			},
			"reason": {
				Type: "string",
			},
			"total_price": {
				Type:    "number",
				Minimum: pointerToFloat64(0),
			},
			"match_score": {
				Type:    "number",
				Minimum: pointerToFloat64(0),
				Maximum: pointerToFloat64(1),
			},
			"metadata": {
				Type: "object",
			},
		},
		Required: []string{"items"},
	}
}

// GetTravelItemSchema returns the schema for travel recommendation items.
func GetTravelItemSchema() *Schema {
	return &Schema{
		Type: "object",
		Properties: map[string]*Schema{
			"item_id": {
				Type:      "string",
				MinLength: pointerToInt(1),
			},
			"category": {
				Type: "string",
				Enum: []interface{}{
					"destination", "food", "hotel", "itinerary", "transport", "activity",
				},
			},
			"name": {
				Type:      "string",
				MinLength: pointerToInt(1),
			},
			"brand": {
				Type: "string",
			},
			"description": {
				Type: "string",
			},
			"price": {
				Type:    "number",
				Minimum: pointerToFloat64(0),
			},
			"url": {
				Type:   "string",
				Format: "uri",
			},
			"image_url": {
				Type:   "string",
				Format: "uri",
			},
			"style": {
				Type:  "array",
				Items: &Schema{Type: "string"},
			},
			"colors": {
				Type:  "array",
				Items: &Schema{Type: "string"},
			},
			"match_reason": {
				Type: "string",
			},
			"metadata": {
				Type: "object",
			},
		},
		Required: []string{"item_id", "name", "category"},
	}
}

// Helper functions.
func toFloat64(v interface{}) (float64, bool) {
	switch val := v.(type) {
	case float64:
		return val, true
	case float32:
		return float64(val), true
	case int:
		return float64(val), true
	case int64:
		return float64(val), true
	case int32:
		return float64(val), true
	case uint:
		return float64(val), true
	case uint64:
		return float64(val), true
	case uint32:
		return float64(val), true
		// Reject string type to avoid ambiguous type conversion
		// Strings should be validated explicitly before conversion
	}
	return 0, false
}

func toInt64(v interface{}) (int64, bool) {
	switch val := v.(type) {
	case int:
		return int64(val), true
	case int64:
		return val, true
	case int32:
		return int64(val), true
	case float64:
		// Check if value is within int64 range
		if val <= float64(^uint64(0)>>1) && val >= float64(^int64(0)) {
			return int64(val), true
		}
		// Value exceeds int64 range, reject it
		return 0, false
	case float32:
		// Check if value is within int64 range
		if float64(val) <= float64(^uint64(0)>>1) && float64(val) >= float64(^int64(0)) {
			return int64(val), true
		}
		// Value exceeds int64 range, reject it
		return 0, false
	case uint:
		if uint64(val) <= uint64(int64(^uint64(0)>>1)) {
			return int64(val), true
		}
		return 0, false
	case uint64:
		if val <= uint64(int64(^uint64(0)>>1)) {
			return int64(val), true
		}
		// Value exceeds int64 range, reject it
		return 0, false
	case uint32:
		return int64(val), true
		// Reject string type to avoid ambiguous type conversion
		// Strings should be validated explicitly before conversion
	}
	return 0, false
}

// Validator errors.
var (
	ErrValidationFailed = errors.New("validation failed")
)
