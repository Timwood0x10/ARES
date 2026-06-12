// nolint: errcheck // Test code may ignore return values
package postgres

import (
	"strings"
	"testing"
)

func TestValidateSQLIdentifier(t *testing.T) {
	tests := []struct {
		name       string
		identifier string
		expectErr  bool
	}{
		{"Valid simple", "users", false},
		{"Valid with underscore", "user_profiles", false},
		{"Valid with numbers", "table123", false},
		{"Empty string", "", true},
		{"Space in name", "user profiles", true},
		{"Special characters", "user-profiles", true},
		{"Semicolon injection", "users; DROP TABLE", true},
		{"Comment injection", "users--", true},
		{"Too long", strings.Repeat("a", 64), true},
		{"Starts with number", "123table", true},
		{"Contains spaces", "my table", true},
		{"Contains quotes", "my'table", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSQLIdentifier(tt.identifier)
			if (err != nil) != tt.expectErr {
				t.Errorf("Expected error=%v, got %v", tt.expectErr, err)
			}
		})
	}
}

func TestValidateSQLIdentifiers(t *testing.T) {
	tests := []struct {
		name        string
		identifiers []string
		expectErr   bool
	}{
		{"All valid", []string{"users", "profiles", "orders"}, false},
		{"One invalid", []string{"users", "invalid; DROP", "profiles"}, true},
		{"Empty list", []string{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSQLIdentifiers(tt.identifiers...)
			if (err != nil) != tt.expectErr {
				t.Errorf("Expected error=%v, got %v", tt.expectErr, err)
			}
		})
	}
}

func TestValidateUserInput(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		maxLength int
		expectErr bool
	}{
		{"Valid input", "normal input", 100, false},
		{"Empty input", "", 100, true},
		{"Too long", strings.Repeat("a", 101), 100, true},
		{"SQL injection OR", "test OR 1=1", 100, true},
		{"SQL injection AND", "test AND 1=1", 100, true},
		{"SQL injection semicolon", "test; DROP TABLE", 100, true},
		{"SQL injection comment", "test -- comment", 100, true},
		{"SQL injection UNION", "test UNION SELECT", 100, true},
		{"Valid with numbers", "test123", 100, false},
		{"Exactly max length", strings.Repeat("a", 100), 100, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateUserInput(tt.input, tt.maxLength)
			if (err != nil) != tt.expectErr {
				t.Errorf("Expected error=%v, got %v", tt.expectErr, err)
			}
		})
	}
}

func TestContainsSQLInjectionPatterns(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"Normal input", "normal text", false},
		{"OR injection", "test OR 1=1", true},
		{"Semicolon injection", "test; DROP", true},
		{"Comment injection", "test --", true},
		{"Block comment", "test /* comment */", true},
		{"DROP keyword", "DROP TABLE", true},
		{"EXEC keyword", "EXEC sp_help", true},
		{"UNION keyword", "UNION SELECT", true},
		{"Valid numbers", "1=1", true},
		{"Lowercase injection", "test or 1=1", true},
		{"Mixed case injection", "Test Or 1=1", true},
		{"Safe SELECT in text", "select your options carefully", false},
		{"Safe WHERE in text", "where to go next", false},
		{"Safe UPDATE in text", "update me on progress", false},
		{"Safe DELETE in text", "delete this later", false},
		{"Safe INSERT in text", "insert a quote here", false},
		{"Safe AND in text", "and then some", false},
		{"Safe input", "user123", false},
		{"Safe input with numbers", "table_2023", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := containsSQLInjectionPatterns(tt.input)
			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestSecurityError(t *testing.T) {
	tests := []struct {
		name      string
		errorType SecurityErrorType
		message   string
	}{
		{"Invalid identifier", SecurityErrorInvalidIdentifier, "invalid format"},
		{"Injection attempt", SecurityErrorInjectionAttempt, "malicious input"},
		{"Invalid input", SecurityErrorInvalidInput, "empty input"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &SecurityError{
				Type:    tt.errorType,
				Message: tt.message,
			}

			if err.Type != tt.errorType {
				t.Errorf("Expected type %v, got %v", tt.errorType, err.Type)
			}

			if err.Message != tt.message {
				t.Errorf("Expected message %s, got %s", tt.message, err.Message)
			}

			errStr := err.Error()
			if !strings.Contains(errStr, "security error") {
				t.Error("Error message should contain 'security error'")
			}
		})
	}
}

func TestSafeFormatTable(t *testing.T) {
	tests := []struct {
		name     string
		table    string
		expected string
	}{
		{"Valid table", "users", "users"},
		{"Valid with underscore", "user_profiles", "user_profiles"},
		{"Invalid - empty", "", ""},
		{"Invalid - injection", "users; DROP", ""},
		{"Invalid - special chars", "user-profiles", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := safeFormatTable(tt.table)
			if tt.expected == "" {
				// Invalid input should return error.
				if err == nil {
					t.Errorf("Expected error for invalid table %q, got nil", tt.table)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error for table %q: %v", tt.table, err)
				}
				if result != tt.expected {
					t.Errorf("Expected %s, got %s", tt.expected, result)
				}
			}
		})
	}
}

func TestToUpperASCII(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"Lowercase", "hello", "HELLO"},
		{"Mixed", "HeLLo", "HELLO"},
		{"Numbers", "123", "123"},
		{"Empty", "", ""},
		{"Special chars", "hello@world", "HELLO@WORLD"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := toUpperASCII(tt.input)
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestIndexOf(t *testing.T) {
	tests := []struct {
		name     string
		haystack string
		needle   string
		expected int
	}{
		{"Found", "hello world", "WORLD", 6},
		{"Not found", "hello", "WORLD", -1},
		{"Empty needle", "hello", "", 0},
		{"Empty haystack", "", "hello", -1},
		{"Case insensitive", "HeLLo", "hello", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := indexOf(tt.haystack, tt.needle)
			if result != tt.expected {
				t.Errorf("Expected %d, got %d", tt.expected, result)
			}
		})
	}
}

// nolint: errcheck // Test code may ignore return values
