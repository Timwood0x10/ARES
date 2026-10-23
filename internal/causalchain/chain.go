// Package causalchain keeps the causal chain of an agent step — WHY it acted,
// WHAT it did, WHAT it saw, and the edge that binds the step to the decision it
// was made from — in a form that survives compression.
//
// The problem it exists for: a conversation history is not a flat list of
// strings, it is a chain of decisions. Compressing it by cutting bytes off every
// message destroys the one irreplaceable part (the intent behind an action)
// while wasting the budget on the part that is cheap to regenerate (a tool
// output that can simply be read again). This package splits the chain into the
// three components that need DIFFERENT treatment:
//
//	intent      (why)          prose, small, irreplaceable → keep, normalized
//	action+obs  (what/what saw) bulk, huge, regenerable    → keep a replayable minimum
//	edge        (depends on)   structure, one id           → keep, never drop half a pair
//
// An Observation is therefore not a truncation: it is a canonical form carrying
// the SHAPE of the output (what it was), a content DIGEST (proof the original is
// identified exactly), and — when the caller has one — a REF (the node the full
// bytes can be re-read from).
//
// Two contracts, and a caller must know which one it holds:
//
//	replayable     shape+digest+REF. The reader can verify the content and fetch
//	               it back. Both production paths can reach this:
//	                 planner  ref = L2 node id, whose envelope holds the output;
//	                 chat     ref = artifact://<session>/<n>, minted by the
//	                          session store at WRITE time (see the artifact
//	                          table in runtime/memory/context) and readable
//	                          through the artifact_read tool.
//	identifiable   shape+digest, no ref. Verifies that the original existed and
//	               pins which bytes it was, but cannot fetch it — what a message
//	               stored before retention existed, or one the per-session byte
//	               cap evicted, degrades to.
//
// Claiming the second is the first is how a system starts planning on evidence
// it cannot recover, so the distinction is part of the type (see Observation.Ref)
// and of what String renders. The rule that keeps it honest: a ref is only ever
// COPIED from the layer that holds the bytes, never synthesized by a compressor.
package causalchain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Timwood0x10/ares/internal/truncate"
)

// DigestLen is how many hex characters of the content hash an Observation
// carries. 12 hex chars (48 bits) is short enough to be free in a prompt and
// long enough that two different tool outputs in one session cannot collide —
// the digest identifies content, it does not secure it.
const DigestLen = 12

// Intent is the bounded, normalized prose of a decision: why the agent acted.
// It is the component a flat compressor throws away, so it is also the one that
// gets the largest guaranteed budget here.
type Intent string

// NormalizeIntent canonicalizes model prose so the SAME reasoning costs the same
// tokens however it was formatted: whitespace runs collapse, markdown fences
// (carrying no reasoning, only bulk) collapse to a marker, and the result is cut
// at a sentence boundary rather than mid-word when it exceeds max runes.
//
// Cutting at a sentence boundary matters: a rationale chopped mid-clause reads
// as a different reason, and a successor quantum reasons from what it reads.
func NormalizeIntent(s string, max int) Intent {
	trimmed := collapseWhitespace(stripFences(s))
	if trimmed == "" {
		return ""
	}
	if max <= 0 || utf8.RuneCountInString(trimmed) <= max {
		return Intent(trimmed)
	}
	// Prefer the last sentence terminator that still fits. SentenceRE-ish scan
	// without a regexp: the input is prose, and the delimiters are few.
	runes := []rune(trimmed)
	window := string(runes[:max])
	if cut := lastSentenceEnd(window); cut > 0 {
		return Intent(strings.TrimSpace(window[:cut]))
	}
	return Intent(truncate.WithEllipsis(trimmed, max))
}

// Action is what the agent did: a tool call. Args travel with it because a
// tool call without its arguments is not replayable ("read a file" is not the
// same action as "read /etc/passwd").
type Action struct {
	// Tool is the tool name as the model called it (no agent-type prefix).
	Tool string
	// CallID is the provider-side call id — the edge's other half.
	CallID string
	// Args is the decoded argument map. May be nil for a no-arg call.
	Args map[string]any
}

