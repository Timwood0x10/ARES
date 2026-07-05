package output

import "encoding/json"

const (
	schemaTypeObject  = "object"
	schemaTypeString  = "string"
	schemaTypeNumber  = "number"
	schemaTypeArray   = "array"
	schemaTypeInteger = "integer"

	keySessionID   = "session_id"
	keyUserID      = "user_id"
	keyItems       = "items"
	keyReason      = "reason"
	keyMatchScore  = "match_score"
	keyMetadata    = "metadata"
	keyItemID      = "item_id"
	keyCategory    = "category"
	keyBrand       = "brand"
	keyPrice       = "price"
	keyImageURL    = "image_url"
	keyStyle       = "style"
	keyColors      = "colors"
	keyDescription = "description"
	keyMatchReason = "match_reason"
	keyAge         = "age"
)

// Schema represents a JSON Schema.
type Schema struct {
	Type        string             `json:"type,omitempty"`
	Properties  map[string]*Schema `json:"properties,omitempty"`
	Items       *Schema            `json:"items,omitempty"`
	Required    []string           `json:"required,omitempty"`
	Minimum     *float64           `json:"minimum,omitempty"`
	Maximum     *float64           `json:"maximum,omitempty"`
	MinLength   *int               `json:"minLength,omitempty"`
	MaxLength   *int               `json:"maxLength,omitempty"`
	Pattern     string             `json:"pattern,omitempty"`
	Enum        []interface{}      `json:"enum,omitempty"`
	Nullable    bool               `json:"nullable,omitempty"`
	MinItems    *int               `json:"minItems,omitempty"`
	MaxItems    *int               `json:"maxItems,omitempty"`
	Description string             `json:"description,omitempty"`
	Format      string             `json:"format,omitempty"`
	Ref         string             `json:"$ref,omitempty"`
}

// GetRecommendResultSchema returns the schema for RecommendResult.
func GetRecommendResultSchema() *Schema {
	return &Schema{
		Type: schemaTypeObject,
		Properties: map[string]*Schema{
			"session_id": {
				Type:      "string",
				MinLength: pointerToInt(1),
			},
			"user_id": {
				Type:      "string",
				MinLength: pointerToInt(1),
			},
			"items": {
				Type:     "array",
				MinItems: pointerToInt(1),
				Items:    GetRecommendItemSchema(),
			},
			"reason": {
				Type: schemaTypeString,
			},
			"total_price": {
				Type:    schemaTypeNumber,
				Minimum: pointerToFloat64(0),
			},
			"match_score": {
				Type:    schemaTypeNumber,
				Minimum: pointerToFloat64(0),
				Maximum: pointerToFloat64(1),
			},
			"occasion": {
				Type: schemaTypeString,
				Enum: []interface{}{
					"casual", "business", "formal", "party", "date", "sports", "outdoor",
				},
			},
			"season": {
				Type: schemaTypeString,
				Enum: []interface{}{
					"spring", "summer", "autumn", "winter", "all_season",
				},
			},
			"metadata": {
				Type: schemaTypeObject,
			},
		},
		Required: []string{keySessionID, keyUserID, keyItems},
	}
}

// GetRecommendItemSchema returns the schema for RecommendItem.
func GetRecommendItemSchema() *Schema {
	return &Schema{
		Type: schemaTypeObject,
		Properties: map[string]*Schema{
			"item_id": {
				Type:      "string",
				MinLength: pointerToInt(1),
			},
			"category": {
				Type: schemaTypeString,
				Enum: []interface{}{
					"top", "bottom", "dress", "outerwear", "shoes", "accessory", "bag", "hat",
				},
			},
			"name": {
				Type:      "string",
				MinLength: pointerToInt(1),
			},
			"brand": {
				Type: schemaTypeString,
			},
			"price": {
				Type:    schemaTypeNumber,
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
				Type: schemaTypeArray,
				Items: &Schema{
					Type: schemaTypeString,
				},
			},
			"colors": {
				Type: schemaTypeArray,
				Items: &Schema{
					Type: schemaTypeString,
				},
			},
			"description": {
				Type: schemaTypeString,
			},
			"match_reason": {
				Type: schemaTypeString,
			},
			"metadata": {
				Type: schemaTypeObject,
			},
		},
		Required: []string{keyItemID, keyCategory, keyName, keyPrice},
	}
}

// GetUserProfileSchema returns the schema for UserProfile.
func GetUserProfileSchema() *Schema {
	return &Schema{
		Type: schemaTypeObject,
		Properties: map[string]*Schema{
			"user_id": {
				Type:      "string",
				MinLength: pointerToInt(1),
			},
			"gender": {
				Type: schemaTypeString,
				Enum: []interface{}{"male", "female", "other"},
			},
			"age": {
				Type:    schemaTypeInteger,
				Minimum: pointerToFloat64(0),
				Maximum: pointerToFloat64(150),
			},
			"style_preferences": {
				Type: schemaTypeArray,
				Items: &Schema{
					Type: schemaTypeString,
				},
			},
			"budget_range": {
				Type: schemaTypeObject,
				Properties: map[string]*Schema{
					"min": {
						Type:    schemaTypeNumber,
						Minimum: pointerToFloat64(0),
					},
					"max": {
						Type:    schemaTypeNumber,
						Minimum: pointerToFloat64(0),
					},
				},
			},
			"favorite_colors": {
				Type: schemaTypeArray,
				Items: &Schema{
					Type: schemaTypeString,
				},
			},
			"favorite_brands": {
				Type: schemaTypeArray,
				Items: &Schema{
					Type: schemaTypeString,
				},
			},
			"body_type": {
				Type: schemaTypeString,
			},
			"occupation": {
				Type: schemaTypeString,
			},
			"location": {
				Type: schemaTypeString,
			},
		},
		Required: []string{keyUserID},
	}
}

// ToJSON returns JSON representation of the schema.
func (s *Schema) ToJSON() (string, error) {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ToJSONString returns compact JSON representation.
func (s *Schema) ToJSONString() (string, error) {
	data, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Helper functions.
func pointerToInt(v int) *int {
	return &v
}

func pointerToFloat64(v float64) *float64 {
	return &v
}
