package leader

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"goagentx/internal/agents/base"
	coreerrors "goagentx/internal/core/errors"
	"goagentx/internal/core/models"
	"goagentx/internal/errors"
	"goagentx/internal/events"
	"goagentx/internal/protocol/ahp"

	"golang.org/x/sync/errgroup"
)

// FailoverStrategy defines how a failed leader is replaced.
type FailoverStrategy interface {
	HandleFailover(ctx context.Context, leaderID string, checkpoint *LeaderCheckpoint) (base.Agent, error)
}

// LeaderSupervisorConfig holds supervisor configuration.
type LeaderSupervisorConfig struct {
	CheckInterval       time.Duration `yaml:"check_interval"`
	FailoverTimeout     time.Duration `yaml:"failover_timeout"`
	MaxFailoverAttempts int           `yaml:"max_failover_attempts"`
}

// DefaultLeaderSupervisorConfig returns sensible defaults.
func DefaultLeaderSupervisorConfig() *LeaderSupervisorConfig {
	return &LeaderSupervisorConfig{
		CheckInterval:       10 * time.Second,
		FailoverTimeout:     2 * time.Minute,
		MaxFailoverAttempts: 3,
	}
}

// LeaderSupervisor monitors leader health and triggers failover.
// Deprecated: production code should use Runtime-level supervision.
// Retained for test compatibility until all test consumers are migrated.
type LeaderSupervisor struct {
	mu           sync.RWMutex
	leaders      map[string]base.Agent
	heartbeatMon *ahp.HeartbeatMonitor
	strategy     FailoverStrategy
	recovery     *TaskRecovery
	checkpoint   *CheckpointRepository
	eventStore   events.EventStore
	config       *LeaderSupervisorConfig
	g            *errgroup.Group
	ctx          context.Context
	gctx         context.Context
	cancel       context.CancelFunc
	started      bool
	stopped      bool
}

// NewLeaderSupervisor creates a LeaderSupervisor.
func NewLeaderSupervisor(
	heartbeatMon *ahp.HeartbeatMonitor,
	strategy FailoverStrategy,
	recovery *TaskRecovery,
	checkpoint *CheckpointRepository,
	eventStore events.EventStore,
	config *LeaderSupervisorConfig,
) (*LeaderSupervisor, error) {
	if heartbeatMon == nil {
		return nil, errors.New("leader supervisor: heartbeat monitor is required")
	}
	if strategy == nil {
		return nil, errors.New("leader supervisor: failover strategy is required")
	}
	if config == nil {
		config = DefaultLeaderSupervisorConfig()
	}
	return &LeaderSupervisor{
		leaders:      make(map[string]base.Agent),
		heartbeatMon: heartbeatMon,
		strategy:     strategy,
		recovery:     recovery,
		checkpoint:   checkpoint,
		eventStore:   eventStore,
		config:       config,
	}, nil
}

// RegisterLeader registers a leader for health monitoring.
func (s *LeaderSupervisor) RegisterLeader(id string, agent base.Agent) {
	if id == "" || agent == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.leaders[id] = agent
}

// Start begins the monitoring loop. Uses errgroup for goroutine management.
func (s *LeaderSupervisor) Start(ctx context.Context) error {
	if ctx == nil {
		return coreerrors.ErrNilPointer
	}
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return errors.New("leader supervisor: already started")
	}
	if s.stopped {
		s.mu.Unlock()
		return errors.New("leader supervisor: already stopped")
	}
	s.started = true
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.g, s.gctx = errgroup.WithContext(s.ctx)
	s.mu.Unlock()

	s.heartbeatMon.RegisterCallback(s.handleFailover)

	s.g.Go(func() error {
		ticker := time.NewTicker(s.config.CheckInterval)
		defer ticker.Stop()
		for {
			select {
			case <-s.gctx.Done():
				return nil
			case <-ticker.C:
				s.heartbeatMon.CheckTimeouts()
			}
		}
	})

	return nil
}

// Stop gracefully stops the supervisor.
func (s *LeaderSupervisor) Stop() error {
	s.mu.Lock()
	if !s.started || s.stopped {
		s.mu.Unlock()
		return nil
	}
	s.stopped = true
	s.cancel()
	s.mu.Unlock()

	if s.g != nil {
		if err := s.g.Wait(); err != nil {
			return errors.Wrap(err, "leader supervisor: wait for goroutines")
		}
	}
	return nil
}

// handleFailover is the callback for heartbeat timeout.
// It launches the failover process asynchronously via errgroup.
func (s *LeaderSupervisor) handleFailover(leaderID string) {
	s.mu.RLock()
	stopped := s.stopped
	g := s.g
	gctx := s.gctx
	s.mu.RUnlock()

	if stopped || g == nil {
		slog.Debug("supervisor stopped, skipping failover", "leader_id", leaderID)
		return
	}

	g.Go(func() error {
		s.doFailover(gctx, leaderID)
		return nil
	})
}

