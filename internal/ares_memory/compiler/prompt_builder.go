// Package compiler — PromptBuilder renders a KM SubGraph into a structured
// prompt context block. It supports multiple output formats (Markdown, XML,
// JSON) and is template-driven so different LLM providers (Claude, GPT,
// Gemini) can use different templates without changing the data layer.
package compiler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"
)

// PromptFormat defines the output format for the rendered prompt.
type PromptFormat int

const (
	FormatMarkdown PromptFormat = iota
	FormatXML
	FormatJSON
)

// String returns the human-readable name of the format.
func (f PromptFormat) String() string {
	switch f {
	case FormatMarkdown:
		return "markdown"
	case FormatXML:
		return "xml"
	case FormatJSON:
		return "json"
	default:
		return fmt.Sprintf("unknown(%d)", int(f))
	}
}

// PromptBuilder renders a KM SubGraph into a structured prompt context block.
// It is template-driven: different templates can be used for different LLM
// providers (Claude, GPT, Gemini) without changing the data layer.
//
// Usage:
//
//	subGraph := km.ToSubGraph(NodeDecision, NodeConstraint, ...)
//	pb := NewPromptBuilder(DefaultPromptTemplate)
//	context, err := pb.Render(subGraph, FormatMarkdown)
type PromptBuilder struct {
	template *template.Template
}

// PromptTemplateData holds the data passed to the prompt template.
type PromptTemplateData struct {
	Goals         []Node
	Decisions     []Node
	Constraints   []Node
	Tradeoffs     []Node
	OpenQuestions []Node
	Facts         []Node
	Entities      []Node
	Tasks         []Node
	Evidence      []Node
	Memory        []Node
	SourceCount   int
	Version       int
}

// DefaultPromptTemplate is the default Markdown template for rendering a KM
// SubGraph into a structured context block for LLM consumption.
const DefaultPromptTemplate = `## Context
{{- if .Goals }}
### Goals
{{ range .Goals }}- {{ index .Attributes "objective" | default (index .Attributes "name" | default .ID) }}
{{ end }}{{ end }}
{{- if .Decisions }}
### Decisions
{{ range .Decisions }}- {{ index .Attributes "choice" | default .ID }}{{ if index .Attributes "rejection" }} (rejected: {{ index .Attributes "rejection" }}){{ end }}
{{ end }}{{ end }}
{{- if .Constraints }}
### Constraints
{{ range .Constraints }}- {{ index .Attributes "name" | default .ID }}
{{ end }}{{ end }}
{{- if .Tradeoffs }}
### Tradeoffs
{{ range .Tradeoffs }}- {{ index .Attributes "name" | default .ID }}
{{ end }}{{ end }}
{{- if .OpenQuestions }}
### Open Questions
{{ range .OpenQuestions }}- {{ index .Attributes "name" | default .ID }}
{{ end }}{{ end }}
{{- if .Facts }}
### Key Facts
{{ range .Facts }}- {{ index .Attributes "subject" }} {{ index .Attributes "predicate" }} {{ index .Attributes "object" }}
{{ end }}{{ end }}
{{- if .Entities }}
### Entities
{{ range .Entities }}- {{ index .Attributes "name" | default .ID }} ({{ index .Attributes "type" | default "unknown" }})
{{ end }}{{ end }}
{{- if .Memory }}
### Relevant Memory
{{ range .Memory }}- {{ index .Attributes "name" | default .ID }}
{{ end }}{{ end }}
`

// NewPromptBuilder creates a new PromptBuilder with the given template.
//
// Args:
//
//	tmpl — Go template string for rendering. Use DefaultPromptTemplate for
//	the default Markdown output.
//
// Returns:
//
//	*PromptBuilder — the configured PromptBuilder. Always non-nil.
func NewPromptBuilder(tmpl string) *PromptBuilder {
	t := template.New("prompt").Funcs(template.FuncMap{
		"default": func(def, val any) any {
			if val != nil && val != "" {
				return val
			}
			return def
		},
	})
	// Parse the template; if it fails, use a minimal fallback.
	parsed, err := t.Parse(tmpl)
	if err != nil {
		parsed = template.Must(template.New("prompt").Parse(
			"## Context\n{{ range .Decisions }}- {{ .Name }}\n{{ end }}"))
	}
	return &PromptBuilder{template: parsed}
}

