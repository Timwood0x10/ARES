package ares_events

// The terminal-answer quality contract. These names travel ONE wire path:
//
//	answer node args (arg.*)            — produced by fabric/agent's planner
//	  → models.TaskResult.Metadata      — stamped by the answer cognition
//	  → envelope StepCheckpoint metadata — wrapped by the scheduler's done branch
//	  → terminal event payload          — mirrored by fabric/task
//	  → read by runtime/ares_evolution  — the fitness observer
//
// They live here, in the neutral event domain, for the same reason
// EventKeyUserID does: three packages touch these strings, and a literal
// repeated in three places breaks the chain SILENTLY when any one of them is
// renamed — no compiler error, and a test that builds its own payload from the
// literal cannot catch it either.
const (
	// EventKeyAnswerBodySource reports where the emitted body came from; see
	// the AnswerBodySource* values. Absent means "no answer path reported it"
	// (a non-answer task, or an envelope predating the key).
	//
	// Note on naming: unlike the event keys around it this one is NOT
	// answer-prefixed as a whole — it is spelled answer_body_source because
	// that is the name the shipped metadata contract already uses.
	EventKeyAnswerBodySource = "answer_body_source"
	// EventKeyAnswerTruncated reports that the answer was forced by the
	// planner's growth-depth guard rather than produced by a free final turn.
	//
	// The word is deliberately the same one tool results use for their own
	// truncation ("truncated") because it means the same thing — output cut
	// short — but the two never meet: tool results travel inside a task's
	// items, while this is a terminal event/task-result key. Anything that
	// starts folding tool output into the event payload must rename one of
	// them first.
	EventKeyAnswerTruncated = "truncated"
)

// AnswerBodySource* is the closed set of EventKeyAnswerBodySource values.
// Wire-visible: consumers key on these strings, so they are stable
// identifiers, not prose.
const (
	// AnswerBodySourceSupplied: the body is the content the producer put on
	// the node — the normal completion.
	AnswerBodySourceSupplied = "supplied"
	// AnswerBodySourceSynthesized: the body was reconstructed by the answer
	// path's synthesis call over the predecessor history — a real answer, but
	// not one the model handed over directly.
	AnswerBodySourceSynthesized = "synthesized"
	// AnswerBodySourceGap: no body existed at all; the result carries the
	// explicit gap body. This is the degraded ending, and the one the fitness
	// observer must not score as a success.
	AnswerBodySourceGap = "gap"
)

// AnswerQualityPenalty is the ONE scoring rule for the degraded-answer signals
// above: a gap body scores 0 (it produced nothing — indistinguishable from
// failure), a depth-capped answer scores 0.5 (something real came out under
// duress: better than nothing, worse than any genuine answer), and absent
// signals keep 1 (no penalty — a non-answer task, or an envelope predating the
// keys).
//
// It lives here, beside the wire names it interprets, because TWO scorers need
// it: the runtime observer's fitness factor and the eval judge's deterministic
// floor. Two copies of these numbers would drift apart, and a drifted penalty is
// silently wrong rather than visibly broken — the same failure mode these keys
// were centralised to prevent.
func AnswerQualityPenalty(bodySource string, truncated bool) (factor float64, degraded bool) {
	if bodySource == AnswerBodySourceGap {
		return 0, true
	}
	if truncated {
		return 0.5, true
	}
	return 1, false
}