// Observation is the replayable minimum of a tool result.
type Observation struct {
	// Tool is the tool that produced it.
	Tool string
	// Shape is the canonical one-line description of WHAT the output was
	// (path+line count, command+exit code, url+status) — the part a human
	// reads to know whether the result is relevant at all.
	Shape string
	// Digest identifies the exact original bytes: sha256, first DigestLen chars.
	Digest string
	// Runes and Lines are the original size in the SAME unit the budget uses,
	// so "how much is missing" is never compared across units: Bytes is the
	// payload's true size, but a reader told "812B" while the budget spoke in
	// runes would misjudge a CJK output by 3x.
	Runes int
	// Bytes is the original size in bytes. Kept for accounting and because the
	// digest is over bytes; it is not what the prompt renders.
	Bytes int
	// Lines is the original line count.
	Lines int
	// Key holds the few lines that carry decision-relevant signal (errors
	// first, then the head). Each entry is bounded and single-line.
	Key []string
	// Ref is where the full text can be re-read (node id / artifact id).
	//
	// It is EMPTY when the caller has no addressable store to point at, and that
	// is a contract difference, not an oversight:
	//
	//	planner path  ref = node id, resolvable (the node's envelope holds the
	//	              output) → digest to verify, ref to recover.
	//	chat path     ref = artifact://<session>/<n>, STAMPED BY THE WRITER that
	//	              retained the bytes. The compressor only copies it; it has
	//	              neither the session identity nor a place to put bytes, so a
	//	              ref it invented would resolve to nothing.
	//
	// No ref means the observation is only IDENTIFIABLE: the digest proves the
	// original existed and pins which bytes it was, but nothing can fetch it —
	// the honest degradation for a message stored before retention, or one the
	// byte cap evicted.
	//
	// Do not fabricate a ref to close that gap: a pointer nobody can dereference
	// is worse than an honest digest, because it reads as recoverable.
	Ref string
}

// ObservationBudgetVerbatim is the budget that disables normalization: the
// output is replayed exactly as it came back.
//
// It exists so the two remaining "0 means verbatim" sites — this package's
// RenderObservation and the planner's evidence view — spell the intent instead
// of a bare literal: a single flipped zero silently turns compression on or off.
// The planner's CONFIGURED budget no longer shares this ambiguity; its zero
// (and the old negative sentinel) moved into agentfabric's ObservationReplayPolicy,
// so no deps-side caller can select "verbatim" with a number any more. The
// literal now appears in exactly one place — here.
const ObservationBudgetVerbatim = 0

// Unit is one atom of the causal chain: an intent, the action it produced, the
// observation that came back, and the edge to the node it was decided from.
//
// NodeID is the action's own id; Parent is the node this decision depended on.
// Both are kept even though a rendered prompt usually shows only the pair,
// because they are what makes the chain a graph rather than a transcript.
//
// Honest scope: the EDGE is preserved in the type (and rendered by Render, used
// by distillation/debugging), but the message list built for a provider does NOT
// emit it — the assistant/tool pairing plus message order already encode
// sequence, and an explicit edge line per action would cost tokens without
// adding information the model can act on. A fan-in node's Parent is only its
// first dependency, so the edge is a summary of the dependency list, not all of
// it. Do not describe the edge as "in the prompt": it is in the record.
type Unit struct {
	NodeID      string
	Parent      string
	Intent      Intent
	Action      Action
	Observation Observation
}

// RenderObservation returns the text to put in a prompt for output: the raw
// output verbatim while it fits in budget runes, otherwise the normalized form.
//
// The budget is in RUNES (what the model and the token accounting both see) and
// ObservationBudgetVerbatim means "never normalize".
//
// The budget is what makes this "cheap, not lossy": a small result is already
// replayable in full, so normalizing it would only cost fidelity for nothing.
// A result over budget is one the prompt cannot afford, and the normalized form
// keeps it answerable ("what did it return, roughly, and where is the rest").
func (o Observation) RenderObservation(output string, budget int) string {
	if budget <= ObservationBudgetVerbatim || utf8.RuneCountInString(output) <= budget {
		return output
	}
	return o.String()
}

// String renders the normalized observation.
//
// The trailing clause states which contract this observation supports — ref or
// no ref — instead of always claiming the bytes are recoverable. A reader that
// cannot tell the two apart will plan on evidence it cannot get back.
func (o Observation) String() string {
	var b strings.Builder
	if o.Shape != "" {
		b.WriteString(o.Shape)
		b.WriteString(" ")
	}
	fmt.Fprintf(&b, "<%dL/%dr sha256:%s", o.Lines, o.Runes, o.Digest)
	if o.Ref != "" {
		fmt.Fprintf(&b, " ref:%s", o.Ref)
	}
	b.WriteString(">")
	if len(o.Key) > 0 {
		b.WriteString(" head: ")
		b.WriteString(strings.Join(o.Key, " | "))
	}
	if o.Ref != "" {
		b.WriteString(" (normalized: shape+size+digest; full text re-readable from ref)")
	} else {
		b.WriteString(" (normalized: shape+size+digest; full text NOT addressable here)")
	}
	return b.String()
}

