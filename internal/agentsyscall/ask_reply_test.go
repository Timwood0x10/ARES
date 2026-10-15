package agentsyscall

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// askReplyFixture builds a Kernel with one asker that has a routable capability.
func askReplyFixture(t *testing.T) (*Kernel, *taskfabric.Fabric, string) {
	t.Helper()
	agents := agentfabric.NewFabric()
	tasks := taskfabric.NewFabric()
	kernel := NewKernel(agents, tasks, nil, nil)

	spawned, err := kernel.SpawnAgent(context.Background(), SpawnAgentArgs{Capability: "ares/plan"})
	if err != nil {
		t.Fatalf("SpawnAgent: %v", err)
	}
	return kernel, tasks, spawned.AgentID
}

// TestAskReplyPayloadShape pins the payload the asker's next quantum reads.
//
// It asserts BOTH keys, and the `input` one is the reason this test exists: the L2
// cognition builds the quantum's prompt from task.Payload["input"], so a reply
// carried only under the structured key reaches the fabric and is never read. An
// earlier version of this test asserted only that the envelope existed, and passed
// while the feature was inert.
func TestAskReplyPayloadShape(t *testing.T) {
	payload := askReplyPayload("agent-B", "delegate-task", map[string]any{"verdict": "ok"})

	reply, ok := payload[askReplyPayloadKey].(map[string]any)
	if !ok {
		t.Fatalf("payload must carry %q as a map, got %T", askReplyPayloadKey, payload[askReplyPayloadKey])
	}
	if reply["from"] != "agent-B" || reply["topic"] != "delegate-task" {
		t.Errorf("reply envelope = %v, want the peer and topic that produced it", reply)
	}
	if _, ok := reply["reply"].(map[string]any); !ok {
		t.Errorf("reply body must be carried verbatim, got %T", reply["reply"])
	}

	rendered, ok := payload[askReplyInputKey].(string)
	if !ok {
		t.Fatalf("payload must carry %q as the string the runtime reads, got %T", askReplyInputKey, payload[askReplyInputKey])
	}
	for _, want := range []string{"agent-B", "delegate-task", "verdict", "ok"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered prompt %q must name %q: an unattributed or bodyless answer is useless to the model", rendered, want)
		}
	}
}

// TestRenderAskReply_HandlesEveryBodyShape: the renderer is on the path that
// decides whether the model sees anything at all, so a nil body must still produce
// a readable line rather than silence.
func TestRenderAskReply_HandlesEveryBodyShape(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply any
		want  string
	}{
		{"nil body", nil, "(no content)"},
		{"plain string", "looks good", "looks good"},
		{"structured body is JSON", map[string]any{"a": 1}, `{"a":1}`},
		{"unmarshalable body still renders", make(chan int), "0x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := renderAskReply("peer", "topic", tc.reply)
			if !strings.Contains(got, tc.want) {
				t.Errorf("renderAskReply = %q, want it to contain %q", got, tc.want)
			}
			if !strings.Contains(got, "peer") || !strings.Contains(got, "topic") {
				t.Errorf("renderAskReply = %q, want the peer and topic named", got)
			}
		})
	}
}

// TestDeliverAgentReply_DeliversTheAnswerAsATask is the C-1-a contract: the reply
// becomes a READY task under the asker's own capability, so the asker's next
// drain reads the answer instead of the answer dying in a log line.
func TestDeliverAgentReply_DeliversTheAnswerAsATask(t *testing.T) {
	kernel, tasks, asker := askReplyFixture(t)

	if err := kernel.DeliverAgentReply(context.Background(), asker, "agent-B", "delegate-task", "looks good"); err != nil {
		t.Fatalf("DeliverAgentReply: %v", err)
	}

	taskID := askReplyTaskID(asker, "agent-B", "delegate-task", "looks good")
	delivered, err := tasks.Task(taskID)
	if err != nil {
		t.Fatalf("reply task %s must exist: %v", taskID, err)
	}
	if delivered.State != taskfabric.StateReady {
		t.Errorf("reply task state = %v, want READY so the asker's next drain picks it up", delivered.State)
	}
	if delivered.Capability != "ares/plan" {
		t.Errorf("reply task capability = %q, want the asker's own", delivered.Capability)
	}
	if delivered.Origin != asker {
		t.Errorf("reply task origin = %q, want the asker %q", delivered.Origin, asker)
	}
	if delivered.Checkpoint == nil {
		t.Error("reply task must carry a checkpoint envelope holding the answer")
	}
	if len(tasks.IDs()) != 1 {
		t.Errorf("expected exactly the reply task, got %v", tasks.IDs())
	}
}

