package agentipc

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestBus_StalledHandlerIsReported: a handler that ignores cancellation keeps its goroutine alive
// after the caller has been answered with a timeout. That is a leak the timeout cannot clean up, and
// before this the only way to learn about it was a goroutine dump during an incident.
//
// The assertion is on the log line, because that is the whole point: the goroutine itself cannot be
// reaped from here.
func TestBus_StalledHandlerIsReported(t *testing.T) {
	var buf syncBuffer
	bus := NewBus().WithLogger(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	release := make(chan struct{})
	defer close(release)
	if err := bus.Register("stuck", func(context.Context, *Message) (*Message, error) {
		<-release // deliberately ignores ctx
		return nil, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	_, err := bus.Request(context.Background(), "caller", "stuck", "topic", "payload", 20*time.Millisecond)
	if err == nil {
		t.Fatal("the request should have timed out")
	}

	if got := buf.String(); !strings.Contains(got, "handler still running after the request timed out") {
		t.Fatalf("a handler that outlived its timeout must be visible in the log; got %q", got)
	}
}

// TestBus_StalledHandlerWarningIsRateLimited: one warning per minute, not one per request. A stuck
// handler under load would otherwise flood the log with the same line and bury the first one.
func TestBus_StalledHandlerWarningIsRateLimited(t *testing.T) {
	var buf syncBuffer
	bus := NewBus().WithLogger(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))

	release := make(chan struct{})
	defer close(release)
	if err := bus.Register("stuck", func(context.Context, *Message) (*Message, error) {
		<-release
		return nil, nil
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	for i := 0; i < 5; i++ {
		_, _ = bus.Request(context.Background(), "caller", "stuck", "topic", "payload", 5*time.Millisecond)
	}

	if got := strings.Count(buf.String(), "handler still running"); got != 1 {
		t.Fatalf("want exactly one warning within the interval, got %d", got)
	}
}

// TestBus_ClockSwapIsRaceFree: allocNow read b.now while WithClock wrote it. Construct-then-use made
// that benign in production, which is exactly the kind of benign that survives until someone swaps
// the clock at runtime (tests do). This one is only meaningful under -race.
func TestBus_ClockSwapIsRaceFree(t *testing.T) {
	bus := NewBus()

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		var n int64
		for {
			select {
			case <-stop:
				return
			default:
				n++
				at := time.Unix(n, 0)
				bus.WithClock(func() time.Time { return at })
			}
		}
	}()

	var reads atomic.Int64
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				_ = bus.allocNow()
				reads.Add(1)
			}
		}()
	}

	// Let the readers finish before stopping the writer, so the loop is not interrupted mid-race.
	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()

	if reads.Load() == 0 {
		t.Fatal("the readers never ran")
	}
}

// syncBuffer is a bytes.Buffer safe for concurrent writes from slog handlers.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
