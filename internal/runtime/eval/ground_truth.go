package eval

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/Timwood0x10/ares/internal/truncate"
)

// Check kinds a corpus case may declare in TestCase.Checks.
//
// A kind is a DECLARATION, never a heuristic: the deterministic layer runs
// exactly what the case asked for, so a case that declares nothing stays as
// judge-scored as it was before this layer existed (see groundTruthChecks). That
// is deliberate. Inferring a check from the mere presence of expected_output
// would retro-fit the whole legacy corpus in one commit and could floor answers
// that were written as free text on purpose — a floor is a hard statement, so it
// must be a stated one.
const (
	// CheckExact requires the answer to BE the expected text, compared after
	// normalization (case and whitespace). Use it when the answer has one
	// canonical spelling.
	CheckExact = "exact"
	// CheckContains requires the expected text to APPEAR in the answer, same
	// normalization. Use it when a fact has to reach the answer inside a
	// sentence, where exact equality would measure formatting instead.
	CheckContains = "contains"
	// CheckNumeric requires the answer to be a number equal to the expected one
	// within a relative tolerance. Use it for computation, where trailing digits
	// and formatting must not decide correctness.
	CheckNumeric = "numeric"
	// CheckTools requires every expected tool to have actually RUN. Use it where
	// the point of the case is that work was delegated rather than narrated.
	CheckTools = "tools"
	// CheckNoError requires the run to have completed without an executor error.
	CheckNoError = "no_error"
)

// knownCheckKinds is the set of kinds this package can run. Kept as a map so
// KnownCheckKind and the dispatch in groundTruthCheck cannot drift: the corpus
// lint test asserts every declared kind is in here, and an unknown kind resolves
// to "missing" (indeterminate) at runtime rather than to a pass.
var knownCheckKinds = map[string]bool{
	CheckExact:    true,
	CheckContains: true,
	CheckNumeric:  true,
	CheckTools:    true,
	CheckNoError:  true,
}

// KnownCheckKind reports whether kind is a check this package can run.
func KnownCheckKind(kind string) bool {
	return knownCheckKinds[kind]
}

// GroundTruthVerdict evaluates a case's declared deterministic checks against an
// observed result and returns the verdict plus the individual checks.
//
// It is the exported entry point for callers that hold a case and an output but no
// TestResult — the arena's paired A/B comparison is exactly that shape — so the SAME
// checks can floor a score outside the LLM judge. A second implementation of "is this
// answer right" would be the thing this package exists to prevent: two rules for one
// question, drifting apart.
//
// A case that declares nothing returns VerdictUncertain, never a pass: an unasked
// question must not read as satisfied.
func GroundTruthVerdict(tc TestCase, result TestResult) (Verdict, []ResultCheck) {
	outcome := evaluateGroundTruth(tc, result, "")
	return outcome.Verdict, outcome.Checks
}

// CountAnchoredCases reports how many cases in a suite declared at least one
// deterministic check.
//
// It is the suite-level answer to "is this evaluation verifiable at all", which a
// caller needs BEFORE running it: a suite that declares nothing is scored entirely by
// the judge, so its accuracy means opinion and nothing else — and that is a property
// of the corpus, knowable at wiring time rather than inferable from the scores.
func CountAnchoredCases(suite TestSuite) int {
	anchored := 0
	for _, tc := range suite.TestCases {
		if len(tc.Checks) > 0 {
			anchored++
		}
	}
	return anchored
}

// numeric tolerances for CheckNumeric. Relative first, because the answers this
// check exists for (a product, a sum) are large enough that an absolute epsilon
// would call a wrong digit sequence equal; the absolute arm only catches the
// both-sides-near-zero case.
const (
	numericRelTol = 1e-6
	numericAbsTol = 1e-9
)

// groundTruthChecks runs the checks a case DECLARED, in declaration order, and
// returns nil when it declared none.
//
// The order is preserved so the persisted evidence lists the checks the way the
// corpus author wrote them, which is how a failing run is read back.
func groundTruthChecks(tc TestCase, result TestResult) []ResultCheck {
	if len(tc.Checks) == 0 {
		return nil
	}
	checks := make([]ResultCheck, 0, len(tc.Checks))
	for _, kind := range tc.Checks {
		checks = append(checks, groundTruthCheck(strings.TrimSpace(kind), tc, result))
	}
	return checks
}

