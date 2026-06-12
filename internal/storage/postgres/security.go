package postgres

import (
	"fmt"
	"regexp"
)

// security patterns for validating SQL identifiers
var (
	// validIdentifierPattern matches valid SQL identifiers (table names, column names, etc.)
	// Allows: letters, numbers, underscores
	// Rejects: spaces, special characters, SQL keywords, semicolons, comments
	validIdentifierPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
)

// validateSQLIdentifier validates that an identifier is safe for use in SQL queries.
// This prevents SQL injection attacks through malicious table/column names.
func validateSQLIdentifier(identifier string) error {
	if identifier == "" {
		return &SecurityError{
			Type:    SecurityErrorInvalidIdentifier,
			Message: "identifier cannot be empty",
		}
	}

	if len(identifier) > 63 { // PostgreSQL identifier limit
		return &SecurityError{
			Type:    SecurityErrorInvalidIdentifier,
			Message: fmt.Sprintf("identifier too long: %d characters (max 63)", len(identifier)),
		}
	}

	// Check against pattern
	if !validIdentifierPattern.MatchString(identifier) {
		return &SecurityError{
			Type:    SecurityErrorInvalidIdentifier,
			Message: fmt.Sprintf("invalid identifier format: %s", identifier),
		}
	}

	return nil
}

// sanitizeSQLTable sanitizes a table name for safe use in SQL queries.
// Returns an error if the table name is invalid.
func sanitizeSQLTable(table string) error {
	return validateSQLIdentifier(table)
}

// validateSQLIdentifiers validates multiple identifiers at once.
func validateSQLIdentifiers(identifiers ...string) error {
	for _, id := range identifiers {
		if err := validateSQLIdentifier(id); err != nil {
			return err
		}
	}
	return nil
}

// SecurityError represents a security-related error.
type SecurityError struct {
	Type    SecurityErrorType
	Message string
}

// SecurityErrorType represents different types of security errors.
type SecurityErrorType string

const (
	SecurityErrorInvalidIdentifier SecurityErrorType = "invalid_identifier"
	SecurityErrorInjectionAttempt  SecurityErrorType = "injection_attempt"
	SecurityErrorInvalidInput      SecurityErrorType = "invalid_input"
)

func (e *SecurityError) Error() string {
	return fmt.Sprintf("security error [%s]: %s", e.Type, e.Message)
}

// validateUserInput validates user input for security purposes.
// This can be extended to include more sophisticated validation.
func validateUserInput(input string, maxLength int) error {
	if input == "" {
		return &SecurityError{
			Type:    SecurityErrorInvalidInput,
			Message: "input cannot be empty",
		}
	}

	if len(input) > maxLength {
		return &SecurityError{
			Type:    SecurityErrorInvalidInput,
			Message: fmt.Sprintf("input too long: %d characters (max %d)", len(input), maxLength),
		}
	}

	// Check for potential SQL injection patterns
	if containsSQLInjectionPatterns(input) {
		return &SecurityError{
			Type:    SecurityErrorInjectionAttempt,
			Message: "input contains potentially dangerous patterns",
		}
	}

	return nil
}

// containsSQLInjectionPatterns checks for common SQL injection patterns.
func containsSQLInjectionPatterns(input string) bool {
	dangerousPatterns := []string{
		" OR ",
		"--",
		";",
		"/*",
		"*/",
		"DROP ",
		"EXEC ",
		"UNION ",
		"1=1",
		"1=2",
	}

	inputUpper := toUpperASCII(input)
	for _, pattern := range dangerousPatterns {
		if contains(inputUpper, toUpperASCII(pattern)) {
			return true
		}
	}

	return false
}

// toUpperASCII converts a string to uppercase (ASCII only).
func toUpperASCII(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			c = c - ('a' - 'A')
		}
		result[i] = c
	}
	return string(result)
}

// contains checks if a string contains another string (case-insensitive).
func contains(s, substr string) bool {
	return indexOf(s, substr) >= 0
}

// indexOf finds the index of substr in s (case-insensitive).
func indexOf(s, substr string) int {
	if len(substr) == 0 {
		return 0
	}
	if len(s) < len(substr) {
		return -1
	}

	s = toUpperASCII(s)
	substr = toUpperASCII(substr)

	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// safeFormatTable safely formats a table name into SQL.
// Returns the validated table name or an error if the name is invalid.
func safeFormatTable(table string) (string, error) {
	if err := sanitizeSQLTable(table); err != nil {
		return "", err
	}
	return table, nil
}