// TestDeliverAgentReply_IsIdempotentPerAsk: the reply path runs in a background
// goroutine with nobody to retry it, so a duplicated delivery must not queue the
// same answer twice — and must not report an error either, or the log would fill
// with phantom failures.
func TestDeliverAgentReply_IsIdempotentPerAsk(t *testing.T) {
	kernel, tasks, asker := askReplyFixture(t)

	for i := 0; i < 2; i++ {
		if err := kernel.DeliverAgentReply(context.Background(), asker, "agent-B", "delegate-task", "looks good"); err != nil {
			t.Fatalf("delivery %d: %v", i, err)
		}
	}
	if got := len(tasks.IDs()); got != 1 {
		t.Errorf("task count = %d, want 1: a repeated delivery must not queue the answer twice", got)
	}

	// A different topic on the same pair IS a different answer.
	if err := kernel.DeliverAgentReply(context.Background(), asker, "agent-B", "other-topic", "different"); err != nil {
		t.Fatalf("second topic: %v", err)
	}
	if got := len(tasks.IDs()); got != 2 {
		t.Errorf("task count = %d, want 2 after a distinct topic", got)
	}
}

// TestDeliverAgentReply_ANewAnswerIsNotSwallowedAsDuplicate pins the boundary of
// the idempotency. Keyed on the QUESTION alone, a second legitimate ask on the same
// topic — which the 30s dedupe window explicitly allows, minutes later — would
// collide with the first reply's id and be dropped as "already delivered", losing
// the answer with no trace. Keyed on the answer too, only a genuinely identical
// delivery collapses.
func TestDeliverAgentReply_ANewAnswerIsNotSwallowedAsDuplicate(t *testing.T) {
	kernel, tasks, asker := askReplyFixture(t)
	ctx := context.Background()

	if err := kernel.DeliverAgentReply(ctx, asker, "agent-B", "delegate-task", "first answer"); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	if err := kernel.DeliverAgentReply(ctx, asker, "agent-B", "delegate-task", "second, different answer"); err != nil {
		t.Fatalf("second delivery: %v", err)
	}
	if got := len(tasks.IDs()); got != 2 {
		t.Errorf("task count = %d, want 2: a new answer to the same question must be delivered, not dropped as a duplicate (%v)", got, tasks.IDs())
	}

	// And the identical one still collapses (that is what the idempotency is for).
	if err := kernel.DeliverAgentReply(ctx, asker, "agent-B", "delegate-task", "first answer"); err != nil {
		t.Fatalf("repeat delivery: %v", err)
	}
	if got := len(tasks.IDs()); got != 2 {
		t.Errorf("task count = %d, want 2: an identical body is still one delivery", got)
	}
}

// TestAskReplyTaskID_IsStableForStructuredBodies: the fingerprint must be stable
// across runs, or the idempotency it provides would depend on map iteration order.
func TestAskReplyTaskID_IsStableForStructuredBodies(t *testing.T) {
	first := askReplyTaskID("a", "b", "t", map[string]any{"x": 1, "y": 2})
	second := askReplyTaskID("a", "b", "t", map[string]any{"y": 2, "x": 1})
	if first != second {
		t.Errorf("structurally equal bodies produced different ids: %s vs %s", first, second)
	}
	if askReplyTaskID("a", "b", "t", map[string]any{"x": 1}) == first {
		t.Error("different bodies must not share an id")
	}
}

