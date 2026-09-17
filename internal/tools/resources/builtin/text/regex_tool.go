package builtin

import (
	"context"
	"fmt"
	"regexp"
	"unicode/utf8"

	"github.com/Timwood0x10/ares/internal/tools/resources/base"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// RegexTool provides regex operations for text processing.
type RegexTool struct {
	*base.BaseTool
}

// NewRegexTool creates a new RegexTool.
func NewRegexTool() *RegexTool {
	params := &core.ParameterSchema{
		Type: "object",
		Properties: map[string]*core.Parameter{
			"operation": {
				Type:        "string",
				Description: "Operation to perform (match, extract, replace)",
				Enum:        []interface{}{"match", "extract", "replace"},
			},
			"text": {
				Type:        "string",
				Description: "Text to process",
			},
			"pattern": {
				Type:        "string",
				Description: "Regex pattern to use",
			},
			"flags": {
				Type:        "array",
				Description: "Regex flags (i: case-insensitive, m: multiline, s: dotall)",
			},
			"replacement": {
				Type:        "string",
				Description: "Replacement string (required for replace operation)",
			},
			"max_results": {
				Type:        "integer",
				Description: "Maximum number of results to return (default: 1000, hard cap: 10000)",
			},
		},
		Required: []string{"operation", "text", "pattern"},
	}

	return &RegexTool{
		BaseTool: base.NewBaseToolWithCapabilities("regex_tool", "Perform regex match, extract, and replace operations", core.CategoryCore, []core.Capability{core.CapabilityText}, params),
	}
}

// maxRegexInputSize limits the input text size for regex operations to prevent
// ReDoS attacks via catastrophic backtracking on very large inputs.
const maxRegexInputSize = 10 * 1024 * 1024 // 10MB

// defaultMaxRegexResults caps the number of matches returned when the caller
// does not specify max_results. -1 (unlimited) on a broad pattern over large
// input can produce millions of matches and exhaust memory.
const defaultMaxRegexResults = 1000

// maxRegexResultsCeiling is the hard cap applied regardless of the caller's
// max_results (#62): even an explicit -1 (unlimited) or an absurd value from
// an LLM must not be able to materialize an unbounded match slice.
const maxRegexResultsCeiling = 10000

// clampMaxResults normalizes the caller-supplied max_results: negative
// (including -1/unlimited) and over-ceiling values clamp to the ceiling.
func clampMaxResults(n int) int {
	if n < 0 || n > maxRegexResultsCeiling {
		return maxRegexResultsCeiling
	}
	return n
}

// Execute performs the regex operation.
func (t *RegexTool) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	operation, ok := params["operation"].(string)
	if !ok || operation == "" {
		return core.NewErrorResult("operation is required"), nil
	}

	text, ok := params["text"].(string)
	if !ok || text == "" {
		return core.NewErrorResult("text is required"), nil
	}

	// Limit input size to prevent ReDoS via catastrophic backtracking.
	if len(text) > maxRegexInputSize {
		return core.NewErrorResult(fmt.Sprintf("input text exceeds maximum size of %d bytes", maxRegexInputSize)), nil
	}

	pattern, ok := params["pattern"].(string)
	if !ok || pattern == "" {
		return core.NewErrorResult("pattern is required"), nil
	}

	// Limit pattern complexity to prevent ReDoS.
	if len(pattern) > 1000 {
		return core.NewErrorResult("regex pattern exceeds maximum length of 1000 characters"), nil
	}

	// Parse flags
	flags := getStringSlice(params, "flags")
	re, err := t.compileRegex(pattern, flags)
	if err != nil {
		return core.NewErrorResult(fmt.Sprintf("invalid regex pattern: %v", err)), nil
	}

	switch operation {
	case "match":
		maxResults := clampMaxResults(getInt(params, "max_results", defaultMaxRegexResults))
		return t.match(ctx, text, re, maxResults)
	case "extract":
		maxResults := clampMaxResults(getInt(params, "max_results", defaultMaxRegexResults))
		return t.extract(ctx, text, re, maxResults)
	case "replace":
		replacement, ok := params["replacement"].(string)
		if !ok || replacement == "" {
			return core.NewErrorResult("replacement is required for replace operation"), nil
		}
		return t.replace(ctx, text, re, replacement)
	default:
		return core.NewErrorResult(fmt.Sprintf("unsupported operation: %s", operation)), nil
	}
}

// compileRegex compiles a regex pattern with flags.
func (t *RegexTool) compileRegex(pattern string, flags []string) (*regexp.Regexp, error) {
	// Build regex flags
	var regexFlags string
	for _, flag := range flags {
		regexFlags += "(?" + flag + ")"
	}

	// If flags are specified, prepend them to pattern
	if regexFlags != "" {
		pattern = regexFlags + pattern
	}

	return regexp.Compile(pattern)
}

