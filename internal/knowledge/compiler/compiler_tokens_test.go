package compiler_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/compiler"
)

// TestCompileContext_ReportsTokensSaved pins that Compile measures the compiled
// context against the raw source material: RawTokens reflects the nodes' raw
// text, TokensSaved is exactly RawTokens-OutputTokens, and compacting verbose
// raw records into short summaries yields a positive saving.
func TestCompileContext_ReportsTokensSaved(t *testing.T) {
	t.Parallel()

	// Verbose raw bytes, terse summary: the compiled prompt uses the summary,
	// so the output must be far smaller than the raw baseline.
	verbose := strings.Repeat("the quick brown fox jumps over the lazy dog. ", 50)
	graph := &knowledge.WorkingGraph{
		Nodes: map[string]*knowledge.KnowledgeObject{
			"n1": {ID: "n1", Type: knowledge.ObjectDecision, Summary: "chose Redis", Raw: []byte(verbose)},
			"n2": {ID: "n2", Type: knowledge.ObjectDecision, Summary: "chose Postgres", Raw: []byte(verbose)},
		},
	}

	c := compiler.NewDefaultCompiler()
	compiled, err := c.Compile(context.Background(), graph,
		compiler.CompileConfig{Formats: []compiler.Format{compiler.FormatPrompt}})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	m := compiled.Metrics
	if m.RawTokens <= 0 {
		t.Fatalf("RawTokens should be positive, got %d", m.RawTokens)
	}
	if m.OutputTokens <= 0 {
		t.Fatalf("OutputTokens should be positive, got %d", m.OutputTokens)
	}
	if want := m.RawTokens - m.OutputTokens; m.TokensSaved != want {
		t.Errorf("TokensSaved = %d, want RawTokens-OutputTokens = %d", m.TokensSaved, want)
	}
	if m.TokensSaved <= 0 {
		t.Errorf("compacting verbose raw into summaries should save tokens, got TokensSaved=%d (raw=%d output=%d)",
			m.TokensSaved, m.RawTokens, m.OutputTokens)
	}
}

// TestCompileContext_RawTokensFallbackOrder pins the raw baseline's source
// precedence: Raw bytes win, else Normalized, else Summary. A node with no Raw
// but a long Normalized must still contribute a raw baseline.
func TestCompileContext_RawTokensFallbackOrder(t *testing.T) {
	t.Parallel()

	normalized := strings.Repeat("normalized sentence. ", 40)
	graph := &knowledge.WorkingGraph{
		Nodes: map[string]*knowledge.KnowledgeObject{
			"n1": {ID: "n1", Summary: "short", Normalized: normalized},
		},
	}

	c := compiler.NewDefaultCompiler()
	compiled, err := c.Compile(context.Background(), graph,
		compiler.CompileConfig{Formats: []compiler.Format{compiler.FormatPrompt}})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if compiled.Metrics.RawTokens <= 0 {
		t.Errorf("RawTokens should count Normalized when Raw is absent, got %d", compiled.Metrics.RawTokens)
	}
}

// TestCompileContext_EmptyGraphZeroSaving pins the degenerate case: no nodes
// means no raw baseline, no output, and a zero (not negative) saving.
func TestCompileContext_EmptyGraphZeroSaving(t *testing.T) {
	t.Parallel()

	graph := &knowledge.WorkingGraph{Nodes: map[string]*knowledge.KnowledgeObject{}}
	c := compiler.NewDefaultCompiler()
	compiled, err := c.Compile(context.Background(), graph,
		compiler.CompileConfig{Formats: []compiler.Format{compiler.FormatPrompt}})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if compiled.Metrics.RawTokens != 0 {
		t.Errorf("empty graph RawTokens = %d, want 0", compiled.Metrics.RawTokens)
	}
	if compiled.Metrics.TokensSaved != compiled.Metrics.RawTokens-compiled.Metrics.OutputTokens {
		t.Errorf("TokensSaved invariant broken: saved=%d raw=%d output=%d",
			compiled.Metrics.TokensSaved, compiled.Metrics.RawTokens, compiled.Metrics.OutputTokens)
	}
}

