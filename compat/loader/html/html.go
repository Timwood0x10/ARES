// Package html is the official HTML document loader for ARES.
//
// It extracts readable text: script/style blocks are dropped, block-level
// closers become newlines, remaining tags are stripped, and HTML entities
// are unescaped. Suitable for documents and simple pages; a full tokenizer
// is not warranted for the compat loader's purpose.
package html

import (
	"context"
	"fmt"
	"html"
	"io"
	"regexp"
	"strings"

	"github.com/Timwood0x10/ares/compat/loader"
	"github.com/Timwood0x10/ares/compat/loader/internal/readutil"
)

// scriptBlocks and styleBlocks match their elements including contents —
// none of it is document text. (RE2 has no backreferences, hence two
// patterns rather than one with a capture.)
var (
	scriptBlocks = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>`)
	styleBlocks  = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style\s*>`)
)

// blockClosers become newlines so adjacent paragraphs/lines do not fuse.
var blockClosers = regexp.MustCompile(`(?i)</(p|div|section|article|header|footer|li|tr|h[1-6]|pre|blockquote)\s*/?>`)

// voidElements are self-closing line breaks with no end tag — they must
// also become newlines (a closing-tag-only pattern can never match them).
var voidElements = regexp.MustCompile(`(?i)<(br|hr)(?:\s[^>]*)?/?>`)

// anyTag matches any remaining element; its text content survives.
var anyTag = regexp.MustCompile(`(?s)<[^>]*>`)

// Loader satisfies compat/loader.DocumentLoader for HTML files.
type Loader struct{}

// New constructs a Loader from a raw config map (currently unused).
func New(_ map[string]any) (*Loader, error) { return &Loader{}, nil }

// Load reads at most readutil.MaxDocumentBytes from r, strips HTML markup,
// and returns a plain-text Document.
func (*Loader) Load(ctx context.Context, source string, r io.Reader) (*loader.Document, error) {
	data, err := readutil.ReadAllLimited(ctx, r, readutil.MaxDocumentBytes)
	if err != nil {
		return nil, fmt.Errorf("compat/loader/html: read: %w", err)
	}
	return &loader.Document{
		Source: source,
		Text:   Strip(string(data)),
	}, nil
}

// Strip converts an HTML document to plain text: script/style blocks are
// removed, block-level closers become newlines, remaining tags are dropped,
// entities are unescaped, and runs of blank lines are collapsed.
func Strip(doc string) string {
	s := scriptBlocks.ReplaceAllString(doc, " ")
	s = styleBlocks.ReplaceAllString(s, " ")
	s = blockClosers.ReplaceAllString(s, "\n")
	s = voidElements.ReplaceAllString(s, "\n")
	s = anyTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	// Collapse whitespace runs per line and drop repeated blank lines.
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			if blank {
				continue
			}
			blank = true
			out = append(out, "")
			continue
		}
		blank = false
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// Name returns the canonical format name.
func (*Loader) Name() string { return "html" }

// Extensions returns the file extensions this loader handles.
func (*Loader) Extensions() []string { return []string{".html", ".htm"} }

// Compile-time interface assertion.
var _ loader.DocumentLoader = (*Loader)(nil)