// TestDeliverAgentReply_RedeliversAfterAFailedReplyTask is the second half of the
// silent-loss fix: the idempotency means the same id can never be created again, so
// a reply task that FAILED terminally would block the answer FOREVER — the failure
// would have turned "not written back" into "written back, then lost, with no way to
// try again". A re-delivery must clear the failed row and deliver.
func TestDeliverAgentReply_RedeliversAfterAFailedReplyTask(t *testing.T) {
	agents := agentfabric.NewFabric()
	tasks := taskfabric.NewFabric()
	kernel := NewKernel(agents, tasks, nil, nil)
	ctx := context.Background()

	spawned, err := kernel.SpawnAgent(ctx, SpawnAgentArgs{Capability: "ares/plan"})
	if err != nil {
		t.Fatalf("SpawnAgent: %v", err)
	}
	asker := spawned.AgentID

	if err := kernel.DeliverAgentReply(ctx, asker, "agent-B", "delegate-task", "the answer"); err != nil {
		t.Fatalf("first delivery: %v", err)
	}

	// Drive the reply task to a terminal failure the way the runtime would.
	taskID := askReplyTaskID(asker, "agent-B", "delegate-task", "the answer")
	epoch, err := tasks.Acquire(taskID, "runner", time.Minute)
	if err != nil {
		t.Fatalf("Acquire reply task: %v", err)
	}
	if err := tasks.Start(taskID, "runner", epoch); err != nil {
		t.Fatalf("Start reply task: %v", err)
	}
	if err := tasks.Fail(taskID, "runner", epoch, taskfabric.MarkPermanent(errors.New("asker vanished"))); err != nil {
		t.Fatalf("Fail reply task: %v", err)
	}
	failed, err := tasks.Task(taskID)
	if err != nil {
		t.Fatalf("Task after failure: %v", err)
	}
	if failed.State != taskfabric.StateFailed {
		t.Fatalf("reply task state = %v, want FAILED for this test to mean anything", failed.State)
	}

	// The re-delivery must not report success while the answer is lost.
	if err := kernel.DeliverAgentReply(ctx, asker, "agent-B", "delegate-task", "the answer"); err != nil {
		t.Fatalf("re-delivery after a failed reply task: %v", err)
	}
	redelivered, err := tasks.Task(taskID)
	if err != nil {
		t.Fatalf("reply task must exist again: %v", err)
	}
	if redelivered.State != taskfabric.StateReady {
		t.Errorf("re-delivered reply task state = %v, want READY: the asker must be able to read the answer", redelivered.State)
	}
}

// TestDeliverAgentReply_LoudOnUnknownAsker: an answer nobody can deliver is not
// the same as a target that never answered, and must not vanish silently.
func TestDeliverAgentReply_LoudOnUnknownAsker(t *testing.T) {
	kernel, tasks, _ := askReplyFixture(t)

	err := kernel.DeliverAgentReply(context.Background(), "nobody", "agent-B", "topic", "answer")
	if err == nil {
		t.Fatal("delivering to an unknown asker must fail")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want it to name the unknown asker", err)
	}
	if len(tasks.IDs()) != 0 {
		t.Errorf("nothing may be queued for an unknown asker, got %v", tasks.IDs())
	}
}

// TestDeliverAgentReply_LoudWhenTheAskerCannotRunIt: an agent with no
// L2-routable capability can never execute the reply task, so the delivery is
// reported instead of parking a task in READY forever.
func TestDeliverAgentReply_LoudWhenTheAskerCannotRunIt(t *testing.T) {
	agents := agentfabric.NewFabric()
	tasks := taskfabric.NewFabric()
	kernel := NewKernel(agents, tasks, nil, nil)

	if _, err := agents.Spawn(context.Background(), agentfabric.SpawnSpec{
		Identity:     "capability-less",
		Capabilities: []string{"custom/thing"},
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	err := kernel.DeliverAgentReply(context.Background(), "capability-less", "agent-B", "topic", "answer")
	if err == nil {
		t.Fatal("delivering to an asker that cannot run the task must fail")
	}
	if !strings.Contains(err.Error(), "L2-routable") {
		t.Errorf("error = %v, want it to explain the missing capability", err)
	}
	if len(tasks.IDs()) != 0 {
		t.Errorf("nothing may be queued, got %v", tasks.IDs())
	}
}
