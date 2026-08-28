// nolint: errcheck // Test code may ignore return values
package ares_security

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSanitize(t *testing.T) {
	sanitizer := NewSanitizer()

	result := sanitizer.Sanitize("api_key=sk-1234567890abcdef")
	if result == "api_key=sk-1234567890abcdef" {
		t.Error("Expected API key to be sanitized")
	}

	if strings.Contains(result, "1234567890") {
		t.Error("Expected partial key to be masked")
	}
}

func TestSanitizeMultipleSensitiveFields(t *testing.T) {
	sanitizer := NewSanitizer()

	input := "api_key=sk-1234567890abcdef&password=secret123&token=abc123xyz"
	result := sanitizer.Sanitize(input)

	if strings.Contains(result, "1234567890") {
		t.Error("Expected API key to be masked")
	}
	if strings.Contains(result, "secret123") {
		t.Error("Expected password to be masked")
	}
	if strings.Contains(result, "abc123xyz") {
		t.Error("Expected token to be masked")
	}
}

func TestSanitizeEmail(t *testing.T) {
	sanitizer := NewSanitizer()

	input := "email=user@example.com"
	result := sanitizer.Sanitize(input)

	if strings.Contains(result, "user@example.com") {
		t.Error("Expected email to be masked")
	}
}

func TestSanitizePhone(t *testing.T) {
	sanitizer := NewSanitizer()

	input := "phone=+1-555-123-4567"
	result := sanitizer.Sanitize(input)

	if strings.Contains(result, "555-123-4567") {
		t.Error("Expected phone number to be masked")
	}
}

func TestSanitizeSSN(t *testing.T) {
	sanitizer := NewSanitizer()

	input := "ssn=123-45-6789"
	result := sanitizer.Sanitize(input)

	if strings.Contains(result, "123-45-6789") {
		t.Error("Expected SSN to be masked")
	}
}

func TestSanitizeCreditCard(t *testing.T) {
	sanitizer := NewSanitizer()

	input := "card=4111111111111111"
	result := sanitizer.Sanitize(input)

	if strings.Contains(result, "4111111111111111") {
		t.Error("Expected credit card number to be masked")
	}
}

func TestSanitizeWithKeepLength(t *testing.T) {
	sanitizer := &Sanitizer{
		patterns: defaultSensitivePatterns(),
		options: SanitizeOptions{
			KeepLength: true,
			MaskChar:   '*',
		},
	}

	input := "api_key=sk-1234567890abcdef"
	result := sanitizer.Sanitize(input)

	if strings.Contains(result, "1234567890") {
		t.Error("Expected API key to be masked")
	}

	// Check that length is preserved
	inputLength := len(input)
	resultLength := len(result)
	if resultLength != inputLength {
		t.Errorf("Expected length to be preserved, got %d vs %d", resultLength, inputLength)
	}
}

func TestSanitizeEmptyInput(t *testing.T) {
	sanitizer := NewSanitizer()

	result := sanitizer.Sanitize("")
	if result != "" {
		t.Error("Expected empty string to remain empty")
	}
}

func TestSanitizeNoSensitiveData(t *testing.T) {
	sanitizer := NewSanitizer()

	input := "username=johndoe&age=30"
	result := sanitizer.Sanitize(input)

	if result != input {
		t.Errorf("Expected unchanged output for non-sensitive data, got %s", result)
	}
}

func TestSanitizeWithOptions(t *testing.T) {
	sanitizer := &Sanitizer{
		patterns: defaultSensitivePatterns(),
		options: SanitizeOptions{
			KeepLength: true,
			MaskChar:   '#',
			PreserveLengthFor: map[SensitiveFieldType]int{
				SensitiveFieldTypeAPIKey: 4,
			},
		},
	}

	input := "api_key=sk-1234567890abcdef"
	result := sanitizer.Sanitize(input)

	if strings.Contains(result, "1234567890") {
		t.Error("Expected API key to be masked")
	}
	if !strings.Contains(result, "sk-") {
		t.Error("Expected prefix to be preserved")
	}
}

func TestMaskString(t *testing.T) {
	tests := []struct {
		input    string
		preserve int
		want     string
	}{
		{"", 3, ""},
		{"a", 3, "*"},
		{"ab", 3, "**"},
		{"abc", 3, "***"},
		{"abcd", 3, "abc*"},
		{"hello", 3, "hel**"},
		{"abcdef", 3, "abc***"},
		{"abcdefg", 3, "abc*efg"},   // length=7 > 6 = 3*2 → third branch
		{"abcdefgh", 3, "abc**fgh"}, // third branch
		{"test", 2, "te**"},
		{"testing", 2, "te***ng"},
		{"testingx", 2, "te****gx"},     // third branch
		{"1234567890", 4, "1234**7890"}, // third branch
		{"12345678", 4, "1234****"},     // length=8 = 4*2 → second branch
		{"1234567", 4, "1234***"},       // length=7 < 8 → second branch
	}
	for _, tt := range tests {
		got := maskString(tt.input, tt.preserve)
		assert.Equal(t, tt.want, got, "maskString(%q, %d)", tt.input, tt.preserve)
	}
}

func TestMaskSSN(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"123-45-6789", "***-**-****"},
		{"123.45.6789", "***.**.****"},
		{"123456789", "***-**-****"},
		{"abc", "***-**-****"},
		{"", "***-**-****"},
	}
	for _, tt := range tests {
		got := maskSSN(tt.input)
		assert.Equal(t, tt.want, got, "maskSSN(%q)", tt.input)
	}
}

