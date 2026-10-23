package causalchain

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNormalizeIntentKeepsTheReasonAndDropsTheBulk pins the intent contract: the
// same reasoning costs the same tokens however it was formatted, and a cut
// intent still ends where the model ended a thought.
func TestNormalizeIntentKeepsTheReasonAndDropsTheBulk(t *testing.T) {
	t.Run("whitespace is canonicalized", func(t *testing.T) {
		assert.Equal(t, Intent("check the parser first because the bug is there"),
			NormalizeIntent("  check the parser\n\nfirst   because\tthe bug is there  ", 200))
	})

	t.Run("fenced code collapses to a marker", func(t *testing.T) {
		got := NormalizeIntent("I need to see this:\n```go\nfunc main() {}\n```\nthen decide.", 200)
		assert.NotContains(t, string(got), "func main()")
		assert.Contains(t, string(got), "<code block>")
		assert.Contains(t, string(got), "then decide.")
	})

	t.Run("cut at a sentence boundary, not mid-clause", func(t *testing.T) {
		long := "The parser is the culprit. I will grep for the tokenizer before touching the lexer."
		got := string(NormalizeIntent(long, 40))
		assert.True(t, len([]rune(got)) <= 40, "bounded, got %q", got)
		assert.Equal(t, "The parser is the culprit.", got)
	})

	t.Run("empty stays empty", func(t *testing.T) {
		assert.Equal(t, Intent(""), NormalizeIntent("   \n\t ", 100))
	})
}

// TestObserveIsReplayable is the core anti-truncation claim: the normalized form
// names WHAT came back, PROVES which bytes it was, and says WHERE the rest is —
// so the observation can be verified and re-read instead of guessed at.
func TestObserveIsReplayable(t *testing.T) {
	output := "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(1) }\n"
	obs := Observe("file_tools", map[string]any{"operation": "read", "path": "/a/main.go"}, output, "sess/1/r2/file_tools/0")

	assert.Contains(t, obs.Shape, `read path="/a/main.go"`)
	assert.Equal(t, 5, obs.Lines)
	assert.Equal(t, len(output), obs.Bytes)
	assert.Equal(t, Digest(output), obs.Digest)
	assert.Len(t, obs.Digest, DigestLen)
	assert.Equal(t, "sess/1/r2/file_tools/0", obs.Ref)
	assert.NotEmpty(t, obs.Key, "the head must survive normalization")

	rendered := obs.String()
	assert.Contains(t, rendered, "sha256:")
	assert.Contains(t, rendered, "ref:sess/1/r2/file_tools/0")
	assert.Contains(t, rendered, `package main`)
}

// TestObserveDigestIdentifiesContent locks the one property that makes the
// digest worth carrying: same bytes, same digest; one byte different, different
// digest. Also asserts the digest is stable across calls (a prompt that
// changes between two identical reads would defeat caching).
func TestObserveDigestIdentifiesContent(t *testing.T) {
	a := Observe("code_runner", nil, "ok\n", "n1")
	b := Observe("code_runner", nil, "ok\n", "n1")
	c := Observe("code_runner", nil, "ok!\n", "n1")
	assert.Equal(t, a.Digest, b.Digest)
	assert.NotEqual(t, a.Digest, c.Digest)
}

// TestObserveKeyLinesPrioritizeFailures: an error line is the highest-signal
// thing in an output; a normalizer that keeps only the head would hide it.
func TestObserveKeyLinesPrioritizeFailures(t *testing.T) {
	output := strings.Join([]string{
		"running tests...",
		"ok  pkg/a",
		"FAIL pkg/b",
		"--- FAIL: TestX (0.01s)",
		"    x_test.go:12: boom",
		"ok  pkg/c",
	}, "\n")

	obs := Observe("code_runner", map[string]any{"command": "go test ./..."}, output, "n2")
	assert.NotEmpty(t, obs.Key)
	assert.Contains(t, strings.Join(obs.Key, "\n"), "FAIL", "a failure must survive normalization")
	assert.Contains(t, obs.Shape, "go test ./...")
}

// TestRenderObservationBudget is the "cheap, not lossy" rule: a small result is
// already replayable, so it is returned verbatim; only a result the prompt
// cannot afford is normalized.
func TestRenderObservationBudget(t *testing.T) {
	small := "line1\nline2\n"
	obs := Observe("file_tools", map[string]any{"path": "/small"}, small, "n3")
	assert.Equal(t, small, obs.RenderObservation(small, 100), "within budget: verbatim")

	big := strings.Repeat("x", 500)
	obs = Observe("file_tools", map[string]any{"path": "/big"}, big, "n3")
	got := obs.RenderObservation(big, 100)
	assert.NotEqual(t, big, got)
	assert.Contains(t, got, "sha256:")
	assert.Contains(t, got, "ref:n3")
}

