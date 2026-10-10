// package graph - tests for schedulers.

package graph

import "testing"

func TestDefaultScheduler(t *testing.T) {
	scheduler := NewDefaultScheduler()

	// Test empty queue
	if id := scheduler.Select([]string{}); id != "" {
		t.Errorf("expected empty string, got %s", id)
	}

	// Test single item
	if id := scheduler.Select([]string{"node1"}); id != "node1" {
		t.Errorf("expected node1, got %s", id)
	}

	// Test multiple items (FIFO)
	queue := []string{"node1", "node2", "node3"}
	if id := scheduler.Select(queue); id != "node1" {
		t.Errorf("expected node1, got %s", id)
	}
}