// Render renders a SubGraph into a structured prompt context block.
//
// Args:
//
//	subGraph — the SubGraph to render (from KM.ToSubGraph or Selector).
//	format — the output format (Markdown, XML, or JSON).
//
// Returns:
//
//	string — the rendered prompt context block.
//	error — non-nil if rendering fails.
func (pb *PromptBuilder) Render(subGraph *SubGraph, format PromptFormat) (string, error) {
	if subGraph == nil {
		return "", fmt.Errorf("prompt builder: subGraph must not be nil")
	}

	data := pb.buildTemplateData(subGraph)

	switch format {
	case FormatMarkdown:
		return pb.renderMarkdown(data)
	case FormatXML:
		return pb.renderXML(data)
	case FormatJSON:
		return pb.renderJSON(data)
	default:
		return "", fmt.Errorf("prompt builder: unsupported format %s", format)
	}
}

// buildTemplateData groups nodes by type for the template.
func (pb *PromptBuilder) buildTemplateData(subGraph *SubGraph) PromptTemplateData {
	data := PromptTemplateData{
		SourceCount: len(subGraph.Nodes),
	}
	for _, n := range subGraph.Nodes {
		switch n.Type {
		case NodeGoal:
			data.Goals = append(data.Goals, *n)
		case NodeDecision:
			data.Decisions = append(data.Decisions, *n)
		case NodeConstraint:
			data.Constraints = append(data.Constraints, *n)
		case NodeTradeoff:
			data.Tradeoffs = append(data.Tradeoffs, *n)
		case NodeQuestion:
			data.OpenQuestions = append(data.OpenQuestions, *n)
		case NodeFact:
			data.Facts = append(data.Facts, *n)
		case NodeEntity:
			data.Entities = append(data.Entities, *n)
		case NodeTask:
			data.Tasks = append(data.Tasks, *n)
		case NodeEvidence:
			data.Evidence = append(data.Evidence, *n)
		case NodeMemory:
			data.Memory = append(data.Memory, *n)
		}
	}
	return data
}

// renderMarkdown renders the template data as Markdown.
func (pb *PromptBuilder) renderMarkdown(data PromptTemplateData) (string, error) {
	var buf bytes.Buffer
	if err := pb.template.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("prompt builder: render markdown: %w", err)
	}
	return buf.String(), nil
}

// renderXML renders the template data as XML.
func (pb *PromptBuilder) renderXML(data PromptTemplateData) (string, error) {
	var b strings.Builder
	b.WriteString("<context>\n")
	writeXMLSection(&b, "goals", data.Goals, "goal")
	writeXMLSection(&b, "decisions", data.Decisions, "decision")
	writeXMLSection(&b, "constraints", data.Constraints, "constraint")
	writeXMLSection(&b, "tradeoffs", data.Tradeoffs, "tradeoff")
	writeXMLSection(&b, "questions", data.OpenQuestions, "question")
	writeXMLSection(&b, "facts", data.Facts, "fact")
	writeXMLSection(&b, "entities", data.Entities, "entity")
	writeXMLSection(&b, "memory", data.Memory, "memory")
	b.WriteString("</context>\n")
	return b.String(), nil
}

func writeXMLSection(b *strings.Builder, sectionName string, nodes []Node, tagName string) {
	if len(nodes) == 0 {
		return
	}
	fmt.Fprintf(b, "  <%s>\n", sectionName)
	for _, n := range nodes {
		display := n.ID
		if name, ok := n.Attributes["name"].(string); ok {
			display = name
		}
		fmt.Fprintf(b, "    <%s>%s</%s>\n", tagName, escapeXML(display), tagName)
	}
	fmt.Fprintf(b, "  </%s>\n", sectionName)
}

func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}

// renderJSON renders the template data as JSON.
func (pb *PromptBuilder) renderJSON(data PromptTemplateData) (string, error) {
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return "", fmt.Errorf("prompt builder: render json: %w", err)
	}
	return string(b), nil
}

// DefaultPromptContext returns a pre-built prompt context string from a KM
// SubGraph using the default Markdown template. This is a convenience wrapper
// for the common case.
//
// Args:
//
//	subGraph — the SubGraph to render.
//
// Returns:
//
//	string — the rendered prompt context block.
//	error — non-nil if rendering fails.
func DefaultPromptContext(subGraph *SubGraph) (string, error) {
	pb := NewPromptBuilder(DefaultPromptTemplate)
	return pb.Render(subGraph, FormatMarkdown)
}
