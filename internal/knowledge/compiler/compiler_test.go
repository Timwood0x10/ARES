package compiler

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"strings"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

func TestDefaultCompilerEmptyGraph(t *testing.T) {
	c := NewDefaultCompiler()
	graph := &knowledge.WorkingGraph{
		Nodes: map[string]*knowledge.KnowledgeObject{},
		Edges: nil,
	}

	cfg := CompileConfig{Formats: []Format{FormatPrompt}}
	ctx, err := c.Compile(context.Background(), graph, cfg)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	if ctx == nil {
		t.Fatal("expected non-nil CompileContext")
	}
}

func TestDefaultCompilerNilGraph(t *testing.T) {
	c := NewDefaultCompiler()
	_, err := c.Compile(context.Background(), nil, CompileConfig{})
	if err == nil {
		t.Error("expected error for nil graph")
	}
}

func TestDefaultCompilerPromptFormat(t *testing.T) {
	c := NewDefaultCompiler()
	graph := testGraph()

	cfg := CompileConfig{Formats: []Format{FormatPrompt}}
	ctx, err := c.Compile(context.Background(), graph, cfg)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}

	prompt, ok := ctx.Formats[FormatPrompt]
	if !ok {
		t.Fatal("expected prompt format")
	}
	if !strings.Contains(prompt, "Knowledge Context") {
		t.Error("expected 'Knowledge Context' in prompt output")
	}
	if !strings.Contains(prompt, "redis") {
		t.Error("expected node 'redis' in prompt output")
	}
	if !strings.Contains(prompt, "Relations") {
		t.Error("expected Relations section in prompt output")
	}
}

func TestDefaultCompilerMarkdownFormat(t *testing.T) {
	c := NewDefaultCompiler()
	graph := testGraph()

	cfg := CompileConfig{Formats: []Format{FormatMarkdown}}
	ctx, err := c.Compile(context.Background(), graph, cfg)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}

	md, ok := ctx.Formats[FormatMarkdown]
	if !ok {
		t.Fatal("expected markdown format")
	}
	if !strings.Contains(md, "redis") {
		t.Error("expected node data in markdown output")
	}
}

func TestDefaultCompilerJSONFormat(t *testing.T) {
	c := NewDefaultCompiler()
	graph := testGraph()

	cfg := CompileConfig{Formats: []Format{FormatJSON}}
	ctx, err := c.Compile(context.Background(), graph, cfg)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}

	jsonOut, ok := ctx.Formats[FormatJSON]
	if !ok {
		t.Fatal("expected json format")
	}
	if !strings.Contains(jsonOut, `"nodes"`) {
		t.Error("expected 'nodes' in JSON output")
	}
	if !strings.Contains(jsonOut, `"edges"`) {
		t.Error("expected 'edges' in JSON output")
	}
}

func TestDefaultCompilerMultipleFormats(t *testing.T) {
	c := NewDefaultCompiler()
	graph := testGraph()

	cfg := CompileConfig{Formats: []Format{FormatPrompt, FormatJSON}}
	ctx, err := c.Compile(context.Background(), graph, cfg)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}

	if len(ctx.Formats) != 2 {
		t.Errorf("expected 2 formats, got %d", len(ctx.Formats))
	}
	if _, ok := ctx.Formats[FormatPrompt]; !ok {
		t.Error("expected prompt format")
	}
	if _, ok := ctx.Formats[FormatJSON]; !ok {
		t.Error("expected json format")
	}
}

func TestDefaultCompilerMaxNodes(t *testing.T) {
	c := NewDefaultCompiler()
	graph := testGraph()

	cfg := CompileConfig{Formats: []Format{FormatPrompt}, MaxNodes: 1}
	ctx, err := c.Compile(context.Background(), graph, cfg)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}

	prompt := ctx.Formats[FormatPrompt]
	// Should only contain one of the three nodes.
	count := strings.Count(prompt, "conf:")
	if count > 2 {
		t.Errorf("expected at most 1 node in output, found ~%d", count)
	}
}

func TestDefaultCompilerMetrics(t *testing.T) {
	c := NewDefaultCompiler()
	graph := testGraph()

	cfg := CompileConfig{Formats: []Format{FormatPrompt, FormatJSON}}
	ctx, err := c.Compile(context.Background(), graph, cfg)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}

	if ctx.Metrics.InputNodes != 3 {
		t.Errorf("expected 3 input nodes, got %d", ctx.Metrics.InputNodes)
	}
	if ctx.Metrics.InputEdges != 1 {
		t.Errorf("expected 1 input edge, got %d", ctx.Metrics.InputEdges)
	}
	if ctx.Metrics.OutputTokens <= 0 {
		t.Errorf("expected positive output tokens, got %d", ctx.Metrics.OutputTokens)
	}
}

func TestDefaultCompilerUnknownFormat(t *testing.T) {
	c := NewDefaultCompiler()
	graph := testGraph()

	cfg := CompileConfig{Formats: []Format{"unknown"}}
	ctx, err := c.Compile(context.Background(), graph, cfg)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	if len(ctx.Formats) != 0 {
		t.Errorf("expected 0 formats for unknown format, got %d", len(ctx.Formats))
	}
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		input string
		min   int
		max   int
	}{
		{"hello world", 2, 3},
		{"", 0, 0},
		{strings.Repeat("a", 100), 24, 26},
	}

	for _, tt := range tests {
		got := estimateTokens(tt.input)
		if got < tt.min || got > tt.max {
			t.Errorf("estimateTokens(%q) = %d, want between %d and %d", tt.input, got, tt.min, tt.max)
		}
	}
}

