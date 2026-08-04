// Package pdf is the official PDF document loader for ARES.
//
// It binds github.com/ledongthuc/pdf under the compat/loader.DocumentLoader
// interface so PDF files can be ingested into the ARES knowledge pipeline
// via compat.RegisterLoader("pdf", …). Text is extracted per-page and
// concatenated; page count is stored in metadata.
package pdf

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/Timwood0x10/ares/compat/loader"

	pdflib "github.com/ledongthuc/pdf"
)

// Loader satisfies compat/loader.DocumentLoader for PDF files.
type Loader struct{}

// New constructs a Loader from a raw config map (currently unused).
// The config schema is reserved for future options (OCR fallback, password).
func New(_ map[string]any) (*Loader, error) { return &Loader{}, nil }

// maxBytes caps the size of a single loaded document (32 MiB). PDF text
// extraction is fully buffered in-memory because ledongthuc/pdf requires an
// io.ReaderAt with a known size.
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

// Load reads at most maxBytes from r, extracts plain text via ledongthuc/pdf,
// and returns a Document with the concatenated page text and page-count metadata.
//
// For very large PDFs callers should pre-chunk; this loader is designed for
// typical document sizes (up to the maxBytes cap).
func (*Loader) Load(ctx context.Context, source string, r io.Reader) (*loader.Document, error) {
	buf, err := readAllLimited(ctx, r, maxBytes)
	if err != nil {
		return nil, fmt.Errorf("compat/loader/pdf: read: %w", err)
	}
	if len(buf) == 0 {
		return nil, fmt.Errorf("compat/loader/pdf: empty input")
	}

	pdfReader, err := pdflib.NewReader(bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		return nil, fmt.Errorf("compat/loader/pdf: open: %w", err)
	}

	numPages := pdfReader.NumPage()
	plainReader, err := pdfReader.GetPlainText()
	if err != nil {
		return nil, fmt.Errorf("compat/loader/pdf: extract: %w", err)
	}
	plain, err := readAllLimited(ctx, plainReader, maxBytes)
	if err != nil {
		return nil, fmt.Errorf("compat/loader/pdf: read text: %w", err)
	}

	meta := map[string]string{
		"pages": fmt.Sprintf("%d", numPages),
	}
	if source != "" {
		meta["source"] = source
	}

	return &loader.Document{
		Source:   source,
		Text:     string(plain),
		Metadata: meta,
	}, nil
}

// Name returns the canonical format name.
func (*Loader) Name() string { return "pdf" }

// Extensions returns the file extensions this loader handles.
func (*Loader) Extensions() []string { return []string{".pdf"} }

// Compile-time interface assertion.
var _ loader.DocumentLoader = (*Loader)(nil)
