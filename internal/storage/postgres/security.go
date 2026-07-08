package postgres

import (
	"fmt"
	"regexp"
	"strings"
)

// security patterns for validating SQL identifiers
var (
	// validIdentifierPattern matches valid SQL identifiers (table names, column
	// names, IDs, etc.). Allows: letters, digits, underscores, and hyphens.
	// Identifiers must start with a letter or underscore (per PostgreSQL unquoted
	// identifier rules). Hyphens are permitted because UUIDs and many tenant/IDs
	// use them; the output is always quoted via quoteIdentifier before
	// interpolation, so a hyphen cannot break out of the identifier context.
	// Schema-qualified names (containing '.') are still rejected. The identifier
	// must not contain consecutive hyphens (`--`), which is a SQL comment marker.
	validIdentifierPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_-]*$`)

	// uuidPattern matches standard UUID format (8-4-4-4-12 hex digits).
	// These may start with a digit and are allowed as identifiers (tenant IDs,
	// entity keys, etc.) because they are always quoted in SQL.
	uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
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

	// Reject schema-qualified names (e.g. "public.users") to prevent bypassing
	// the table allowlist. Quoting handles embedded double quotes, but a dot
	// would let an attacker reference arbitrary schemas. Also reject `--`
	// (SQL comment marker) to prevent comment injection.
	if strings.ContainsAny(identifier, ".;'\"`(){}\\") || strings.Contains(identifier, "--") {
		return &SecurityError{
			Type:    SecurityErrorInvalidIdentifier,
			Message: fmt.Sprintf("identifier contains forbidden character: %s", identifier),
		}
	}

	// UUIDs are allowed even though they may start with a digit.
	if uuidPattern.MatchString(identifier) {
		return nil
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
// This is a defense-in-depth check; primary protection comes from using
// parameterized queries and quoteIdentifier for identifiers. The check uses
// case-insensitive substring matching against a curated pattern list.
func containsSQLInjectionPatterns(input string) bool {
	dangerousPatterns := []string{
		"--",
		";",
		"/*",
		"*/",
		"drop ",
		"exec ",
		"union ",
		" or ",
		"1=1",
		"1=2",
	}

	inputUpper := strings.ToUpper(input)
	for _, pattern := range dangerousPatterns {
		if strings.Contains(inputUpper, strings.ToUpper(pattern)) {
			return true
		}
	}

	return false
}

// safeFormatTable safely formats a table name into SQL.
// Returns the validated table name or an error if the name is invalid.
func safeFormatTable(table string) (string, error) {
	if err := sanitizeSQLTable(table); err != nil {
		return "", err
	}
	return table, nil
}
