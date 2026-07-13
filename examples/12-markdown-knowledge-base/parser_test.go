package main

import (
	"strings"
	"testing"
)

// sampleDoc is a markdown document exercising every recognised block type.
const sampleDoc = "---\n" +
	"title: Test Doc\n" +
	"tags: [alpha, beta]\n" +
	"---\n\n" +
	"# Heading One\n\n" +
	"Intro paragraph with #inline tag.\n\n" +
	"## Sub Section\n\n" +
	"- item one\n" +
	"- item two\n\n" +
	"> a quoted line\n\n" +
	"```go\n" +
	"func main() {}\n" +
	"```\n\n" +
	"| a | b |\n" +
	"| --- | --- |\n" +
	"| 1 | 2 |\n"

func TestParseContentBlockTypes(t *testing.T) {
	doc := ParseContent("test.md", sampleDoc)

	if doc.Title != "Test Doc" {
		t.Fatalf("title = %q, want %q", doc.Title, "Test Doc")
	}

	want := []BlockType{
		BlockHeading, BlockParagraph, BlockHeading,
		BlockList, BlockQuote, BlockCode, BlockTable,
	}
	if len(doc.Blocks) != len(want) {
		t.Fatalf("block count = %d, want %d: %+v", len(doc.Blocks), len(want), blockKinds(doc.Blocks))
	}
	for i, w := range want {
		if doc.Blocks[i].Type != w {
			t.Errorf("block[%d].Type = %q, want %q", i, doc.Blocks[i].Type, w)
		}
	}
}

func TestParseContentTags(t *testing.T) {
	doc := ParseContent("test.md", sampleDoc)
	got := strings.Join(doc.Tags, ",")
	for _, want := range []string{"alpha", "beta", "inline"} {
		if !strings.Contains(got, want) {
			t.Errorf("tags %q missing %q", got, want)
		}
	}
}

func TestParseContentCodeBlockPreserved(t *testing.T) {
	doc := ParseContent("test.md", sampleDoc)
	var code *Block
	for i := range doc.Blocks {
		if doc.Blocks[i].Type == BlockCode {
			code = &doc.Blocks[i]
			break
		}
	}
	if code == nil {
		t.Fatal("no code block parsed")
	}
	if code.Lang != "go" {
		t.Errorf("code lang = %q, want go", code.Lang)
	}
	if !strings.Contains(code.Text, "func main() {}") {
		t.Errorf("code body not preserved: %q", code.Text)
	}
	if strings.Count(code.Text, "```") != 2 {
		t.Errorf("code fences not preserved: %q", code.Text)
	}
}

func TestParseContentHeadingLevels(t *testing.T) {
	doc := ParseContent("test.md", sampleDoc)
	if doc.Blocks[0].Level != 1 || doc.Blocks[0].Title != "Heading One" {
		t.Errorf("first heading = level %d %q", doc.Blocks[0].Level, doc.Blocks[0].Title)
	}
	if doc.Blocks[2].Level != 2 || doc.Blocks[2].Title != "Sub Section" {
		t.Errorf("second heading = level %d %q", doc.Blocks[2].Level, doc.Blocks[2].Title)
	}
}

func TestParseContentTitleFallbackToFilename(t *testing.T) {
	doc := ParseContent("/notes/my-note.md", "just a paragraph, no heading\n")
	if doc.Title != "my-note" {
		t.Errorf("title = %q, want my-note", doc.Title)
	}
}

// blockKinds is a test helper returning block types for diagnostics.
func blockKinds(blocks []Block) []BlockType {
	kinds := make([]BlockType, len(blocks))
	for i, b := range blocks {
		kinds[i] = b.Type
	}
	return kinds
}
