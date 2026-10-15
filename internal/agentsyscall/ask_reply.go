package agentsyscall

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"

	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// askReplyPayloadKey is the payload key the reply lives under, so a reader (or a
// model told about it) can find the answer without knowing the task's shape.
//
// It IS the fabric's constant rather than a second literal: the fabric has to
// recognise a reply task when one fails terminally (a lost answer is the defect),
// and two spellings of the same fact is how the two halves drift apart.
const askReplyPayloadKey = taskfabric.PayloadKeyCollaborationReply

// askReplyInputKey is the payload key the RUNTIME reads when it builds a quantum's
// prompt. It is not a second copy of the answer for convenience: the L2 cognition
// takes its prompt from task.Payload["input"] (planner_context.go's fallback path
// and rootCognition), so a reply carried only under askReplyPayloadKey reaches the
// fabric and is never read by the model — the "wired but inert" shape this fix
// exists to end.
const askReplyInputKey = "input"

// DeliverAgentReply hands a collaboration reply to the asker as a task.
//
// This is C-1-a, the writeback ask_agent never had (defect I1). The asking
// quantum is GONE by the time a reply arrives — ask_agent yields immediately so
// the drain cannot deadlock — which is why there is no asking task left to write
// into. A task is the one channel this system already has for delivering
// information to an agent, so the reply becomes one: the asker's next drain picks
// it up and the answer sits in its payload, which is exactly the "the next
// quantum reads it naturally" contract the defect asks for.
//
// Idempotent by construction: the task id is derived from (asker, peer, topic), so
// a retried or duplicated delivery finds ErrTaskExists and reports success rather
// than queueing the same answer twice. That matters because the reply path runs in
// a background goroutine with no one to retry it and no way to observe it.
//
// Fails LOUDLY when the reply cannot be delivered — an unknown asker, or one with
// no L2-routable capability to run the task. Those are the two shapes where the
// answer would otherwise vanish without a trace, which is the failure mode this
// whole path exists to end.
func (k *Kernel) DeliverAgentReply(ctx context.Context, askerID, peerID, topic string, reply any) error {
	if k.fabric == nil {
		return stderrors.New("agentsyscall: task fabric not wired")
	}
	if askerID == "" {
		return stderrors.New("agentsyscall: asker id is required")
	}
	capability, err := k.askReplyCapability(askerID)
	if err != nil {
		return err
	}

	payload := askReplyPayload(peerID, topic, reply)
	task := &taskfabric.Task{
		ID:          askReplyTaskID(askerID, peerID, topic, reply),
		Capability:  capability,
		Origin:      askerID,
		RetryPolicy: taskfabric.RetryPolicy{MaxRetries: 1},
		// Tenant comes from the DELIVERING context, exactly as CreateTask stamps
		// the creating quantum's: a reply must land in the same tenant envelope
		// the asker reads under, or it is invisible to its own tenant's recall.
		Checkpoint: tenantStampedEnvelope(ctx, payload),
	}
	if err := k.fabric.Create(task, taskfabric.WithResolvedDependencies()); err != nil {
		if stderrors.Is(err, taskfabric.ErrTaskExists) {
			return k.redeliverReplyTask(ctx, task, askerID, peerID, topic)
		}
		return fmt.Errorf("agentsyscall: create ask reply task: %w", err)
	}
	return nil
}

// redeliverReplyTask decides what a repeated delivery means when a task with the
// same id already exists.
//
// An existing task in any live or completed state means the answer is on its way or
// already read: success, nothing to do. A task that FAILED terminally is the case
// this function exists for — the idempotency would otherwise block the answer
// FOREVER (the same id can never be created again), turning "the delivery failed" into
// "the answer is lost with no way back", which is the silent hole the defect was.
// The failed row is cleared and the delivery retried, with a Warning: a lost
// collaboration answer is exactly what must not be quiet.
//
// Deletion is only attempted for FAILED tasks, which is the one terminal state the
// fabric allows to be deleted (READY/COMPLETED/FAILED; a leased or running reply must
// finish or expire through the normal paths).
func (k *Kernel) redeliverReplyTask(ctx context.Context, task *taskfabric.Task, askerID, peerID, topic string) error {
	existing, err := k.fabric.Task(task.ID)
	if err != nil {
		return fmt.Errorf("agentsyscall: inspect existing ask reply task %s: %w", task.ID, err)
	}
	if existing.State != taskfabric.StateFailed {
		return nil // on its way, or already read by the asker
	}
	log.WarnContext(ctx, "agentsyscall: re-delivering a collaboration reply whose previous task failed",
		"task_id", task.ID, "asker", askerID, "peer", peerID, "topic", topic)
	if err := k.fabric.Delete(task.ID); err != nil {
		return fmt.Errorf("agentsyscall: clear failed ask reply task %s: %w", task.ID, err)
	}
	if err := k.fabric.Create(task, taskfabric.WithResolvedDependencies()); err != nil {
		return fmt.Errorf("agentsyscall: recreate ask reply task %s: %w", task.ID, err)
	}
	return nil
}