// groundTruthCheck dispatches one declared kind. An unknown kind — or a known
// kind whose input the case does not carry — resolves to StatusMissing, which
// ResultVerifier maps to VerdictUncertain: the bottom layer must PROVE success,
// so a check it cannot run may never be counted as one.
func groundTruthCheck(kind string, tc TestCase, result TestResult) ResultCheck {
	switch kind {
	case CheckExact:
		return exactCheck(tc, result)
	case CheckContains:
		return containsCheck(tc, result)
	case CheckNumeric:
		return numericCheck(tc, result)
	case CheckTools:
		return toolsCheck(tc, result)
	case CheckNoError:
		return noErrorCheck(result)
	default:
		return ResultCheck{
			Name:   kind,
			Status: StatusMissing,
			Detail: fmt.Sprintf("unknown check kind %q: this package cannot prove it, so it is not counted as a pass", kind),
		}
	}
}

// exactCheck compares the answer to expected_output after normalization.
func exactCheck(tc TestCase, result TestResult) ResultCheck {
	const name = CheckExact
	if strings.TrimSpace(tc.ExpectedOutput) == "" {
		return ResultCheck{Name: name, Status: StatusMissing, Detail: "check declares no expected_output to compare against"}
	}
	got, want := normalizeAnswer(result.ActualOutput), normalizeAnswer(tc.ExpectedOutput)
	if got == want {
		return ResultCheck{Name: name, Status: StatusPassed, Detail: fmt.Sprintf("answer is the expected text (%q)", truncate.WithEllipsis(want, 120))}
	}
	return ResultCheck{
		Name:   name,
		Status: StatusFailed,
		Detail: fmt.Sprintf("answer %q != expected %q (compared normalized)", truncate.WithEllipsis(got, 120), truncate.WithEllipsis(want, 120)),
	}
}

// containsCheck requires the expected text to appear in the answer, normalized
// the same way, so a correct fact wrapped in a sentence still passes while a
// missing fact does not.
func containsCheck(tc TestCase, result TestResult) ResultCheck {
	const name = CheckContains
	want := normalizeAnswer(tc.ExpectedOutput)
	if want == "" {
		return ResultCheck{Name: name, Status: StatusMissing, Detail: "check declares no expected_output to look for"}
	}
	got := normalizeAnswer(result.ActualOutput)
	if strings.Contains(got, want) {
		return ResultCheck{Name: name, Status: StatusPassed, Detail: fmt.Sprintf("answer contains %q", truncate.WithEllipsis(want, 120))}
	}
	return ResultCheck{
		Name:   name,
		Status: StatusFailed,
		Detail: fmt.Sprintf("answer does not contain %q: %q", truncate.WithEllipsis(want, 120), truncate.WithEllipsis(got, 160)),
	}
}

// numericCheck compares a numeric answer to a numeric expectation.
//
// The asymmetry between its two readings is intentional. An expectation that is
// not a number is a CORPUS bug — there is nothing to compare against, so the check
// reports missing (cannot prove). An answer is scanned for a number instead, and
// one is enough: requiring the WHOLE answer to be a bare number failed correct
// answers like "The answer is 56088." or "约 56,088 元", which would have turned the
// accuracy signal wrong rather than thin — the worse of the two failures.
//
// The obvious cost of scanning: an answer that names the right figure while arguing
// against it ("the answer is 99999, not 56088") passes. That is accepted, because
// this check is a FLOOR against wrong answers, not a lie detector, and the
// conservative direction for a floor is to pass a correct-looking answer. A case
// where presence-vs-claim matters declares CheckExact instead.
func numericCheck(tc TestCase, result TestResult) ResultCheck {
	const name = CheckNumeric
	want, err := parseNumeric(tc.ExpectedOutput)
	if err != nil {
		return ResultCheck{
			Name:   name,
			Status: StatusMissing,
			Detail: fmt.Sprintf("expected_output %q is not a number, so there is nothing to compare (corpus bug)", truncate.WithEllipsis(tc.ExpectedOutput, 80)),
		}
	}
	found := extractNumerics(result.ActualOutput)
	if len(found) == 0 {
		return ResultCheck{
			Name:   name,
			Status: StatusFailed,
			Detail: fmt.Sprintf("answer %q contains no number; expected %s", truncate.WithEllipsis(result.ActualOutput, 120), formatNumeric(want)),
		}
	}
	for _, candidate := range found {
		if numericClose(candidate, want) {
			return ResultCheck{
				Name:   name,
				Status: StatusPassed,
				Detail: fmt.Sprintf("answer contains %s as expected", formatNumeric(want)),
			}
		}
	}
	return ResultCheck{
		Name:   name,
		Status: StatusFailed,
		Detail: fmt.Sprintf("answer contains %s but not the expected %s", formatNumericList(found), formatNumeric(want)),
	}
}

