package pgvector

import (
	"context"
	"sync"
	"testing"

	"github.com/Timwood0x10/ares/internal/storage/postgres"
	"github.com/Timwood0x10/ares/internal/storage/postgres/repositories"
)

// newTestAdapter builds an Adapter the way New does, without requiring a live
// PostgreSQL pool. The repository is constructed with nil handles — the tests
// here only exercise the closed-adapter guards, which must fire before any
// database access.
func newTestAdapter() *Adapter {
	db := (&postgres.Pool{}).GetDB()
	return &Adapter{
		pool:  &postgres.Pool{},
		repo:  repositories.NewKnowledgeRepository(db, db),
		table: "knowledge_chunks_1024",
	}
}

// TestAdapter_UseAfterCloseIsErrorNotPanic is the #45 regression: Close()
// drops the pool/repo references, but readers (Search/Upsert/HealthCheck)
// never synchronized on that. A concurrent or subsequent read dereferenced a
// nil repo/pool and panicked. After Close, every operation must return a
// clean "closed" error instead.
func TestAdapter_UseAfterCloseIsErrorNotPanic(t *testing.T) {
	a := newTestAdapter()
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("second Close must be idempotent, got: %v", err)
	}

	if _, err := a.Search(context.Background(), []float64{0.1}, "tenant", 5); err == nil {
		t.Error("Search after Close must return an error")
	}
	if err := a.Upsert(context.Background(), "tenant", nil); err == nil {
		t.Error("Upsert after Close must return an error")
	}
	if err := a.HealthCheck(context.Background()); err == nil {
		t.Error("HealthCheck after Close must return an error")
	}
}

// TestAdapter_ConcurrentCloseVsReads runs readers against a Close under -race:
// the race detector flags unsynchronized access to the pool/repo fields if a
// reader touches them without the mutex.
func TestAdapter_ConcurrentCloseVsReads(t *testing.T) {
	a := newTestAdapter()

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			// Errors are expected (closed adapter, or nil-handle repo);
			// a panic or a -race failure is the regression.
			_, _ = a.Search(context.Background(), []float64{0.1}, "t", 1)
			_ = a.HealthCheck(context.Background())
			_ = a.Upsert(context.Background(), "t", nil)
		}()
	}
	close(start)
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	wg.Wait()
}
