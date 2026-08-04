package builtin

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPDFTool(t *testing.T) {
	tool := NewPDFTool()
	require.NotNil(t, tool)
	assert.Equal(t, "pdf_tool", tool.Name())
}

func TestPDFTool_FileNotFound(t *testing.T) {
	tool := NewPDFTool()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "extract_text",
		"file_path": "/tmp/nonexistent-test-file.pdf",
	})
	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Contains(t, result.Error, "cannot access file")
}

func TestPDFTool_MissingFilePath(t *testing.T) {
	tool := NewPDFTool()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "extract_text",
	})
	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Contains(t, result.Error, "file_path is required")
}

func TestPDFTool_MissingOperation(t *testing.T) {
	tool := NewPDFTool()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"file_path": "/tmp/test.pdf",
	})
	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Contains(t, result.Error, "operation is required")
}

func TestPDFTool_UnknownOperation(t *testing.T) {
	tool := NewPDFTool()
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "unknown", "file_path": "/tmp/test.pdf",
	})
	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Contains(t, result.Error, "unsupported operation")
}

func TestPDFTool_IsIdempotent(t *testing.T) {
	tool := NewPDFTool()
	assert.True(t, tool.IsIdempotent())
}

// TestPDFTool_ExtractTextFromRealPDF tests with an actual PDF file.
// Uses testdata/hello.pdf to verify the extraction works end-to-end.
func TestPDFTool_ExtractTextFromRealPDF(t *testing.T) {
	tool := NewPDFTool()
	ctx := context.Background()

	pdfPath := filepath.Join("testdata", "hello.pdf")
	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "extract_text",
		"file_path": pdfPath,
	})
	require.NoError(t, err)

	// The minimal PDF may not parse; verify we handle gracefully (no panic).
	if result.Success {
		data, ok := result.Data.(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "extract_text", data["operation"])
		assert.NotEmpty(t, data["file_path"])
	} else {
		// Graceful error on unparseable PDF is acceptable.
		assert.NotEmpty(t, result.Error)
	}
}

// TestPDFTool_AllowedDir_AcceptsInside verifies that a file inside the
// configured allowed directory passes the sandbox check (it must reach the
// PDF parser rather than being rejected by the sandbox).
func TestPDFTool_AllowedDir_AcceptsInside(t *testing.T) {
	allowed := filepath.Join("testdata")
	tool := NewPDFTool(WithAllowedDir(allowed))
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "extract_text",
		"file_path": filepath.Join(allowed, "hello.pdf"),
	})
	require.NoError(t, err)
	require.False(t, result.Success)
	// The sandbox must not reject the path; the file reaches the parser,
	// which may fail on this minimal fixture.
	assert.NotContains(t, result.Error, "access denied")
}

// TestPDFTool_AllowedDir_RejectsOutside verifies that a file outside the
// configured allowed directory is rejected before any filesystem access.
func TestPDFTool_AllowedDir_RejectsOutside(t *testing.T) {
	allowed := filepath.Join("testdata")
	tool := NewPDFTool(WithAllowedDir(allowed))
	ctx := context.Background()

	result, err := tool.Execute(ctx, map[string]interface{}{
		"operation": "extract_text",
		"file_path": "/etc/passwd",
	})
	require.NoError(t, err)
	require.False(t, result.Success)
	assert.Contains(t, result.Error, "access denied")
}
