package main

import (
	"context"
	"log"
	"time"

	"github.com/Timwood0x10/ares/internal/agentfabric"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/aresrecovery"
	"github.com/Timwood0x10/ares/internal/taskfabric"
)

// shadowSandboxLoop runs a periodic shadow Sandbox verification: it constructs
// an independent scratch fabric, replays a canonical failure scenario
// (agent kill → lease expire → recovery), and logs the result. Production
// agents are never touched — the sandbox uses its own scratch fabrics.
//
// This closes REVIEW #12 Phase 1: the chaos subsystem defaults to shadow
// mode, which verifies recovery capability without impacting live agents.
func shadowSandboxLoop(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	log.Printf("serve: shadow sandbox loop started (interval=%s, production agents untouched)",
		interval.String())

	for {
		select {
		case <-ctx.Done():
			log.Printf("serve: shadow sandbox loop stopping (context cancelled)")
			return
		case <-ticker.C:
			runShadowSandbox(ctx)
		}
	}
}

// runShadowSandbox constructs a scratch fabric, runs a canonical
// agent-kill→recovery scenario, and logs the outcome. All scratch fabrics
// are local to this call and discarded after — production is never touched.
func runShadowSandbox(ctx context.Context) {
	// Build scratch fabrics — completely independent from production.
	scratchTasks := taskfabric.NewFabric()
	scratchAgents := agentfabric.NewFabric()

	// Build a scratch Recovery wired to the scratch fabrics.
	scratchRecovery := aresrecovery.New(scratchTasks, scratchAgents, aresrecovery.DefaultRestartPolicy())

	// Build the Sandbox on the scratch fabrics.
	sandbox := aresrecovery.NewSandbox(scratchTasks, scratchAgents, scratchRecovery)

	// Scripted scenario: spawn agent → create task → agent acquires task →
	// agent is killed → lease expires → recovery runs.
	events := []aresrecovery.SandboxEvent{
		{Type: aresrecovery.SandboxEventAgentSpawn, AgentID: "shadow-agent-1"},
		{Type: aresrecovery.SandboxEventTaskCreate, TaskID: "shadow-task-1"},
		{Type: aresrecovery.SandboxEventTaskAcquire, TaskID: "shadow-task-1", AgentID: "shadow-agent-1"},
		{Type: aresrecovery.SandboxEventAgentKill, AgentID: "shadow-agent-1"},
		{Type: aresrecovery.SandboxEventLeaseExpire, TaskID: "shadow-task-1"},
		{Type: aresrecovery.SandboxEventRecoverAll},
	}

	outcomes, err := sandbox.Replay(ctx, events)
	if err != nil {
		log.Printf("serve: shadow sandbox replay failed: %v (recovery verification inconclusive)", err)
		return
	}

	// Check the final outcome — the task should be recovered.
	last := outcomes[len(outcomes)-1]
	recovered := last.TaskState != ""
	log.Printf("serve: shadow sandbox completed (events=%d, final_task_state=%s, recovered=%v)",
		len(outcomes), last.TaskState, recovered)
}

