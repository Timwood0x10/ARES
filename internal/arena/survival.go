package arena

import (
	"context"
	"log/slog"
	"math/rand"
	"sync"
	"time"
)

// SurvivalConfig configures a survival test run.
type SurvivalConfig struct {
	Duration   time.Duration `json:"duration"`
	Interval   time.Duration `json:"interval"`
	AgentCount int           `json:"agent_count"`
}

// defaultSurvivalConfig returns sensible defaults for a survival run.
func defaultSurvivalConfig() SurvivalConfig {
	return SurvivalConfig{
		Duration: 30 * time.Minute,
		Interval: 10 * time.Second,
	}
}

// SurvivalReport holds the result of a survival run.
type SurvivalReport struct {
	Duration   time.Duration   `json:"duration"`
	ActionsRun int             `json:"actions_run"`
	Score      ResilienceScore `json:"score"`
	Timeline   []SurvivalEvent `json:"timeline"`
}

// SurvivalEvent records a single chaos event.
type SurvivalEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Action    Action    `json:"action"`
	Result    Result    `json:"result"`
}

// SurvivalStatus holds the current state of a running survival test.
type SurvivalStatus struct {
	Running    bool            `json:"running"`
	Config     SurvivalConfig  `json:"config"`
	ActionsRun int             `json:"actions_run"`
	StartedAt  time.Time       `json:"started_at,omitempty"`
	Elapsed    time.Duration   `json:"elapsed"`
	Timeline   []SurvivalEvent `json:"timeline"`
}

// survivalState tracks the in-progress survival run.
type survivalState struct {
	mu      sync.RWMutex
	running bool
	config  SurvivalConfig
	started time.Time
	events  []SurvivalEvent
}

// RunSurvival runs chaos actions at intervals for the configured duration.
// It randomly kills agents, removes edges, etc., and records everything.
// Only one survival run can be active at a time.
func (s *Service) RunSurvival(ctx context.Context, cfg SurvivalConfig) SurvivalReport {
	if cfg.Duration <= 0 {
		cfg = defaultSurvivalConfig()
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 10 * time.Second
	}

	// Mark survival as running.
	s.survival.mu.Lock()
	if s.survival.running {
		s.survival.mu.Unlock()
		slog.Warn("arena: survival already running, returning empty report")
		return SurvivalReport{}
	}
	s.survival.running = true
	s.survival.config = cfg
	s.survival.started = time.Now()
	s.survival.events = make([]SurvivalEvent, 0)
	s.survival.mu.Unlock()

	defer func() {
		s.survival.mu.Lock()
		s.survival.running = false
		s.survival.mu.Unlock()
	}()

	start := time.Now()
	slog.Info("arena: survival mode started",
		"duration", cfg.Duration,
		"interval", cfg.Interval,
	)

	timeline := make([]SurvivalEvent, 0)
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()

	deadline := start.Add(cfg.Duration)

	for {
		select {
		case <-ctx.Done():
			slog.Info("arena: survival mode cancelled by context")
			return s.buildSurvivalReport(time.Since(start), timeline)
		case now := <-ticker.C:
			if now.After(deadline) {
				slog.Info("arena: survival mode completed",
					"actions", len(timeline),
					"duration", time.Since(start),
				)
				return s.buildSurvivalReport(time.Since(start), timeline)
			}

			action := s.randomChaosAction()
			result := s.Execute(ctx, action)

			event := SurvivalEvent{
				Timestamp: time.Now(),
				Action:    action,
				Result:    result,
			}
			timeline = append(timeline, event)

			// Update shared survival state for status queries.
			s.survival.mu.Lock()
			s.survival.events = append(s.survival.events, event)
			s.survival.mu.Unlock()

			slog.Info("arena: survival event",
				"type", action.Type,
				"target", action.TargetID,
				"success", result.Success,
				"elapsed", time.Since(start).Round(time.Second),
			)
		}
	}
}

// GetSurvivalStatus returns the current status of the survival run.
func (s *Service) GetSurvivalStatus() SurvivalStatus {
	s.survival.mu.RLock()
	defer s.survival.mu.RUnlock()

	status := SurvivalStatus{
		Running:    s.survival.running,
		Config:     s.survival.config,
		ActionsRun: len(s.survival.events),
		Timeline:   s.survival.events,
	}
	if s.survival.running {
		status.StartedAt = s.survival.started
		status.Elapsed = time.Since(s.survival.started)
	}
	return status
}

// buildSurvivalReport constructs the final report from the timeline.
func (s *Service) buildSurvivalReport(duration time.Duration, timeline []SurvivalEvent) SurvivalReport {
	stats := s.Stats()
	avgRecovery := s.calculateAvgRecoveryTime(timeline)
	score := CalculateScore(stats, avgRecovery)

	return SurvivalReport{
		Duration:   duration,
		ActionsRun: len(timeline),
		Score:      score,
		Timeline:   timeline,
	}
}

// calculateAvgRecoveryTime computes the average duration of successful actions.
func (s *Service) calculateAvgRecoveryTime(events []SurvivalEvent) time.Duration {
	var total time.Duration
	var count int
	for _, ev := range events {
		if ev.Result.Success && ev.Result.Duration > 0 {
			total += ev.Result.Duration
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return total / time.Duration(count)
}

// randomChaosAction generates a random chaos action targeting available resources.
func (s *Service) randomChaosAction() Action {
	actionTypes := []ActionType{
		ActionKillAgent,
		ActionKillLeader,
		ActionRemoveNode,
		ActionRemoveEdge,
		ActionPauseAgent,
		ActionResumeAgent,
		ActionSlowAgent,
		ActionKillOrchestrator,
		ActionNetworkPartition,
	}
	actionType := actionTypes[rand.Intn(len(actionTypes))] //nolint:gosec // non-crypto rand is fine for chaos testing

	action := Action{
		ID:        randomID(),
		Type:      actionType,
		CreatedAt: time.Now(),
	}

	switch actionType {
	case ActionKillAgent:
		ids := s.injector.AvailableAgentIDs()
		if len(ids) > 0 {
			action.TargetID = ids[rand.Intn(len(ids))] //nolint:gosec
		}
	case ActionRemoveNode:
		ids := s.injector.AvailableNodeIDs()
		if len(ids) > 0 {
			action.TargetID = ids[rand.Intn(len(ids))] //nolint:gosec
		}
	case ActionRemoveEdge:
		ids := s.injector.AvailableAgentIDs()
		if len(ids) >= 2 {
			fromIdx := rand.Intn(len(ids))   //nolint:gosec
			toIdx := rand.Intn(len(ids) - 1) //nolint:gosec
			if toIdx >= fromIdx {
				toIdx++
			}
			action.SourceID = ids[fromIdx]
			action.TargetID = ids[toIdx]
		}
	case ActionKillLeader:
	case ActionKillOrchestrator:
	case ActionPauseAgent, ActionResumeAgent, ActionSlowAgent, ActionNetworkPartition:
		ids := s.injector.AvailableAgentIDs()
		if len(ids) > 0 {
			action.TargetID = ids[rand.Intn(len(ids))] //nolint:gosec
			if actionType == ActionSlowAgent {
				action.Metadata = map[string]any{
					"duration": time.Duration(5+rand.Intn(10)) * time.Second,
				}
			}
		}
	}

	return action
}

// randomID generates a short random hex identifier.
func randomID() string {
	const hex = "0123456789abcdef"
	b := make([]byte, 16)
	for i := range b {
		b[i] = hex[rand.Intn(16)] //nolint:gosec
	}
	return string(b)
}
