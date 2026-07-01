// Package promotion provides strategy promotion and demotion logic.
// This file implements the DefaultPromoter which manages strategy lifecycle states.
package promotion

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_evolution/experience"
)

var (
	// ErrStrategyNotFound indicates the strategy does not exist.
	ErrStrategyNotFound = errors.New("strategy not found")

	// ErrInvalidStateTransition indicates an invalid state transition.
	ErrInvalidStateTransition = errors.New("invalid state transition")

	// ErrCoolDownActive indicates the strategy is in cool-down period.
	ErrCoolDownActive = errors.New("cool-down period active")

	// ErrInsufficientEvidence indicates insufficient evidence for promotion.
	ErrInsufficientEvidence = errors.New("insufficient evidence for promotion")
)

// DefaultPromoter implements the PromotionLogic interface.
// It manages strategy states across generations with thread-safe operations.
type DefaultPromoter struct {
	// mu protects all internal state.
	mu sync.RWMutex

	// criteria defines the promotion and demotion criteria.
	criteria *PromotionCriteria

	// strategies maps strategyID to StrategyInfo.
	strategies map[string]*StrategyInfo

	// history maps strategyID to a slice of promotion records.
	history map[string][]StrategyPromotionRecord

	// champions maps taskType to a slice of champion strategy IDs.
	champions map[string][]string

	// currentGeneration is the current evolution generation.
	currentGeneration int

	// previousScores maps strategyID to previous evidence scores for demotion checks.
	previousScores map[string]float64
}

// NewDefaultPromoter creates a new DefaultPromoter with the given criteria.
// If criteria is nil, default criteria are used.
func NewDefaultPromoter(criteria *PromotionCriteria) *DefaultPromoter {
	if criteria == nil {
		criteria = DefaultPromotionCriteria()
	}

	return &DefaultPromoter{
		criteria:       criteria,
		strategies:     make(map[string]*StrategyInfo),
		history:        make(map[string][]StrategyPromotionRecord),
		champions:      make(map[string][]string),
		previousScores: make(map[string]float64),
		currentGeneration: 0,
	}
}

// Evaluate evaluates a strategy's evidence and determines its appropriate state.
// Returns the recommended state, a human-readable reason, and any error.
func (p *DefaultPromoter) Evaluate(
	ctx context.Context,
	strategyID string,
	evidence experience.Evidence,
) (StrategyState, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Get or create strategy info
	info, exists := p.strategies[strategyID]
	if !exists {
		info = &StrategyInfo{
			StrategyID:      strategyID,
			TaskType:        evidence.TaskType,
			CurrentState:    StrategyStateCandidate,
			Generation:      p.currentGeneration,
			LastStateChange: time.Now(),
		}
		p.strategies[strategyID] = info
	}

	// Calculate score
	score := CalculateEvidenceScore(evidence)
	p.previousScores[strategyID] = score

	// Check cool-down period
	if !p.canTransition(info) {
		return info.CurrentState, "cool-down period active", nil
	}

	// Evaluate based on current state
	var recommendedState StrategyState
	var reason string

	switch info.CurrentState {
	case StrategyStateCandidate:
		recommendedState, reason = p.evaluateCandidate(strategyID, evidence, score)

	case StrategyStateShadow:
		recommendedState, reason = p.evaluateShadow(strategyID, evidence, score, info)

	case StrategyStateChampion:
		recommendedState, reason = p.evaluateChampion(strategyID, evidence, score, info)

	case StrategyStateDemoted:
		recommendedState, reason = p.evaluateDemoted(strategyID, evidence, score, info)

	case StrategyStateRetired:
		recommendedState = StrategyStateRetired
		reason = "strategy is retired"

	default:
		return info.CurrentState, "unknown state", fmt.Errorf("unknown state: %s", info.CurrentState)
	}

	return recommendedState, reason, nil
}

// evaluateCandidate evaluates a candidate strategy for promotion to shadow mode.
func (p *DefaultPromoter) evaluateCandidate(
	strategyID string,
	evidence experience.Evidence,
	score float64,
) (StrategyState, string) {
	// Candidate needs to show some promise to move to shadow mode
	if evidence.SampleCount >= 10 && evidence.SuccessRate >= 0.5 {
		return StrategyStateShadow, fmt.Sprintf(
			"promoted to shadow: success_rate=%.2f, sample_count=%d",
			evidence.SuccessRate, evidence.SampleCount,
		)
	}

	return StrategyStateCandidate, "needs more evidence to enter shadow mode"
}

