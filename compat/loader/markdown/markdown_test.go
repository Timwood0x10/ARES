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