// TestCompileContext_TokensSavedUsesPrimaryFormat pins that the saving is
// measured against the FIRST requested format only. OutputTokens sums every
// requested format, so basing the saving on it would understate what a consumer
// actually saves (it would read as if all formats were sent to the model).
func TestCompileContext_TokensSavedUsesPrimaryFormat(t *testing.T) {
	t.Parallel()

	verbose := strings.Repeat("the quick brown fox jumps over the lazy dog. ", 50)
	graph := &knowledge.WorkingGraph{
		Nodes: map[string]*knowledge.KnowledgeObject{
			"n1": {ID: "n1", Type: knowledge.ObjectDecision, Summary: "chose Redis", Raw: []byte(verbose)},
		},
	}
	c := compiler.NewDefaultCompiler()
	ctx := context.Background()

	primaryOnly, err := c.Compile(ctx, graph,
		compiler.CompileConfig{Formats: []compiler.Format{compiler.FormatPrompt}})
	if err != nil {
		t.Fatalf("Compile primary-only: %v", err)
	}
	multi, err := c.Compile(ctx, graph,
		compiler.CompileConfig{Formats: []compiler.Format{compiler.FormatPrompt, compiler.FormatJSON}})
	if err != nil {
		t.Fatalf("Compile multi-format: %v", err)
	}

	if multi.Metrics.OutputTokens <= primaryOnly.Metrics.OutputTokens {
		t.Fatalf("precondition failed: multi-format OutputTokens %d must exceed primary-only %d",
			multi.Metrics.OutputTokens, primaryOnly.Metrics.OutputTokens)
	}
	if multi.Metrics.TokensSaved != primaryOnly.Metrics.TokensSaved {
		t.Errorf("TokensSaved must follow the primary format: multi=%d primary-only=%d",
			multi.Metrics.TokensSaved, primaryOnly.Metrics.TokensSaved)
	}
	if want := multi.Metrics.RawTokens - primaryOnly.Metrics.OutputTokens; multi.Metrics.TokensSaved != want {
		t.Errorf("TokensSaved = %d, want RawTokens-primaryOutputTokens = %d", multi.Metrics.TokensSaved, want)
	}
}

// TestCompileContext_NoFormatZeroSaving pins the degenerate branch: with no
// format compiled there is nothing to compare against the raw baseline, so the
// saving stays zero rather than reporting the whole baseline as saved.
func TestCompileContext_NoFormatZeroSaving(t *testing.T) {
	t.Parallel()

	graph := &knowledge.WorkingGraph{
		Nodes: map[string]*knowledge.KnowledgeObject{
			"n1": {ID: "n1", Type: knowledge.ObjectDecision, Summary: "chose Redis", Raw: []byte("raw text here")},
		},
	}
	compiled, err := compiler.NewDefaultCompiler().Compile(context.Background(), graph,
		compiler.CompileConfig{})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if compiled.Metrics.TokensSaved != 0 {
		t.Errorf("no-format compile TokensSaved = %d, want 0", compiled.Metrics.TokensSaved)
	}
}

// TestCompileContext_RawTokensIgnoresNonUTF8Raw pins the raw baseline's binary
// guard: Raw holding non-text bytes (an attachment, a bytea column) must not be
// counted as raw text, which would inflate the baseline and overstate the
// saving. The baseline falls back to Normalized instead.
func TestCompileContext_RawTokensIgnoresNonUTF8Raw(t *testing.T) {
	t.Parallel()

	normalized := strings.Repeat("normalized sentence. ", 40)
	c := compiler.NewDefaultCompiler()
	ctx := context.Background()
	cfg := compiler.CompileConfig{Formats: []compiler.Format{compiler.FormatPrompt}}

	textOnly, err := c.Compile(ctx, &knowledge.WorkingGraph{
		Nodes: map[string]*knowledge.KnowledgeObject{
			"n1": {ID: "n1", Summary: "short", Normalized: normalized},
		},
	}, cfg)
	if err != nil {
		t.Fatalf("Compile text-only: %v", err)
	}

	withBinaryRaw, err := c.Compile(ctx, &knowledge.WorkingGraph{
		Nodes: map[string]*knowledge.KnowledgeObject{
			"n1": {ID: "n1", Summary: "short", Normalized: normalized,
				Raw: []byte{0xff, 0xfe, 0x00, 0x01, 0x80, 0x81}},
		},
	}, cfg)
	if err != nil {
		t.Fatalf("Compile with binary Raw: %v", err)
	}

	if textOnly.Metrics.RawTokens <= 0 {
		t.Fatalf("baseline should count Normalized, got %d", textOnly.Metrics.RawTokens)
	}
	if withBinaryRaw.Metrics.RawTokens != textOnly.Metrics.RawTokens {
		t.Errorf("non-UTF8 Raw must not count toward the baseline: with-binary=%d text-only=%d",
			withBinaryRaw.Metrics.RawTokens, textOnly.Metrics.RawTokens)
	}
}