// evaluateShadow evaluates a shadow strategy for promotion to champion.
func (p *DefaultPromoter) evaluateShadow(
	strategyID string,
	evidence experience.Evidence,
	score float64,
	info *StrategyInfo,
) (StrategyState, string) {
	// Check if evidence meets promotion criteria
	if MeetsPromotionCriteria(evidence, p.criteria) {
		return StrategyStateChampion, fmt.Sprintf(
			"promoted to champion: success_rate=%.2f, error_rate=%.2f, latency_p95=%d, confidence=%.2f",
			evidence.SuccessRate, evidence.ErrorRate, evidence.LatencyP95, evidence.Confidence,
		)
	}

	// Check for demotion due to poor performance
	if evidence.SampleCount >= int64(p.criteria.MinSampleCount) &&
		evidence.SuccessRate < (p.criteria.MinSuccessRate-p.criteria.DemotionThreshold) {
		return StrategyStateDemoted, fmt.Sprintf(
			"demoted: success_rate=%.2f below threshold",
			evidence.SuccessRate,
		)
	}

	return StrategyStateShadow, "collecting more evidence"
}

// evaluateChampion evaluates a champion strategy for demotion.
func (p *DefaultPromoter) evaluateChampion(
	strategyID string,
	evidence experience.Evidence,
	score float64,
	info *StrategyInfo,
) (StrategyState, string) {
	// Check if performance has degraded
	if evidence.SampleCount >= int64(p.criteria.MinSampleCount) {
		// Check for significant performance drop
		if evidence.SuccessRate < (p.criteria.MinSuccessRate - p.criteria.DemotionThreshold) ||
			evidence.ErrorRate > p.criteria.MaxErrorRate ||
			evidence.LatencyP95 > p.criteria.MaxLatencyP95 {
			return StrategyStateDemoted, fmt.Sprintf(
				"demoted: success_rate=%.2f, error_rate=%.2f, latency_p95=%d",
				evidence.SuccessRate, evidence.ErrorRate, evidence.LatencyP95,
			)
		}
	}

	// Check if champion hold period has passed
	if info.GenerationCount >= p.criteria.ChampionHoldPeriod {
		return StrategyStateChampion, "champion status maintained"
	}

	return StrategyStateChampion, "champion status stable"
}

// evaluateDemoted evaluates a demoted strategy for re-promotion or retirement.
func (p *DefaultPromoter) evaluateDemoted(
	strategyID string,
	evidence experience.Evidence,
	score float64,
	info *StrategyInfo,
) (StrategyState, string) {
	// Check if the strategy has improved enough to re-enter shadow mode
	if MeetsPromotionCriteria(evidence, p.criteria) {
		return StrategyStateShadow, fmt.Sprintf(
			"re-promoted to shadow: success_rate=%.2f, confidence=%.2f",
			evidence.SuccessRate, evidence.Confidence,
		)
	}

	// Check if it should be retired after extended poor performance
	if info.GenerationCount >= p.criteria.ChampionHoldPeriod*2 {
		return StrategyStateRetired, "retired due to extended poor performance"
	}

	return StrategyStateDemoted, "monitoring for improvement"
}

// Promote promotes a strategy to the next higher state.
func (p *DefaultPromoter) Promote(ctx context.Context, strategyID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	info, exists := p.strategies[strategyID]
	if !exists {
		return ErrStrategyNotFound
	}

	// Determine target state
	var targetState StrategyState
	switch info.CurrentState {
	case StrategyStateCandidate:
		targetState = StrategyStateShadow
	case StrategyStateShadow:
		targetState = StrategyStateChampion
	case StrategyStateDemoted:
		targetState = StrategyStateShadow
	default:
		return fmt.Errorf("%w: cannot promote from %s", ErrInvalidStateTransition, info.CurrentState)
	}

	// Validate transition
	if !info.CurrentState.CanPromoteTo(targetState) {
		return fmt.Errorf("%w: cannot promote from %s to %s",
			ErrInvalidStateTransition, info.CurrentState, targetState)
	}

	// Check cool-down
	if !p.canTransition(info) {
		return ErrCoolDownActive
	}

	// Perform promotion
	p.transitionState(info, targetState, p.currentGeneration, "manual promotion")

	// Update champions list if promoted to champion
	if targetState == StrategyStateChampion {
		p.addToChampions(info.TaskType, strategyID)
	}

	return nil
}

// Demote demotes a strategy to a lower state with the given reason.
func (p *DefaultPromoter) Demote(ctx context.Context, strategyID string, reason string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	info, exists := p.strategies[strategyID]
	if !exists {
		return ErrStrategyNotFound
	}

	// Determine target state
	var targetState StrategyState
	switch info.CurrentState {
	case StrategyStateCandidate:
		targetState = StrategyStateRetired
	case StrategyStateShadow:
		targetState = StrategyStateDemoted
	case StrategyStateChampion:
		targetState = StrategyStateDemoted
	case StrategyStateDemoted:
		targetState = StrategyStateRetired
	default:
		return fmt.Errorf("%w: cannot demote from %s", ErrInvalidStateTransition, info.CurrentState)
	}

	// Validate transition
	if !info.CurrentState.CanDemoteTo(targetState) {
		return fmt.Errorf("%w: cannot demote from %s to %s",
			ErrInvalidStateTransition, info.CurrentState, targetState)
	}

	// Check cool-down (except for retirement)
	if targetState != StrategyStateRetired && !p.canTransition(info) {
		return ErrCoolDownActive
	}

	// Perform demotion
	p.transitionState(info, targetState, p.currentGeneration, reason)

	// Remove from champions list if demoted from champion
	if info.CurrentState == StrategyStateChampion {
		p.removeFromChampions(info.TaskType, strategyID)
	}

	return nil
}