func TestDefaultCompilerXMLFormat(t *testing.T) {
	c := NewDefaultCompiler()
	graph := testGraph()

	cfg := CompileConfig{Formats: []Format{FormatXML}}
	ctx, err := c.Compile(context.Background(), graph, cfg)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}

	xmlOut, ok := ctx.Formats[FormatXML]
	if !ok {
		t.Fatal("expected xml format")
	}
	if !strings.Contains(xmlOut, "<knowledge_context>") {
		t.Error("expected <knowledge_context> in XML output")
	}
	if !strings.Contains(xmlOut, "<nodes") {
		t.Error("expected <nodes> in XML output")
	}
	if !strings.Contains(xmlOut, "<relations") {
		t.Error("expected <relations> in XML output")
	}
}

func TestDefaultCompilerToolSchemaFormat(t *testing.T) {
	c := NewDefaultCompiler()
	graph := testGraph()

	cfg := CompileConfig{Formats: []Format{FormatToolSchema}}
	ctx, err := c.Compile(context.Background(), graph, cfg)
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}

	schema, ok := ctx.Formats[FormatToolSchema]
	if !ok {
		t.Fatal("expected tool_schema format")
	}
	if !strings.Contains(schema, "$schema") {
		t.Error("expected $schema in tool_schema output")
	}
	if !strings.Contains(schema, "nodes") {
		t.Error("expected 'nodes' property in tool_schema")
	}
	if !strings.Contains(schema, "relations") {
		t.Error("expected 'relations' property in tool_schema")
	}
}

func TestEscapeXML(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"a&b", "a&amp;b"},
		{"<tag>", "&lt;tag&gt;"},
		{"\"quoted\"", "&quot;quoted&quot;"},
		{"'single'", "&apos;single&apos;"},
	}
	for _, tt := range tests {
		got := escapeXML(tt.input)
		if got != tt.expected {
			t.Errorf("escapeXML(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

// testGraph creates a simple graph for testing.
func testGraph() *knowledge.WorkingGraph {
	return &knowledge.WorkingGraph{
		Nodes: map[string]*knowledge.KnowledgeObject{
			"redis": {ID: "redis", Type: knowledge.ObjectDecision, Summary: "Chose Redis for caching", Confidence: 0.9},
			"pg":    {ID: "pg", Type: knowledge.ObjectDecision, Summary: "Chose PostgreSQL for storage", Confidence: 0.8},
			"cache": {ID: "cache", Type: knowledge.ObjectArchitecture, Summary: "Cache layer architecture", Confidence: 0.7},
		},
		Edges: []knowledge.Relation{
			{From: "cache", To: "redis", Name: knowledge.RelDependsOn, Score: 0.9},
		},
	}
}

// TestFormatJSON_EdgeFieldsAreValidJSON locks REVIEW 3.5: the edge fields of
// the JSON format must be encoded with json.Marshal, not %q. %q produces Go
// string-literal escapes (\a, \x…) that are invalid JSON when an edge field
// contains control characters, so downstream json.Unmarshal failed.
func TestFormatJSON_EdgeFieldsAreValidJSON(t *testing.T) {
	c := NewDefaultCompiler()
	graph := testGraph()
	// Control characters in every edge field: %q would emit \a (an escape
	// JSON does not define) and \x07, both rejected by json.Unmarshal.
	graph.Edges = []knowledge.Relation{
		{From: "src\x07", To: "dst\x07", Name: "depends\bon", Score: 0.5},
	}

	ctx, err := c.Compile(context.Background(), graph, CompileConfig{Formats: []Format{FormatJSON}})
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	out := ctx.Formats[FormatJSON]

	// The whole document must parse as JSON.
	var decoded struct {
		Edges []struct {
			From string `json:"from"`
			To   string `json:"to"`
			Name string `json:"name"`
		} `json:"edges"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("compiled JSON is not parseable (%%q escaping?): %v\noutput: %s", err, out)
	}
	if len(decoded.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(decoded.Edges))
	}
	if decoded.Edges[0].From != "src\x07" || decoded.Edges[0].To != "dst\x07" || decoded.Edges[0].Name != "depends\bon" {
		t.Errorf("edge fields did not round-trip: %+v", decoded.Edges[0])
	}
}

// TestFormatXML_EdgeAttributesAreEscaped locks REVIEW 3.5: the relation
// attributes of the XML format must be escaped with escapeXMLAttr, not %q.
// %q leaves & and < unescaped (and adds Go quoting), producing XML no parser
// accepts.
func TestFormatXML_EdgeAttributesAreEscaped(t *testing.T) {
	c := NewDefaultCompiler()
	graph := testGraph()
	graph.Edges = []knowledge.Relation{
		{From: "a<b", To: "c&d", Name: "depends<on>&", Score: 0.5},
	}

	ctx, err := c.Compile(context.Background(), graph, CompileConfig{Formats: []Format{FormatXML}})
	if err != nil {
		t.Fatalf("Compile error: %v", err)
	}
	out := ctx.Formats[FormatXML]

	// The whole document must parse as well-formed XML.
	dec := xml.NewDecoder(strings.NewReader(out))
	for {
		_, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("compiled XML is not well-formed (%%q attributes?): %v\noutput: %s", err, out)
		}
	}

	// The escaped values must round-trip through a real parser.
	parsed := struct {
		XMLName  xml.Name `xml:"knowledge_context"`
		Relation []struct {
			From string `xml:"from,attr"`
			To   string `xml:"to,attr"`
			Name string `xml:"name,attr"`
		} `xml:"relations>relation"`
	}{}
	if err := xml.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("xml.Unmarshal: %v", err)
	}
	if len(parsed.Relation) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(parsed.Relation))
	}
	r := parsed.Relation[0]
	if r.From != "a<b" || r.To != "c&d" || r.Name != "depends<on>&" {
		t.Errorf("relation attributes did not round-trip: %+v", r)
	}
}
