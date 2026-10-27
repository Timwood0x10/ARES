package kernel

import (
	"context"
	"errors"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	apperrors "github.com/Timwood0x10/ares/internal/errors"
	agentfabric "github.com/Timwood0x10/ares/internal/fabric/agent"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// logFailure logs a task failure, throttling the two "still waiting" states:
// ErrNoCapableCandidate (nobody matches the capability) and
// ErrNoAffordableCandidate (every capable agent is out of budget/deadline/
// reserve). Both are legitimate waits the scheduler re-polls every interval,
// so they must not spam the log; other errors are logged every time (they are
// transient and need attention).
//
// Each cause owns its own throttle slot. One shared slot was a real
// observability hole: with a steady stream of "no capable candidate" the
// budget incident — the one an operator must act on, because the fix is a
// bigger budget, not a new agent — was suppressed for as long as the other
// kept firing.
// logFailure reports an execution failure. ctx is the drain's context, not Background: the logger
// attaches module and method itself, but the caller's context carries the values/trace that make
// the line attributable, and passing Background silently dropped them.
func (s *Scheduler) logFailure(ctx context.Context, taskID string, err error) {
	// errors.Is, not ==: both paths return their sentinel wrapped in an
	// apperrors.Kernel attribution (see executeWithCandidates), so an identity
	// comparison never matches and the throttle silently dies.
	now := time.Now()
	s.noCandidateMu.Lock()
	switch {
	case errors.Is(err, taskfabric.ErrNoAffordableCandidate):
		if now.Sub(s.lastNoAffordableLog) < noCandidateLogInterval {
			s.noCandidateMu.Unlock()
			return
		}
		s.lastNoAffordableLog = now
	case errors.Is(err, taskfabric.ErrNoCapableCandidate):
		if now.Sub(s.lastNoCandidateLog) < noCandidateLogInterval {
			s.noCandidateMu.Unlock()
			return
		}
		s.lastNoCandidateLog = now
	}
	s.noCandidateMu.Unlock()
	log.Error(ctx, "logFailure",
		"kernel scheduler: execute task failed", err, "task_id", taskID)
}

// Submission-time metadata (UserProfile + Payload + UsedExperienceID) rides in
// the task's Checkpoint slot inside a *taskfabric.CheckpointEnvelope
// (unversioned-v0 → versioned-v1 migration). Without the envelope the
// executor saw profile==nil and degraded to an empty executeByType fallback —
// a silent no-op that still reported success (the serve result-reflux bug
// chain). The scheduler re-wraps EVERY quantum's returned checkpoint (yield
// AND done) back into an envelope (EncodeCheckpoint), so the submission
// metadata survives a yield: RunQuantum overwrites the task Checkpoint with
// the step's checkpoint, and re-wrapping it inside the envelope means the next
// quantum's toModelTask can still restore UserProfile/Payload (yield→resume
// otherwise lost the profile and degraded to executeByType). nil before the
// first quantum runs.

// execute runs the full fabric path for one task: Schedule → Acquire →
// RunQuantum (delegating the actual work to the winning sub-agent) →
// finalize. Errors are returned to the caller for logging; the fabric
// state machine (RetryPolicy) decides requeue vs. final failure.
func (s *Scheduler) execute(ctx context.Context, taskID string) error {
	// Build the candidate list from the registered executors so scheduling is
	// always consistent with what can actually run. Each candidate declares its
	// OWN capabilities (from the agent's Type), NOT the task's — the scorer
	// compares the task's required capability against what the agent can do.
	// Load/Confidence come from the live tracker: real busy
	// fraction and historical success rate, not static placeholders.
	//
	// Recovery binding: a recovery executor bound to THIS task is the only
	// candidate (the replacement must run the task it was spawned for). Bound
	// executors of OTHER tasks are excluded so a replacement can never hijack
	// a different READY task.
	execs := s.allExecutors()
	if boundID, bound := s.boundFor(taskID); bound {
		cands := make([]taskfabric.Candidate, 0, 1)
		if agent, ok := execs[boundID]; ok && agent != nil {
			// A binding is only meaningful while the replacement can actually
			// RUN the task. A bound executor whose capability does not overlap
			// used to strand the task forever: it was the only candidate, Pick
			// scored it 0, Schedule failed with ErrNoCapableCandidate on every
			// drain, and the binding is only released at terminal state —
			// which never comes. Fall through to the general pool instead so
			// any other capable executor can pick the task up.
			if tk, tkErr := s.fabric.Task(taskID); tkErr == nil &&
				taskfabric.CapabilityOverlap(tk.Capability, []string{string(agent.Type())}) > 0 {
				cands = append(cands, taskfabric.Candidate{
					AgentID:      boundID,
					Capabilities: []string{string(agent.Type())},
					Load:         s.tracker.Load(boundID),
					Confidence:   s.tracker.Confidence(boundID),
					Priority:     s.tracker.Priority(boundID),
				})
			}
		}
		if len(cands) == 0 {
			// The bound executor is gone (already unregistered) — fall through
			// to the normal pool so the task is not stranded.
			return s.executeUnbound(ctx, taskID)
		}
		return s.executeWithCandidates(ctx, taskID, cands)
	}
	return s.executeUnbound(ctx, taskID)
}

// executeUnbound runs the fabric path for a task with no recovery binding:
// the candidate pool comes from the shared buildCandidates (every registered,
// unbound executor whose capability overlaps the task, plus every live IDLE
// fabric agent), scored and selected by the fabric.
func (s *Scheduler) executeUnbound(ctx context.Context, taskID string) error {
	return s.executeWithCandidates(ctx, taskID, s.buildCandidates(taskID))
}

// handleStaleWinner resolves the case where the scheduling winner died (or
// became non-executable) between candidate build and executor lookup.
//
// The task must only be released to someone who can actually pick it up.
// Releasing clears the lease, which also removes the task from
// CheckExpiredLeases' scope — so releasing into an empty world would strand
// it permanently, which is strictly worse than the TTL stall. Hence three
// cases, in order of preference:
//
//  1. Another capable executor exists → release; the next drain re-schedules
//     within one poll interval.
//  2. No capable executor, but a recovery loop is wired → release AND
//     nominate the task to it. Recovery gives the task a replacement
//     execution body promptly, instead of the task waiting out the full
//     lease TTL. This is the production path (cmd/ares peer mode).
//  3. Neither → keep the lease. TTL expiry is then the ONLY recovery trigger
//     available, and keeping the lease is what makes the task visible to
//     CheckExpiredLeases. Leader/SDK/chaos-sandbox paths land here.
//
// Release is epoch-fenced (only the current holder can release) and
// PRESERVES the checkpoint, so the "resume, don't restart" contract holds in
// cases 1 and 2.
func (s *Scheduler) handleStaleWinner(taskID, winner string, epoch uint64) error {
	if s.HasCapableExecutor(taskID) {
		if releaseErr := s.fabric.ReleaseReason(taskID, winner, epoch, agentfabric.StopUnfit.String()); releaseErr != nil {
			log.Error(context.Background(), "handleStaleWinner",
				"kernel scheduler: release for stale winner failed", releaseErr,
				"task_id", taskID, "winner", winner)
			return nil
		}
		log.Info(context.Background(), "handleStaleWinner",
			"kernel scheduler: reclaimed task from a winner with no live executor; another capable executor will pick it up",
			"task_id", taskID, "winner", winner, "reason", agentfabric.StopUnfit.String())
		return nil
	}
	if s.hasRecoveryHint() {
		if releaseErr := s.fabric.ReleaseReason(taskID, winner, epoch, agentfabric.StopUnfit.String()); releaseErr != nil {
			log.Error(context.Background(), "handleStaleWinner",
				"kernel scheduler: release for stale winner failed", releaseErr,
				"task_id", taskID, "winner", winner)
			return nil
		}
		log.Warn(context.Background(), "handleStaleWinner",
			"kernel scheduler: winner no longer executable; released to READY and nominated for recovery",
			"winner", winner, "task_id", taskID, "reason", agentfabric.StopUnfit.String())
		s.notifyRecovery(taskID)
		return nil
	}
	log.Warn(context.Background(), "handleStaleWinner",
		"kernel scheduler: winner no longer executable and no capable replacement or recovery loop exists; task stays leased until TTL expiry",
		"winner", winner, "task_id", taskID, "reason", agentfabric.StopUnfit.String())
	return nil
}

// resolveExecutor resolves the winning agent to a CapabilityExecutor.
// When a fabric is wired it tries the fabric's live agent first; only when
// no live fabric agent exists does it fall back to the static executor
// registry. Returns (executor, fallback) where fallback is true when the
// executor came from the static registry (for observability logging).
func (s *Scheduler) resolveExecutor(winner string) (CapabilityExecutor, bool) {
	if s.agents != nil {
		if exec := s.fabricExecutor(winner); exec != nil {
			return exec, false
		}
	}
	exec, ok := s.LookupExecutor(winner)
	if !ok || exec == nil {
		return nil, false
	}
	return exec, true
}

// executeWithCandidates runs the shared Schedule → Acquire → RunQuantum →
// finalize path for a prebuilt candidate list. The task capability is read
// for attribution at the outcome boundary.
// admitWinner takes everything a quantum needs from the winner, in order, before
// it starts: the concurrency slot, the fabric agent's busy claim, and (on either
// failure) the release of what was already taken. It reports false when the
// winner cannot start a quantum right now, having given back the slot and the
// lease (epoch-fenced, checkpoint preserved) so the next drain re-schedules the
// task — exactly like the budget gate in the caller.
//
// Two gates, two different stale-snapshot hazards, and both must be atomic:
//   - the concurrency slot (tracker.TryBegin): the candidate snapshot was built
//     before Schedule, so two concurrent drain goroutines could both see this
//     agent idle and hand it two tasks — two quanta on one agent process, whose
//     cognitive state is not reentrant.
//   - the busy claim (Agent.TryBeginQuantum): the agent can leave IDLE between
//     that snapshot and here, most often because idle reclamation retired it —
//     and retiring releases its execution body, so proceeding would fail the task
//     for a reason that has nothing to do with the task.
//
// Extracted from executeWithCandidates to keep its cyclomatic complexity inside
// the gate; the two failure branches are the same shape on purpose.
func (s *Scheduler) admitWinner(
	ctx context.Context, taskID, winner string, epoch uint64, agent *agentfabric.Agent,
) bool {
	if !s.tracker.TryBegin(winner, maxConcurrentPerAgent) {
		if releaseErr := s.fabric.Release(taskID, winner, epoch); releaseErr != nil {
			log.Error(ctx, "executeWithCandidates",
				"kernel scheduler: release for busy winner failed", releaseErr,
				"task_id", taskID, "winner", winner)
		}
		return false
	}
	if agent != nil && !agent.TryBeginQuantum() {
		s.tracker.EndNeutral(winner)
		if releaseErr := s.fabric.Release(taskID, winner, epoch); releaseErr != nil {
			log.Error(ctx, "executeWithCandidates",
				"kernel scheduler: release for a winner that left IDLE failed", releaseErr,
				"task_id", taskID, "winner", winner)
		}
		return false
	}
	return true
}

func (s *Scheduler) executeWithCandidates(ctx context.Context, taskID string, cands []taskfabric.Candidate) error {
	tk, err := s.fabric.Task(taskID)
	if err != nil {
		return err
	}
	if len(cands) == 0 {
		// KernelError carries task attribution while keeping the sentinel on the
		// chain, so errors.Is(err, taskfabric.ErrNoCapableCandidate) still matches.
		return apperrors.Kernel("schedule", "no_capable_candidate", taskID, "", taskfabric.ErrNoCapableCandidate)
	}
	// Pre-schedule budget filter: drop candidates whose governance budget or
	// deadline is exhausted BEFORE Schedule acquires a lease for them (see
	// filterBudgetAffordable — the post-acquire gate below releases the task
	// back to READY, which with a sole exhausted candidate became a silent
	// acquire/release livelock).
	cands = s.filterBudgetAffordable(cands)
	if len(cands) == 0 {
		// Distinct code AND distinct sentinel for the admission-filtered empty
		// pool: "every capable agent is budget/deadline/reserve-exhausted" and
		// "no agent ever matched" are different incidents, and collapsing them
		// into one code made a governance budget that was simply too small for
		// the LLM look exactly like a missing executor (07-hil's silent
		// no_capable_candidate hang). The sentinel — not just the code string —
		// must differ so errors.Is consumers (the log throttle) can separate
		// the two.
		return apperrors.Kernel("schedule", "no_affordable_candidate", taskID, "", taskfabric.ErrNoAffordableCandidate)
	}
	// Capability-specific confidence: the candidate builders only know
	// agentID; the task capability is available here, so re-resolve each
	// candidate's confidence against (agentID, task capability) before
	// Schedule scores them. Without a capability override this falls back to
	// the agent-level value (design-fix: per-capability feedback is consumed).
	//
	// M4.4 read-side closure: when the fabric carries a RECORDED experience
	// prior for this capability, a candidate whose tracker confidence is the
	// NEUTRAL prior (no history, no override) is zeroed so the fabric's
	// Schedule fills it with that prior. A measured value — including the
	// evolution feedback loop's overrides — always wins over the prior: live
	// feedback outranks stale priors. When NO prior exists the neutral value
	// stands (the historical default), so wiring a ConfidenceSource alone
	// cannot change a system whose Experience store is empty.
	prior := s.fabric.PriorConfidence(tk.Capability)
	for i := range cands {
		conf, measured := s.tracker.ConfidenceForMeasured(cands[i].AgentID, tk.Capability)
		switch {
		case measured:
			cands[i].Confidence = conf
		case prior > 0:
			// Unmeasured + prior exists: zero so Schedule's fill applies it.
			cands[i].Confidence = 0
		default:
			// Unmeasured + no prior: the neutral prior stands (default
			// behavior when nothing was ever recorded).
			cands[i].Confidence = conf
		}
	}
	winner, epoch, err := s.fabric.Schedule(taskID, cands, s.ttl)
	// Record the scheduling decision for the Observatory:
	// candidate breakdown + winner. Recorded even on failure (e.g. no capable
	// candidate) so the panel explains why a task stayed unscheduled.
	if s.decisions != nil {
		d := ScheduleDecision{
			TaskID:     taskID,
			Capability: tk.Capability,
			Candidates: scoreCandidates(tk.Capability, cands),
			Time:       time.Now(),
		}
		if err == nil {
			d.Winner = winner
			d.Epoch = epoch
		} else {
			d.Err = err.Error()
		}
		s.decisions.Record(d)
	}
	if err != nil {
		// No claim, no dispatch sample: a task that never found an agent has not been "started".
		// Sampling before the claim is what made this histogram describe attempts to run rather
		// than runs.
		return err
	}
	// Sampled after the lease and the agent exist: from here the task really is dispatched, and the
	// wait it reports is the wait it actually served.
	s.observeDispatchWait(taskID)
	// When the fabric is wired, resolve the winner through the fabric
	// FIRST — the fabric copy is the live, lifecycle-managed agent (kill/
	// recovery affect it), so a same-id static registration must not shadow
	// it. Only when the fabric has no live agent for the winner (legacy
	// mode, or a recovery-bound static executor) fall back to the registry.
	executor, fallback := s.resolveExecutor(winner)
	if executor == nil {
		return s.handleStaleWinner(taskID, winner, epoch)
	}
	if fallback {
		// N-3 observability: in peer mode the fabric had no live agent for the
		// winner, yet a static registration exists (legacy mode or a
		// recovery-bound executor). Correctness is fenced — a stale holder's
		// completion is rejected by the epoch token — but the dispatch stays
		// visible instead of silent so a dead agent's lingering static
		// registration can be spotted in logs.
		log.Debug(ctx, "executeWithCandidates",
			"kernel scheduler: peer-mode dispatch fell back to a static registration",
			"task_id", taskID, "winner", winner)
	}
	// Determine whether the winner is a live fabric agent so we can bridge
	// the R4 busy flag. The fabricExecutor path above already confirmed the
	// agent was IDLE at selection time; setting busy here closes the window
	// between candidate build and quantum start so a concurrent drain cannot
	// hand the same agent a second task. The flag is cleared via defer so a
	// panic or early return never strands the agent as permanently busy.
	var fabricAgent *agentfabric.Agent
	if s.agents != nil {
		if a, err := s.agents.Get(winner); err == nil && a != nil {
			fabricAgent = a
		}
	}
	// Track the busy slot while the quantum runs so the next Schedule sees the
	// real load; end records the outcome for confidence.
	// Preserve the submission metadata across the quantum: the task's current
	// checkpoint is the meta envelope written at submission time (submitPeerTask
	// / POST /api/graphs) or by a previous quantum (yield/done re-wraps below).
	// Capturing it here — before
	// RunQuantum overwrites the task Checkpoint — is what keeps UserProfile
	// alive through an arbitrary number of yield→resume cycles.
	meta, decodeErr := s.decodeCheckpoint(tk.Checkpoint)
	if decodeErr != nil {
		log.Warn(ctx, "executeWithCandidates",
			"kernel scheduler: decode checkpoint failed", "task_id", taskID, "error", decodeErr)
	}
	// Pre-quantum gate: if the winner is not admissible (deadline passed,
	// budget spent, or its budget reserve reached), RECLAIM the task — release
	// the lease so it goes back to READY for a fresher agent, with the reason
	// recorded on the release event. This closes the loop at the scheduler
	// boundary — the fabric's state machine (ReleaseReason→READY) drives the
	// requeue. The checkpoint is preserved, so this is a hand-over, not a
	// restart.
	if ok, reason := s.admissible(winner); !ok {
		if releaseErr := s.fabric.ReleaseReason(taskID, winner, epoch, reason.String()); releaseErr != nil {
			log.Error(ctx, "executeWithCandidates",
				"kernel scheduler: reclaim for inadmissible winner failed", releaseErr,
				"task_id", taskID, "winner", winner, "reason", reason.String())
			return nil
		}
		// The reclaim succeeded: the task is READY again with its checkpoint
		// intact and the reason is on its task.released event. Logged at Info
		// because this is the moment an operator watching "why did this task
		// stop moving" needs — the per-candidate cause sits in admissible's
		// Debug line, the aggregate in the no_affordable_candidate Error line.
		log.Info(ctx, "executeWithCandidates",
			"kernel scheduler: reclaimed task from an inadmissible winner",
			"task_id", taskID, "winner", winner, "reason", reason.String())
		return nil
	}
	if !s.admitWinner(ctx, taskID, winner, epoch, fabricAgent) {
		return nil
	}
	busyCleared := false
	clearBusy := func() {
		if fabricAgent != nil && !busyCleared {
			fabricAgent.SetBusy(false)
			busyCleared = true
		}
	}
	// Panic guard: registered IMMEDIATELY after the busy slot is taken, so the
	// whole span it protects — user hooks (beforeQuantum runs third-party
	// plugin code), heartbeat setup, the quantum itself — cannot leak the
	// winner's LoadTracker slot or the R4 busy flag. A panic anywhere in that
	// span would otherwise unwind past this function with load never
	// decremented: Score multiplies by (1-clamp01(load)) = 0, so the agent is
	// permanently unschedulable (the pre-fix gap: the guard used to be
	// registered only after the heartbeat setup, leaving beforeQuantum panics
	// leaking the slot). The deferred release only fires on the panic path:
	// the normal path clears the flag right after endQuantumOutcome, so there
	// is no double-release.
	slotReleased := false
	var stopHeartbeat func()
	defer func() {
		if r := recover(); r != nil {
			// stopHeartbeat is assigned before the heartbeat goroutine is
			// launched (nil before that point, when there is nothing to
			// stop).
			if stopHeartbeat != nil {
				stopHeartbeat()
			}
			clearBusy()
			if !slotReleased {
				// Log BEFORE releasing: the stack trace is the only forensic
				// trail for the panic, and the load slot is released so the
				// agent stays schedulable (the fabric's expired-lease
				// requeue reclaims the stuck task separately).
				log.Error(ctx, "executeWithCandidates",
					"kernel scheduler: panic in executor, releasing load slot", nil,
					"task_id", taskID, "agent", winner, "panic", r)
				s.tracker.EndNeutral(winner)
				slotReleased = true
			}
		}
	}()
	// Quantum boundary hooks (observational): before the quantum runs and
	// after it finalizes. See quantum_hook.go for the contract.
	s.beforeQuantum(ctx, taskID, winner)
	// Lease heartbeat: renew the winner's lease while the quantum runs so a
	// long step (> TTL) is not requeued by lease expiry and executed a
	// second time concurrently. stopHeartbeat is assigned BEFORE the
	// goroutine launches (nil before that point, when there is nothing to
	// stop) so the panic guard above can never observe a half-initialized
	// heartbeat. sync.Once inside removes the double-close hazard: the
	// normal path and the panic-recovery defer both call it, and a panic
	// occurring AFTER the normal close must not close an already-closed
	// channel (that panic would itself unwind, skip EndNeutral, and leak the
	// load slot — the very bug this guards).
	stopHeartbeat = s.startLeaseHeartbeat(ctx, taskID, winner, epoch)
	// Capture the quantum's wall-clock duration before RunQuantum so
	// endQuantumOutcome can attribute real latency to the deterministic scorer.
	// The old Record() path passed 0,0,0, which made the
	// latency/retry/recover weights dead and collapsed every score to
	// 0.70×successRate+0.30 (no added information).
	//
	// The retry count is DERIVED from the RunQuantum error
	// via quantumRetries — never read from RetryPolicy.Attempts (cumulative,
	// would over-attribute) and never re-read from the task (races another
	// drain). See quantumRetries for the full rationale.
	quantumStart := time.Now()
	var usage quantumUsage
	err = s.fabric.RunQuantum(taskID, winner, epoch, s.buildQuantumStep(ctx, executor, tk, meta, &usage))
	quantumLatency := time.Since(quantumStart)
	retries := quantumRetries(err)
	// Release the busy slot and attribute the outcome (see endQuantumOutcome).
	stopHeartbeat()
	clearBusy()
	s.afterQuantum(ctx, taskID, winner, err)
	s.endQuantumOutcome(winner, tk.Capability, taskID, err, quantumLatency, retries)
	slotReleased = true
	// Post-quantum bookkeeping: record the quantum's consumption (its LLM
	// tokens + 1 tool round) so the next gate sees the new balance. Runs even
	// on step errors — the quantum did execute (or partially execute) and
	// spent budget. usage.tokens is 0 when the step produced no measurable
	// result.
	if s.governance != nil {
		s.consumeBudget(winner, usage.tokens)
	}
	if err == nil {
		// Count TASKS, not quanta: a multi-quantum task (yield → resume →
		// done) must increment exactly once, at its terminal COMPLETED
		// state. Counting every successful quantum inflated the observability
		// metric by the task's quantum depth.
		if tkEnd, tkErr := s.fabric.Task(taskID); tkErr == nil && tkEnd.State == taskfabric.StateCompleted {
			s.Scheduled.Add(1)
		}
	}
	s.unbindRecoveryExecutorAfterTerminal(taskID)
	return err
}

// startLeaseHeartbeat launches the lease-renewal goroutine for one quantum
// and returns the idempotent stop function. The heartbeat renews the
// winner's lease at ttl/3 (floor 5s) while the quantum runs so a long step
// (> TTL) is not requeued by lease expiry and executed a second time
// concurrently; it stops when stopped, the scheduler context is cancelled,
// or a renewal fails (ownership lost — preemption/expiry). The stop function
// closes the stop channel and waits for the goroutine EXACTLY ONCE
// (sync.Once), so the normal quantum path and the panic-recovery defer can
// both call it without a double-close panic.
func (s *Scheduler) startLeaseHeartbeat(ctx context.Context, taskID, winner string, epoch uint64) func() {
	renewStop := make(chan struct{})
	qg, qgCtx := errgroup.WithContext(ctx)
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			close(renewStop)
			_ = qg.Wait()
		})
	}
	qg.Go(func() error {
		interval := leaseHeartbeatInterval(s.ttl)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-renewStop:
				return nil
			case <-qgCtx.Done():
				return nil
			case <-ticker.C:
				if rerr := s.fabric.Renew(taskID, winner, epoch, s.ttl); rerr != nil {
					// Ownership was lost (preempted, requeued after expiry,
					// finalized) or the lease already expired — either way the
					// holder must stop renewing, so the error is terminal for
					// this heartbeat, not retried.
					log.Error(ctx, "startLeaseHeartbeat",
						"kernel scheduler: lease renew failed, stopping heartbeat", rerr,
						"task_id", taskID, "winner", winner, "epoch", epoch)
					return nil
				}
			}
		}
	})
	return stop
}

// leaseHeartbeatInterval derives the renewal cadence from the lease TTL
// (F-04): ttl/3 gives three renewal chances per lease and is floored at 5s
// for normal TTLs. A ttl SHORTER than 15s must NOT take that floor — the
// first heartbeat would fire after the lease expired and recovery would
// requeue a still-running quantum — so short TTLs keep their ttl/3 cadence
// (chaos/recovery demos configure lease_ttl as low as 2s). A zero or
// negative ttl falls back to the legacy 5s floor; callers configuring ttl
// that low have no lease semantics to protect anyway.
func leaseHeartbeatInterval(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return 5 * time.Second
	}
	interval := ttl / 3
	if interval >= 5*time.Second {
		return interval
	}
	// Short TTL: cadence shrinks with the lease; cap at ttl/2 so the first
	// renewal always lands inside the lease window.
	if interval > ttl/2 {
		return ttl / 2
	}
	return interval
}