// numericCandidatePattern finds number-shaped tokens inside prose: an optional
// sign, digits with optional thousands separators, and an optional fraction.
var numericCandidatePattern = regexp.MustCompile(`[-+]?[0-9][0-9,]*(?:\.[0-9]+)?`)

// extractNumerics returns every number an answer contains, in order.
//
// A token that looks numeric but does not parse (a malformed grouping) is skipped
// rather than failing the check: the answer may still contain the expected figure
// elsewhere, and an unparsable neighbour is not evidence about that figure.
func extractNumerics(s string) []float64 {
	matches := numericCandidatePattern.FindAllString(s, -1)
	out := make([]float64, 0, len(matches))
	for _, match := range matches {
		value, err := parseNumeric(match)
		if err != nil {
			continue
		}
		out = append(out, value)
	}
	return out
}

// formatNumericList renders the numbers an answer offered, bounded so a long
// answer cannot flood the evidence detail.
func formatNumericList(values []float64) string {
	const limit = 8
	parts := make([]string, 0, len(values)+1)
	for i, v := range values {
		if i >= limit {
			parts = append(parts, "...")
			break
		}
		parts = append(parts, formatNumeric(v))
	}
	return strings.Join(parts, ", ")
}

// toolsCheck requires every tool the case expects to appear among the tools that
// actually ran.
//
// It reads TestResult.ToolsUsed, which the agent runner fills from the executor
// that ran the session (agent_runner.go), so the check is about the run that
// happened rather than about the plan the model narrated. Every expected tool
// must have run: partial credit would let a session that skipped half its
// declared work keep a passing anchor, which is the shape this layer exists to
// refuse.
func toolsCheck(tc TestCase, result TestResult) ResultCheck {
	const name = CheckTools
	if len(tc.ExpectedTools) == 0 {
		return ResultCheck{Name: name, Status: StatusMissing, Detail: "check declares no expected_tools"}
	}
	used := make(map[string]struct{}, len(result.ToolsUsed))
	for _, tool := range result.ToolsUsed {
		used[tool] = struct{}{}
	}
	var missing []string
	for _, want := range tc.ExpectedTools {
		if _, ok := used[want]; !ok {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		return ResultCheck{
			Name:   name,
			Status: StatusFailed,
			Detail: fmt.Sprintf("expected tool(s) did not run: %s (ran: %s)", strings.Join(missing, ", "), orNone(strings.Join(result.ToolsUsed, ", "))),
		}
	}
	return ResultCheck{Name: name, Status: StatusPassed, Detail: fmt.Sprintf("expected tool(s) ran: %s", strings.Join(tc.ExpectedTools, ", "))}
}

// noErrorCheck requires the run to have finished without an executor error.
func noErrorCheck(result TestResult) ResultCheck {
	const name = CheckNoError
	if result.Error != "" {
		return ResultCheck{Name: name, Status: StatusFailed, Detail: fmt.Sprintf("run reported an error: %s", truncate.WithEllipsis(result.Error, 160))}
	}
	return ResultCheck{Name: name, Status: StatusPassed, Detail: "run completed without an executor error"}
}

// groundTruthOutcome is the deterministic layer's complete answer for ONE case:
// what it checked, what it concluded, and the structured diagnosis behind it.
type groundTruthOutcome struct {
	// TaskID is the case the outcome belongs to, carried so a log line about a
	// disagreement names the case.
	TaskID string
	// Declared is true when the case asked for at least one check. It is what
	// separates "the deterministic layer had no opinion on this case" (the judge
	// governs, unchanged) from "it had an opinion" (the judge is floored by it).
	Declared bool
	// Checks are the per-check outcomes, in declaration order.
	Checks []ResultCheck
	// Verdict is ResultVerifier's overall determination.
	Verdict Verdict
	// Evidence is ResultVerifier's structured diagnosis; nil when not declared.
	Evidence *Evidence
}

// Anchored reports whether the deterministic layer reached a Pass or Fail — the
// only two verdicts that may floor the judge. A declared check that could not be
// resolved is Uncertain and floors nothing (see recordGroundTruth, which counts
// it separately instead of dressing it up as agreement).
func (o groundTruthOutcome) Anchored() bool {
	return o.Declared && o.Verdict != VerdictUncertain
}

// evaluateGroundTruth runs the declared checks for one case through the shared
// bottom-layer verifier.
//
// Verification goes through NewResultVerifier rather than re-deriving a verdict
// here on purpose: that type already encodes Ch.8's rule (any failed check fails
// the task; a missing/skipped check means "cannot prove success"; an unknown
// status is treated as a failure, stricter than missing), and a second copy of
// that rule is how two halves of a system drift apart.
func evaluateGroundTruth(tc TestCase, result TestResult, role string) groundTruthOutcome {
	checks := groundTruthChecks(tc, result)
	if len(checks) == 0 {
		// No declaration: no opinion. Verdict is set explicitly rather than left
		// at its zero value, which is VerdictPass — an unasked question must
		// never read as a pass.
		return groundTruthOutcome{TaskID: tc.ID, Verdict: VerdictUncertain}
	}
	ev := NewResultVerifier().Verify(tc.ID, role, checks)
	return groundTruthOutcome{TaskID: tc.ID, Declared: true, Checks: checks, Verdict: ev.Verdict, Evidence: ev}
}

// GroundTruthAgreement counts how the LLM rubric judge and the deterministic
// layer related across the cases that had a deterministic opinion.
//
// It exists because "the accuracy channel is wired" is not "the judge can be
// trusted". Every case the deterministic layer does not anchor is scored by the
// judge alone, so the only way to know how much of the signal the GA selects on
// is opinion is to count where the two disagreed. Without this number the
// corpus can grow while the judge quietly gets more lenient, and the signal
// stays plausible the whole time.
//
// The two disagreement directions are counted apart because they corrupt
// selection differently: Lenient hands a WRONG answer a good score (the
// reward-hacking direction G1 exists to stop), Harsh gives a CORRECT answer a bad
// one (a false negative, which merely makes the signal conservative).
type GroundTruthAgreement struct {
	// Anchored is the number of cases that produced a Pass or Fail verdict.
	Anchored int
	// Lenient is the number of anchored cases where ground truth FAILED and the
	// judge still gave the answer correctness credit.
	Lenient int
	// Harsh is the number where ground truth PASSED and the judge scored
	// correctness zero anyway.
	Harsh int
	// Broken is the number of cases that DECLARED checks the deterministic layer
	// could not resolve. They are not anchored (the judge keeps the case) and are
	// counted so that a corpus problem is visible instead of silent.
	Broken int
}

// LeniencyRate is the share of anchored cases the judge would have rewarded
// despite the deterministic layer failing them. The headline number: it bounds
// how much of the unanchored accuracy signal can be reward hacking.
func (a GroundTruthAgreement) LeniencyRate() float64 {
	if a.Anchored == 0 {
		return 0
	}
	return float64(a.Lenient) / float64(a.Anchored)
}

// String renders the counts for a log line.
func (a GroundTruthAgreement) String() string {
	return fmt.Sprintf("anchored=%d lenient=%d harsh=%d broken=%d leniency_rate=%.3f",
		a.Anchored, a.Lenient, a.Harsh, a.Broken, a.LeniencyRate())
}

// normalizeAnswer folds an answer into the single form the text checks compare
// in: case-insensitive, with whitespace runs collapsed and the ends trimmed.
//
// It is normalization, not fuzzy matching. "Paris" and "paris" are the same
// answer; "Paris" and "the capital is Paris, a city of 2 million" are not, which
// is why a case wanting the latter declares CheckContains instead. Keeping the
// rule that narrow is what makes a failure mean something.
func normalizeAnswer(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// parseNumeric reads a number out of an answer, tolerating the commas people
// write in large figures (56,088) and surrounding whitespace.
func parseNumeric(s string) (float64, error) {
	cleaned := strings.ReplaceAll(strings.TrimSpace(s), ",", "")
	if cleaned == "" {
		return 0, fmt.Errorf("empty")
	}
	return strconv.ParseFloat(cleaned, 64)
}

// numericClose reports whether two numbers agree within the tolerances above.
func numericClose(a, b float64) bool {
	diff := math.Abs(a - b)
	if diff <= numericAbsTol {
		return true
	}
	scale := math.Max(math.Abs(a), math.Abs(b))
	return diff <= scale*numericRelTol
}

// formatNumeric renders a number for a check detail without trailing zeros.
func formatNumeric(v float64) string {
	return strconv.FormatFloat(v, 'g', -1, 64)
}

// orNone names the empty case in a detail, so "ran: " never reads as a tool that
// ran with an empty name.
func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// passedCheckCount counts the checks that explicitly passed, for the evidence
// dimension score.
func passedCheckCount(checks []ResultCheck) int {
	n := 0
	for _, c := range checks {
		if c.Status == StatusPassed {
			n++
		}
	}
	return n
}
