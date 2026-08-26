package main

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/agentfabric"
	"github.com/Timwood0x10/ares/internal/ares_config"
	"github.com/Timwood0x10/ares/internal/ares_ratelimit"
	"github.com/Timwood0x10/ares/internal/aresrecovery"
	"github.com/Timwood0x10/ares/internal/taskfabric"
)

// chaosStopControl is the process-level kill switch for the live chaos loop
// (REVIEW #12 Phase 2 emergency stop). The HTTP handler POST /api/chaos/stop
// calls RequestStop; the loop polls Stopped and exits permanently. Shadow
// mode is unaffected — it never touches production agents.
type chaosStopControl struct {
	mu      sync.Mutex
	stopped bool
}

// liveChaosCtl is the singleton control for this process.
var liveChaosCtl = &chaosStopControl{}

// RequestStop trips the kill switch.
func (c *chaosStopControl) RequestStop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = true
}

// Stopped reports whether the kill switch has been tripped.
func (c *chaosStopControl) Stopped() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stopped
}

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

	// Check the final outcome — after the recovery chain the task must be
	// back in READY (requeued for execution), not merely in any non-empty
	// state. A missing/empty outcome list is treated as inconclusive.
	if len(outcomes) == 0 {
		log.Printf("serve: shadow sandbox replay produced no outcomes (recovery verification inconclusive)")
		return
	}
	last := outcomes[len(outcomes)-1]
	recovered := last.TaskState == string(taskfabric.StateReady)
	log.Printf("serve: shadow sandbox completed (events=%d, final_task_state=%s, recovered_ready=%v)",
		len(outcomes), last.TaskState, recovered)
	if !recovered {
		log.Printf("serve: shadow sandbox WARNING — task did not return to READY; recovery chain may be degraded")
	}
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
func wireChaos(ctx context.Context, cfg *ares_config.Config, peerKernel *kernelHandle, gaActive func() bool) {
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
		// Only construct the Chaos harness when explicitly confirmed AND a
		// non-empty target whitelist is configured (#12 Phase 2): an empty
		// eligible_capabilities list must disable injection entirely rather
		// than default to "everything is a target".
		if len(cfg.Kernel.Chaos.EligibleCapabilities) == 0 {
			log.Printf("serve: live chaos requested but eligible_capabilities is empty — refusing to arm (falling back to shadow)")
			interval := parseChaosInterval(cfg.Kernel.Chaos.Interval, 5*time.Minute)
			go shadowSandboxLoop(ctx, interval)
			return
		}
		if peerKernel != nil && peerKernel.agents != nil && peerKernel.recovery != nil {
			if cfg.Kernel.Chaos.StopToken == "" {
				log.Printf("serve: live chaos requested but stop_token is empty — refusing to arm without an emergency-stop credential")
				interval := parseChaosInterval(cfg.Kernel.Chaos.Interval, 5*time.Minute)
				go shadowSandboxLoop(ctx, interval)
				return
			}
			chaos := aresrecovery.NewChaos(peerKernel.agents, peerKernel.recovery)
			interval := parseChaosInterval(cfg.Kernel.Chaos.Interval, 5*time.Minute)
			go liveChaosLoop(ctx, chaos, peerKernel.agents, interval, cfg.Kernel.Chaos, gaActive)
			log.Printf("serve: LIVE chaos mode enabled — agents WILL be killed (interval=%s, rate=%d/min enforced, whitelist=%v)",
				interval.String(), cfg.Kernel.Chaos.RatePerMin, cfg.Kernel.Chaos.EligibleCapabilities)
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

// liveChaosGuard holds the enforced safety state for a live chaos loop:
// the rate limiter, per-agent cooldowns, the round-robin cursor, and the
// fail-safe stop latch (REVIEW #12 Phase 2).
type liveChaosGuard struct {
	limiter     *ares_ratelimit.TokenBucketLimiter
	cooldownFor time.Duration
	nextIndex   int

	mu       sync.Mutex
	cooldown map[string]time.Time // agentID -> earliest next injection time
	stopped  bool                 // set when recovery verification fails; stops all future injections
}

func newLiveChaosGuard(ratePerMin int, cooldown time.Duration) *liveChaosGuard {
	if ratePerMin <= 0 {
		ratePerMin = 2
	}
	if cooldown <= 0 {
		cooldown = 10 * time.Minute
	}
	return &liveChaosGuard{
		// Token bucket: ratePerMin injections per minute → per-second rate,
		// burst 1 so injections can never stack.
		limiter: ares_ratelimit.NewTokenBucketLimiter(&ares_ratelimit.LimiterConfig{
			Rate:  float64(ratePerMin) / 60.0,
			Burst: 1,
		}),
		cooldownFor: cooldown,
		cooldown:    make(map[string]time.Time),
	}
}

// allowTarget reports whether agentID is outside its cooldown window. An
// expired cooldown entry is dropped on first touch so the map stays bounded to
// in-cooldown agents instead of accumulating every injected id forever.
func (g *liveChaosGuard) allowTarget(agentID string, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	until, ok := g.cooldown[agentID]
	if !ok {
		return true
	}
	if now.After(until) {
		delete(g.cooldown, agentID)
		return true
	}
	return false
}

// markInjected records that agentID was just injected and advances the
// round-robin cursor past it.
func (g *liveChaosGuard) markInjected(agentID string, now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cooldown[agentID] = now.Add(g.cooldownFor)
}

// stop trips the fail-safe latch; after this no further injections run.
func (g *liveChaosGuard) stop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stopped = true
}

func (g *liveChaosGuard) isStopped() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.stopped
}