// TestArgShapeIsDeterministic locks the argument rendering: keys sorted (cache
// and diff stability), long values bounded, structure not silently dropped.
func TestArgShapeIsDeterministic(t *testing.T) {
	args := map[string]any{"path": "/a.go", "operation": "read", "pattern": strings.Repeat("p", 200)}
	got := ArgShape(args, 40)
	assert.True(t, strings.HasPrefix(got, `operation="read", path="/a.go"`), got)
	assert.LessOrEqual(t, len([]rune(got)), 120)

	assert.Equal(t, `query="x"`, ArgShapeJSON(`{"query":"x"}`, 40))
	assert.Equal(t, "{not json", ArgShapeJSON("{not json", 40))
}

// TestUnitRenderKeepsTheEdge: the rendered unit must still say which node this
// decision hung off — that edge is the "graph" half of the causal chain.
func TestUnitRenderKeepsTheEdge(t *testing.T) {
	u := Unit{
		NodeID: "sess/1/r2/grep/0",
		Parent: "sess/1/r1/plan/0",
		Intent: "grep because the parser is suspected",
		Action: Action{Tool: "grep", CallID: "c1", Args: map[string]any{"query": "tokenizer"}},
		Observation: Observe("grep",
			map[string]any{"query": "tokenizer"}, "a.go:12: tokenizer\n", "sess/1/r2/grep/0"),
	}
	got := u.Render()
	assert.Contains(t, got, "why: grep because the parser is suspected")
	assert.Contains(t, got, `do: grep(query="tokenizer")`)
	assert.Contains(t, got, "edge: sess/1/r1/plan/0 -> sess/1/r2/grep/0")
	assert.Contains(t, got, "saw: ")
}

// TestObserveNeverFailsOnUnknownTool: an unknown tool must still produce a
// replayable description — a normalizer that returns "" would silently delete
// history.
func TestObserveNeverFailsOnUnknownTool(t *testing.T) {
	obs := Observe("mystery_tool", map[string]any{"b": 1, "a": "x"}, "whatever", "n4")
	assert.Equal(t, "mystery_tool(a,b)", obs.Shape)
	assert.NotEmpty(t, obs.Digest)
	assert.Equal(t, 1, obs.Lines)
}

// TestNormalizeIntentNeverCutsInsideANumber pins the delimiter rule: a '.' that
// sits between digits is a version decimal, not a sentence end. Cutting there
// yields "upgrade to v3.1" — a reason that reads as a different and wrong
// reason, handed to the successor that reasons from it.
func TestNormalizeIntentNeverCutsInsideANumber(t *testing.T) {
	long := "The crash is in the upgrade path. Please start by checking v3.14 because only that release changed the parser defaults."
	got := string(NormalizeIntent(long, 60))
	assert.True(t, len([]rune(got)) <= 60, "bounded: %q", got)
	for _, bad := range []string{"v3.1", "v3.1 "} {
		if strings.HasSuffix(got, bad) {
			t.Fatalf("cut inside a version number: %q", got)
		}
	}
	// A semicolon is a clause separator, not a sentence end: keeping only up to
	// it would drop the clause that justifies the action.
	got = string(NormalizeIntent("Grep the parser because the failure is there; the lexer is only reachable through it and the suite is slow.", 40))
	assert.NotEqual(t, "Grep the parser because the failure is there;", got)
	assert.False(t, strings.HasSuffix(got, ";"))
}

// TestObservationStringStatesItsContract: a normalized observation must say
// whether its full text is recoverable. Claiming "from ref" when no ref exists
// invites a successor to plan on evidence it cannot fetch.
func TestObservationStringStatesItsContract(t *testing.T) {
	body := strings.Repeat("line\n", 100)

	replayable := Observe("file_tools", map[string]any{"operation": "read", "path": "/a"}, body, "sess/1/r2/file_tools/0")
	assert.Contains(t, replayable.String(), "re-readable from ref")
	assert.Contains(t, replayable.String(), "ref:sess/1/r2/file_tools/0")

	identifiable := Observe("file_tools", map[string]any{"operation": "read", "path": "/a"}, body, "")
	assert.NotContains(t, identifiable.String(), "ref:")
	assert.Contains(t, identifiable.String(), "NOT addressable")
}

// TestObservationSizesAreReportedInTheBudgetUnit: budget is runes, so the size
// the model reads is runes too. A CJK output is ~3 bytes per rune — reporting
// bytes next to a rune budget misjudges "how much is missing" by 3x.
func TestObservationSizesAreReportedInTheBudgetUnit(t *testing.T) {
	cjk := strings.Repeat("错误", 50) // 100 runes, 300 bytes
	o := Observe("file_tools", map[string]any{"path": "/cjk"}, cjk, "n")

	assert.Equal(t, 100, o.Runes)
	assert.Equal(t, len(cjk), o.Bytes)
	assert.Contains(t, o.String(), "/100r", "the rendered size is in runes, like the budget")
	assert.NotContains(t, o.String(), "/300r")
}
