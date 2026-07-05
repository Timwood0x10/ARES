package builtin

import (
	"context"
	"fmt"
	"os"

	"github.com/Timwood0x10/ares/internal/tools/resources/base"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
	"github.com/ledongthuc/pdf"
)

const (
	paramOperation       = "operation"
	paramFilePath        = "file_path"
	operationExtractText = "extract_text"
)

// PDFTool provides PDF document processing operations.
type PDFTool struct {
	*base.BaseTool
}

// NewPDFTool creates a new PDFTool.
func NewPDFTool() *PDFTool {
	params := &core.ParameterSchema{
		Type: "object",
		Properties: map[string]*core.Parameter{
			paramOperation: {
				Type:        "string",
				Description: "Operation: extract_text (extract all text from PDF)",
				Enum:        []interface{}{operationExtractText},
			},
			paramFilePath: {
				Type:        "string",
				Description: "Path to the PDF file",
			},
		},
		Required: []string{paramOperation, paramFilePath},
	}

	return &PDFTool{
		BaseTool: base.NewBaseToolWithCapabilities("pdf_tool",
			"Extract text content from PDF files. Supports text extraction from any PDF document.",
			core.CategoryCore, []core.Capability{core.CapabilityText}, params),
	}
}

// Execute performs the PDF operation.
func (t *PDFTool) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	operation, ok := params[paramOperation].(string)
	if !ok || operation == "" {
		return core.NewErrorResult("operation is required"), nil
	}

	filePath, ok := params[paramFilePath].(string)
	if !ok || filePath == "" {
		return core.NewErrorResult("file_path is required"), nil
	}

	switch operation {
	case operationExtractText:
		return t.extractText(ctx, filePath)
	default:
		return core.NewErrorResult(fmt.Sprintf("unsupported operation: %s", operation)), nil
	}
}

func (t *PDFTool) extractText(ctx context.Context, filePath string) (core.Result, error) {
	// Verify file exists and is readable.
	info, err := os.Stat(filePath)
	if err != nil {
		return core.NewErrorResult(fmt.Sprintf("cannot access file: %v", err)), nil
	}
	if info.IsDir() {
		return core.NewErrorResult("path is a directory, not a file"), nil
	}

	f, r, err := pdf.Open(filePath)
	if err != nil {
		return core.NewErrorResult(fmt.Sprintf("failed to open PDF: %v", err)), nil
	}
	defer func() {
		if cerr := f.Close(); cerr != nil {
			log.Error("pdf: close file error", "path", filePath, "error", cerr)
		}
	}()

	var text string
	for i := 1; i <= r.NumPage(); i++ {
		page := r.Page(i)
		if page.V.IsNull() {
			continue
		}
		pageText, err := page.GetPlainText(nil)
		if err != nil {
			log.Warn("pdf: page text extraction failed", "page", i, "error", err)
			continue
		}
		text += fmt.Sprintf("--- Page %d ---\n%s\n", i, pageText)
	}

	return core.NewResult(true, map[string]interface{}{
		paramOperation: operationExtractText,
		paramFilePath:  filePath,
		"pages":        r.NumPage(),
		"text":         text,
		"char_count":   len([]rune(text)),
	}), nil
}

// IsIdempotent returns true since PDF reading has no side effects.
func (t *PDFTool) IsIdempotent() bool { return true }
