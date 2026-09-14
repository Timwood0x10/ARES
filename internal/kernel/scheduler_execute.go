package kernel

import (
	"context"
	"errors"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	apperrors "github.com/Timwood0x10/ares/internal/errors"
	taskfabric "github.com/Timwood0x10/ares/internal/fabric/task"
)

// logFailure logs a task failure, throttling ErrNoCapableCandidate: an
// unschedulable task is a legitimate "waiting for a capable agent" state that
// the scheduler re-polls every interval, so it must not spam the log. Other
// errors are logged every time (they are transient and need attention).
func (s *Scheduler) logFailure(taskID string, err error) {
	// errors.Is, not ==: the empty-candidate path returns the sentinel wrapped
	// in an apperrors.Kernel attribution (see executeWithCandidates), so an
	// identity comparison never matches and the throttle silently dies.
	if errors.Is(err, taskfabric.ErrNoCapableCandidate) {
		now := time.Now()
		s.noCandidateMu.Lock()
		defer s.noCandidateMu.Unlock()
		if now.Sub(s.lastNoCandidateLog) < noCandidateLogInterval {
			return
		}
		s.lastNoCandidateLog = now
	}
	log.Error("kernel scheduler: execute task failed", "task_id", taskID, "error", err)
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
		if releaseErr := s.fabric.Release(taskID, winner, epoch); releaseErr != nil {
			log.Error("kernel scheduler: release for stale winner failed", "task_id", taskID, "winner", winner, "error", releaseErr)
		}
		return nil
	}
	if s.hasRecoveryHint() {
		if releaseErr := s.fabric.Release(taskID, winner, epoch); releaseErr != nil {
			log.Error("kernel scheduler: release for stale winner failed", "task_id", taskID, "winner", winner, "error", releaseErr)
			return nil
		}
		log.Warn("kernel scheduler: winner no longer executable; released to READY and nominated for recovery", "winner", winner, "task_id", taskID)
		s.notifyRecovery(taskID)
		return nil
	}
	log.Warn("kernel scheduler: winner no longer executable and no capable replacement or recovery loop exists; task stays leased until TTL expiry", "winner", winner, "task_id", taskID)
	return nil
}

// executeWithCandidates runs the shared Schedule → Acquire → RunQuantum →
// finalize path for a prebuilt candidate list. The task capability is read
// for attribution at the outcome boundary.
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
		return apperrors.Kernel("schedule", "no_capable_candidate", taskID, "", taskfabric.ErrNoCapableCandidate)
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
	// Record the scheduling decision for the Observatory (dashboard.md §7):
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
		return err
	}
	// When the fabric is wired, resolve the winner through the fabric
	// FIRST — the fabric copy is the live, lifecycle-managed agent (kill/
	// recovery affect it), so a same-id static registration must not shadow
	// it. Only when the fabric has no live agent for the winner (legacy
	// mode, or a recovery-bound static executor) fall back to the registry.
	var executor CapabilityExecutor
	var ok bool
	if s.agents != nil {
		executor = s.fabricExecutor(winner)
	}
	if executor == nil {
		executor, ok = s.LookupExecutor(winner)
		if !ok || executor == nil {
			return s.handleStaleWinner(taskID, winner, epoch)
		}
		// N-3 observability: in peer mode the fabric had no live agent for the
		// winner, yet a static registration exists (legacy mode or a
		// recovery-bound executor). Correctness is fenced — a stale holder's
		// completion is rejected by the epoch token — but the dispatch stays
		// visible instead of silent so a dead agent's lingering static
		// registration can be spotted in logs.
		if s.agents != nil {
			log.Debug("kernel scheduler: peer-mode dispatch fell back to a static registration",
				"task_id", taskID, "winner", winner)
		}
	}
	// Track the busy slot while the quantum runs so the next Schedule sees the
	// real load; end records the outcome for confidence.
	// Preserve the submission metadata across the quantum: the task's current
	// checkpoint is the meta envelope written by submitFabricTask or by a
	// previous quantum (yield/done re-wraps below). Capturing it here — before
	// RunQuantum overwrites the task Checkpoint — is what keeps UserProfile
	// alive through an arbitrary number of yield→resume cycles.
	meta, decodeErr := taskfabric.DecodeCheckpoint(tk.Checkpoint)
	if decodeErr != nil {
		log.Warn("kernel scheduler: decode checkpoint failed", "task_id", taskID, "error", decodeErr)
	}
	// Pre-quantum gate: if the winner's budget/deadline is exhausted, yield
	// the task back (release the lease) so another capable agent (or a later
	// quantum under a replacement agent) can pick it up. This closes the loop at
	// the scheduler boundary — the fabric's state machine (Release→READY)
	// drives the requeue ("budget.exceeded → yield()").
	if !s.budgetOK(winner) {
		if releaseErr := s.fabric.Release(taskID, winner, epoch); releaseErr != nil {
			log.Error("kernel scheduler: release for budget-exhausted failed", "task_id", taskID, "winner", winner, "error", releaseErr)
		}
		return nil
	}
	// Admission gate: take the winner's busy slot ATOMICALLY before the quantum
	// starts. The candidate snapshot above was built before Schedule, so two
	// concurrent drain goroutines could both see this agent idle and hand it two
	// different tasks — running two quanta on one agent process at once, whose
	// cognitive state is not reentrant. A full gate releases the lease
	// (epoch-fenced, checkpoint preserved) so the next drain re-schedules the
	// task onto a free agent, exactly like the budget gate above.
	if !s.tracker.TryBegin(winner, maxConcurrentPerAgent) {
		if releaseErr := s.fabric.Release(taskID, winner, epoch); releaseErr != nil {
			log.Error("kernel scheduler: release for busy winner failed", "task_id", taskID, "winner", winner, "error", releaseErr)
		}
		return nil
	}
	// Panic guard: registered IMMEDIATELY after the busy slot is taken, so the
	// whole span it protects — user hooks (beforeQuantum runs third-party
	// plugin code), heartbeat setup, the quantum itself — cannot leak the
	// winner's LoadTracker slot. A panic anywhere in that span would otherwise
	// unwind past this function with load never decremented: Score multiplies
	// by (1-clamp01(load)) = 0, so the agent is permanently unschedulable (the
	// pre-fix gap: the guard used to be registered only after the heartbeat
	// setup, leaving beforeQuantum panics leaking the slot). The deferred
	// release only fires on the panic path: the normal path clears the flag
	// right after endQuantumOutcome, so there is no double-release.
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
			if !slotReleased {
				// Log BEFORE releasing: the stack trace is the only forensic
				// trail for the panic, and the load slot is released so the
				// agent stays schedulable (the fabric's expired-lease
				// requeue reclaims the stuck task separately).
				log.Error("kernel scheduler: panic in executor, releasing load slot", "task_id", taskID, "agent", winner, "panic", r)
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
		interval := s.ttl / 3
		if interval < 5*time.Second {
			interval = 5 * time.Second
		}
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
					log.Error("kernel scheduler: lease renew failed, stopping heartbeat", "task_id", taskID, "winner", winner, "error", rerr)
					return nil
				}
			}
		}
	})
	return stop
}
