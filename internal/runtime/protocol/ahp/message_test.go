// nolint: errcheck // Test code may ignore return values
package ahp

import (
	"testing"

	"github.com/Timwood0x10/ares/internal/core/models"
)

// TestAHPMessage covers the AHP message vocabulary retained after the
// zero-instantiation protocol machinery (Protocol/Queue/DLQ/HeartbeatMonitor/
// Codec) was retired in 0.3.3 (plan/0.3.3_task.md Appendix A.1). Only
// message.go survives — AHPMessage + its constructors/predicates are the live
// surface, used by agents/base, agents/peer and cmd/ares.
func TestAHPMessage(t *testing.T) {
	t.Run("create message", func(t *testing.T) {
		msg := NewMessage(AHPMethodTask, "leader", "sub1", "task1", "session1")

		if msg.Method != AHPMethodTask {
			t.Errorf("expected TASK method, got %s", msg.Method)
		}
		if msg.AgentID != "leader" {
			t.Errorf("expected leader, got %s", msg.AgentID)
		}
		if msg.TargetAgent != "sub1" {
			t.Errorf("expected sub1, got %s", msg.TargetAgent)
		}
		if msg.TaskID != "task1" {
			t.Errorf("expected task1, got %s", msg.TaskID)
		}
		if msg.SessionID != "session1" {
			t.Errorf("expected session1, got %s", msg.SessionID)
		}
		if msg.MessageID == "" {
			t.Errorf("expected message id to be set")
		}
		if msg.Payload == nil {
			t.Errorf("expected payload to be initialized")
		}
	})

	t.Run("create task message", func(t *testing.T) {
		payload := map[string]any{"key": "value"}
		msg := NewTaskMessage("leader", "sub1", "task1", "session1", payload)

		if msg.Method != AHPMethodTask {
			t.Errorf("expected TASK method")
		}
		if msg.Payload["key"] != "value" {
			t.Errorf("expected payload to be set")
		}
	})

	t.Run("create result message", func(t *testing.T) {
		result := &models.TaskResult{TaskID: "task1", Success: true}
		msg := NewResultMessage("sub1", "leader", "task1", "session1", result)

		if msg.Method != AHPMethodResult {
			t.Errorf("expected RESULT method")
		}
	})

	t.Run("create progress message", func(t *testing.T) {
		msg := NewProgressMessage("sub1", "leader", "task1", "session1", 0.5)

		if msg.Method != AHPMethodProgress {
			t.Errorf("expected PROGRESS method")
		}
		if msg.Payload["progress"] != 0.5 {
			t.Errorf("expected progress 0.5")
		}
	})

	t.Run("create ACK message", func(t *testing.T) {
		msg := NewACKMessage("sub1", "leader", "task1", "session1")

		if msg.Method != AHPMethodACK {
			t.Errorf("expected ACK method")
		}
	})

	t.Run("create heartbeat message", func(t *testing.T) {
		msg := NewHeartbeatMessage("agent1")

		if msg.Method != AHPMethodHeartbeat {
			t.Errorf("expected HEARTBEAT method")
		}
		if msg.AgentID != "agent1" {
			t.Errorf("expected agent1")
		}
	})

	t.Run("is task", func(t *testing.T) {
		msg := NewMessage(AHPMethodTask, "leader", "sub1", "task1", "session1")
		if !msg.IsTask() {
			t.Errorf("expected IsTask to return true")
		}
	})

	t.Run("is result", func(t *testing.T) {
		msg := NewMessage(AHPMethodResult, "leader", "sub1", "task1", "session1")
		if !msg.IsResult() {
			t.Errorf("expected IsResult to return true")
		}
	})

	t.Run("is heartbeat", func(t *testing.T) {
		msg := NewMessage(AHPMethodHeartbeat, "leader", "sub1", "task1", "session1")
		if !msg.IsHeartbeat() {
			t.Errorf("expected IsHeartbeat to return true")
		}
	})
}

// TestMessageGetMethods covers the typed accessors on the retained AHPMessage.
func TestMessageGetMethods(t *testing.T) {
	t.Run("get result success", func(t *testing.T) {
		result := &models.TaskResult{TaskID: "task1", Success: true}
		msg := NewResultMessage("sub1", "leader", "task1", "session1", result)

		retrieved, ok := msg.GetResult()
		if !ok {
			t.Errorf("expected to get result")
		}
		if retrieved.TaskID != "task1" {
			t.Errorf("expected task1")
		}
	})

	t.Run("get result wrong method", func(t *testing.T) {
		msg := NewMessage(AHPMethodTask, "leader", "sub1", "task1", "session1")
		_, ok := msg.GetResult()
		if ok {
			t.Errorf("expected false for wrong method")
		}
	})

	t.Run("get progress success", func(t *testing.T) {
		msg := NewProgressMessage("sub1", "leader", "task1", "session1", 0.75)

		progress, ok := msg.GetProgress()
		if !ok {
			t.Errorf("expected to get progress")
		}
		if progress != 0.75 {
			t.Errorf("expected 0.75")
		}
	})

	t.Run("get progress wrong method", func(t *testing.T) {
		msg := NewMessage(AHPMethodTask, "leader", "sub1", "task1", "session1")
		_, ok := msg.GetProgress()
		if ok {
			t.Errorf("expected false for wrong method")
		}
	})
}
