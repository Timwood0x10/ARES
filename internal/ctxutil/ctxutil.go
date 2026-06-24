// Package ctxutil provides context utilities for tracing and lifecycle management.
package ctxutil

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

type detachedKey struct{}

// WithDetachedLabel wraps context.Background() with a label that can be
// identified in traces and debug logging. Use when you genuinely need to
// detach from a parent context (e.g., during shutdown when the parent is
// already cancelled) but still want observability.
func WithDetachedLabel(label string) context.Context {
	if label != "" {
		trackBackground(label)
	}
	return context.WithValue(context.Background(), detachedKey{}, label)
}

// DetachedLabel extracts the detached context label, if any.
func DetachedLabel(ctx context.Context) string {
	if v, ok := ctx.Value(detachedKey{}).(string); ok {
		return v
	}
	return ""
}

// WithDetachedTimeout is like WithDetachedLabel but also sets a timeout.
func WithDetachedTimeout(label string, timeout time.Duration) (context.Context, context.CancelFunc) {
	if label != "" {
		trackBackground(label)
	}
	ctx := context.WithValue(context.Background(), detachedKey{}, label)
	return context.WithTimeout(ctx, timeout)
}

// backgroundTracker records active background operations for observability.
var bgTracker struct {
	mu   sync.Mutex
	jobs map[string]int64
	seq  atomic.Int64
}

func trackBackground(label string) {
	bgTracker.mu.Lock()
	defer bgTracker.mu.Unlock()
	if bgTracker.jobs == nil {
		bgTracker.jobs = make(map[string]int64)
	}
	bgTracker.jobs[label]++
}

// DoneBackground records that a background operation completed.
func DoneBackground(label string) {
	bgTracker.mu.Lock()
	defer bgTracker.mu.Unlock()
	if bgTracker.jobs == nil {
		return
	}
	if v := bgTracker.jobs[label]; v > 1 {
		bgTracker.jobs[label] = v - 1
	} else {
		delete(bgTracker.jobs, label)
	}
}

// BackgroundStats returns a snapshot of currently active background operations.
func BackgroundStats() map[string]int64 {
	bgTracker.mu.Lock()
	defer bgTracker.mu.Unlock()
	snap := make(map[string]int64, len(bgTracker.jobs))
	for k, v := range bgTracker.jobs {
		snap[k] = v
	}
	return snap
}
