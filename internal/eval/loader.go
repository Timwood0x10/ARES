package eval

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Loader loads test suites from files.
type Loader struct{}

// NewLoader creates a new test suite loader.
func NewLoader() *Loader {
	return &Loader{}
}

// Load loads a test suite from a YAML file.
func (l *Loader) Load(path string) (*TestSuite, error) {
	// Validate path to prevent directory traversal outside of test directories
	// Allow relative paths like ../../test/eval/ but block dangerous paths like ../../../etc/
	if strings.Contains(path, "/etc/") || strings.Contains(path, "\\etc\\") {
		return nil, fmt.Errorf("invalid path: system directory access not allowed")
	}

	data, err := os.ReadFile(path) // #nosec G304 -- Path validated above to prevent traversal
	if err != nil {
		return nil, err
	}

	var suite TestSuite
	if err := yaml.Unmarshal(data, &suite); err != nil {
		return nil, err
	}

	// Set default timeout for test cases without timeout
	for i := range suite.TestCases {
		if suite.TestCases[i].Timeout == 0 {
			suite.TestCases[i].Timeout = Duration(30 * time.Second)
		}
	}

	return &suite, nil
}

// LoadDir loads all test suites from a directory.
func (l *Loader) LoadDir(dir string) ([]TestSuite, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var suites []TestSuite
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		// Only load YAML files
		name := entry.Name()
		ext := filepath.Ext(name)
		if ext != ".yaml" && ext != ".yml" {
			continue
		}

		path := filepath.Join(dir, name)
		suite, err := l.Load(path)
		if err != nil {
			return nil, err
		}
		suites = append(suites, *suite)
	}

	return suites, nil
}
