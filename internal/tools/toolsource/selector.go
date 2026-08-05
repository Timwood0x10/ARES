package toolsource

import (
	"context"
	"sort"
	"strings"

	core "github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// ToolSelector narrows the available tool pool to the subset exposed to the
// LLM for a single run. Selectors must be safe for concurrent use.
type ToolSelector interface {
	// Select returns the subset of available tools relevant for the given
	// user input. Callers must not mutate available.
	Select(ctx context.Context, input string, available []core.Tool) ([]core.Tool, error)
}

// AllSelector returns all available tools unchanged (default; zero behavior
// change vs. the legacy WithTools-only path). Tools are sorted by Name for
// deterministic ordering across runs.
type AllSelector struct{}

// Select returns available sorted by Name. The input slice is copied so the
// caller's slice is never mutated.
func (AllSelector) Select(_ context.Context, _ string, available []core.Tool) ([]core.Tool, error) {
	out := make([]core.Tool, len(available))
	copy(out, available)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name() < out[j].Name()
	})
	return out, nil
}

// TagSelector keeps tools whose TaggableTool tags match a keyword-derived
// query derived from the input. If the input yields no keywords, or if no
// tool matches, all available tools are returned (graceful fallback so the
// LLM never sees an empty toolset).
type TagSelector struct{}

// Select derives tag queries from input keywords and keeps TaggableTool
// implementations whose tag values contain any keyword.
func (TagSelector) Select(_ context.Context, input string, available []core.Tool) ([]core.Tool, error) {
	keywords := extractKeywords(input)
	if len(keywords) == 0 {
		return available, nil
	}
	matched := make([]core.Tool, 0)
	for _, t := range available {
		tt, ok := t.(core.TaggableTool)
		if !ok {
			continue
		}
		if tagsMatchKeywords(tt.Tags(), keywords) {
			matched = append(matched, t)
		}
	}
	if len(matched) == 0 {
		return available, nil
	}
	return matched, nil
}

// stopWords are common words excluded from keyword extraction so that inputs
// like "please do the math" reduce to ["math"].
var stopWords = map[string]bool{
	"the": true, "a": true, "an": true, "and": true, "or": true,
	"to": true, "of": true, "in": true, "on": true, "for": true,
	"is": true, "are": true, "was": true, "were": true, "be": true,
	"i": true, "you": true, "we": true, "it": true, "this": true,
	"that": true, "with": true, "from": true, "by": true, "at": true,
	"do": true, "does": true, "can": true, "could": true, "please": true,
	"me": true, "my": true, "how": true, "what": true, "who": true,
	"some": true, "get": true, "want": true, "need": true, "use": true,
	"using": true, "via": true, "into": true, "out": true,
}

// extractKeywords splits input on whitespace/punctuation, lowercases tokens,
// and drops stopwords and single-character tokens. Returns nil for empty
// input. Duplicate keywords are collapsed.
func extractKeywords(input string) []string {
	if strings.TrimSpace(input) == "" {
		return nil
	}
	lower := strings.ToLower(input)
	fields := strings.FieldsFunc(lower, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	keywords := make([]string, 0, len(fields))
	seen := make(map[string]bool, len(fields))
	for _, f := range fields {
		if len(f) < 2 || stopWords[f] {
			continue
		}
		if !seen[f] {
			seen[f] = true
			keywords = append(keywords, f)
		}
	}
	if len(keywords) == 0 {
		return nil
	}
	return keywords
}

// tagsMatchKeywords reports whether any tag value contains any keyword
// (case-insensitive). Keywords are already lowercased by extractKeywords.
func tagsMatchKeywords(tags map[string]string, keywords []string) bool {
	for _, kw := range keywords {
		for _, v := range tags {
			if strings.Contains(strings.ToLower(v), kw) {
				return true
			}
		}
	}
	return false
}
