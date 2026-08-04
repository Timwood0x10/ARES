package markdown_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Timwood0x10/ares/compat/loader"
	"github.com/Timwood0x10/ares/compat/loader/markdown"
)

func TestMarkdownLoader_Basic(t *testing.T) {
	t.Parallel()

	l, err := markdown.New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if l.Name() != "markdown" {
		t.Fatalf("expected name=markdown, got %q", l.Name())
	}

	doc, err := l.Load(context.Background(), "test.md", strings.NewReader("# Hello\nWorld"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if doc.Source != "test.md" {
		t.Fatalf("expected source=test.md, got %q", doc.Source)
	}
	if doc.Text != "# Hello\nWorld" {
		t.Fatalf("unexpected text: %q", doc.Text)
	}
}

func TestMarkdownLoader_ImplementsInterface(t *testing.T) {
	t.Parallel()

	var _ loader.DocumentLoader = (*markdown.Loader)(nil)
}

// TestMarkdownLoader_SizeLimit verifies that an oversized document is
// rejected instead of being buffered without limit.
func TestMarkdownLoader_SizeLimit(t *testing.T) {
	t.Parallel()

	l, err := markdown.New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// maxBytes is 32 MiB; a larger reader must be rejected.
	big := strings.NewReader(strings.Repeat("a", 33<<20))
	_, err = l.Load(context.Background(), "big.md", big)
	if err == nil {
		t.Fatal("expected an error for an oversized document, got nil")
	}
}

// TestMarkdownLoader_CancelledContext verifies that a cancelled context
// aborts the load instead of reading the whole document.
func TestMarkdownLoader_CancelledContext(t *testing.T) {
	t.Parallel()

	l, err := markdown.New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before Load

	_, err = l.Load(ctx, "cancel.md", strings.NewReader("data"))
	if err == nil {
		t.Fatal("expected an error for a cancelled context, got nil")
	}
}