// GetHistory returns the promotion history for a strategy.
func (p *DefaultPromoter) GetHistory(ctx context.Context, strategyID string) ([]StrategyPromotionRecord, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	history, exists := p.history[strategyID]
	if !exists {
		return []StrategyPromotionRecord{}, nil
	}

	return history, nil
}

// GetCurrentState returns the current state of a strategy.
func (p *DefaultPromoter) GetCurrentState(ctx context.Context, strategyID string) (StrategyState, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	info, exists := p.strategies[strategyID]
	if !exists {
		return StrategyStateCandidate, nil
	}

	return info.CurrentState, nil
}

// GetChampions returns all strategies currently in champion state for a task type.
func (p *DefaultPromoter) GetChampions(ctx context.Context, taskType string) ([]StrategyInfo, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	championIDs, exists := p.champions[taskType]
	if !exists {
		return []StrategyInfo{}, nil
	}

	champions := make([]StrategyInfo, 0, len(championIDs))
	for _, id := range championIDs {
		if info, exists := p.strategies[id]; exists && info.CurrentState == StrategyStateChampion {
			champions = append(champions, *info)
		}
	}

	return champions, nil
}

// GetStrategyInfo returns detailed information about a strategy.
func (p *DefaultPromoter) GetStrategyInfo(ctx context.Context, strategyID string) (*StrategyInfo, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	info, exists := p.strategies[strategyID]
	if !exists {
		return nil, ErrStrategyNotFound
	}

	return info, nil
}

// SetGeneration sets the current evolution generation.
func (p *DefaultPromoter) SetGeneration(generation int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.currentGeneration = generation

	// Update generation count for all strategies
	for _, info := range p.strategies {
		info.GenerationCount++
	}
}

// canTransition checks if the strategy can transition based on cool-down period.
// New strategies (GenerationCount == 0) can transition immediately.
func (p *DefaultPromoter) canTransition(info *StrategyInfo) bool {
	// Allow immediate transition for new strategies
	if info.GenerationCount == 0 {
		return true
	}
	// Require cool-down period for subsequent transitions
	if info.GenerationCount < p.criteria.CoolDownGenerations {
		return false
	}
	return true
}

// transitionState performs a state transition and records it in history.
func (p *DefaultPromoter) transitionState(
	info *StrategyInfo,
	newState StrategyState,
	generation int,
	reason string,
) {
	oldState := info.CurrentState

	// Update strategy info
	info.CurrentState = newState
	info.Generation = generation
	info.LastStateChange = time.Now()
	info.GenerationCount = 0

	if newState == StrategyStateChampion {
		now := time.Now()
		info.ChampionSince = &now
	} else {
		info.ChampionSince = nil
	}

	// Record in history
	record := StrategyPromotionRecord{
		StrategyID:     info.StrategyID,
		State:          newState,
		PreviousState:  oldState,
		Generation:     generation,
		Evidence:       experience.Evidence{}, // Will be populated by caller if needed
		Reason:         reason,
		Timestamp:      time.Now(),
	}

	p.history[info.StrategyID] = append(p.history[info.StrategyID], record)
}

// addToChampions adds a strategy to the champions list for a task type.
func (p *DefaultPromoter) addToChampions(taskType string, strategyID string) {
	champions := p.champions[taskType]

	// Check if already in list
	for _, id := range champions {
		if id == strategyID {
			return
		}
	}

	// Add to list
	p.champions[taskType] = append(champions, strategyID)
}

// removeFromChampions removes a strategy from the champions list for a task type.
func (p *DefaultPromoter) removeFromChampions(taskType string, strategyID string) {
	champions := p.champions[taskType]

	// Find and remove
	for i, id := range champions {
		if id == strategyID {
			p.champions[taskType] = append(champions[:i], champions[i+1:]...)
			return
		}
	}
}

// RegisterStrategy registers a new strategy with initial state.
func (p *DefaultPromoter) RegisterStrategy(strategyID string, taskType string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, exists := p.strategies[strategyID]; exists {
		return fmt.Errorf("strategy %s already registered", strategyID)
	}

	p.strategies[strategyID] = &StrategyInfo{
		StrategyID:      strategyID,
		TaskType:        taskType,
		CurrentState:    StrategyStateCandidate,
		Generation:      p.currentGeneration,
		LastStateChange: time.Now(),
		GenerationCount: 0,
	}

	return nil
}

// GetAllStrategies returns all registered strategies.
func (p *DefaultPromoter) GetAllStrategies() map[string]StrategyInfo {
	p.mu.RLock()
	defer p.mu.RUnlock()

	result := make(map[string]StrategyInfo)
	for id, info := range p.strategies {
		result[id] = *info
	}

	return result
}