// match checks if the pattern matches the text. Empty matches are skipped
// (#62): an empty-matching pattern (e.g. "a*") produces one meaningless
// entry per position — millions on large input — so only non-empty matches
// are reported.
func (t *RegexTool) match(ctx context.Context, text string, re *regexp.Regexp, maxResults int) (core.Result, error) {
	// Non-empty matches only, and empty matches must not consume the
	// maxResults budget (FindAllString counts them against n, starving real
	// matches behind a run of empties).
	indexes := findAllNonEmptyIndexes(re, text, maxResults)

	results := make([]map[string]interface{}, 0, len(indexes))
	for _, idx := range indexes {
		results = append(results, map[string]interface{}{
			"match": text[idx[0]:idx[1]],
			"start": idx[0],
			"end":   idx[1],
		})
	}

	return core.NewResult(true, map[string]interface{}{
		"operation":   "match",
		"pattern":     re.String(),
		"matched":     len(results) > 0,
		"matches":     results,
		"match_count": len(results),
	}), nil
}

// extract extracts all matches using capturing groups. Entries whose full
// match is empty are skipped (#62 — see match).
func (t *RegexTool) extract(ctx context.Context, text string, re *regexp.Regexp, maxResults int) (core.Result, error) {
	// Iterate like findAllNonEmptyIndexes but with submatch support: empty
	// full matches are skipped WITHOUT consuming the maxResults budget.
	var extracted []map[string]interface{}
	pos := 0
	for pos <= len(text) && (maxResults <= 0 || len(extracted) < maxResults) {
		match := re.FindStringSubmatchIndex(text[pos:])
		if match == nil {
			break
		}
		start, end := match[0], match[1]
		adv := end
		if end > start {
			groups := make([]string, 0, len(match)/2)
			for i := 0; i < len(match); i += 2 {
				if match[i] < 0 {
					groups = append(groups, fmt.Sprintf("group_%d: ", i/2))
					continue
				}
				groups = append(groups, fmt.Sprintf("group_%d: %s", i/2, text[pos+match[i]:pos+match[i+1]]))
			}
			extracted = append(extracted, map[string]interface{}{
				"full_match": text[pos+start : pos+end],
				"groups":     groups,
				"count":      len(match) / 2,
			})
		} else {
			_, size := utf8.DecodeRuneInString(text[pos+adv:])
			if size == 0 {
				break
			}
			adv += size
		}
		pos += adv
	}

	if len(extracted) == 0 {
		return core.NewResult(true, map[string]interface{}{
			"operation":   "extract",
			"pattern":     re.String(),
			"matched":     false,
			"extracted":   []interface{}{},
			"match_count": 0,
		}), nil
	}

	return core.NewResult(true, map[string]interface{}{
		"operation":   "extract",
		"pattern":     re.String(),
		"matched":     true,
		"extracted":   extracted,
		"match_count": len(extracted),
	}), nil
}

// replace replaces all matches with the replacement string.
func (t *RegexTool) replace(ctx context.Context, text string, re *regexp.Regexp, replacement string) (core.Result, error) {
	// Count non-empty matches without materializing the full match slice
	// (#62): FindAllString(text, -1) on an empty-matching pattern builds one
	// entry per position — a multi-hundred-MB spike on 10MB inputs.
	matchCount := countNonEmptyMatches(re, text)

	// Perform replacement
	result := re.ReplaceAllString(text, replacement)

	return core.NewResult(true, map[string]interface{}{
		"operation":    "replace",
		"pattern":      re.String(),
		"replacement":  replacement,
		"original":     text,
		"result":       result,
		"match_count":  matchCount,
		"replacements": matchCount,
	}), nil
}

// countNonEmptyMatches counts non-empty matches iteratively. An empty match
// advances by one rune so the scan always makes progress (mirroring the
// stdlib's FindAll advancement rule) without collecting a slice.
func countNonEmptyMatches(re *regexp.Regexp, text string) int {
	count := 0
	pos := 0
	for pos <= len(text) {
		loc := re.FindStringIndex(text[pos:])
		if loc == nil {
			break
		}
		if loc[1] > loc[0] {
			count++
		}
		adv := loc[1]
		if adv == loc[0] {
			// Empty match: advance one rune to avoid re-matching here forever.
			_, size := utf8.DecodeRuneInString(text[pos+adv:])
			if size == 0 {
				break // end of text
			}
			adv += size
		}
		pos += adv
	}
	return count
}

// findAllNonEmptyIndexes iterates like countNonEmptyMatches but returns the
// [start,end) indexes of the first maxResults NON-EMPTY matches. Empty
// matches never consume the budget, so an empty-able pattern (a?, x*) whose
// real matches sit behind a run of empties is not starved by FindAllString's
// limit — which counts empties against n.
func findAllNonEmptyIndexes(re *regexp.Regexp, text string, maxResults int) [][]int {
	var out [][]int
	pos := 0
	for pos <= len(text) && (maxResults <= 0 || len(out) < maxResults) {
		loc := re.FindStringIndex(text[pos:])
		if loc == nil {
			break
		}
		adv := loc[1]
		if loc[1] > loc[0] {
			out = append(out, []int{pos + loc[0], pos + loc[1]})
		} else {
			_, size := utf8.DecodeRuneInString(text[pos+adv:])
			if size == 0 {
				break
			}
			adv += size
		}
		pos += adv
	}
	return out
}

func (t *RegexTool) IsIdempotent() bool { return true }
