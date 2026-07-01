package ares_ctxutil

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestWithDetachedLabel(t *testing.T) {
	ctx := WithDetachedLabel("shutdown")
	assert.NotNil(t, ctx)
	assert.Equal(t, "shutdown", DetachedLabel(ctx))
}

func TestWithDetachedLabel_Empty(t *testing.T) {
	ctx := WithDetachedLabel("")
	assert.NotNil(t, ctx)
	assert.Equal(t, "", DetachedLabel(ctx))
}

func TestDetachedLabel_NoLabel(t *testing.T) {
	ctx := context.Background()
	assert.Equal(t, "", DetachedLabel(ctx))
}

func TestDetachedLabel_WrongType(t *testing.T) {
	ctx := context.WithValue(context.Background(), detachedKey{}, 42)
	assert.Equal(t, "", DetachedLabel(ctx))
}

func TestWithDetachedTimeout(t *testing.T) {
	ctx, cancel := WithDetachedTimeout("timeout-op", 100*time.Millisecond)
	defer cancel()
	assert.NotNil(t, ctx)
	assert.Equal(t, "timeout-op", DetachedLabel(ctx))

	deadline, ok := ctx.Deadline()
	assert.True(t, ok)
	assert.WithinDuration(t, time.Now().Add(100*time.Millisecond), deadline, 50*time.Millisecond)
}

func TestWithDetachedTimeout_EmptyLabel(t *testing.T) {
	ctx, cancel := WithDetachedTimeout("", 100*time.Millisecond)
	defer cancel()
	assert.NotNil(t, ctx)
	assert.Equal(t, "", DetachedLabel(ctx))
}

func TestWithDetachedTimeout_Expires(t *testing.T) {
	ctx, cancel := WithDetachedTimeout("fast", 1*time.Millisecond)
	defer cancel()

	<-ctx.Done()
	assert.Error(t, ctx.Err())
}

func TestBackgroundStats_Tracking(t *testing.T) {
	// Start fresh
	bgTracker.mu.Lock()
	bgTracker.jobs = make(map[string]int64)
	bgTracker.mu.Unlock()

	ctx1 := WithDetachedLabel("job1")
	_ = ctx1
	stats := BackgroundStats()
	assert.Equal(t, int64(1), stats["job1"])

	ctx2 := WithDetachedLabel("job1")
	_ = ctx2
	stats = BackgroundStats()
	assert.Equal(t, int64(2), stats["job1"])

	ctx3 := WithDetachedLabel("job2")
	_ = ctx3
	stats = BackgroundStats()
	assert.Equal(t, int64(1), stats["job2"])
}

func TestDoneBackground(t *testing.T) {
	bgTracker.mu.Lock()
	bgTracker.jobs = map[string]int64{"task": 2}
	bgTracker.mu.Unlock()

	DoneBackground("task")
	stats := BackgroundStats()
	assert.Equal(t, int64(1), stats["task"])

	DoneBackground("task")
	stats = BackgroundStats()
	assert.NotContains(t, stats, "task")
}

func TestDoneBackground_NilMap(t *testing.T) {
	bgTracker.mu.Lock()
	bgTracker.jobs = nil
	bgTracker.mu.Unlock()

	// Should not panic
	DoneBackground("any")
}

func TestDoneBackground_UnknownLabel(t *testing.T) {
	bgTracker.mu.Lock()
	bgTracker.jobs = make(map[string]int64)
	bgTracker.mu.Unlock()

	// Should not panic
	DoneBackground("unknown")
}

func TestBackgroundStats_Empty(t *testing.T) {
	bgTracker.mu.Lock()
	bgTracker.jobs = make(map[string]int64)
	bgTracker.mu.Unlock()

	stats := BackgroundStats()
	assert.Empty(t, stats)
}

func TestBackgroundStats_Snapshot(t *testing.T) {
	bgTracker.mu.Lock()
	bgTracker.jobs = map[string]int64{"a": 1, "b": 2}
	bgTracker.mu.Unlock()

	stats := BackgroundStats()
	assert.Equal(t, int64(1), stats["a"])
	assert.Equal(t, int64(2), stats["b"])

	// Verify it's a copy, not a reference
	delete(stats, "a")
	bgTracker.mu.Lock()
	assert.Equal(t, int64(1), bgTracker.jobs["a"])
	bgTracker.mu.Unlock()
}

func TestBackgroundStats_ConcurrentSafe(t *testing.T) {
	bgTracker.mu.Lock()
	bgTracker.jobs = map[string]int64{"safe": 1}
	bgTracker.mu.Unlock()

	// BackgroundStats acquires its own lock
	stats := BackgroundStats()
	assert.Equal(t, int64(1), stats["safe"])
}
