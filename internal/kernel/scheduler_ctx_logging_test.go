package kernel

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

type traceKey struct{}

// probeHandler records the ctx it was handed. slog passes the caller's ctx to Handle, so this is
// how a test can see whether a log line was emitted from the drain's context or from
// context.Background().
type probeHandler struct {
	mu   sync.Mutex
	seen []any
}

func (h *probeHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *probeHandler) Handle(ctx context.Context, _ slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seen = append(h.seen, ctx.Value(traceKey{}))
	return nil
}

func (h *probeHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *probeHandler) WithGroup(string) slog.Handler      { return h }

func (h *probeHandler) values() []any {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]any(nil), h.seen...)
}

// TestHandleStaleWinner_LogsFromTheCallersContext: these helpers used to log with
// context.Background(), so the line carried no trace or tenant — the values the drain ctx holds are
// exactly what makes an incident readable. Threading ctx through is only worth anything if it
// actually reaches the handler.
func TestHandleStaleWinner_LogsFromTheCallersContext(t *testing.T) {
	probe := &probeHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(probe))
	t.Cleanup(func() { slog.SetDefault(prev) })

	fabric := taskfabric.NewFabric()
	require.NoError(t, fabric.Create(&taskfabric.Task{ID: "stale-task", Capability: "code"}))
	sched := New(fabric, map[string]CapabilityExecutor{}, NewLoadTracker())

	ctx := context.WithValue(context.Background(), traceKey{}, "trace-abc")
	_ = sched.handleStaleWinner(ctx, "stale-task", "ghost-agent", 7)

	seen := probe.values()
	require.NotEmpty(t, seen, "the helper must log something")
	for _, v := range seen {
		assert.Equal(t, "trace-abc", v,
			"a log line emitted without the caller's context is unattributable to a request")
	}
}

// TestScheduler_SettersAreSafeWhileRunning: WithMaxConcurrent and WithTTL were plain field writes
// on an object the drain loop reads every tick. The documented contract was "call before Run", but
// nothing enforced it and the setters are a fluent chain on a shared pointer.
//
// The assertion is the absence of a data race under -race; the value checks only confirm the late
// calls actually took effect.
func TestScheduler_SettersAreSafeWhileRunning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sched := New(taskfabric.NewFabric(), map[string]CapabilityExecutor{}, NewLoadTracker())
	sched.PollInterval = time.Millisecond
	go sched.Run(ctx)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			sched.WithMaxConcurrent(n%4 + 1)
			sched.WithTTL(time.Duration(n%3+1) * time.Second)
		}(i)
	}
	wg.Wait()

	assert.Positive(t, sched.leaseTTL())
}
