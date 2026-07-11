// Package pdf is the official PDF document loader for ARES.
//
// This is a placeholder skeleton. The real adapter will bind to a PDF text
// extraction library (e.g. pdfcpu, ledongthuc/pdf) and preserve page metadata.
// The stub returns ErrNotImplemented so callers fail fast until the binding
// is wired.
package pdf

import (
	"context"
	"errors"
	"io"

	"github.com/Timwood0x10/ares/compat/loader"
)

// ErrNotImplemented is returned by stub methods until the full binding is wired.
var ErrNotImplemented = errors.New("compat/loader/pdf: not implemented yet")

// Loader satisfies compat/loader.DocumentLoader for PDF files.
type Loader struct{}

// New constructs a Loader from a raw config map (currently unused).
func New(_ map[string]any) (*Loader, error) { return &Loader{}, nil }

// Load reads all bytes from r and returns a plain-text Document.
func (*Loader) Load(_ context.Context, _ string, _ io.Reader) (*loader.Document, error) {
	return nil, ErrNotImplemented
}

// Name returns the canonical format name.
func (*Loader) Name() string { return "pdf" }

// Extensions returns the file extensions this loader handles.
func (*Loader) Extensions() []string { return []string{".pdf"} }

// Compile-time interface assertion.
var _ loader.DocumentLoader = (*Loader)(nil)
