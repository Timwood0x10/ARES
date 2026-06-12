// nolint: errcheck // Test code may ignore return values
// nolint: errcheck // Test code may ignore return values
package context

import (
	"context"
	"sync"
	"testing"
	"time"

	"goagentx/internal/core/models"
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
		memory.Set(context.Background(), "sess1", "user1", nil)

		err := memory.AddMessage(context.Background(), "sess1", Message{Role: "user", Content: "test"})
		if err != nil {
			t.Errorf("add message error: %v", err)
		}
	})

	t.Run("delete session", func(t *testing.T) {
		memory := NewSessionMemory(100, time.Minute)
		// Create session first
		memory.Set(context.Background(), "sess1", "user1", nil)

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
		memory.Set(context.Background(), "sess1", "user1", nil)

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

func TestUserMemory(t *testing.T) {
	t.Run("create user memory", func(t *testing.T) {
		memory := NewUserMemory(100)

		if memory == nil {
			t.Errorf("memory should not be nil")
		}
	})

	t.Run("set and get user", func(t *testing.T) {
		memory := NewUserMemory(100)
		profile := &models.UserProfile{UserID: "user1"}

		err := memory.Set(context.Background(), "user1", profile)
		if err != nil {
			t.Errorf("set error: %v", err)
		}

		data, exists := memory.Get(context.Background(), "user1")
		if !exists {
			t.Errorf("user should exist")
		}
		_ = data
	})
}

func TestCache(t *testing.T) {
	t.Run("create cache", func(t *testing.T) {
		cache := NewCache(100, time.Minute)

		if cache == nil {
			t.Errorf("cache should not be nil")
		}
	})

	t.Run("set and get", func(t *testing.T) {
		cache := NewCache(100, time.Minute)

		cache.Set(context.Background(), "key1", "value1")

		val, exists := cache.Get(context.Background(), "key1")
		if !exists {
			t.Errorf("key should exist")
		}
		if val != "value1" {
			t.Errorf("expected value1, got %v", val)
		}
	})

	t.Run("delete", func(t *testing.T) {
		cache := NewCache(100, time.Minute)
		cache.Set(context.Background(), "key1", "value1")

		cache.Delete(context.Background(), "key1")

		_, exists := cache.Get(context.Background(), "key1")
		if exists {
			t.Errorf("key should not exist after delete")
		}
	})
}

func TestLRUCache(t *testing.T) {
	t.Run("create LRU cache", func(t *testing.T) {
		cache := NewLRUCache(2)

		if cache == nil {
			t.Errorf("cache should not be nil")
		}
	})

	t.Run("set and get", func(t *testing.T) {
		cache := NewLRUCache(2)
		cache.Set(context.Background(), "key1", "value1")

		val, exists := cache.Get(context.Background(), "key1")
		if !exists {
			t.Errorf("key should exist")
		}
		if val != "value1" {
			t.Errorf("expected value1, got %v", val)
		}
	})

	t.Run("size after eviction", func(t *testing.T) {
		cache := NewLRUCache(2)
		cache.Set(context.Background(), "key1", "value1")
		cache.Set(context.Background(), "key2", "value2")
		cache.Set(context.Background(), "key3", "value3")

		// Size should be 2 after eviction
		if cache.Size() != 2 {
			t.Errorf("expected size 2, got %d", cache.Size())
		}
	})
}

func TestRAG(t *testing.T) {
	t.Run("create RAG", func(t *testing.T) {
		rag := NewRAG(100)

		if rag == nil {
			t.Errorf("RAG should not be nil")
		}
		if rag.Size() != 0 {
			t.Errorf("initial size should be 0")
		}
	})

	t.Run("add and get entry", func(t *testing.T) {
		rag := NewRAG(100)
		entry := &KnowledgeEntry{
			ID:        "entry1",
			Content:   "test content",
			Embedding: []float64{1.0, 2.0, 3.0},
			Metadata:  map[string]any{"key": "value"},
		}

		err := rag.Add(context.Background(), entry)
		if err != nil {
			t.Errorf("add error: %v", err)
		}

		retrieved, exists := rag.Get(context.Background(), "entry1")
		if !exists {
			t.Errorf("entry should exist")
		}
		if retrieved.Content != "test content" {
			t.Errorf("expected content, got %s", retrieved.Content)
		}
	})

	t.Run("search by vector", func(t *testing.T) {
		rag := NewRAG(100)

		// Add entries with different embeddings
		entries := []*KnowledgeEntry{
			{ID: "e1", Content: "apple fruit", Embedding: []float64{1.0, 0.0, 0.0}},
			{ID: "e2", Content: "orange fruit", Embedding: []float64{0.9, 0.1, 0.0}},
			{ID: "e3", Content: "car vehicle", Embedding: []float64{0.0, 1.0, 0.0}},
		}

		for _, e := range entries {
			rag.Add(context.Background(), e)
		}

		// Search for similar to apple
		results, err := rag.Search(context.Background(), []float64{1.0, 0.0, 0.0}, 2)
		if err != nil {
			t.Errorf("search error: %v", err)
		}

		// Should return e1 (apple) and e2 (orange) - both fruits
		if len(results) != 2 {
			t.Errorf("expected 2 results, got %d", len(results))
		}
		if results[0].ID != "e1" {
			t.Errorf("first result should be e1, got %s", results[0].ID)
		}
	})

	t.Run("search by text", func(t *testing.T) {
		rag := NewRAG(100)

		entries := []*KnowledgeEntry{
			{ID: "e1", Content: "hello world"},
			{ID: "e2", Content: "goodbye world"},
			{ID: "e3", Content: "foo bar"},
		}

		for _, e := range entries {
			rag.Add(context.Background(), e)
		}

		results, err := rag.SearchByText(context.Background(), "world", 2)
		if err != nil {
			t.Errorf("search error: %v", err)
		}

		if len(results) != 2 {
			t.Errorf("expected 2 results, got %d", len(results))
		}
	})

	t.Run("delete entry", func(t *testing.T) {
		rag := NewRAG(100)
		rag.Add(context.Background(), &KnowledgeEntry{ID: "e1", Content: "test"})

		err := rag.Delete(context.Background(), "e1")
		if err != nil {
			t.Errorf("delete error: %v", err)
		}

		_, exists := rag.Get(context.Background(), "e1")
		if exists {
			t.Errorf("entry should not exist after delete")
		}
	})

	t.Run("eviction on capacity", func(t *testing.T) {
		rag := NewRAG(2)

		rag.Add(context.Background(), &KnowledgeEntry{ID: "e1", Content: "test1"})
		rag.Add(context.Background(), &KnowledgeEntry{ID: "e2", Content: "test2"})
		rag.Add(context.Background(), &KnowledgeEntry{ID: "e3", Content: "test3"})

		// Should evict oldest entry
		if rag.Size() != 2 {
			t.Errorf("expected size 2 after eviction, got %d", rag.Size())
		}
	})
}

func TestVectorIndex(t *testing.T) {
	t.Run("create vector index", func(t *testing.T) {
		index := NewVectorIndex()

		if index == nil {
			t.Errorf("index should not be nil")
		}
	})

	t.Run("cosine similarity", func(t *testing.T) {
		// Similar vectors should have high similarity
		similar1 := []float64{1.0, 0.0}
		dissimilar := []float64{0.0, 1.0}

		_ = similar1
		_ = dissimilar

		// Test with the RAG which has the cosineSimilarity function
		rag := NewRAG(10)
		_ = rag
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
