// Package markdown is the official Markdown document loader for ARES.
//
// Markdown is plain text, so this loader is a trivial pass-through that
// records the source path and reads all bytes into Text.
package markdown

import (
	"context"
	"fmt"
	"io"

	"github.com/Timwood0x10/ares/compat/loader"
	"github.com/Timwood0x10/ares/compat/loader/internal/readutil"
)

// Loader satisfies compat/loader.DocumentLoader for Markdown files.
type Loader struct{}

// New constructs a Loader from a raw config map (currently unused).
func New(_ map[string]any) (*Loader, error) { return &Loader{}, nil }

// Load reads at most readutil.MaxDocumentBytes from r and returns them as a
// plain-text Document.
func (*Loader) Load(ctx context.Context, source string, r io.Reader) (*loader.Document, error) {
	data, err := readutil.ReadAllLimited(ctx, r, readutil.MaxDocumentBytes)
	if err != nil {
		return nil, fmt.Errorf("compat/loader/markdown: read: %w", err)
	}
	return &loader.Document{
		Source: source,
		Text:   string(data),
	}, nil
}

// Name returns the canonical format name.
func (*Loader) Name() string { return "markdown" }

// Extensions returns the file extensions this loader handles.
func (*Loader) Extensions() []string { return []string{".md", ".markdown"} }

// Compile-time interface assertion.
var _ loader.DocumentLoader = (*Loader)(nil)
