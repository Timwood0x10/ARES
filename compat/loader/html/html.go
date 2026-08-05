// Package html is the official HTML document loader for ARES.
//
// This is a placeholder skeleton. The real adapter will strip tags via a
// tokenizer (e.g. bluemonday) and preserve title/meta. The stub extracts
// text via a naive regex strip that is sufficient for skeleton wiring tests.
package html

import (
	"context"
	"fmt"
	"io"
	"regexp"

	"github.com/Timwood0x10/ares/compat/loader"
)

// maxBytes caps the size of a single loaded document (32 MiB).
const maxBytes = 32 << 20

// readAllLimited reads at most limit bytes from r, polling ctx between reads
// so a cancelled context aborts promptly without leaking a goroutine.
func readAllLimited(ctx context.Context, r io.Reader, limit int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var buf []byte
	tmp := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			if int64(len(buf)) > limit {
				return nil, fmt.Errorf("document exceeds %d byte limit", limit)
			}
		}
		if err != nil {
			if err == io.EOF {
				return buf, nil
			}
			return nil, err
		}
	}
}

// Loader satisfies compat/loader.DocumentLoader for HTML files.
type Loader struct{}

// New constructs a Loader from a raw config map (currently unused).
func New(_ map[string]any) (*Loader, error) { return &Loader{}, nil }

// Load reads at most maxBytes from r, strips HTML tags, and returns a
// plain-text Document.
func (*Loader) Load(ctx context.Context, source string, r io.Reader) (*loader.Document, error) {
	data, err := readAllLimited(ctx, r, maxBytes)
	if err != nil {
		return nil, fmt.Errorf("compat/loader/html: read: %w", err)
	}
	// Naive tag strip — sufficient for skeleton wiring; the real adapter will
	// use a tokenizer.
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
