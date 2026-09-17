// nolint: errcheck // Test code may ignore return values
// nolint: errcheck // Test code may ignore return values
package context

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSessionMemory(t *testing.T) {
	t.Run("create session memory", func(t *testing.T) {
		memory := NewSessionMemory(100, time.Minute)

		if memory == nil {
			t.Errorf("memory should not be nil")
		}
	})

	t.Run("set and get session", func(t *testing.T) {
		memory := NewSessionMemory(100, time.Minute)
		messages := []Message{{Role: "user", Content: "hello"}}

		// Test code: memory.Set is used to set test data
		// nolint: errcheck // This is intentional in test code
		err := memory.Set(context.Background(), "sess1", "user1", messages)
		if err != nil {
			t.Errorf("set error: %v", err)
		}

		data, exists := memory.Get(context.Background(), "sess1")
		if !exists {
			t.Errorf("session should exist")
		}
		if data.UserID != "user1" {
			t.Errorf("expected user1, got %s", data.UserID)
		}
	})

	t.Run("add message", func(t *testing.T) {
		memory := NewSessionMemory(100, time.Minute)
		// Create session first
		_ = memory.Set(context.Background(), "sess1", "user1", nil) // Test setup, error ignored

		err := memory.AddMessage(context.Background(), "sess1", Message{Role: "user", Content: "test"})
		if err != nil {
			t.Errorf("add message error: %v", err)
		}
	})

	t.Run("delete session", func(t *testing.T) {
		memory := NewSessionMemory(100, time.Minute)
		// Create session first
		_ = memory.Set(context.Background(), "sess1", "user1", nil) // Test setup, error ignored

		err := memory.Delete(context.Background(), "sess1")
		if err != nil {
			t.Errorf("delete error: %v", err)
		}

		_, exists := memory.Get(context.Background(), "sess1")
		if exists {
			t.Errorf("session should not exist after delete")
		}
	})

	t.Run("size", func(t *testing.T) {
		memory := NewSessionMemory(100, time.Minute)
		// Create session first
		_ = memory.Set(context.Background(), "sess1", "user1", nil) // Test setup, error ignored

		if memory.Size() != 1 {
			t.Errorf("expected size 1, got %d", memory.Size())
		}
	})
}

func TestTaskMemoryTTL(t *testing.T) {
	t.Run("TTL cleanup removes expired tasks", func(t *testing.T) {
		memory := NewTaskMemory(100, 100*time.Millisecond)
		ctx := context.Background()

		err := memory.Set(ctx, "task1", "sess1", "user1", "input1")
		if err != nil {
			t.Fatalf("set error: %v", err)
		}

		_, exists := memory.Get(ctx, "task1")
		if !exists {
			t.Errorf("task1 should exist immediately after set")
		}

		memory.Start(ctx)
		defer memory.Stop()

		time.Sleep(200 * time.Millisecond)

		_, exists = memory.Get(ctx, "task1")
		if exists {
			t.Errorf("task1 should have been cleaned up after TTL expired")
		}
	})

	t.Run("Start is idempotent", func(t *testing.T) {
		memory := NewTaskMemory(100, time.Minute)
		ctx := context.Background()

		memory.Start(ctx)
		memory.Start(ctx)
		memory.Start(ctx)

		memory.Stop()
	})

	t.Run("Stop is idempotent", func(t *testing.T) {
		memory := NewTaskMemory(100, time.Minute)
		ctx := context.Background()

		memory.Start(ctx)
		memory.Stop()
		memory.Stop()
		memory.Stop()
	})
}

// TestSessionMemory_ConcurrentGet tests that concurrent Get calls don't cause data races.
func TestSessionMemory_ConcurrentGet(t *testing.T) {
	sm := NewSessionMemory(100, 10*time.Second)
	sm.StartCleanup()
	defer sm.Close(context.Background())

	// Pre-populate a session.
	err := sm.Set(context.Background(), "session-1", "user-1", []Message{
		{Role: "user", Content: "hello", Time: time.Now()},
	})
	if err != nil {
		t.Fatalf("failed to set session: %v", err)
	}

	// Concurrent reads should not race.
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				data, ok := sm.Get(context.Background(), "session-1")
				if !ok {
					t.Error("expected session to exist")
					return
				}
				if data.SessionID != "session-1" {
					t.Errorf("expected session ID session-1, got %s", data.SessionID)
				}
			}
		}()
	}
	wg.Wait()
}

// TestSessionMemory_GetUpdatesAccessedAt tests that Get updates the access time.
func TestSessionMemory_GetUpdatesAccessedAt(t *testing.T) {
	sm := NewSessionMemory(100, 10*time.Second)
	defer sm.Close(context.Background())

	err := sm.Set(context.Background(), "session-1", "user-1", []Message{
		{Role: "user", Content: "hello", Time: time.Now()},
	})
	if err != nil {
		t.Fatalf("failed to set session: %v", err)
	}

	// Get the session.
	data, ok := sm.Get(context.Background(), "session-1")
	if !ok {
		t.Fatal("expected session to exist")
	}

	originalAccessedAt := data.AccessedAt

	// Wait a bit and get again.
	time.Sleep(10 * time.Millisecond)

	data, ok = sm.Get(context.Background(), "session-1")
	if !ok {
		t.Fatal("expected session to exist after second get")
	}

	if !data.AccessedAt.After(originalAccessedAt) {
		t.Error("AccessedAt should be updated on each Get call")
	}
}

