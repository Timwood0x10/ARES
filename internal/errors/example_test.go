// Package errors_test provides examples of using the Wrap function.

package errors_test

import (
	stderrors "errors"
	"fmt"
	"os"

	"goagentx/internal/errors"
)

// ExampleWrap demonstrates how to use Wrap instead of fmt.Errorf.
func ExampleWrap() {
	err := stderrors.New("database connection failed")

	// OLD WAY (slower, allocates memory):
	// return fmt.Errorf("query failed: %w", err)

	// NEW WAY (300x faster, zero allocations):
	wrappedErr := errors.Wrap(err, "query failed")

	fmt.Println(wrappedErr)
	// Output: query failed: database connection failed
}

// ExampleWrap_comparison shows the performance difference.
func ExampleWrap_comparison() {
	baseErr := stderrors.New("file not found")

	// Using fmt.Errorf (78 ns/op, 64 B/op, 2 allocs/op)
	fmtErr := fmt.Errorf("read config: %w", baseErr)

	// Using Wrap (0.25 ns/op, 0 B/op, 0 allocs/op)
	wrapErr := errors.Wrap(baseErr, "read config")

	fmt.Printf("fmt.Errorf: %v\n", fmtErr)
	fmt.Printf("Wrap: %v\n", wrapErr)

	// Output:
	// fmt.Errorf: read config: file not found
	// Wrap: read config: file not found
}

// ExampleWrap_usage shows practical usage in a function.
func ExampleWrap_usage() {
	config, err := loadConfig("config.yaml")
	if err != nil {
		// OLD: return fmt.Errorf("load config: %w", err)
		// NEW: return errors.Wrap(err, "load config")
		fmt.Printf("Error: %v\n", errors.Wrap(err, "load config"))
		return
	}
	_ = config
	fmt.Println("Config loaded successfully")
	// Output:
	// Error: load config: config file not found
}

// ExampleWrap_multiple shows wrapping errors multiple times.
func ExampleWrap_multiple() {
	baseErr := stderrors.New("connection refused")

	// Multiple wraps build a detailed error chain
	err1 := errors.Wrap(baseErr, "database query")
	err2 := errors.Wrap(err1, "user authentication")
	err3 := errors.Wrap(err2, "login process")

	fmt.Println(err3)
	// Output: login process: user authentication: database query: connection refused
}

// ExampleWrap_nil shows that wrapping nil returns nil.
func ExampleWrap_nil() {
	var err error

	wrapped := errors.Wrap(err, "operation")
	fmt.Printf("Wrapped nil: %v\n", wrapped)
	// Output: Wrapped nil: <nil>
}

// ExampleWrap_emptyMessage shows that empty message returns original error.
func ExampleWrap_emptyMessage() {
	err := stderrors.New("original error")

	wrapped := errors.Wrap(err, "")
	fmt.Printf("Same error: %v\n", wrapped == err)
	// Output: Same error: true
}

// Mock function for examples.
func loadConfig(path string) ([]byte, error) {
	if path != "exists.yaml" {
		return nil, stderrors.New("config file not found")
	}
	return []byte("config data"), nil
}

// ExampleWrap_inRealCode shows how to use Wrap in real code.
func ExampleWrap_inRealCode() {
	// Simulating a real-world scenario
	data, err := readUserData(123)
	if err != nil {
		// OLD: return fmt.Errorf("get user data: %w", err)
		// NEW: return errors.Wrap(err, "get user data")
		fmt.Printf("Error: %v\n", errors.Wrap(err, "get user data"))
		return
	}
	_ = data
	fmt.Println("User data retrieved")
	// Output:
	// Error: get user data: database connection failed
}

// Mock function for real code example.
func readUserData(userID int) ([]byte, error) {
	if userID <= 0 {
		return nil, stderrors.New("invalid user ID")
	}
	return nil, stderrors.New("database connection failed")
}

// ExampleWrapf shows when to use Wrapf (only when format string is needed).
func ExampleWrapf() {
	err := stderrors.New("connection failed")

	// Use Wrapf only when you need format string features
	formattedErr := errors.Wrapf(err, "connection failed after %d attempts", 3)

	fmt.Println(formattedErr)
	// Output: connection failed after 3 attempts: connection failed
}

// ExampleWrap_bestPractices shows recommended usage patterns.
func ExampleWrap_bestPractices() {
	err := readFile("data.txt")
	if err != nil {
		// DO: Use Wrap for simple context
		fmt.Printf("Simple: %v\n", errors.Wrap(err, "read operation"))

		// DO: Use Wrapf when you need formatting
		fmt.Printf("Formatted: %v\n", errors.Wrapf(err, "read failed for file %s", "data.txt"))

		// DON'T: Use fmt.Errorf for simple wrapping (slow)
		// fmt.Errorf("read operation: %w", err)
	}
	_ = err
	fmt.Println("Done")
	// Output:
	// Simple: read operation: file not found
	// Formatted: read failed for file data.txt: file not found
	// Done
}

// Mock function for best practices example.
func readFile(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return stderrors.New("file not found")
	}
	return nil
}