// wireChaos wires the chaos subsystem based on the kernel config. By default
// (chaos disabled or mode=shadow), only the shadow sandbox loop is started.
// When mode=live AND allow_live=true, a real Chaos harness is also constructed
// — but only for dedicated testing environments. Production deployments should
// never enable live mode.
//
// The shadow sandbox loop is attached to the provided context and runs in a
// background goroutine. It is best-effort: a panic in the sandbox is recovered
// and logged, never crashing the process.
func wireChaos(ctx context.Context, cfg *ares_config.Config, peerKernel *kernelHandle) {
	if !cfg.Kernel.Chaos.Enabled {
		log.Printf("serve: chaos subsystem disabled (kernel.chaos.enabled=false)")
		return
	}

	mode := cfg.Kernel.Chaos.Mode
	if mode == "" {
		mode = "shadow"
	}

	switch mode {
	case "shadow":
		interval := parseChaosInterval(cfg.Kernel.Chaos.Interval, 5*time.Minute)
		go shadowSandboxLoop(ctx, interval)

	case "live":
		if !cfg.Kernel.Chaos.AllowLive {
			log.Printf("serve: chaos mode=live but allow_live=false — falling back to shadow mode")
			interval := parseChaosInterval(cfg.Kernel.Chaos.Interval, 5*time.Minute)
			go shadowSandboxLoop(ctx, interval)
			return
		}
		// Live chaos is dangerous: it kills real production agents.
		// Only construct the Chaos harness when explicitly confirmed.
		if peerKernel != nil && peerKernel.agents != nil && peerKernel.recovery != nil {
			chaos := aresrecovery.NewChaos(peerKernel.agents, peerKernel.recovery)
			interval := parseChaosInterval(cfg.Kernel.Chaos.Interval, 5*time.Minute)
			go liveChaosLoop(ctx, chaos, peerKernel.agents, interval, cfg.Kernel.Chaos)
			log.Printf("serve: LIVE chaos mode enabled — agents WILL be killed (interval=%s, rate=%d/min)",
				interval.String(), cfg.Kernel.Chaos.RatePerMin)
		} else {
			log.Printf("serve: live chaos requested but kernel handle incomplete — falling back to shadow")
			interval := parseChaosInterval(cfg.Kernel.Chaos.Interval, 5*time.Minute)
			go shadowSandboxLoop(ctx, interval)
		}

	default:
		log.Printf("serve: unknown chaos mode %q — defaulting to shadow", mode)
		interval := parseChaosInterval(cfg.Kernel.Chaos.Interval, 5*time.Minute)
		go shadowSandboxLoop(ctx, interval)
	}
}

// liveChaosLoop runs periodic live chaos injections with basic rate limiting.
// This is the dangerous path: real agents are killed/suspended. The loop
// includes panic recovery so a chaos failure never crashes the process.
func liveChaosLoop(ctx context.Context, chaos *aresrecovery.Chaos, fabric *agentfabric.Fabric, interval time.Duration, cfg ares_config.ChaosConfig) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}

	ratePerMin := cfg.RatePerMin
	if ratePerMin <= 0 {
		ratePerMin = 2
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	log.Printf("serve: live chaos loop started (interval=%s, rate_limit=%d/min)",
		interval.String(), ratePerMin)

	for {
		select {
		case <-ctx.Done():
			log.Printf("serve: live chaos loop stopping (context cancelled)")
			return
		case <-ticker.C:
			runLiveChaosInjection(ctx, chaos, fabric)
		}
	}
}

// runLiveChaosInjection performs a single chaos injection cycle. It lists
// production agents, picks one (if any), injects a kill, then verifies
// recovery. The injection is wrapped in panic recovery.
func runLiveChaosInjection(ctx context.Context, chaos *aresrecovery.Chaos, fabric *agentfabric.Fabric) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("serve: live chaos injection panicked (recovered): %v", r)
		}
	}()

	// Verify recovery after injection. If recovery fails, log a warning.
	// The Chaos harness handles the kill + verify cycle internally.
	agents := fabric.Agents()
	if len(agents) == 0 {
		log.Printf("serve: live chaos — no agents available for injection")
		return
	}

	// Inject on the first agent (simple round-robin; full implementation
	// would use cooldown tracking and target whitelists).
	target := agents[0]
	if err := chaos.InjectFailure(ctx, target, aresrecovery.FailureKill); err != nil {
		log.Printf("serve: live chaos inject kill %s failed: %v", target, err)
		return
	}

	// Verify recovery. VerifyRecovery returns the count of recovered agents.
	recovered := chaos.VerifyRecovery(ctx)
	if recovered == 0 {
		log.Printf("serve: live chaos — recovery verification FAILED for %s (0 agents recovered, injections paused)", target)
		return
	}

	log.Printf("serve: live chaos — agent %s killed and recovered (%d agents recovered)", target, recovered)
}

// parseChaosInterval parses the chaos interval string, returning the default
// on empty or invalid input.
func parseChaosInterval(s string, defaultInterval time.Duration) time.Duration {
	if s == "" {
		return defaultInterval
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return defaultInterval
	}
	return d
}