// askReplyPayload is the payload shape a delivered reply travels in.
//
// Extracted so the shape can be asserted directly: the checkpoint envelope is
// opaque by contract, and "the answer is somewhere in there" is not a check.
//
// BOTH keys are load-bearing. askReplyInputKey is what makes the answer visible to
// the asker's next quantum (see its doc); askReplyPayloadKey is the structured form
// a tool, a test or a future reader can rely on without parsing prose. A reply
// written under only one of them is either invisible to the model or unusable by
// code.
func askReplyPayload(peerID, topic string, reply any) map[string]any {
	return map[string]any{
		askReplyPayloadKey: map[string]any{
			"from":  peerID,
			"topic": topic,
			"reply": reply,
		},
		askReplyInputKey: renderAskReply(peerID, topic, reply),
	}
}

// renderAskReply renders the reply as the single line the model will read.
//
// It names the peer and the topic because the reading quantum has no other way to
// know who answered what — an unattributed answer is worse than none, since the
// model would attribute it to itself or to the wrong collaborator. The body is
// JSON when it can be, so a structured answer survives the round trip intact.
func renderAskReply(peerID, topic string, reply any) string {
	body := ""
	switch v := reply.(type) {
	case nil:
		body = "(no content)"
	case string:
		body = v
	default:
		if encoded, err := json.Marshal(v); err == nil {
			body = string(encoded)
		} else {
			body = fmt.Sprintf("%v", v)
		}
	}
	return fmt.Sprintf("Collaboration reply from %s on topic %q: %s", peerID, topic, body)
}

// askReplyCapability picks the capability the reply task runs under: the asker's
// first L2-routable capability.
//
// Deterministic (sorted) so the same asker always yields the same id-space and the
// same scheduling behaviour, and strict: an asker with no routable capability
// cannot run the task no matter how the task were named, so it is reported rather
// than queued into READY-forever.
func (k *Kernel) askReplyCapability(askerID string) (string, error) {
	if k.agents == nil {
		return "", stderrors.New("agentsyscall: agent fabric not wired")
	}
	agent, err := k.agents.Get(askerID)
	if err != nil {
		return "", fmt.Errorf("agentsyscall: asker %q not found: %w", askerID, err)
	}
	candidates := make([]string, 0, len(agent.Capabilities))
	for _, capability := range agent.Capabilities {
		if agentfabric.IsL2Capability(capability) {
			candidates = append(candidates, capability)
		}
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("agentsyscall: asker %q has no L2-routable capability to receive a reply (capabilities: %v)", askerID, agent.Capabilities)
	}
	sort.Strings(candidates)
	return candidates[0], nil
}

// askReplyTaskID derives the reply task's id from the ask AND the answer, so the
// same delivery twice is one task while a NEW answer to the same question is not
// swallowed as a duplicate.
//
// The reply is part of the fingerprint, and that is the whole point: keyed on
// (asker, peer, topic) alone, a second legitimate ask on the same topic — which
// the 30s dedupe window explicitly allows, minutes later — would collide with the
// first reply's id and be dropped as "already delivered", silently losing the
// answer. Identical bodies still collapse, which is the duplicate this idempotency
// is for.
//
// The fingerprint is hashed rather than concatenated because topic and peer ids are
// caller-supplied and may contain the separator; a hash keeps the id a predictable
// length and free of path-like characters.
func askReplyTaskID(askerID, peerID, topic string, reply any) string {
	sum := fnv.New64a()
	for _, part := range []string{askerID, peerID, topic, replyFingerprint(reply)} {
		_, _ = sum.Write([]byte(part))
		_, _ = sum.Write([]byte{0})
	}
	return "ask-reply-" + strconv.FormatUint(sum.Sum64(), 36)
}

// replyFingerprint renders a reply stably for the id.
//
// JSON is used so a map body fingerprints identically across runs (encoding/json
// sorts object keys); a body that cannot be encoded falls back to its %v form,
// which is stable for the value kinds that can reach here.
func replyFingerprint(reply any) string {
	encoded, err := json.Marshal(reply)
	if err != nil {
		return fmt.Sprintf("%v", reply)
	}
	return string(encoded)
}
