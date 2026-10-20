package agentruntime

// provenance_test.go pins the read side of the eval executor's provenance path
// against the REAL write path: the answer task is created, acquired and completed
// through the fabric's own API (CompleteWithCheckpoint is what the scheduler's
// done branch calls), so the fixture cannot drift from what production records —
// a hand-built envelope would prove only that the test agrees with itself.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/core/models"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// completedAnswerTask creates a session's answer task and completes it with the
// given result metadata, exactly as the runtime does. round is the growth round
// the node id carries (SessionNodeID's d<N>); an empty content leaves the envelope
// bodyless, which is the shape a gap answer has.
func completedAnswerTask(
	t *testing.T, fabric *taskfabric.Fabric, sessionID string, round int, content string, metadata map[string]any,
) {
	t.Helper()
	const agentID = "agent-1"
	answerID := agentfabric.SessionNodeID(sessionID, round, "answer", 0)

	require.NoError(t, fabric.Create(&taskfabric.Task{
		ID:         answerID,
		Capability: AnswerCapability,
	}))
	epoch, err := fabric.Acquire(answerID, agentID, time.Minute)
	require.NoError(t, err)
	// LEASED → RUNNING before completing: the fabric's state machine refuses
	// Complete straight from a lease (same order the scheduler drives).
	require.NoError(t, fabric.Start(answerID, agentID, epoch))
	// The scheduler's done branch wraps the terminal result as
	// outMap["metadata"] = res.Metadata (plus the items); this is that envelope.
	envelope := map[string]any{"metadata": metadata}
	if content != "" {
		envelope["items"] = []*models.RecommendItem{{ItemID: answerID, Content: content}}
	}
	require.NoError(t, fabric.CompleteWithCheckpoint(answerID, agentID, epoch, envelope))
}

// TestSessionAnswerProvenance_ReadsARecordedGap is the join the eval executor
// depends on: the provenance the runtime records for a gap answer must be what
// SessionAnswerProvenance reports. Without it the floor would be fed "no signal"
// for exactly the answers it exists to catch.
func TestSessionAnswerProvenance_ReadsARecordedGap(t *testing.T) {
	fabric := taskfabric.NewFabric()
	const sessionID = "sess-provenance-1"
	completedAnswerTask(t, fabric, sessionID, 1, "", map[string]any{
		ares_events.EventKeyAnswerBodySource: ares_events.AnswerBodySourceGap,
		ares_events.EventKeyAnswerTruncated:  false,
	})

	bodySource, truncated, ok := SessionAnswerProvenance(fabric, sessionID)
	require.True(t, ok, "the completed answer task must be found")
	assert.Equal(t, ares_events.AnswerBodySourceGap, bodySource)
	assert.False(t, truncated)
}

// TestSessionAnswerProvenance_ReportsNoSignalVerbatim: an answer task completed
// without the keys reports ("", false, true) — found, but no signal — which the
// shared penalty rule scores as no-penalty. It must never be dressed up as a
// healthy answer.
func TestSessionAnswerProvenance_ReportsNoSignalVerbatim(t *testing.T) {
	fabric := taskfabric.NewFabric()
	const sessionID = "sess-provenance-2"
	completedAnswerTask(t, fabric, sessionID, 1, "", map[string]any{"unrelated": "value"})

	bodySource, truncated, ok := SessionAnswerProvenance(fabric, sessionID)
	require.True(t, ok)
	assert.Empty(t, bodySource)
	assert.False(t, truncated)

	factor, degraded := ares_events.AnswerQualityPenalty(bodySource, truncated)
	assert.False(t, degraded)
	assert.InDelta(t, 1.0, factor, 1e-9)
}

// TestSessionAnswerProvenance_AbsentWhenNoAnswerYet keeps the "not found" case
// distinct from "found without signal": before the answer lands there is nothing
// to report, and a caller that conflated the two would score a running session as
// degraded.
func TestSessionAnswerProvenance_AbsentWhenNoAnswerYet(t *testing.T) {
	fabric := taskfabric.NewFabric()
	_, _, ok := SessionAnswerProvenance(fabric, "sess-empty")
	assert.False(t, ok)
}