// Render renders a whole unit as one block. Used where a compact causal
// transcript is wanted (distillation, debugging, tests); the message list built
// for a provider keeps intent on the assistant message and observation on the
// tool message instead.
func (u Unit) Render() string {
	var b strings.Builder
	if u.Intent != "" {
		fmt.Fprintf(&b, "why: %s\n", u.Intent)
	}
	fmt.Fprintf(&b, "do: %s(%s)\n", u.Action.Tool, ArgShape(u.Action.Args, argPreviewMax))
	if u.Parent != "" {
		fmt.Fprintf(&b, "edge: %s -> %s\n", u.Parent, u.NodeID)
	}
	if u.Observation.Shape != "" || u.Observation.Digest != "" {
		fmt.Fprintf(&b, "saw: %s", u.Observation.String())
	}
	return strings.TrimRight(b.String(), "\n")
}

// argPreviewMax bounds one argument value inside an action description.
const argPreviewMax = 80

// keyLineMax bounds one observation key line.
const keyLineMax = 160

// keyLineCount is how many signal lines an observation keeps.
const keyLineCount = 4

// Observe builds the normalized form of a tool result. It never fails: an
// unparseable result degrades to a generic shape and keeps the digest, so the
// caller always gets a replayable description of what came back.
func Observe(tool string, args map[string]any, output, ref string) Observation {
	o := Observation{
		Tool:   tool,
		Runes:  utf8.RuneCountInString(output),
		Bytes:  len(output),
		Lines:  countLines(output),
		Digest: Digest(output),
		Ref:    ref,
		Shape:  shapeOf(tool, args, output),
		Key:    keyLines(output),
	}
	return o
}

// Digest returns the content fingerprint an Observation carries.
func Digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	full := hex.EncodeToString(sum[:])
	if len(full) <= DigestLen {
		return full
	}
	return full[:DigestLen]
}

// shapeOf deserializes the special-case knowledge of what a tool's output
// looks like into one canonical line. Only shapes that are cheap to detect are
// special-cased: everything else falls back to tool+arg-keys, which is still
// more informative than a byte count.
func shapeOf(tool string, args map[string]any, output string) string {
	argString := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := args[k].(string); ok && v != "" {
				return v
			}
		}
		return ""
	}
	switch tool {
	case "file_tools", "file_read", "file_write", "file_list", "file_search":
		op := argString("operation", "op")
		if op == "" {
			op = strings.TrimPrefix(tool, "file_")
		}
		path := argString("path", "file", "file_path")
		if path == "" {
			return fmt.Sprintf("%s %s", tool, op)
		}
		return fmt.Sprintf("%s %s path=%q", tool, op, path)
	case "code_runner", "shell", "exec", "bash":
		cmd := argString("command", "cmd", "script")
		return fmt.Sprintf("%s cmd=%q exit=%s", tool, truncate.Plain(cmd, argPreviewMax), exitCodeOf(output))
	case "http_request":
		return fmt.Sprintf("http %s %s status=%s", upperOr(argString("method"), "GET"),
			argString("url", "URL"), statusOf(output))
	case "web_scraper", "web_fetch":
		return fmt.Sprintf("scrape %s", argString("url", "URL"))
	case "knowledge_search", "memory_search", "search":
		return fmt.Sprintf("%s query=%q", tool, truncate.Plain(argString("query", "q"), argPreviewMax))
	case "":
		return "tool"
	default:
		keys := sortedKeys(args)
		if len(keys) == 0 {
			return tool
		}
		return fmt.Sprintf("%s(%s)", tool, strings.Join(keys, ","))
	}
}

// exitCodeOf extracts an exit code a runner printed, "?" when absent.
func exitCodeOf(output string) string {
	for _, line := range strings.Split(output, "\n") {
		t := strings.TrimSpace(line)
		lower := strings.ToLower(t)
		if strings.HasPrefix(lower, "exit code:") || strings.HasPrefix(lower, "exit_code:") {
			if _, v, ok := strings.Cut(t, ":"); ok {
				if v = strings.TrimSpace(v); v != "" {
					return v
				}
			}
		}
	}
	return "?"
}

// statusOf extracts an HTTP status from a response summary, "?" when absent.
func statusOf(output string) string {
	for _, line := range strings.Split(output, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(t), "status") {
			if _, v, ok := strings.Cut(t, ":"); ok {
				if v = strings.TrimSpace(v); v != "" {
					return v
				}
			}
		}
	}
	return "?"
}

