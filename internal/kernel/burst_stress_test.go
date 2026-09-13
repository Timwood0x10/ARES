package kernel

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	"github.com/Timwood0x10/ares/internal/fabric/planprojection"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// TestBurstStressConcurrentSchedulers reproduces the full-suite flaky window:
// several bursts run concurrently (mimicking package-parallel test load) so
// the coordinator goroutines and schedulers contend for CPU, timers, and the
// fabric lock. Any burst whose tail nodes stay unmaterialized past the
// generous window is the F-21-family stall, caught deterministically enough
// to iterate on.
func TestBurstStressConcurrentSchedulers(t *testing.T) {
	const bursts = 6
	const n = 70

	var wg sync.WaitGroup
	errs := make(chan string, bursts)
	for b := 0; b < bursts; b++ {
		wg.Add(1)
		go func(b int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()

			fabric := taskfabric.NewFabric()
			coord := planprojection.NewCompileCoordinator(fabric, nil)
			plan, err := agentfabric.NewL2Graph("root", fmt.Sprintf("burst-%d", b), nil)
			if err != nil {
				errs <- fmt.Sprintf("burst %d: %v", b, err)
				return
			}
			stop := coord.SubscribeGraphEvents(ctx, plan.DAG())
			defer stop()
			// No t-based helpers from this goroutine: require's FailNow may
			// only run on the test goroutine. The root compile is the same
			// one admitSessionRoot performs.
			root := plan.DAG().StepIndex()[plan.Root()]
			if root == nil {
				errs <- fmt.Sprintf("burst %d: L2 plan carries no session root", b)
				return
			}
			if _, err := fabric.CompileNode(ctx, planprojection.ProjectStep(root)); err != nil {
				errs <- fmt.Sprintf("burst %d: compile root: %v", b, err)
				return
			}

			agents := agentfabric.NewFabric()
			if err := spawnSessionAgent(ctx, agents, &echoBinder{}); err != nil {
				errs <- fmt.Sprintf("burst %d: %v", b, err)
				return
			}
			sched := New(fabric, map[string]CapabilityExecutor{}, NewLoadTracker())
			sched.WithAgentFabric(agents)
			sched.PollInterval = 2 * time.Millisecond
			go sched.Run(ctx)

			ids := make([]string, 0, n+1)
			ids = append(ids, "root")
			prev := "root"
			for i := 0; i < n; i++ {
				id := fmt.Sprintf("b%d", i)
				if err := plan.AddToolNode(ctx, id, "echo", map[string]any{"query": fmt.Sprintf("q%d", i)}, prev); err != nil {
					errs <- fmt.Sprintf("burst %d: %v", b, err)
					return
				}
				ids = append(ids, id)
				prev = id
			}

			// Wait for FULL COMPLETION (the drain must finish every chain).
			var missing []string
			deadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(deadline) {
				missing = missing[:0]
				for _, id := range ids {
					tk, err := fabric.Task(id)
					if err != nil || (tk.State != taskfabric.StateCompleted && tk.State != taskfabric.StateFailed) {
						missing = append(missing, id)
					}
				}
				if len(missing) == 0 {
					return // burst converged
				}
				select {
				case <-ctx.Done():
					errs <- fmt.Sprintf("burst %d: ctx done with %d unfinished", b, len(missing))
					return
				case <-time.After(50 * time.Millisecond):
				}
			}
			states := make([]string, 0, len(missing))
			for _, id := range missing[:min(8, len(missing))] {
				tk, err := fabric.Task(id)
				if err != nil {
					states = append(states, id+":missing")
				} else {
					states = append(states, fmt.Sprintf("%s:%s", id, tk.State))
				}
			}
			// Dump all goroutine stacks at the stall moment: the subscriber
			// goroutine's parked location is the root cause.
			buf := make([]byte, 1<<20)
			n := runtime.Stack(buf, true)
			errs <- fmt.Sprintf("burst %d: GOROUTINE DUMP AT STALL:\n%s", b, buf[:n])
			// Probe: does a manual reconcile fix it now?
			res, rerr := coord.Reconcile(ctx, plan.DAG())
			fixed := 0
			for _, id := range missing {
				if _, err := fabric.Task(id); err == nil {
					fixed++
				}
			}
			errs <- fmt.Sprintf("burst %d: %d/%d unfinished after 30s: %v | manual reconcile: err=%v created=%d skipped=%d fixed=%d",
				b, len(missing), len(ids), states, rerr, len(res.Created), len(res.Skipped), fixed)
		}(b)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}
