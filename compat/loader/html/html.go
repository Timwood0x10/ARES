// Package html is the official HTML document loader for ARES.
//
// This is a placeholder skeleton. The real adapter will strip tags via a
// tokenizer (e.g. bluemonday) and preserve title/meta. The stub extracts
// text via a naive regex strip that is sufficient for骨架 wiring tests.
package html

import (
	"context"
	"io"
	"regexp"

	"github.com/Timwood0x10/ares/compat/loader"
)

// Loader satisfies compat/loader.DocumentLoader for HTML files.
type Loader struct{}

// New constructs a Loader from a raw config map (currently unused).
func New(_ map[string]any) (*Loader, error) { return &Loader{}, nil }

// Load reads all bytes from r, strips HTML tags, and returns a plain-text Document.
func (*Loader) Load(_ context.Context, source string, r io.Reader) (*loader.Document, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	// Naive tag strip — sufficient for骨架 wiring; the real adapter will use a tokenizer.
	stripped := tagStrip.ReplaceAllString(string(data), "")
	return &loader.Document{
		Source: source,
		Text:   stripped,
	}, nil
}

// Name returns the canonical format name.
func (*Loader) Name() string { return "html" }

// Extensions returns the file extensions this loader handles.
func (*Loader) Extensions() []string { return []string{".html", ".htm"} }

var tagStrip = regexp.MustCompile(`<[^>]+>`)

// Compile-time interface assertion.
var _ loader.DocumentLoader = (*Loader)(nil)