// keyLines picks the lines worth keeping: error markers first (a failure the
// agent must react to is the highest-signal line in any output), then the head
// of the output. Deduplicated, order-preserving, each bounded to keyLineMax.
func keyLines(output string) []string {
	if output == "" {
		return nil
	}
	errors := make([]string, 0, keyLineCount)
	head := make([]string, 0, keyLineCount)
	seen := make(map[string]bool, keyLineCount)
	add := func(dst *[]string, line string) bool {
		line = truncate.Plain(strings.TrimSpace(line), keyLineMax)
		if line == "" || seen[line] || len(*dst) >= keyLineCount {
			return false
		}
		seen[line] = true
		*dst = append(*dst, line)
		return true
	}
	for _, line := range strings.Split(output, "\n") {
		if isSignalLine(line) {
			add(&errors, line)
		}
	}
	for _, line := range strings.Split(output, "\n") {
		if len(head) >= keyLineCount-len(errors) {
			break
		}
		if !isSignalLine(line) {
			add(&head, line)
		}
	}
	return append(errors, head...)
}

// isSignalLine reports whether a line carries a failure/attention marker. The
// match is deliberately case-insensitive and substring-based: a missed error
// line costs the agent a wrong next step, a false positive costs a few tokens.
func isSignalLine(line string) bool {
	lower := strings.ToLower(line)
	for _, marker := range []string{
		"error", "panic", "fatal", "failed", "failure", "not found",
		"no such file", "denied", "refused", "timeout", "timed out", "traceback",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// ArgShape renders an argument map as a compact, deterministic description:
// key=value for short values, key=<len/digest> for long ones. Deterministic
// ordering matters because a prompt cache and a diff both depend on it.
func ArgShape(args map[string]any, maxValue int) string {
	if len(args) == 0 {
		return ""
	}
	keys := sortedKeys(args)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		switch v := args[k].(type) {
		case string:
			parts = append(parts, fmt.Sprintf("%s=%q", k, truncate.Plain(v, maxValue)))
		case nil:
			parts = append(parts, k+"=null")
		case bool, float64, int, int64:
			parts = append(parts, fmt.Sprintf("%s=%v", k, v))
		default:
			parts = append(parts, fmt.Sprintf("%s=<%T>", k, v))
		}
	}
	return strings.Join(parts, ", ")
}

// ArgShapeJSON renders a JSON arguments string via ArgShape, degrading to the
// raw (bounded) text when the string is not a JSON object.
func ArgShapeJSON(argsJSON string, maxValue int) string {
	if argsJSON == "" {
		return ""
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return truncate.Plain(argsJSON, maxValue)
	}
	return ArgShape(args, maxValue)
}

// sortedKeys returns the map's keys in sorted order.
func sortedKeys(m map[string]any) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// collapseWhitespace folds every whitespace run into one space. A model's
// pretty-printed plan and the same plan on one line are the same reasoning and
// should cost the same tokens.
func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// stripFences replaces fenced code blocks with a marker. A fence inside a
// rationale is bulk, not reasoning: the SUCCESSOR needs the argument, not the
// snippet, and snippets are recoverable from the artifact they were pasted from.
func stripFences(s string) string {
	if !strings.Contains(s, "```") {
		return s
	}
	var b strings.Builder
	rest := s
	for {
		start := strings.Index(rest, "```")
		if start < 0 {
			b.WriteString(rest)
			return b.String()
		}
		b.WriteString(rest[:start])
		after := rest[start+3:]
		// Skip the fence's language tag line.
		if nl := strings.IndexByte(after, '\n'); nl >= 0 {
			after = after[nl+1:]
		}
		end := strings.Index(after, "```")
		if end < 0 {
			b.WriteString(" <code block>")
			return b.String()
		}
		b.WriteString(" <code block> ")
		rest = after[end+3:]
	}
}

// lastSentenceEnd returns the byte index just past the last sentence
// terminator in s, or 0 when there is none.
//
// A '.' between two digits is NOT a sentence end: cutting "upgrade to v3.14"
// there yields "upgrade to v3.1" — a reason that reads as a different, and
// wrong, reason, delivered to exactly the reader that reasons from it. The
// semicolon is excluded for the same class of reason: "…because A; B only
// follows if…" loses the clause that justifies the action.
func lastSentenceEnd(s string) int {
	cut := 0
	var prev rune
	for i, r := range s {
		switch r {
		case '.', '!', '?', '\n', '。', '！', '？':
			size := utf8.RuneLen(r)
			next, _ := utf8.DecodeRuneInString(s[i+size:])
			if r == '.' && unicode.IsDigit(prev) && unicode.IsDigit(next) {
				prev = r
				continue
			}
			cut = i + size
		}
		prev = r
	}
	return cut
}

// countLines counts lines the way a reader does: a trailing newline does not
// start another line.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// upperOr returns s upper-cased, or fallback when s is empty.
func upperOr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return strings.ToUpper(s)
}
