package output

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"goagentx/internal/core/models"
	gerr "goagentx/internal/errors"
)

// Pre-compiled regular expressions for better performance.
var (
	markdownPattern = regexp.MustCompile("```(?:json)?\\s*([\\s\\S]*?)\\s*```")
	trailingComma   = regexp.MustCompile(`,\s*([\}\]])`)
)

// Parser parses LLM output into structured types.
type Parser struct {
	fixJSON        bool
	inputValidator *InputValidator
}

// NewParser creates a new Parser.
func NewParser() *Parser {
	return &Parser{
		fixJSON:        true,
		inputValidator: NewInputValidator(),
	}
}

// NewParserWithValidator creates a new Parser with custom input validation.
func NewParserWithValidator(validator *InputValidator) *Parser {
	return &Parser{
		fixJSON:        true,
		inputValidator: validator,
	}
}

// ParseRecommendResult parses LLM output into RecommendResult.
func (p *Parser) ParseRecommendResult(output string) (*models.RecommendResult, error) {
	// Validate input length before processing
	if err := p.inputValidator.ValidateInput(output); err != nil {
		return nil, gerr.Wrap(err, "input validation failed")
	}

	jsonStr := p.extractJSON(output)
	if jsonStr == "" {
		return nil, ErrInvalidJSON
	}

	// Validate JSON content length
	if err := p.inputValidator.ValidateJSONLength(jsonStr); err != nil {
		return nil, gerr.Wrap(err, "JSON validation failed")
	}

	// Try to detect if it's an array or object
	jsonStr = strings.TrimSpace(jsonStr)

	// If it's an array, wrap it in an object
	if strings.HasPrefix(jsonStr, "[") {
		return p.parseArrayFormat(jsonStr)
	}

	// Try parsing as object first
	var result models.RecommendResult
	if err := json.Unmarshal([]byte(jsonStr), &result); err == nil {
		return &result, nil
	}

	// If object parsing fails, try to fix JSON
	if p.fixJSON {
		fixed, fixErr := p.fixJSONString(jsonStr)
		if fixErr == nil {
			if err := json.Unmarshal([]byte(fixed), &result); err == nil {
				return &result, nil
			}
		}
	}

	// Try array format as fallback
	return p.parseArrayFormat(jsonStr)
}

// parseArrayFormat handles JSON array format.
func (p *Parser) parseArrayFormat(jsonStr string) (*models.RecommendResult, error) {
	// Ensure it's a valid array
	if !strings.HasPrefix(jsonStr, "[") {
		jsonStr = "[" + jsonStr + "]"
	}

	var items []*models.RecommendItem
	if err := json.Unmarshal([]byte(jsonStr), &items); err != nil {
		// Try to fix JSON
		if p.fixJSON {
			fixed, fixErr := p.fixJSONString(jsonStr)
			if fixErr == nil {
				if err := json.Unmarshal([]byte(fixed), &items); err == nil {
					return &models.RecommendResult{
						Items: items,
					}, nil
				}
			}
		}
		return nil, gerr.WrapError(ErrInvalidJSON, err)
	}

	return &models.RecommendResult{
		Items: items,
	}, nil
}

// extractJSON extracts JSON from output.
// It uses string-aware brace/bracket matching so that { } inside
// JSON string values do not corrupt depth tracking.
func (p *Parser) extractJSON(output string) string {
	output = strings.TrimSpace(output)

	matches := markdownPattern.FindStringSubmatch(output)
	if len(matches) > 1 {
		result := strings.TrimSpace(matches[1])
		if strings.HasPrefix(result, "{") || strings.HasPrefix(result, "[") {
			return result
		}
	}

	start := strings.Index(output, "{")
	if start == -1 {
		start = strings.Index(output, "[")
		if start == -1 {
			return ""
		}
		end := findMatchingBracket(output, start, '[', ']')
		if end == -1 {
			return ""
		}
		return output[start:end]
	}

	end := findMatchingBracket(output, start, '{', '}')
	if end == -1 {
		return ""
	}
	return output[start:end]
}

