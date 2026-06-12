// Package main provides tests for the DevAgent main functionality.
package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"goagentx/internal/llm/output"
	"goagentx/internal/workflow/engine"
)

// setOutputDir sets the output directory for testing.
func setOutputDir(dir string) func() {
	oldOutputDir := outputDir
	outputDir = dir
	return func() { outputDir = oldOutputDir }
}

// TestInitializeOutputDirectories tests output directory initialization.
func TestInitializeOutputDirectories(t *testing.T) {
	tempDir := t.TempDir()
	defer setOutputDir(tempDir)()

	if err := initializeOutputDirectories(); err != nil {
		t.Fatalf("initializeOutputDirectories failed: %v", err)
	}

	dirs := []string{
		tempDir,
		filepath.Join(tempDir, codeDir),
		filepath.Join(tempDir, testDir),
		filepath.Join(tempDir, docsDir),
	}

	for _, dir := range dirs {
		info, err := os.Stat(dir)
		if err != nil {
			t.Errorf("Directory %s not created: %v", dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%s is not a directory", dir)
		}
	}
}

// TestDetectOutputType tests output type detection.
func TestDetectOutputType(t *testing.T) {
	tests := []struct {
		name     string
		stepName string
		expected OutputType
	}{
		{
			name:     "code step",
			stepName: "Generate Code",
			expected: OutputTypeCode,
		},
		{
			name:     "test step",
			stepName: "Generate Tests",
			expected: OutputTypeTest,
		},
		{
			name:     "docs step",
			stepName: "Generate Documentation",
			expected: OutputTypeDocs,
		},
		{
			name:     "review step",
			stepName: "Code Review",
			expected: OutputTypeReview,
		},
		{
			name:     "unknown step",
			stepName: "Unknown Step",
			expected: OutputTypeDocs,
		},
		{
			name:     "mixed case code",
			stepName: "generate CODE",
			expected: OutputTypeCode,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detectOutputType(tt.stepName)
			if result != tt.expected {
				t.Errorf("detectOutputType(%q) = %v, want %v", tt.stepName, result, tt.expected)
			}
		})
	}
}

// TestSanitizeFilename tests filename sanitization.
func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple filename",
			input:    "myfile",
			expected: "myfile",
		},
		{
			name:     "with spaces",
			input:    "my file",
			expected: "my_file",
		},
		{
			name:     "with special characters",
			input:    "my-file@#$",
			expected: "my-file",
		},
		{
			name:     "with mixed case",
			input:    "MyFile",
			expected: "MyFile",
		},
		{
			name:     "with numbers",
			input:    "file123",
			expected: "file123",
		},
		{
			name:     "empty input",
			input:    "",
			expected: "",
		},
		{
			name:     "only special characters",
			input:    "@#$%",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeFilename(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestGetFileExtension tests file extension retrieval.
func TestGetFileExtension(t *testing.T) {
	tests := []struct {
		name       string
		outputType OutputType
		expected   string
	}{
		{
			name:       "code",
			outputType: OutputTypeCode,
			expected:   ".go",
		},
		{
			name:       "test",
			outputType: OutputTypeTest,
			expected:   ".go",
		},
		{
			name:       "docs",
			outputType: OutputTypeDocs,
			expected:   ".md",
		},
		{
			name:       "review",
			outputType: OutputTypeReview,
			expected:   "_review.md",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getFileExtension(tt.outputType)
			if result != tt.expected {
				t.Errorf("getFileExtension(%v) = %q, want %q", tt.outputType, result, tt.expected)
			}
		})
	}
}

// TestGetStepEmoji tests step emoji retrieval.
func TestGetStepEmoji(t *testing.T) {
	tests := []struct {
		name     string
		stepName string
		want     string
	}{
		{"extract", "Extract Requirements", "📋"},
		{"code", "Generate Code", "💻"},
		{"test", "Generate Tests", "🧪"},
		{"docs", "Generate Documentation", "📚"},
		{"review", "Code Review", "🔍"},
		{"unknown", "Unknown Step", "📦"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getStepEmoji(tt.stepName); got != tt.want {
				t.Errorf("getStepEmoji(%q) = %q, want %q", tt.stepName, got, tt.want)
			}
		})
	}
}