// liveChaosLoop runs periodic live chaos injections. This is the dangerous
// path: real production agents are killed/suspended. Every injection cycle is
// gated by six enforced guardrails (REVIEW #12 Phase 2):
//
//  1. Emergency stop — POST /api/chaos/stop (X-Chaos-Token) exits the loop
//     permanently.
//  2. Fail-safe latch — if recovery verification ever fails, ALL further
//     injections stop until process restart.
//  3. GA quiet window — when cfg.PauseDuringGA is set, injections are deferred
//     while gaActive() reports a generation mid-flight.
//  4. Rate limit — token bucket capped at cfg.RatePerMin injections/minute.
//  5. Cooldown — an injected agent is not targeted again for cfg.Cooldown.
//  6. Target whitelist — only agents declaring a capability from
//     cfg.EligibleCapabilities qualify (arming itself refuses an empty list).
func liveChaosLoop(ctx context.Context, chaos *aresrecovery.Chaos, fabric *agentfabric.Fabric, interval time.Duration, cfg ares_config.ChaosConfig, gaActive func() bool) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}

	ratePerMin := cfg.RatePerMin
	if ratePerMin <= 0 {
		ratePerMin = 2
	}
	cooldown := parseChaosInterval(cfg.Cooldown, 10*time.Minute)
	guard := newLiveChaosGuard(ratePerMin, cooldown)
	pausedForGA := false

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	log.Printf("serve: live chaos loop started (interval=%s, rate_limit=%d/min ENFORCED, cooldown=%s ENFORCED, fail_safe=latch, whitelist=%v, ga_pause=%t)",
		interval.String(), ratePerMin, cooldown.String(), cfg.EligibleCapabilities, cfg.PauseDuringGA)

	for {
		select {
		case <-ctx.Done():
			log.Printf("serve: live chaos loop stopping (context cancelled)")
			return
		case <-ticker.C:
			// Emergency stop (REVIEW #12 Phase 2): POST /api/chaos/stop trips
			// this permanently — the loop exits rather than idles.
			if liveChaosCtl.Stopped() {
				log.Printf("serve: live chaos loop stopped by emergency stop endpoint")
				return
			}
			if guard.isStopped() {
				log.Printf("serve: live chaos loop stopped by fail-safe latch (earlier recovery verification failed)")
				return
			}
			// GA quiet window (#12 Phase 2): defer injections while a
			// generation is mid-flight. State transitions are logged once so
			// operators can see the pause engaging and releasing.
			if cfg.PauseDuringGA && gaActive != nil && gaActive() {
				if !pausedForGA {
					pausedForGA = true
					log.Printf("serve: live chaos paused — GA generation in flight (quiet window)")
				}
				continue
			}
			if pausedForGA {
				pausedForGA = false
				log.Printf("serve: live chaos resumed — GA generation finished")
			}
			runLiveChaosInjection(ctx, chaos, fabric, guard, cfg.EligibleCapabilities)
		}
	}
}

// runLiveChaosInjection performs a single chaos injection cycle against the
// next round-robin target that is outside its cooldown window. It injects a
// kill, then verifies recovery; a failed verification trips the fail-safe
// latch so no further injections occur. The cycle is wrapped in panic
// recovery so a chaos failure never crashes the process.
func runLiveChaosInjection(ctx context.Context, chaos *aresrecovery.Chaos, fabric *agentfabric.Fabric, guard *liveChaosGuard, eligible []string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("serve: live chaos injection panicked (recovered): %v", r)
		}
	}()

	agents := fabric.Agents()
	if len(agents) == 0 {
		log.Printf("serve: live chaos — no agents available for injection")
		return
	}

	now := time.Now()

	// Round-robin target selection, skipping agents inside their cooldown
	// window AND agents whose declared capabilities are not whitelisted
	// (#12 Phase 2). If no agent qualifies, skip this cycle entirely.
	var target string
	for i := 0; i < len(agents); i++ {
		candidate := agents[guard.nextIndex%len(agents)]
		guard.nextIndex++
		if !guard.allowTarget(candidate, now) {
			continue
		}
		if !agentEligibleForChaos(fabric, candidate, eligible) {
			continue
		}
		target = candidate
		break
	}
	if target == "" {
		log.Printf("serve: live chaos — no eligible target (cooldown or whitelist), skipping cycle")
		return
	}

	// Enforced rate limit: the token bucket admits at most RatePerMin
	// injections per minute regardless of ticker cadence.
	if allowed, err := guard.limiter.Allow(ctx); err != nil || !allowed {
		log.Printf("serve: live chaos — rate limited (%v), skipping injection on %s", err, target)
		return
	}

	if err := chaos.InjectFailure(ctx, target, aresrecovery.FailureKill); err != nil {
		log.Printf("serve: live chaos inject kill %s failed: %v", target, err)
		return
	}
	guard.markInjected(target, now)

	// Verify recovery. VerifyRecovery returns the count of recovered agents;
	// zero means the recovery chain did not restore anything — trip the
	// fail-safe latch so no further injections run.
	recovered := chaos.VerifyRecovery(ctx)
	if recovered == 0 {
		guard.stop()
		log.Printf("serve: live chaos — recovery verification FAILED for %s (0 agents recovered); FURTHER INJECTIONS STOPPED by fail-safe latch", target)
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

// agentEligibleForChaos reports whether the named agent declares at least one
// capability present in the whitelist (#12 Phase 2). The whitelist is matched
// against the agent's own Capabilities list; an unknown agent is never
// eligible.
func agentEligibleForChaos(fabric *agentfabric.Fabric, agentID string, whitelist []string) bool {
	if len(whitelist) == 0 {
		return false
	}
	a, err := fabric.Get(agentID)
	if err != nil || a == nil {
		return false
	}
	for _, capName := range a.Capabilities {
		for _, w := range whitelist {
			if capName == w {
				return true
			}
		}
	}
	return false
}
