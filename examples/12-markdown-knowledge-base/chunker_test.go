package main

import (
	"strings"
	"testing"
)

// testKnowledgeConfig returns a knowledge config for chunking tests.
func testKnowledgeConfig(chunkSize int) KnowledgeConfig {
	return KnowledgeConfig{
		ChunkSize:     chunkSize,
		ChunkOverlap:  40,
		TopK:          6,
		MinScore:      0.35,
		PassagePrefix: "passage:",
		QueryPrefix:   "query:",
	}
}

func TestChunkDocumentSectionSplit(t *testing.T) {
	content := "# Title\n\nlead in\n\n" +
		"## Section A\n\nalpha body\n\n" +
		"## Section B\n\nbeta body\n"
	doc := ParseContent("doc.md", content)

	chunks := ChunkDocument(doc, testKnowledgeConfig(1500))
	if len(chunks) != 3 {
		t.Fatalf("chunk count = %d, want 3", len(chunks))
	}
	if !strings.Contains(chunks[1].Content, "alpha body") {
		t.Errorf("section A chunk missing body: %q", chunks[1].Content)
	}
	if chunks[1].Level != 2 {
		t.Errorf("section A level = %d, want 2", chunks[1].Level)
	}
}

func TestChunkDocumentNeverSplitsCodeBlock(t *testing.T) {
	body := strings.Repeat("x := compute()\n", 60) // ~900 chars of code
	content := "## Big Code\n\n```go\n" + body + "```\n"
	doc := ParseContent("doc.md", content)

	chunks := ChunkDocument(doc, testKnowledgeConfig(200))
	if len(chunks) < 2 {
		t.Fatalf("expected the oversized section to split, got %d chunks", len(chunks))
	}
	for i, c := range chunks {
		if strings.Count(c.Content, "```")%2 != 0 {
			t.Errorf("chunk[%d] splits a code fence: %q", i, c.Content)
		}
	}
}

func TestChunkDocumentContinuationCarriesHeading(t *testing.T) {
	para := strings.Repeat("sentence. ", 40)
	content := "## Heading X\n\n" + para + "\n\n" + para + "\n\n" + para + "\n"
	doc := ParseContent("doc.md", content)

	chunks := ChunkDocument(doc, testKnowledgeConfig(300))
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	if !strings.Contains(chunks[1].Content, "Heading X") {
		t.Errorf("continuation chunk missing heading breadcrumb: %q", chunks[1].Content)
	}
}

func TestChunkMetadata(t *testing.T) {
	content := "# Doc Title\n\n## Parent\n\n### Child\n\nbody text #topic\n"
	doc := ParseContent("/notes/x.md", content)

	chunks := ChunkDocument(doc, testKnowledgeConfig(1500))
	var target *Chunk
	for i := range chunks {
		if strings.Contains(chunks[i].Content, "body text") {
			target = &chunks[i]
			break
		}
	}
	if target == nil {
		t.Fatal("child section chunk not found")
	}

	meta := chunkMetadata(*target, doc)
	if meta["heading_path"] != "Doc Title > Parent > Child" {
		t.Errorf("heading_path = %v", meta["heading_path"])
	}
	if meta["file"] != "/notes/x.md" {
		t.Errorf("file = %v", meta["file"])
	}
	if meta["heading"] != "Child" {
		t.Errorf("heading = %v", meta["heading"])
	}
}

func TestGroupBlocksKeepsOversizedBlockAlone(t *testing.T) {
	blocks := []Block{
		{Type: BlockParagraph, Text: "short"},
		{Type: BlockCode, Text: strings.Repeat("y", 500)},
		{Type: BlockParagraph, Text: "tail"},
	}
	groups := groupBlocks(blocks, 200)
	for _, g := range groups {
		if len(g) == 1 && g[0].Type == BlockCode {
			return // oversized code isolated as expected
		}
	}
	t.Fatalf("oversized code block was not isolated: %d groups", len(groups))
}