// TestSessionMemory_GetExpiredSession tests that expired sessions are cleaned up on Get.
func TestSessionMemory_GetExpiredSession(t *testing.T) {
	sm := NewSessionMemory(100, 50*time.Millisecond)
	defer sm.Close(context.Background())

	err := sm.Set(context.Background(), "session-1", "user-1", []Message{
		{Role: "user", Content: "hello", Time: time.Now()},
	})
	if err != nil {
		t.Fatalf("failed to set session: %v", err)
	}

	// Wait for TTL to expire.
	time.Sleep(60 * time.Millisecond)

	_, ok := sm.Get(context.Background(), "session-1")
	if ok {
		t.Error("expired session should not be found")
	}
}

func contains(text, substr string) bool {
	return strings.Contains(text, substr)
}

// TestSessionMemory_AddMessageBounded pins the storage bound (P1): AddMessage
// used to append without limit — MaxHistory only truncates at BuildContext
// READ time, so a long-lived session's stored message slice (and every
// GetMessages/BuildContext copy of it) grew without bound across a server
// lifetime. The store keeps the newest maxMessages and drops the oldest,
// which is exactly what the read-side "keep last N" window draws from.
func TestSessionMemory_AddMessageBounded(t *testing.T) {
	memory := NewSessionMemory(10, time.Minute).WithMaxMessages(5)
	if err := memory.Set(context.Background(), "s1", "u1", nil); err != nil {
		t.Fatalf("Set: %v", err)
	}
	for i := range 12 {
		msg := Message{Role: RoleUser, Content: strings.Repeat("x", i+1)}
		if err := memory.AddMessage(context.Background(), "s1", msg); err != nil {
			t.Fatalf("AddMessage %d: %v", i, err)
		}
	}

	got, err := memory.GetMessages(context.Background(), "s1")
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("stored messages = %d, want the cap of 5 (newest kept)", len(got))
	}
	// The survivors must be the NEWEST five (contents of length 8..12).
	for i, msg := range got {
		wantLen := 8 + i
		if len(msg.Content) != wantLen {
			t.Fatalf("kept message %d has content length %d, want %d (oldest must be dropped first)",
				i, len(msg.Content), wantLen)
		}
	}
}

// TestSessionMemory_DefaultMessageCap pins that a plain constructor is
// bounded too — the fix must not depend on a caller remembering to set a cap.
func TestSessionMemory_DefaultMessageCap(t *testing.T) {
	memory := NewSessionMemory(10, time.Minute)
	if err := memory.Set(context.Background(), "s1", "u1", nil); err != nil {
		t.Fatalf("Set: %v", err)
	}
	for i := 0; i < defaultMaxSessionMessages+50; i++ {
		if err := memory.AddMessage(context.Background(), "s1", Message{Role: RoleUser}); err != nil {
			t.Fatalf("AddMessage %d: %v", i, err)
		}
	}
	got, err := memory.GetMessages(context.Background(), "s1")
	if err != nil {
		t.Fatalf("GetMessages: %v", err)
	}
	if len(got) != defaultMaxSessionMessages {
		t.Fatalf("stored messages = %d, want the default cap %d", len(got), defaultMaxSessionMessages)
	}
}

// TestSessionMemory_GetMessagesRefreshesTTL pins the TTL contract (P1):
// GetMessages took only an RLock and never touched AccessedAt, so a session
// reached ONLY through GetMessages (BuildPromptMessages/BuildContext and the
// memory tool both read via it) aged out of the TTL sweep mid-conversation —
// its history vanished between two turns of an active chat. Every read that
// proves the session is in use must refresh the access time, like Get does.
func TestSessionMemory_GetMessagesRefreshesTTL(t *testing.T) {
	memory := NewSessionMemory(10, 100*time.Millisecond)
	if err := memory.Set(context.Background(), "s1", "u1", []Message{{Role: RoleUser, Content: "hi"}}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Age the session to the edge of its TTL, then read it via GetMessages
	// only (no Get/AddMessage) — the read must count as proof of life.
	memory.mu.Lock()
	memory.sessions["s1"].AccessedAt = time.Now().Add(-90 * time.Millisecond)
	before := memory.sessions["s1"].AccessedAt
	memory.mu.Unlock()

	if _, err := memory.GetMessages(context.Background(), "s1"); err != nil {
		t.Fatalf("GetMessages: %v", err)
	}

	memory.mu.Lock()
	after := memory.sessions["s1"].AccessedAt
	memory.mu.Unlock()
	if !after.After(before) {
		t.Fatalf("GetMessages must refresh AccessedAt: before=%v after=%v", before, after)
	}

	// And the refreshed access keeps the session alive past the original
	// deadline: the cleanup sweep must not reap it.
	time.Sleep(30 * time.Millisecond) // now 120ms after the original stamp
	if removed := memory.Cleanup(context.Background()); removed != 0 {
		t.Fatalf("cleanup removed %d sessions; a session read via GetMessages must survive its TTL window", removed)
	}
	if _, err := memory.GetMessages(context.Background(), "s1"); err != nil {
		t.Fatalf("session must still be readable after the sweep: %v", err)
	}
}