// TestCountCompletedSteps tests counting completed steps.
func TestCountCompletedSteps(t *testing.T) {
	tests := []struct {
		name  string
		steps []*engine.StepResult
		want  int
	}{
		{
			name:  "all completed",
			steps: []*engine.StepResult{{Status: "completed"}, {Status: "completed"}},
			want:  2,
		},
		{
			name:  "some completed",
			steps: []*engine.StepResult{{Status: "completed"}, {Status: "failed"}},
			want:  1,
		},
		{
			name:  "none completed",
			steps: []*engine.StepResult{{Status: "failed"}, {Status: "pending"}},
			want:  0,
		},
		{
			name:  "empty steps",
			steps: []*engine.StepResult{},
			want:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := countCompletedSteps(tt.steps); got != tt.want {
				t.Errorf("countCompletedSteps() = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestParseStepOutput tests step output parsing with LLM output parser.
func TestParseStepOutput(t *testing.T) {
	parser := output.NewParser()

	tests := []struct {
		name       string
		stepName   string
		stepOutput string
		wantCount  int
		wantType   OutputType
	}{
		{
			name:     "valid JSON with items",
			stepName: "Generate Code",
			stepOutput: `{
				"items": [
					{
						"name": "main",
						"description": "Main function",
						"content": "package main\n\nfunc main() {}",
						"language": "go"
					}
				]
			}`,
			wantCount: 1,
			wantType:  OutputTypeCode,
		},
		{
			name:     "valid JSON single item",
			stepName: "Generate Tests",
			stepOutput: `{
				"name": "test",
				"description": "Test function",
				"content": "func TestMain(t *testing.T) {}",
				"language": "go"
			}`,
			wantCount: 1,
			wantType:  OutputTypeTest,
		},
		{
			name:       "empty output",
			stepName:   "Generate Code",
			stepOutput: "",
			wantCount:  0,
			wantType:   OutputTypeCode,
		},
		{
			name:       "markdown code blocks",
			stepName:   "Generate Code",
			stepOutput: "Here is the code:\n\n```go\npackage main\n\nfunc main() {\n    fmt.Println(\"Hello\")\n}\n```\n\nAnd another:\n\n```go\nfunc test() {}\n```",
			wantCount:  1,
			wantType:   OutputTypeCode,
		},
		{
			name:       "plain text fallback",
			stepName:   "Generate Documentation",
			stepOutput: "This is plain text documentation without JSON or code blocks.",
			wantCount:  1,
			wantType:   OutputTypeDocs,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items, err := parseStepOutput(parser, tt.stepName, tt.stepOutput)
			if err != nil {
				t.Errorf("parseStepOutput() error = %v", err)
				return
			}

			if len(items) != tt.wantCount {
				t.Errorf("parseStepOutput() got %d items, want %d", len(items), tt.wantCount)
			}

			for _, item := range items {
				if item.Type != tt.wantType {
					t.Errorf("parseStepOutput() item type = %v, want %v", item.Type, tt.wantType)
				}
			}
		})
	}
}

// TestSaveOutputItem tests saving output items to files.
func TestSaveOutputItem(t *testing.T) {
	tempDir := t.TempDir()
	defer setOutputDir(tempDir)()

	if err := initializeOutputDirectories(); err != nil {
		t.Fatalf("initializeOutputDirectories failed: %v", err)
	}

	tests := []struct {
		name     string
		stepName string
		item     *OutputItem
		index    int
		wantDir  string
	}{
		{
			name:     "code item",
			stepName: "Generate Code",
			item: &OutputItem{
				Name:    "main",
				Content: "package main\n\nfunc main() {}",
				Type:    OutputTypeCode,
			},
			index:   0,
			wantDir: codeDir,
		},
		{
			name:     "test item",
			stepName: "Generate Tests",
			item: &OutputItem{
				Name:    "test",
				Content: "func TestMain(t *testing.T) {}",
				Type:    OutputTypeTest,
			},
			index:   0,
			wantDir: testDir,
		},
		{
			name:     "docs item",
			stepName: "Generate Documentation",
			item: &OutputItem{
				Name:    "readme",
				Content: "# README",
				Type:    OutputTypeDocs,
			},
			index:   0,
			wantDir: docsDir,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filePath, err := saveOutputItem(tt.stepName, tt.item, tt.index)
			if err != nil {
				t.Fatalf("saveOutputItem() error = %v", err)
			}

			expectedDir := filepath.Join(tempDir, tt.wantDir)
			if !strings.HasPrefix(filePath, expectedDir) {
				t.Errorf("saveOutputItem() path = %q, should start with %q", filePath, expectedDir)
			}

			content, err := os.ReadFile(filePath)
			if err != nil {
				t.Fatalf("read saved file error = %v", err)
			}

			if string(content) != tt.item.Content {
				t.Errorf("saved file content = %q, want %q", string(content), tt.item.Content)
			}
		})
	}
}

// TestGenerateArchitectureDocument tests architecture document generation.
func TestGenerateArchitectureDocument(t *testing.T) {
	tempDir := t.TempDir()
	defer setOutputDir(tempDir)()

	if err := initializeOutputDirectories(); err != nil {
		t.Fatalf("initializeOutputDirectories failed: %v", err)
	}

	result := &engine.WorkflowResult{
		ExecutionID: "test-exec-1",
		Status:      engine.WorkflowStatusCompleted,
		Duration:    10 * time.Second,
		Steps: []*engine.StepResult{
			{
				Name:     "Generate Code",
				Status:   "completed",
				Duration: 5 * time.Second,
			},
			{
				Name:     "Generate Tests",
				Status:   "completed",
				Duration: 5 * time.Second,
			},
		},
	}

	codeContent := []string{"package main\n\nfunc main() {}"}
	testContent := []string{"func TestMain(t *testing.T) {}"}
	docsContent := []string{"# Documentation"}

	ctx := context.Background()
	if err := generateArchitectureDocument(ctx, result, codeContent, testContent, docsContent); err != nil {
		t.Fatalf("generateArchitectureDocument() error = %v", err)
	}

	filePath := filepath.Join(tempDir, docsDir, "architecture_design.md")
	if _, err := os.Stat(filePath); err != nil {
		t.Errorf("architecture document not created: %v", err)
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read architecture document error = %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, "# Architecture Design Document") {
		t.Error("architecture document missing title")
	}
	if !strings.Contains(contentStr, "test-exec-1") {
		t.Error("architecture document missing execution ID")
	}
}

// TestGenerateAuditDocument tests audit document generation.
func TestGenerateAuditDocument(t *testing.T) {
	tempDir := t.TempDir()
	defer setOutputDir(tempDir)()

	if err := initializeOutputDirectories(); err != nil {
		t.Fatalf("initializeOutputDirectories failed: %v", err)
	}

	result := &engine.WorkflowResult{
		ExecutionID: "test-exec-2",
		Status:      engine.WorkflowStatusCompleted,
		Duration:    15 * time.Second,
		Steps: []*engine.StepResult{
			{
				Name:     "Generate Code",
				Status:   "completed",
				Duration: 10 * time.Second,
			},
			{
				Name:     "Generate Tests",
				Status:   "completed",
				Duration: 5 * time.Second,
			},
		},
	}

	codeContent := []string{"package main\n\nfunc main() {}"}
	testContent := []string{"func TestMain(t *testing.T) {}"}

	ctx := context.Background()
	if err := generateAuditDocument(ctx, result, codeContent, testContent); err != nil {
		t.Fatalf("generateAuditDocument() error = %v", err)
	}

	filePath := filepath.Join(tempDir, docsDir, "audit_report.md")
	if _, err := os.Stat(filePath); err != nil {
		t.Errorf("audit document not created: %v", err)
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read audit document error = %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, "# Code Audit Report") {
		t.Error("audit document missing title")
	}
	if !strings.Contains(contentStr, "test-exec-2") {
		t.Error("audit document missing execution ID")
	}
	if !strings.Contains(contentStr, "Executive Summary") {
		t.Error("audit document missing executive summary")
	}
}

// TestExtractItemsFromData tests extracting items from parsed JSON data.
func TestExtractItemsFromData(t *testing.T) {
	tests := []struct {
		name    string
		data    map[string]interface{}
		wantLen int
	}{
		{
			name: "array of items",
			data: map[string]interface{}{
				"items": []interface{}{
					map[string]interface{}{
						"name":        "item1",
						"description": "description1",
						"content":     "content1",
						"language":    "go",
					},
					map[string]interface{}{
						"name":        "item2",
						"description": "description2",
						"content":     "content2",
						"language":    "go",
					},
				},
			},
			wantLen: 2,
		},
		{
			name: "single item",
			data: map[string]interface{}{
				"name":        "single",
				"description": "single description",
				"content":     "single content",
				"language":    "go",
			},
			wantLen: 1,
		},
		{
			name:    "no items",
			data:    map[string]interface{}{},
			wantLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items, err := extractItemsFromData(tt.data)
			if err != nil {
				t.Errorf("extractItemsFromData() error = %v", err)
				return
			}

			if len(items) != tt.wantLen {
				t.Errorf("extractItemsFromData() got %d items, want %d", len(items), tt.wantLen)
			}
		})
	}
}

// TestGetString tests safe string extraction from map.
func TestGetString(t *testing.T) {
	tests := []struct {
		name string
		m    map[string]interface{}
		key  string
		want string
	}{
		{
			name: "existing key with string",
			m: map[string]interface{}{
				"name": "value",
			},
			key:  "name",
			want: "value",
		},
		{
			name: "existing key with non-string",
			m: map[string]interface{}{
				"name": 123,
			},
			key:  "name",
			want: "",
		},
		{
			name: "missing key",
			m:    map[string]interface{}{},
			key:  "missing",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getString(tt.m, tt.key); got != tt.want {
				t.Errorf("getString() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestGetDefaultFileName tests default file name generation.
func TestGetDefaultFileName(t *testing.T) {
	tests := []struct {
		name       string
		outputType OutputType
		want       string
	}{
		{"code", OutputTypeCode, "main"},
		{"test", OutputTypeTest, "main_test"},
		{"docs", OutputTypeDocs, "README"},
		{"review", OutputTypeReview, "CODE_REVIEW"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getDefaultFileName(tt.outputType); got != tt.want {
				t.Errorf("getDefaultFileName(%v) = %q, want %q", tt.outputType, got, tt.want)
			}
		})
	}
}

// TestExtractCodeBlocks tests code block extraction from markdown.
func TestExtractCodeBlocks(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantCount int
	}{
		{
			name:      "single code block",
			input:     "```go\npackage main\n```",
			wantCount: 1,
		},
		{
			name:      "multiple code blocks",
			input:     "```go\nfunc a() {}\n```\ntext\n```go\nfunc b() {}\n```",
			wantCount: 2,
		},
		{
			name:      "no code blocks",
			input:     "just plain text",
			wantCount: 0,
		},
		{
			name:      "empty input",
			input:     "",
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocks := extractCodeBlocks(tt.input)
			if len(blocks) != tt.wantCount {
				t.Errorf("extractCodeBlocks() got %d blocks, want %d", len(blocks), tt.wantCount)
			}
		})
	}
}

// TestGetSubDir tests subdirectory selection by output type.
func TestGetSubDir(t *testing.T) {
	tests := []struct {
		name       string
		outputType OutputType
		want       string
	}{
		{"code", OutputTypeCode, codeDir},
		{"test", OutputTypeTest, testDir},
		{"docs", OutputTypeDocs, docsDir},
		{"review", OutputTypeReview, docsDir},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getSubDir(tt.outputType); got != tt.want {
				t.Errorf("getSubDir(%v) = %q, want %q", tt.outputType, got, tt.want)
			}
		})
	}
}