// TestSessionAnswerProvenance_PicksTheNewestRound pins the selection rule a
// re-admitted session's second turn depends on. The older turn's answer is a gap
// (what a first turn can leave behind), the live turn's is a real answer; the
// reader must report the LIVE one.
//
// Asserted over repetitions on purpose: the previous "first completed answer" read
// decided by the fabric's map-walk order, so it returned the gap on roughly half of
// the iterations — a coin flip, not a rule. A single-shot assertion would pass
// half the time against the defect this test exists to prevent.
func TestSessionAnswerProvenance_PicksTheNewestRound(t *testing.T) {
	fabric := taskfabric.NewFabric()
	const sessionID = "sess-two-turns"

	completedAnswerTask(t, fabric, sessionID, 1, "", map[string]any{
		ares_events.EventKeyAnswerBodySource: ares_events.AnswerBodySourceGap,
	})
	completedAnswerTask(t, fabric, sessionID, 2, "the live turn's answer", map[string]any{
		ares_events.EventKeyAnswerBodySource: ares_events.AnswerBodySourceSupplied,
	})

	for i := 0; i < 25; i++ {
		bodySource, truncated, ok := SessionAnswerProvenance(fabric, sessionID)
		require.True(t, ok)
		assert.Equal(t, ares_events.AnswerBodySourceSupplied, bodySource,
			"iteration %d must read the newest round's facts, not the map's luck", i)
		assert.False(t, truncated)
	}
}

// TestSessionAnswer_PicksTheNewestRound is the body half of the same rule: the
// content must come from the same turn the provenance describes, or a judge is
// handed one turn's text beside another turn's facts.
func TestSessionAnswer_PicksTheNewestRound(t *testing.T) {
	fabric := taskfabric.NewFabric()
	const sessionID = "sess-two-turns-body"

	completedAnswerTask(t, fabric, sessionID, 1, "the earlier turn", map[string]any{})
	completedAnswerTask(t, fabric, sessionID, 2, "the live turn", map[string]any{})

	for i := 0; i < 25; i++ {
		content, ok := SessionAnswer(fabric, sessionID)
		require.True(t, ok)
		assert.Equal(t, "the live turn", content, "iteration %d must read the newest round's body", i)
	}
}

// TestSessionAnswer_AndProvenanceDescribeTheSameTurn is the PAIRING contract, and
// the case that makes it non-trivial: the newest completed answer carries facts of
// its own but no readable body (a checkpoint that lost its items). The readers must
// then agree on the earlier turn — body and facts both from it — instead of handing
// a judge one turn's text beside another turn's provenance.
//
// An independent "newest answer" read reproduces exactly that mismatch, which is why
// the selection lives in one function (adoptedAnswerTask) rather than in each reader.
func TestSessionAnswer_AndProvenanceDescribeTheSameTurn(t *testing.T) {
	fabric := taskfabric.NewFabric()
	const sessionID = "sess-pairing"

	// The older turn: a real answer, body and facts both present.
	completedAnswerTask(t, fabric, sessionID, 1, "the earlier turn's body", map[string]any{
		ares_events.EventKeyAnswerBodySource: ares_events.AnswerBodySourceSupplied,
	})
	// The newest turn: completed with facts, but no readable body.
	completedAnswerTask(t, fabric, sessionID, 2, "", map[string]any{
		ares_events.EventKeyAnswerBodySource: ares_events.AnswerBodySourceGap,
	})

	body, ok := SessionAnswer(fabric, sessionID)
	require.True(t, ok)
	require.Equal(t, "the earlier turn's body", body)

	bodySource, truncated, found := SessionAnswerProvenance(fabric, sessionID)
	require.True(t, found)
	assert.Equal(t, ares_events.AnswerBodySourceSupplied, bodySource,
		"the facts must describe the same turn as the body, not the bodyless newer one")
	assert.False(t, truncated)
}

// TestSessionAnswerProvenance_BodylessSessionStillReportsFacts keeps the provenance
// contract intact for the case the pairing rule's step 2 covers: a completed answer
// with no readable body anywhere still reports ("", false, true) — found, no signal
// — rather than disappearing as "not found".
func TestSessionAnswerProvenance_BodylessSessionStillReportsFacts(t *testing.T) {
	fabric := taskfabric.NewFabric()
	const sessionID = "sess-bodyless"
	completedAnswerTask(t, fabric, sessionID, 1, "", map[string]any{
		ares_events.EventKeyAnswerTruncated: true,
	})

	body, ok := SessionAnswer(fabric, sessionID)
	assert.False(t, ok, "no readable body means no answer body to report")
	assert.Empty(t, body)

	_, truncated, found := SessionAnswerProvenance(fabric, sessionID)
	require.True(t, found, "the completed answer is still found")
	assert.True(t, truncated, "and its facts are still reported")
}

// TestNewSessionEvalExecutor_RefusesAnUnassembledCore pins the fail-loud posture:
// an executor that cannot actually run must not silently report "no signal" for
// every case (which scores as no-penalty and would look healthy).
func TestNewSessionEvalExecutor_RefusesAnUnassembledCore(t *testing.T) {
	_, err := NewSessionEvalExecutor(nil, AwaitOptions{})
	require.Error(t, err)

	_, err = NewSessionEvalExecutor(&Execution{}, AwaitOptions{})
	require.Error(t, err, "an execution core without a submitter/sessions must be refused")
}
