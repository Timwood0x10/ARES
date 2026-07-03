// Package genome provides population management for genetic algorithm evolution.
package genome


func copyRecoveryActions(src map[string]int) map[string]int {
	if src == nil {
		return make(map[string]int)
	}
	dst := make(map[string]int, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func (p *Population) appendHistoryLocked() {
	if p.HistoryMaxSize == 0 {
		return
	}
	entry := GenerationHistoryEntry{
		Generation:     p.Generation,
		PopulationSize: len(p.Agents),
		Diversity:      p.measureDiversityReportLocked().Overall,
	}
	if len(p.Agents) > 0 {
		entry.BestScore, entry.AvgScore, entry.WorstScore = p.computeStatsLocked()
	}
	entry.MutationTypes = make(map[string]int)
	parentSet := make(map[string]struct{})
	for _, agent := range p.Agents {
		mt := agent.StrategyMutationType.String()
		if mt == "" {
			mt = "unknown"
		}
		entry.MutationTypes[mt]++
		if agent.ParentID != "" {
			parentSet[agent.ParentID] = struct{}{}
		}
	}
	entry.NumDiverse = len(parentSet)
	entry.RecoveryActions = make(map[string]int, len(p.recoveryActions))
	for k, v := range p.recoveryActions {
		entry.RecoveryActions[k] = v
	}
	p.recoveryActions = make(map[string]int)
	p.history = append(p.history, entry)
	if p.HistoryMaxSize > 0 && len(p.history) > p.HistoryMaxSize {
		p.history = p.history[len(p.history)-p.HistoryMaxSize:]
	}
}

func (p *Population) History() []GenerationHistoryEntry {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.history) == 0 {
		return nil
	}
	cp := make([]GenerationHistoryEntry, len(p.history))
	copy(cp, p.history)
	return cp
}

func (p *Population) HistoryCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.history)
}

func (p *Population) CurrentGeneration() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.Generation
}

func (p *Population) StagnantGenerations() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.stagnantGens
}

func (p *Population) CurrentMutationRate() float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.currentMutationRate
}

func (p *Population) RecordRecoveryAction(action string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.recoveryActions == nil {
		p.recoveryActions = make(map[string]int)
	}
	p.recoveryActions[action]++
}

func (p *Population) RecoveryActions() map[string]int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.recoveryActions) == 0 {
		return nil
	}
	result := make(map[string]int, len(p.recoveryActions))
	for k, v := range p.recoveryActions {
		result[k] = v
	}
	return result
}
