package taskfabric

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCheckpointEnvelopeIsAlwaysVersionStamped locks the WRITE side of the
// versioned-checkpoint contract.
//
// Regression (docs/reviews/0.3.1-final-deep-review.md finding F-7): five call
// sites built &CheckpointEnvelope{...} directly, leaving SchemaVersion at 0 —
// a value DecodeCheckpoint accepted as "legacy". The read side enforced a
// version gate that the write side routinely bypassed, so a future schema
// migration would have had to guess at those records.
//
// The check is a source scan rather than a behaviour test because the contract
// is "no producer constructs the envelope literally": a new call site added
// tomorrow is exactly the regression this is meant to catch, and a behaviour
// test would only cover the sites that exist today.
func TestCheckpointEnvelopeIsAlwaysVersionStamped(t *testing.T) {
	repoRoot := filepath.Join("..", "..", "..")

	var offenders []string
	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // unreadable subtree: not this test's concern
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "testdata", "vendor":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		for _, lit := range unversionedEnvelopeLiterals(stripGoComments(string(data))) {
			rel, relErr := filepath.Rel(repoRoot, path)
			if relErr != nil {
				rel = path
			}
			offenders = append(offenders, rel+": "+lit)
		}
		return nil
	})
	require.NoError(t, err)

	require.Empty(t, offenders,
		"checkpoint envelopes must set SchemaVersion (prefer NewCheckpointEnvelope); "+
			"a bare &CheckpointEnvelope{} persists schema_version 0 and defeats the decode-side version gate")
}

// stripGoComments removes line comments, block comments and string literals so
// the scan matches CODE, not prose: doc comments legitimately mention the
// envelope literal (and quoting it is how the contract is explained), and a
// comment must never be reported as a violation.
//
// String literals are dropped for the same reason in reverse — a URL inside a
// string contains "//", which a naive comment stripper would treat as the start
// of a comment and use to hide the rest of the line.
func stripGoComments(src string) string {
	const (
		normal = iota
		lineComment
		blockComment
		doubleQuoted
		rawQuoted
	)
	var b strings.Builder
	b.Grow(len(src))
	state := normal

	for i := 0; i < len(src); i++ {
		c := src[i]
		next := byte(0)
		if i+1 < len(src) {
			next = src[i+1]
		}
		switch state {
		case normal:
			switch {
			case c == '/' && next == '/':
				state = lineComment
				i++
			case c == '/' && next == '*':
				state = blockComment
				i++
			case c == '"':
				state = doubleQuoted
				b.WriteByte('"')
			case c == '`':
				state = rawQuoted
			default:
				b.WriteByte(c)
			}
		case lineComment:
			if c == '\n' {
				state = normal
				b.WriteByte(c)
			}
		case blockComment:
			if c == '*' && next == '/' {
				state = normal
				i++
			}
		case doubleQuoted:
			if c == '\\' {
				i++ // skip the escaped byte
				continue
			}
			if c == '"' {
				state = normal
			}
		case rawQuoted:
			if c == '`' {
				state = normal
			}
		}
	}
	return b.String()
}

// unversionedEnvelopeLiterals returns every `CheckpointEnvelope{...}` composite
// literal in src whose body does not mention SchemaVersion. Brace-matching (not
// a line regex) is used so a multi-line literal is captured whole and a field
// set on a later line still counts.
func unversionedEnvelopeLiterals(src string) []string {
	const marker = "CheckpointEnvelope{"

	var out []string
	offset := 0
	for {
		idx := strings.Index(src[offset:], marker)
		if idx < 0 {
			return out
		}
		start := offset + idx
		brace := start + len(marker) - 1 // index of the opening '{'

		depth := 0
		end := -1
		for i := brace; i < len(src); i++ {
			switch src[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					end = i
				}
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 {
			return out // unbalanced (unlikely): stop scanning this file
		}
		body := src[brace : end+1]
		if !strings.Contains(body, "SchemaVersion") {
			out = append(out, strings.ReplaceAll(body, "\n", " "))
		}
		offset = end + 1
	}
}

// TestNewCheckpointEnvelopeStampsCurrentVersion verifies the constructor itself
// writes the live schema version, not a hardcoded older one.
func TestNewCheckpointEnvelopeStampsCurrentVersion(t *testing.T) {
	env := NewCheckpointEnvelope(map[string]any{"k": "v"})

	require.Equal(t, CurrentCheckpointSchemaVersion, env.SchemaVersion)
	require.Equal(t, map[string]any{"k": "v"}, env.Payload)
}
