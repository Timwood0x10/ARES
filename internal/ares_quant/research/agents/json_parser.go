package agents

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/Timwood0x10/ares/internal/truncate"
)

// JSONParser provides generic JSON extraction and deserialization from LLM output.
// It handles various LLM output formats: pure JSON, markdown code blocks, and mixed text.
type JSONParser[T any] struct{}

// NewJSONParser creates a new JSONParser for the target type T.
//
// Type parameter:
//   - T: the target Go type for JSON deserialization.
//
// Returns:
//   - pointer to the initialized parser.
func NewJSONParser[T any]() *JSONParser[T] {
	return &JSONParser[T]{}
}

// Parse extracts JSON from raw LLM output and deserializes it to type T.
//
// Extraction strategy (tried in order):
//  1. Direct json.Unmarshal of the entire raw string.
//  2. Extract content from ```json ... ``` markdown code block.
//  3. Find the first { ... } JSON object in the text.
//  4. Return ParseError with original output for debugging.
//
// Args:
//   - raw: the raw text output from the LLM.
//
// Returns:
//   - pointer to the parsed T value on success.
//   - ParseError containing the original output if all strategies fail.
func (p *JSONParser[T]) Parse(raw string) (*T, error) {
	var result T

	// Strategy 1: Try direct unmarshal.
	if err := tryUnmarshal(raw, &result); err == nil {
		return &result, nil
	}

	// Strategy 2: Extract from ```json ... ``` code block.
	if extracted := extractCodeBlock(raw); extracted != "" {
		if err := tryUnmarshal(extracted, &result); err == nil {
			return &result, nil
		}
	}

	// Strategy 3: Find first { ... } block.
	if extracted := extractJSONObject(raw); extracted != "" {
		if err := tryUnmarshal(extracted, &result); err == nil {
			return &result, nil
		}
	}

	// All strategies failed.
	return nil, &ParseError{Raw: raw, TargetType: genericTypeName[T]()}
}

// ParseError is returned when JSON extraction fails after all strategies.
// It preserves the original LLM output for debugging purposes.
type ParseError struct {
	Raw        string
	TargetType string
}

// Error implements the error interface.
func (e *ParseError) Error() string {
	return fmt.Sprintf("json parse failed for type %s: could not extract valid JSON from LLM output (first 200 chars: %s...)",
		e.TargetType, truncate.Plain(e.Raw, 200))
}

// Unwrap allows errors.Is/As to work with ParseError.
func (e *ParseError) Unwrap() error {
	return ErrParseFailed
}

// RawOutput returns the original LLM output for debugging.
func (e *ParseError) RawOutput() string {
	return e.Raw
}

// ─── Internal extraction strategies ────────────────────

var jsonCodeBlockRe = regexp.MustCompile("(?s)(?:```json\\s*\\n?)(.*?)(?:\\n?\\s*```)")

var jsonObjectRe = regexp.MustCompile(`(?s)\{(?:[^{}]|\{[^{}]*\})*\}`)

func tryUnmarshal(data string, target interface{}) error {
	data = strings.TrimSpace(data)
	if data == "" {
		return errors.New("empty input")
	}
	err := json.Unmarshal([]byte(data), target)
	if err != nil {
		return err
	}
	return nil
}

func extractCodeBlock(raw string) string {
	matches := jsonCodeBlockRe.FindStringSubmatch(raw)
	if len(matches) < 2 {
		return ""
	}
	return strings.TrimSpace(matches[1])
}

func extractJSONObject(raw string) string {
	match := jsonObjectRe.FindString(raw)
	if match == "" {
		return ""
	}
	return match
}



// genericTypeName returns a readable identifier for the generic type T without using reflect.
func genericTypeName[T any]() string {
	var t T
	switch any(t).(type) {
	case map[string]interface{}:
		return "map[string]interface{}"
	case string:
		return "string"
	case int, int8, int16, int32, int64:
		return "int"
	case float32, float64:
		return "float64"
	case bool:
		return "bool"
	default:
		return "struct"
	}
}