func TestMaskEmail(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"user@example.com", "us**@ex*******om"},
		{"a@e.co", "*@e.**"},
		{"ab@x.y", "**@x.*"},
		{"noatsign", "***@***.***"},
	}
	for _, tt := range tests {
		got := maskEmail(tt.input)
		assert.Equal(t, tt.want, got, "maskEmail(%q)", tt.input)
	}
}

func TestMaskPhone(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"1234567890", "123***7890"},   // cleaned=10, maskLen=3
		{"1234567", "123*567"},         // cleaned=7, maskLen=0 → maskString
		{"12", "**"},                   // masked via maskString
		{"555-123-4567", "555***4567"}, // cleaned=5551234567 → "555***4567"
	}
	for _, tt := range tests {
		got := maskPhone(tt.input)
		assert.Equal(t, tt.want, got, "maskPhone(%q)", tt.input)
	}
}

func TestMaskCreditCard(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"4111111111111111", "4111********1111"}, // 16 digits → maskLen=8
		{"12345678", "1234****"},                 // 8 digits, maskLen=0 → maskString
		{"123456789012", "1234****9012"},         // 12 digits → maskLen=4
		{"123", "***"},                           // too short → maskString
	}
	for _, tt := range tests {
		got := maskCreditCard(tt.input)
		assert.Equal(t, tt.want, got, "maskCreditCard(%q)", tt.input)
	}
}

func TestMaskAPIKey(t *testing.T) {
	tests := []struct {
		input string
		check func(string) bool
	}{
		{"api_key=sk-1234567890abcdef", func(got string) bool {
			return !strings.Contains(got, "1234567890")
		}},
		{"key=abcdefghijklmnopqrstuvwxyz", func(got string) bool {
			return !strings.Contains(got, "abcdefghijklmnopqrstuvwxyz")
		}},
		{"nothing here", func(got string) bool { return true }},
	}
	for _, tt := range tests {
		got := maskAPIKey(tt.input)
		if !tt.check(got) {
			t.Errorf("maskAPIKey(%q) = %q, check failed", tt.input, got)
		}
	}
}

func TestMaskToken(t *testing.T) {
	tests := []struct {
		input string
		check func(string) bool
	}{
		{"token=abc123xyz", func(got string) bool {
			return !strings.Contains(got, "abc123xyz")
		}},
		{"bearer: xyz789token", func(got string) bool {
			return !strings.Contains(got, "xyz789token")
		}},
		{"no token", func(got string) bool { return true }},
	}
	for _, tt := range tests {
		got := maskToken(tt.input)
		if !tt.check(got) {
			t.Errorf("maskToken(%q) = %q, check failed", tt.input, got)
		}
	}
}

// TestSanitizeOptionsAreEffective locks the REVIEW #37 contract: MaskChar,
// PreserveLengthFor and KeepLength must all alter the masked output instead
// of being silently ignored.
func TestSanitizeOptionsAreEffective(t *testing.T) {
	t.Run("mask_char_replaces_star", func(t *testing.T) {
		s := &Sanitizer{
			patterns: defaultSensitivePatterns(),
			options:  SanitizeOptions{MaskChar: '#'},
		}
		got := s.Sanitize("password=hunter2secret")
		if strings.Contains(got, "*") {
			t.Errorf("expected no '*' with MaskChar='#', got %q", got)
		}
		if !strings.Contains(got, "#") {
			t.Errorf("expected '#' in masked output, got %q", got)
		}
	})

	t.Run("preserve_length_for_overrides_prefix", func(t *testing.T) {
		s := &Sanitizer{
			patterns: defaultSensitivePatterns(),
			options: SanitizeOptions{
				PreserveLengthFor: map[SensitiveFieldType]int{
					SensitiveFieldTypeEmail: 1,
				},
			},
		}
		got := s.Sanitize("contact alice@example.com now")
		if !strings.Contains(got, "a") {
			t.Errorf("expected first char preserved, got %q", got)
		}
		if strings.Contains(got, "alice@example.com") {
			t.Errorf("expected email masked, got %q", got)
		}
	})

	t.Run("keep_length_pads_masked_output", func(t *testing.T) {
		s := &Sanitizer{
			patterns: defaultSensitivePatterns(),
			options:  SanitizeOptions{KeepLength: true},
		}
		in := "token=abcdefghij"
		got := s.Sanitize(in)
		inLen := len([]rune(in))
		// The matched region is "token=abcdefghij"; the whole string is the
		// match, so total length must be preserved.
		if len([]rune(got)) != inLen {
			t.Errorf("KeepLength: expected length %d, got %d (%q)", inLen, len([]rune(got)), got)
		}
	})
}

// TestSanitizeJSONNumericSecrets locks the REVIEW #38 contract: JSON numbers
// whose digit form matches a sensitive pattern are degraded to their masked
// string form, while benign numbers keep their numeric type (they are NOT
// quoted stringified copies).
func TestSanitizeJSONNumericSecrets(t *testing.T) {
	s := NewSanitizer()

	in := `{"card": 4111111111111111, "count": 42, "phone": "13800138000"}`
	out := s.SanitizeJSON(in)

	if strings.Contains(out, "4111111111111111") {
		t.Errorf("numeric card number not masked: %s", out)
	}
	if strings.Contains(out, `"42"`) {
		t.Errorf("benign number must keep its numeric type, got quoted copy: %s", out)
	}
	if !strings.Contains(out, `"count":42`) {
		t.Errorf("benign number must pass through as a JSON number: %s", out)
	}
}
