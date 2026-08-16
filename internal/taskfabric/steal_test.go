package taskfabric

import "testing"

// capMap is a tiny capabilityOf helper for the queue tests.
func capMap(m map[string]string) func(string) string {
	return func(id string) string { return m[id] }
}

// TestAgentQueueEnqueueLen verifies queue basics.
func TestAgentQueueEnqueueLen(t *testing.T) {
	q := NewAgentQueue("agent-a")
	if q.Len() != 0 {
		t.Fatalf("want empty, got %d", q.Len())
	}
	q.Enqueue("t1")
	q.Enqueue("t2")
	if q.Len() != 2 {
		t.Fatalf("want 2, got %d", q.Len())
	}
}

// TestStealIsCapabilityAware verifies the stealer only takes tasks it is
// capable of, skipping incapable ones (design §8: capability-aware stealing).
func TestStealIsCapabilityAware(t *testing.T) {
	owner := NewAgentQueue("agent-c")
	owner.Enqueue("python-task")
	owner.Enqueue("rust-task")
	caps := map[string]string{"python-task": "python", "rust-task": "rust"}

	stealer := NewAgentQueue("agent-rust")
	stolen, ok := stealer.Steal(owner, []string{"rust"}, capMap(caps))
	if !ok || stolen != "rust-task" {
		t.Fatalf("want rust-task stolen, got %q ok=%v", stolen, ok)
	}
	// The python task is untouched.
	if owner.Len() != 1 {
		t.Fatalf("want 1 task left, got %d", owner.Len())
	}
}

// TestStealNothingWhenIncapable verifies a stealer with no matching
// capability steals nothing and the owner's queue stays intact.
func TestStealNothingWhenIncapable(t *testing.T) {
	owner := NewAgentQueue("agent-c")
	owner.Enqueue("rust-task")
	caps := map[string]string{"rust-task": "rust"}

	stealer := NewAgentQueue("agent-python")
	if _, ok := stealer.Steal(owner, []string{"python"}, capMap(caps)); ok {
		t.Fatal("incapable stealer must steal nothing")
	}
	if owner.Len() != 1 {
		t.Fatalf("owner queue must be intact, got %d", owner.Len())
	}
}
