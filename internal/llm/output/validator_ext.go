package output

import (
	"errors"
	"fmt"

	gerr "github.com/Timwood0x10/ares/internal/errors"
)

// Validation limits for LLM output parsing.
const (
	// MaxInputLength is the maximum allowed input length for LLM output parsing.
	// This prevents memory exhaustion from extremely long outputs.
	MaxInputLength = 1 * 1024 * 1024 // 1MB

	// MaxJSONLength is the maximum allowed JSON content length.
	MaxJSONLength = 500 * 1024 // 500KB

	// MaxJSONDepth is the maximum nesting depth allowed in JSON.
	MaxJSONDepth = 10

	// MaxArrayLength is the maximum number of elements allowed in JSON arrays.
	MaxArrayLength = 1000

	// MaxStringLength is the maximum length for string values in JSON.
	MaxStringLength = 500 * 1024 // 500KB - increased for code generation

	// MaxObjectKeyLength is the maximum length for object keys in JSON.
	MaxObjectKeyLength = 256
)

// Validation errors.
var (
	ErrInputTooLong      = errors.New("input exceeds maximum allowed length")
	ErrJSONTooLong       = errors.New("JSON content exceeds maximum allowed length")
	ErrJSONDepthExceeded = errors.New("JSON nesting depth exceeds maximum allowed depth")
	ErrArrayTooLarge     = errors.New("array length exceeds maximum allowed size")
	ErrStringTooLong     = errors.New("string value exceeds maximum allowed length")
	ErrKeyTooLong        = errors.New("object key exceeds maximum allowed length")
)

// InputValidator validates input for security and performance reasons.
type InputValidator struct {
	maxInputLength     int
	maxJSONLength      int
	maxJSONDepth       int
	maxArrayLength     int
	maxStringLength    int
	maxObjectKeyLength int
}

// NewInputValidator creates a new InputValidator with default limits.
func NewInputValidator() *InputValidator {
	return &InputValidator{
		maxInputLength:     MaxInputLength,
		maxJSONLength:      MaxJSONLength,
		maxJSONDepth:       MaxJSONDepth,
		maxArrayLength:     MaxArrayLength,
		maxStringLength:    MaxStringLength,
		maxObjectKeyLength: MaxObjectKeyLength,
	}
}

// ValidateInput validates the input length and returns an error if it exceeds limits.
func (v *InputValidator) ValidateInput(input string) error {
	if len(input) > v.maxInputLength {
		return gerr.Wrapf(ErrInputTooLong, "%d bytes (max %d bytes)", len(input), v.maxInputLength)
	}
	return nil
}

// ValidateJSONLength validates the JSON content length.
func (v *InputValidator) ValidateJSONLength(jsonContent string) error {
	if len(jsonContent) > v.maxJSONLength {
		return gerr.Wrapf(ErrJSONTooLong, "%d bytes (max %d bytes)", len(jsonContent), v.maxJSONLength)
	}
	return nil
}

// GetMaxInputLength returns the maximum input length.
func (v *InputValidator) GetMaxInputLength() int {
	return v.maxInputLength
}

// GetMaxArrayLength returns the maximum array length.
func (v *InputValidator) GetMaxArrayLength() int {
	return v.maxArrayLength
}

// GetMaxJSONLength returns the maximum JSON length.
func (v *InputValidator) GetMaxJSONLength() int {
	return v.maxJSONLength
}

// GetMaxJSONDepth returns the maximum JSON depth.
func (v *InputValidator) GetMaxJSONDepth() int {
	return v.maxJSONDepth
}

// GetMaxStringLength returns the maximum string length.
func (v *InputValidator) GetMaxStringLength() int {
	return v.maxStringLength
}

// GetMaxObjectKeyLength returns the maximum object key length.
func (v *InputValidator) GetMaxObjectKeyLength() int {
	return v.maxObjectKeyLength
}

// ValidateJSONDepth validates JSON nesting depth (simplified check).
func (v *InputValidator) ValidateJSONDepth(jsonContent string) error {
	// Count opening braces to estimate depth
	depth := 0
	maxDepth := 0

	for _, char := range jsonContent {
		switch char {
		case '{':
			depth++
			if depth > maxDepth {
				maxDepth = depth
			}
		case '}':
			depth--
			if depth < 0 {
				return fmt.Errorf("%w: unbalanced braces", ErrJSONDepthExceeded)
			}
		}
	}

	if maxDepth > v.maxJSONDepth {
		return fmt.Errorf("%w: depth %d (max %d)", ErrJSONDepthExceeded, maxDepth, v.maxJSONDepth)
	}

	return nil
}

// ValidateStringLength validates a string value length.
func (v *InputValidator) ValidateStringLength(value string) error {
	if len(value) > v.maxStringLength {
		return gerr.Wrapf(ErrStringTooLong, "%d chars (max %d chars)", len(value), v.maxStringLength)
	}
	return nil
}

// ValidateArrayLength validates an array length.
func (v *InputValidator) ValidateArrayLength(length int) error {
	if length > v.maxArrayLength {
		return gerr.Wrapf(ErrArrayTooLarge, "length %d (max %d)", length, v.maxArrayLength)
	}
	return nil
}

// ValidateObjectKeyLength validates an object key length.
func (v *InputValidator) ValidateObjectKeyLength(key string) error {
	if len(key) > v.maxObjectKeyLength {
		return gerr.Wrapf(ErrKeyTooLong, "%d chars (max %d chars)", len(key), v.maxObjectKeyLength)
	}
	return nil
}

// GetConfig returns the current validation configuration.
func (v *InputValidator) GetConfig() InputValidatorConfig {
	return InputValidatorConfig{
		MaxInputLength:     v.maxInputLength,
		MaxJSONLength:      v.maxJSONLength,
		MaxJSONDepth:       v.maxJSONDepth,
		MaxArrayLength:     v.maxArrayLength,
		MaxStringLength:    v.maxStringLength,
		MaxObjectKeyLength: v.maxObjectKeyLength,
	}
}

// InputValidatorConfig represents the validation configuration.
type InputValidatorConfig struct {
	MaxInputLength     int `json:"maxInputLength"`
	MaxJSONLength      int `json:"maxJSONLength"`
	MaxJSONDepth       int `json:"maxJSONDepth"`
	MaxArrayLength     int `json:"maxArrayLength"`
	MaxStringLength    int `json:"maxStringLength"`
	MaxObjectKeyLength int `json:"maxObjectKeyLength"`
}