// doFailover executes the full failover sequence for a failed leader.
func (s *LeaderSupervisor) doFailover(ctx context.Context, leaderID string) {
	slog.Warn("leader failover triggered", "leader_id", leaderID)

	s.mu.RLock()
	agent, exists := s.leaders[leaderID]
	eventStore := s.eventStore
	s.mu.RUnlock()
	if !exists {
		slog.Warn("leader not registered, skipping failover", "leader_id", leaderID)
		return
	}
	if agent.Status() == models.AgentStatusOffline {
		slog.Debug("leader already offline, skipping failover", "leader_id", leaderID)
		return
	}

	// Emit failover triggered event for event sourcing.
	if eventStore != nil {
		if err := eventStore.Append(ctx, leaderID, []*events.Event{
			{
				Type: events.EventFailoverTriggered,
				Payload: map[string]any{
					"leader_id": leaderID,
				},
			},
		}, 0); err != nil {
			slog.Warn("Failed to emit failover triggered event", "error", err)
		}
	}

	// Use a detached context for Stop because the incoming ctx (gctx) may already
	// be cancelled during supervisor shutdown, which would cause Stop to fail
	// immediately without actually cleaning up the agent.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
	if err := agent.Stop(stopCtx); err != nil {
		slog.Error("failed to stop old leader", "leader_id", leaderID, "error", err)
	}
	stopCancel()

	var cp *LeaderCheckpoint
	if s.checkpoint != nil {
		var err error
		cp, err = s.checkpoint.GetLatest(ctx, leaderID)
		if err != nil {
			slog.Error("failed to retrieve checkpoint for leader", "leader_id", leaderID, "error", err)
		}
	}

	// Attempt event-based recovery when checkpoint is missing or incomplete.
	if eventStore != nil && (cp == nil || cp.SessionID == "") {
		recovery := NewEventRecovery(eventStore)
		state, err := recovery.RecoverFromEvents(ctx, leaderID)
		if err != nil {
			slog.Warn("event recovery failed", "leader_id", leaderID, "error", err)
		} else if state != nil && state.SessionID != "" {
			if cp == nil {
				cp = &LeaderCheckpoint{LeaderID: leaderID}
			}
			cp.SessionID = state.SessionID
			slog.Info("session recovered from events",
				"leader_id", leaderID,
				"session_id", state.SessionID,
				"last_version", state.LastVersion,
			)
		}
	}

	if cp == nil {
		cp = &LeaderCheckpoint{LeaderID: leaderID}
	}

	var newAgent base.Agent
	var failoverErr error
	for attempt := 1; attempt <= s.config.MaxFailoverAttempts; attempt++ {
		failoverCtx, failoverCancel := context.WithTimeout(ctx, s.config.FailoverTimeout)
		newAgent, failoverErr = s.strategy.HandleFailover(failoverCtx, leaderID, cp)
		failoverCancel()

		if failoverErr == nil {
			break
		}
		slog.Error("failover attempt failed",
			"leader_id", leaderID,
			"attempt", attempt,
			"error", failoverErr,
		)
	}

	if failoverErr != nil {
		slog.Error("all failover attempts exhausted",
			"leader_id", leaderID,
			"max_attempts", s.config.MaxFailoverAttempts,
		)
		return
	}

	// Recover stale tasks BEFORE registering the new leader,
	// so the new leader doesn't receive requests while stale tasks are unresolved.
	if s.recovery != nil && cp.SessionID != "" {
		staleTasks, err := s.recovery.RecoverStaleTasks(ctx, cp.SessionID)
		if err != nil {
			slog.Error("failed to recover stale tasks",
				"leader_id", leaderID,
				"session_id", cp.SessionID,
				"error", err,
			)
		} else if len(staleTasks) > 0 {
			slog.Info("recovered stale tasks",
				"leader_id", leaderID,
				"session_id", cp.SessionID,
				"count", len(staleTasks),
			)
		}
	}

	s.mu.Lock()
	s.leaders[leaderID] = newAgent
	s.mu.Unlock()

	// Emit failover completed event for event sourcing.
	if eventStore != nil {
		if err := eventStore.Append(ctx, leaderID, []*events.Event{
			{
				Type: events.EventFailoverCompleted,
				Payload: map[string]any{
					"leader_id":    leaderID,
					"new_agent_id": newAgent.ID(),
				},
			},
		}, 0); err != nil {
			slog.Warn("Failed to emit failover completed event", "error", err)
		}
	}

	slog.Info("failover completed, new leader registered",
		"leader_id", leaderID,
		"new_agent_id", newAgent.ID(),
	)
}

// ColdRestartStrategy replaces a failed leader by creating a new instance via factory.
type ColdRestartStrategy struct {
	factory     func(ctx context.Context, config interface{}) (base.Agent, error)
	agentConfig interface{}
	checkpoint  *CheckpointRepository
}

// NewColdRestartStrategy creates a ColdRestartStrategy.
func NewColdRestartStrategy(
	factory func(ctx context.Context, config interface{}) (base.Agent, error),
	agentConfig interface{},
	checkpoint *CheckpointRepository,
) (*ColdRestartStrategy, error) {
	if factory == nil {
		return nil, errors.New("cold restart strategy: factory is required")
	}
	return &ColdRestartStrategy{
		factory:     factory,
		agentConfig: agentConfig,
		checkpoint:  checkpoint,
	}, nil
}

// HandleFailover creates a new leader instance and starts it.
// Injects checkpoint into the new agent so session recovery works.
func (s *ColdRestartStrategy) HandleFailover(
	ctx context.Context,
	leaderID string,
	checkpoint *LeaderCheckpoint,
) (base.Agent, error) {
	if leaderID == "" {
		return nil, errors.New("cold restart strategy: empty leader ID")
	}

	config := s.agentConfig
	if config == nil && checkpoint != nil && len(checkpoint.Metadata) > 0 {
		config = checkpoint.Metadata
	}

	agent, err := s.factory(ctx, config)
	if err != nil {
		return nil, errors.Wrap(err, "cold restart strategy: create agent")
	}

	// Inject checkpoint repository so the new agent can recover session from checkpoint.
	if s.checkpoint != nil {
		if la, ok := agent.(*leaderAgent); ok {
			la.checkpoint = s.checkpoint
		}
	}

	if err := agent.Start(ctx); err != nil {
		return nil, errors.Wrap(err, "cold restart strategy: start agent")
	}

	return agent, nil
}
