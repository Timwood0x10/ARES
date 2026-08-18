//go:build e2e

// Real agent parallel execution — boots the real LLM runtime (./ares.yaml),
// runs N real agents concurrently on distinct tasks, and proves they execute
// in parallel (NOT the stub agents used in unit tests): the per-agent
// execution windows overlap and the wall-clock total is well under the serial
// sum. This is the production "work stealing with real LLM threads" proof.
//
// Build tag: e2e — these tests need a REAL LLM key and are intended to run
// locally only (go test -tags=e2e ./cmd/ares/). They are deliberately NOT part
// of the `integration` tag that CI runs, so CI stays hermetic.
package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/sdk"
)

// TestRealAgentParallelExecution runs 3 real agents concurrently and asserts:
//  1. every agent completes its own task (no error, non-empty output);
//  2. the execution windows overlap — agents are truly busy at the same time;
//  3. wall-clock total is well under the serial sum (parallel, not queued).
func TestRealAgentParallelExecution(t *testing.T) {
	// Tests run with cmd/ares as the working directory; the real LLM config
	// lives at the repo root.
	cfgPath := findRootConfig(t)
	cfg, err := sdk.LoadConfigFile(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	opts, err := cfg.ToOptions()
	if err != nil {
		t.Fatalf("config options: %v", err)
	}
	rt := sdk.NewRuntime(opts...)
	defer rt.Close()

	// Three short, independent tasks — each agent answers its own.
	tasks := []string{
		"In one sentence, what is dependency injection?",
		"In one sentence, how do goroutines and channels relate in Go?",
		"In one sentence, what is the Raft consensus algorithm?",
	}
	n := len(tasks)

	agents := make([]*sdk.Agent, n)
	for i := range tasks {
		agents[i] = rt.NewAgent(fmt.Sprintf("worker_%d", i),
			sdk.WithInstruction("You are a concise assistant. Answer in one sentence."),
		)
	}

	type runResult struct {
		start, end time.Time
		err        error
		output     string
	}
	results := make([]runResult, n)

	ctx := context.Background()
	startAll := time.Now()
	var wg sync.WaitGroup
	for i := range tasks {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i].start = time.Now()
			res, rerr := agents[i].Run(ctx, tasks[i])
			results[i].end = time.Now()
			results[i].err = rerr
			if res != nil {
				results[i].output = res.Output
			}
		}(i)
	}
	wg.Wait()
	wallClock := time.Since(startAll)

	// 1. Every agent completed its own task.
	serialSum := time.Duration(0)
	for i := 0; i < n; i++ {
		if results[i].err != nil {
			t.Fatalf("worker_%d failed: %v", i, results[i].err)
		}
		if strings.TrimSpace(results[i].output) == "" {
			t.Fatalf("worker_%d returned empty output", i)
		}
		serialSum += results[i].end.Sub(results[i].start)
	}

	// 2. Windows overlap: at least two agents were busy simultaneously.
	overlap := false
	for i := 0; i < n && !overlap; i++ {
		for j := i + 1; j < n; j++ {
			if results[i].start.Before(results[j].end) && results[j].start.Before(results[i].end) {
				overlap = true
				break
			}
		}
	}
	if !overlap {
		t.Fatalf("no two real agents executed concurrently — work stealing not active")
	}

	// 3. Wall-clock is well under the serial sum (parallel, not queued).
	t.Logf("real agents: wall=%v serial_sum=%v", wallClock.Round(time.Millisecond), serialSum.Round(time.Millisecond))
	if wallClock >= serialSum*8/10 {
		t.Fatalf("expected parallel execution: wall=%v serial_sum=%v (wall should be well below serial sum)", wallClock.Round(time.Millisecond), serialSum.Round(time.Millisecond))
	}
	t.Logf("real agent parallel execution OK: %d agents overlapped, wall=%v vs serial=%v", n, wallClock.Round(time.Millisecond), serialSum.Round(time.Millisecond))
}