// findMatchingBracket scans from start position looking for the closing bracket,
// respecting JSON string boundaries and escape sequences.
func findMatchingBracket(s string, start int, open, close byte) int {
	depth := 0
	inString := false

	for i := start; i < len(s); i++ {
		c := s[i]

		if inString {
			if c == '\\' && i+1 < len(s) {
				i++
				continue
			}
			if c == '"' {
				inString = false
			}
			continue
		}

		if c == '"' {
			inString = true
			continue
		}

		switch c {
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}

// fixJSONString attempts to fix common JSON errors.
// It uses a state-machine scanner that respects JSON string boundaries,
// so regex-based fixes are never applied inside string values.
func (p *Parser) fixJSONString(jsonStr string) (string, error) {
	if json.Valid([]byte(jsonStr)) {
		return jsonStr, nil
	}

	fixed := trailingComma.ReplaceAllString(jsonStr, "$1")

	if json.Valid([]byte(fixed)) {
		return fixed, nil
	}

	fixed = scanFixJSON(fixed)

	if !json.Valid([]byte(fixed)) {
		return "", errors.New("failed to fix JSON")
	}
	return fixed, nil
}

// scanFixJSON applies comment removal, unquoted-key fixing, and single-quote
// fixing using a character-level state machine that tracks string boundaries.
func scanFixJSON(s string) string {
	var buf strings.Builder
	buf.Grow(len(s))

	inString := false
	i := 0
	for i < len(s) {
		c := s[i]

		if inString {
			buf.WriteByte(c)
			if c == '\\' && i+1 < len(s) {
				i++
				buf.WriteByte(s[i])
			} else if c == '"' {
				inString = false
			}
			i++
			continue
		}

		if c == '"' {
			inString = true
			buf.WriteByte(c)
			i++
			continue
		}

		if c == '/' && i+1 < len(s) {
			next := s[i+1]
			if next == '/' {
				for i < len(s) && s[i] != '\n' {
					i++
				}
				continue
			}
			if next == '*' {
				endIdx := strings.Index(s[i:], "*/")
				if endIdx == -1 {
					i = len(s)
				} else {
					i += endIdx + 2
				}
				continue
			}
		}

		if (c == '{' || c == ',') && i+1 < len(s) && isAlpha(s[i+1]) {
			j := i + 1
			for j < len(s) && isAlphaNum(s[j]) {
				j++
			}
			if j < len(s) && s[j] == ':' {
				buf.WriteByte(c)
				buf.WriteByte('"')
				buf.WriteString(s[i+1 : j])
				buf.WriteString("\":")
				i = j + 1
				continue
			}
		}

		if c == '\'' {
			endSingle := strings.IndexByte(s[i+1:], '\'')
			if endSingle != -1 {
				buf.WriteByte('"')
				writeEscaped(&buf, s[i+1:i+1+endSingle])
				buf.WriteByte('"')
				i = i + 2 + endSingle
				continue
			}
		}

		buf.WriteByte(c)
		i++
	}
	return buf.String()
}

func isAlpha(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func isAlphaNum(c byte) bool {
	return isAlpha(c) || (c >= '0' && c <= '9')
}

func writeEscaped(buf *strings.Builder, s string) {
	for i := 0; i < len(s); i++ {
		if s[i] == '"' || s[i] == '\\' {
			buf.WriteByte('\\')
		}
		buf.WriteByte(s[i])
	}
}

// ParseGeneric parses generic JSON output.
func (p *Parser) ParseGeneric(output string, target interface{}) error {
	jsonStr := p.extractJSON(output)
	if jsonStr == "" {
		return ErrInvalidJSON
	}

	if err := json.Unmarshal([]byte(jsonStr), target); err != nil {
		if !p.fixJSON {
			return gerr.WrapError(ErrInvalidJSON, err)
		}

		fixed, fixErr := p.fixJSONString(jsonStr)
		if fixErr != nil {
			return fmt.Errorf("%w: tried fix: %v", ErrInvalidJSON, fixErr)
		}

		if err := json.Unmarshal([]byte(fixed), target); err != nil {
			return gerr.WrapError(ErrInvalidJSON, err)
		}
	}

	return nil
}

// ParseJSON parses LLM output into a generic map.
func (p *Parser) ParseJSON(output string) (map[string]interface{}, error) {
	// Validate input length before processing
	if err := p.inputValidator.ValidateInput(output); err != nil {
		return nil, gerr.Wrap(err, "input validation failed")
	}

	jsonStr := p.extractJSON(output)
	if jsonStr == "" {
		return nil, ErrInvalidJSON
	}

	// Validate JSON content length
	if err := p.inputValidator.ValidateJSONLength(jsonStr); err != nil {
		return nil, gerr.Wrap(err, "JSON validation failed")
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		if !p.fixJSON {
			return nil, gerr.WrapError(ErrInvalidJSON, err)
		}

		fixed, fixErr := p.fixJSONString(jsonStr)
		if fixErr != nil {
			return nil, fmt.Errorf("%w: tried fix: %v", ErrInvalidJSON, fixErr)
		}

		if err := json.Unmarshal([]byte(fixed), &result); err != nil {
			return nil, gerr.WrapError(ErrInvalidJSON, err)
		}
	}

	return result, nil
}

// ParseArray parses JSON array output.
func (p *Parser) ParseArray(output string) ([]interface{}, error) {
	// Validate input length before processing
	if err := p.inputValidator.ValidateInput(output); err != nil {
		return nil, gerr.Wrap(err, "input validation failed")
	}

	jsonStr := p.extractJSON(output)
	if jsonStr == "" {
		return nil, ErrInvalidJSON
	}

	// Validate JSON content length
	if err := p.inputValidator.ValidateJSONLength(jsonStr); err != nil {
		return nil, gerr.Wrap(err, "JSON validation failed")
	}

	// Check if it's an array
	jsonStr = strings.TrimSpace(jsonStr)
	if !strings.HasPrefix(jsonStr, "[") {
		return nil, ErrInvalidJSON
	}

	var result []interface{}
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, gerr.WrapError(ErrInvalidJSON, err)
	}

	// Validate array length
	if len(result) > p.inputValidator.GetMaxArrayLength() {
		return nil, gerr.Wrapf(ErrArrayTooLarge, "%d elements (max %d)", len(result), p.inputValidator.GetMaxArrayLength())
	}

	return result, nil
}

// ParseJSONSlice extracts a JSON array from LLM output and returns it as a slice.
// It reuses extractJSON and fixJSONString for robust extraction.
func (p *Parser) ParseJSONSlice(output string) ([]interface{}, error) {
	if err := p.inputValidator.ValidateInput(output); err != nil {
		return nil, gerr.Wrap(err, "input validation failed")
	}

	// extractJSON prefers { over [, so for array extraction we try the
	// markdown path first and then look for [ directly before falling
	// back to the generic extractJSON.
	jsonStr := p.extractJSONArray(output)
	if jsonStr == "" {
		return nil, ErrInvalidJSON
	}

	if err := p.inputValidator.ValidateJSONLength(jsonStr); err != nil {
		return nil, gerr.Wrap(err, "JSON validation failed")
	}

	jsonStr = strings.TrimSpace(jsonStr)
	if !strings.HasPrefix(jsonStr, "[") {
		return nil, ErrInvalidJSON
	}

	var result []interface{}
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		if !p.fixJSON {
			return nil, gerr.WrapError(ErrInvalidJSON, err)
		}

		fixed, fixErr := p.fixJSONString(jsonStr)
		if fixErr != nil {
			return nil, fmt.Errorf("%w: tried fix: %v", ErrInvalidJSON, fixErr)
		}

		if err := json.Unmarshal([]byte(fixed), &result); err != nil {
			return nil, gerr.WrapError(ErrInvalidJSON, err)
		}
	}

	if len(result) > p.inputValidator.GetMaxArrayLength() {
		return nil, gerr.Wrapf(ErrArrayTooLarge, "%d elements (max %d)", len(result), p.inputValidator.GetMaxArrayLength())
	}

	return result, nil
}

// extractJSONArray extracts a JSON array from output, preferring [ over {.
func (p *Parser) extractJSONArray(output string) string {
	output = strings.TrimSpace(output)

	// Check markdown code blocks first.
	matches := markdownPattern.FindStringSubmatch(output)
	if len(matches) > 1 {
		result := strings.TrimSpace(matches[1])
		if strings.HasPrefix(result, "[") {
			return result
		}
	}

	// Look for array bracket directly.
	start := strings.Index(output, "[")
	if start != -1 {
		end := findMatchingBracket(output, start, '[', ']')
		if end != -1 {
			return output[start:end]
		}
	}

	// Fall back to generic extraction (may return an object).
	return p.extractJSON(output)
}

// ParseStructured extracts JSON from LLM output and unmarshals into target.
// It reuses extractJSON and fixJSONString for robust extraction.
func (p *Parser) ParseStructured(output string, target interface{}) error {
	if target == nil {
		return fmt.Errorf("target must not be nil")
	}

	return p.ParseGeneric(output, target)
}

// ParseKeyValue extracts key-value pairs from text like "Key: Value" or "Key = Value".
// It handles both colon and equals delimiters, trims whitespace, and skips empty lines.
func (p *Parser) ParseKeyValue(output string) map[string]string {
	result := make(map[string]string)
	if output == "" {
		return result
	}

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		key, value, ok := parseKeyValueLine(line)
		if ok {
			result[key] = value
		}
	}

	return result
}

// parseKeyValueLine extracts a single key-value pair from a line.
// It supports "Key: Value" and "Key = Value" formats.
func parseKeyValueLine(line string) (key, value string, ok bool) {
	// Try colon delimiter first.
	if idx := strings.Index(line, ":"); idx > 0 {
		key = strings.TrimSpace(line[:idx])
		value = strings.TrimSpace(line[idx+1:])
		if key != "" {
			return key, value, true
		}
	}

	// Try equals delimiter.
	if idx := strings.Index(line, "="); idx > 0 {
		key = strings.TrimSpace(line[:idx])
		value = strings.TrimSpace(line[idx+1:])
		if key != "" {
			return key, value, true
		}
	}

	return "", "", false
}

// Parser errors.
var (
	ErrInvalidJSON = errors.New("invalid JSON")
)
