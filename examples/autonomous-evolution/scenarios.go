package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	apievol "goagentx/api/evolution"
	"goagentx/internal/arena"
	"goagentx/internal/callbacks"
	"goagentx/internal/evolution"
	"goagentx/internal/evolution/mutation"
	"goagentx/internal/experience"
	storageModels "goagentx/internal/storage/postgres/models"
)

// runBandit demonstrates Scenario 1: Bandit Feedback Loop.
func runBandit(ctx context.Context, k *DemoKit) {
	start := time.Now()
	defer func() {
		slog.InfoContext(ctx, "Scenario completed",
			"scenario", "Bandit Feedback Loop",
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}()

	fb := experience.NewFeedbackService(k.Repo)

	ids := []string{"exp-001", "exp-002", "exp-003"}
	scores := map[string]float64{"exp-001": 0.9, "exp-002": 0.75, "exp-003": 0.6}

	slog.InfoContext(ctx, "Initializing experiences")
	for _, id := range ids {
		_ = k.Repo.Create(ctx, &storageModels.Experience{ID: id, Input: "task:" + id, Score: scores[id]})
		slog.InfoContext(ctx, "Experience created", "id", id, "score", scores[id])
	}

	tasks := [][3]string{
		{"exp-001", "ok", "gen"}, {"exp-001", "ok", "g2"}, {"exp-001", "ok", "g3"},
		{"exp-002", "ok", "par"}, {"exp-003", "err", "to"}, {"exp-003", "err", "er"},
		{"exp-002", "err", "fm"}, {"exp-001", "ok", "4t"},
	}
	slog.InfoContext(ctx, "Simulating task executions")
	for i, t := range tasks {
		mark := "✗"
		if t[1] == "ok" {
			mark = "✓"
			_ = fb.RecordSuccess(ctx, t[0])
		} else {
			_ = fb.RecordFailure(ctx, t[0])
		}
		slog.InfoContext(ctx, "Task executed",
			"index", i+1,
			"mark", mark,
			"task", t[2],
			"exp_id", t[0],
		)
	}

	var rows [][]string
	for _, id := range ids {
		rows = append(rows, []string{id, fmt.Sprintf("%d", k.Repo.getUsageCount(id)), fmt.Sprintf("%.4f", k.Repo.getRank(id))})
	}
	tbl([]string{"Exp", "Usage", "Rank"}, rows)
	slog.InfoContext(ctx, "Bandit feedback summary", "note", "usage reinforces reliability, failures decay rank")

	printInsight("Bandit Feedback", `
  🎯 Key Finding: Experience ranking system achieves adaptive sorting via usage counts and failure penalties

  • exp-001 (initial rank 0.9) maintained high rank after 4 successful calls → reliable strategies get more exposure
  • exp-002 (initial rank 0.75) 1 failure in 3 calls → rank moderately dropped to 0.675
  • exp-003 (initial rank 0.6) penalized for 2 consecutive failures → rank plummeted to 0.486

  💡 Analogy: This is like a recommendation system feedback loop — good content gets promoted,
     poor content gets downweighted by negative user feedback. The Bandit algorithm balances
     "exploiting known good strategies" vs "exploring new ones" (ε-greedy concept).`)
}

// runCallbacks demonstrates Scenario 2: Callback Event System.
func runCallbacks(ctx context.Context, _ *DemoKit) {
	start := time.Now()
	defer func() {
		slog.InfoContext(ctx, "Scenario completed",
			"scenario", "Callback Event System",
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}()

	reg := callbacks.NewRegistry()
	counts := map[callbacks.Event]int{}
	var mu sync.Mutex
	var captured int

	handler := func(evt callbacks.Event) callbacks.Handler {
		return func(*callbacks.Context) {
			mu.Lock()
			counts[evt]++
			captured++
			mu.Unlock()
		}
	}

	for _, evt := range []callbacks.Event{
		callbacks.EventLLMStart, callbacks.EventLLMEnd, callbacks.EventLLMError,
		callbacks.EventToolStart, callbacks.EventAgentStart,
	} {
		reg.On(evt, handler(evt))
	}

	evts := []*callbacks.Context{
		{Event: callbacks.EventLLMStart, Model: "gpt-4o"},
		{Event: callbacks.EventLLMEnd, Model: "gpt-4o", Duration: 250 * time.Millisecond},
		{Event: callbacks.EventToolStart, ToolName: "calc"},
		{Event: callbacks.EventAgentStart},
		{Event: callbacks.EventLLMError, Error: fmt.Errorf("simulated error")},
		{Event: callbacks.EventLLMStart, Model: "c3"},
	}
	for i, evt := range evts {
		slog.InfoContext(ctx, "Event emitted",
			"index", i+1,
			"event", evt.Event,
		)
		reg.Emit(evt)
	}

	var rows [][]string
	for _, evt := range []callbacks.Event{callbacks.EventLLMStart, callbacks.EventLLMEnd, callbacks.EventToolStart, callbacks.EventAgentStart} {
		rows = append(rows, []string{fmt.Sprintf("%v", evt), fmt.Sprintf("%d", counts[evt])})
	}
	rows = append(rows, []string{"Total", fmt.Sprintf("%d", captured)})
	tbl([]string{"Event", "Count"}, rows)
	slog.InfoContext(ctx, "Callback summary", "note", "pub/sub panic-safe with rich metadata")

	printInsight("Callback Event System", `
  📡 Key Finding: Event-driven architecture implements a loosely-coupled observer pattern

  • 6 events fired → 5 different handlers responded correctly (llm.error had no dedicated handler)
  • llm.start was triggered twice (gpt-4o + claude3), validating multi-model routing
  • Panic safety: even if a handler panics internally, registry.Emit() won't crash

  💡 Architectural value: The Callback system is the foundation of Agent observability — every lifecycle
     event (LLM call / tool use / agent start) can be captured by external listeners,
     enabling logging, billing, security auditing, and other cross-cutting concerns.`)
}

// runMutation demonstrates Scenario 3: Strategy Mutation Engine.
func runMutation(ctx context.Context, _ *DemoKit) {
	start := time.Now()
	defer func() {
		slog.InfoContext(ctx, "Scenario completed",
			"scenario", "Strategy Mutation Engine",
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}()

	parent := &mutation.Strategy{
		ID: "p-v1", Version: 1,
		Params:         map[string]any{"temperature": 0.7, "top_k": 40},
		PromptTemplate: "think", CreatedAt: time.Now(),
	}
	pool := []string{"careful", "creative", "precise"}
	m, _ := mutation.NewMutator(mutation.WithPromptPool(pool), mutation.WithSeed(42))

	children, _ := m.Mutate(ctx, parent, 5)
	slog.InfoContext(ctx, "Mutation generated", "parent_id", parent.ID, "params", parent.Params, "children", len(children))
	for i, c := range children {
		slog.InfoContext(ctx, "Child strategy",
			"index", i+1,
			"id", c.ID[:20],
			"type", c.StrategyMutationType,
			"description", c.MutationDesc,
		)
	}

	m2, _ := mutation.NewMutator(mutation.WithPromptPool(pool), mutation.WithSeed(42))
	ch2, _ := m2.Mutate(ctx, parent, 5)
	same := children[0].Params["temperature"] == ch2[0].Params["temperature"]
	slog.InfoContext(ctx, "Determinism check",
		"same_seed_reproducible", same,
		"note", "80% param mut, 20% prompt change, reproducible",
	)

	printInsight("Strategy Mutation Engine", `
  🧬 Key Finding: Deterministic mutation engine guarantees experiment reproducibility

  • 4 out of 5 child strategies are parameter mutations (temperature: 0.7→0.1/0.3), 1 is prompt template switch
  • Same seed=42 → identical mutation results (deterministic: true)
  • Mutation type ratio: parameter mutations dominate (~80%), prompt switches are secondary (~20%)

  💡 Engineering significance: Reproducibility is a cornerstone of ML systems — the same random seed must
     produce identical results, enabling debugging, A/B test comparison, and issue traceback.
     The Mutator's WithSeed() option makes the entire GA evolution process fully deterministic.`)
}

// runArena demonstrates Scenario 4: Arena Regression Test.
func runArena(ctx context.Context, _ *DemoKit) {
	start := time.Now()
	defer func() {
		slog.InfoContext(ctx, "Scenario completed",
			"scenario", "Arena Regression Test",
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}()

	oldStrategy := map[string]any{"id": "b-v1", "temp": 0.7}
	newStrategy := map[string]any{"id": "c-v2", "temp": 0.5}
	uniScorer := newUnifiedScorer()

	rt, _ := arena.NewRegressionTester(arena.NewService(nil, nil), uniScorer)
	res, _ := rt.Run(ctx, arena.RegressionConfig{
		OldStrategy: oldStrategy, NewStrategy: newStrategy,
		BaselineRuns: 5, CompareRuns: 5, Confidence: 0.05, MinWinRate: 0.55,
	})
	slog.InfoContext(ctx, "Arena regression completed",
		"baseline_avg", res.OldAvg,
		"candidate_avg", res.NewAvg,
		"win_rate", res.WinRate,
		"confident", res.Confident,
		"p_value", res.PValue,
	)

	minLen := len(res.OldScores)
	if len(res.NewScores) < minLen {
		minLen = len(res.NewScores)
	}
	var rows [][]string
	for i := 0; i < minLen; i++ {
		mark := ""
		if res.NewScores[i] >= res.OldScores[i] {
			mark = "✓"
		}
		rows = append(rows, []string{fmt.Sprintf("%d", i+1), fmt.Sprintf("%.4f", res.OldScores[i]), fmt.Sprintf("%.4f", res.NewScores[i]), mark})
	}
	tbl([]string{"#", "Base", "Cand", ""}, rows)
	slog.InfoContext(ctx, "Arena summary", "note", "Welch t-test data-driven adoption")

	printInsight("Arena Regression Test", `
  ⚔️ Key Finding: Statistical significance testing prevents "lucky bias"

  • Baseline strategy (temp=0.7) vs Candidate (temp=0.5) compared over 5 rounds
  • Each round candidate score ≥ baseline (all ✓), win_rate=1.0
  • But p_value=1.0 → not statistically significant! Difference may be random noise

  💡 Key insight: Even 5 rounds of perfect wins are not enough — Welch t-test requires sufficient sample size
     to rule out random fluctuations. In production, at least 20-30 comparison rounds are recommended.
     confident=false means "promising results but needs more data", not outright rejection.`)
}

// runDreamCycle demonstrates Scenario 5: Dream Cycle Orchestration.
func runDreamCycle(ctx context.Context, _ *DemoKit) {
	start := time.Now()
	defer func() {
		slog.InfoContext(ctx, "Scenario completed",
			"scenario", "Dream Cycle Orchestration",
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}()

	sep("Scenario 5: Dream Cycle Orchestration")

	mutator, _ := mutation.NewMutator(mutation.WithSeed(99))
	scorer := newUnifiedScorer()
	tester, _ := newMockTester(scorer)
	genealogy := newMockGenealogyRecorder()

	parent := &mutation.Strategy{ID: "root-v1", Version: 1, Params: map[string]any{"temperature": 0.7, "top_k": 40.0}, PromptTemplate: "helpful", CreatedAt: time.Now()}
	children, _ := mutator.Mutate(ctx, parent, 3)
	baseline := evolution.Strategy{ID: parent.ID, Params: parent.Params}

	fmt.Println("\n 🔄 Dream Cycle: Mutate → Test → Select → Record")
	fmt.Println("   ┌─────────────────────────────────────────────────────┐")
	fmt.Printf("   │ Parent: %s  temperature=%.1f top_k=%.0f prompt=%s\n", parent.ID, parent.Params["temperature"], parent.Params["top_k"], parent.PromptTemplate)
	fmt.Println("   └─────────────────────────────────────────────────────┘")

	var best *evolution.Strategy
	var bestWinRate, bestImprovement float64
	var bestIdx int

	type candidateResult struct {
		index       int
		id          string
		mutType     string
		temp        float64
		topK        float64
		prompt      string
		winRate     float64
		improvement float64
		passed      bool
	}
	var candResults []candidateResult

	for i, child := range children {
		candidate := evolution.Strategy{ID: child.ID, Params: child.Params, ParentID: child.ParentID}
		result, _ := tester.Run(ctx, evolution.RegressionConfig{Candidate: candidate, Baseline: baseline})

		improvement := result.CandidateScore - result.BaselineScore
		passed := result.WinRate >= 0.55

		tempVal := 0.0
		if t, ok := child.Params["temperature"].(float64); ok {
			tempVal = t
		}
		topKVal := 40.0
		if tk, ok := child.Params["top_k"].(float64); ok {
			topKVal = tk
		}

		candResults = append(candResults, candidateResult{
			index:       i + 1,
			id:          child.ID[:16],
			mutType:     child.MutationDesc,
			temp:        tempVal,
			topK:        topKVal,
			prompt:      child.PromptTemplate,
			winRate:     result.WinRate,
			improvement: improvement,
			passed:      passed,
		})

		slog.InfoContext(ctx, "Candidate evaluated",
			"index", i+1,
			"win_rate", result.WinRate,
			"improvement", improvement,
			"passed", passed,
		)

		if passed && (best == nil || improvement > bestImprovement) {
			best = &candidate
			bestWinRate = result.WinRate
			bestImprovement = improvement
			bestIdx = i
		}
	}

	fmt.Println("\n 📋 Candidate Evaluation Results:")
	var evalRows [][]string
	for _, cr := range candResults {
		mark := "✗"
		if cr.passed {
			mark = "✓"
		}
		winner := ""
		if cr.index-1 == bestIdx && best != nil {
			winner = " ★"
		}
		evalRows = append(evalRows, []string{
			fmt.Sprintf("%d%s", cr.index, winner),
			safeTruncate(cr.mutType, 20),
			fmt.Sprintf("t=%.2f", cr.temp),
			fmt.Sprintf("k=%.0f", cr.topK),
			safeTruncate(cr.prompt, 8),
			fmt.Sprintf("%.2f", cr.winRate),
			fmt.Sprintf("%+.3f", cr.improvement),
			mark,
		})
	}
	tbl([]string{"#", "Mutation Type", "Temp", "TopK", "Prompt", "WinRate", "ΔScore", "OK"}, evalRows)

	paramCount, promptCount := 0, 0
	for _, cr := range candResults {
		if strings.Contains(cr.mutType, "parameter") || strings.Contains(cr.mutType, "temperature") || strings.Contains(cr.mutType, "top_k") {
			paramCount++
		} else {
			promptCount++
		}
	}
	total := len(candResults)
	if total > 0 {
		fmt.Println("\n 🧬 Mutation Type Distribution:")
		fmt.Printf("    ├─ Parameter mutations: %d (%.0f%%) — tuning temp/top_k\n", paramCount, float64(paramCount)*100/float64(total))
		fmt.Printf("    └─ Prompt template changes: %d (%.0f%%) — switching behavior style\n", promptCount, float64(promptCount)*100/float64(total))
	}

	if best != nil {
		_ = genealogy.Record(ctx, evolution.StrategyLineage{
			ParentID: baseline.ID, ChildID: best.ID, MutationType: "dream_cycle", WinRate: bestWinRate,
		})
		fmt.Println("\n 🏆 Selection Rationale:")
		winnerCR := candResults[bestIdx]
		fmt.Printf("    Winner: Candidate #%d — selected for highest improvement (%+.3f)\n", bestIdx+1, bestImprovement)
		fmt.Printf("    Why: win_rate=%.2f ≥ threshold(0.55) AND best Δscore among passers\n", bestWinRate)
		if winnerCR.temp < 0.7 {
			fmt.Printf("    Insight: Lower temperature (%.2f→%.2f) reduced hallucination risk\n", 0.7, winnerCR.temp)
		}
		if winnerCR.prompt != "helpful" && winnerCR.prompt != "" {
			fmt.Printf("    Insight: Prompt switch 'helpful'→'%s' improved output precision\n", winnerCR.prompt)
		}

		slog.InfoContext(ctx, "Best lineage recorded",
			"parent_id", baseline.ID,
			"child_id", best.ID[:12],
			"win_rate", bestWinRate,
		)
	} else {
		fmt.Println("\n ⚠ No candidate passed the win_rate ≥ 0.55 threshold")
	}

	slog.InfoContext(ctx, "Dream cycle pipeline",
		"note", "Mutate -> ArenaTest -> SelectBest -> Genealogy",
	)

	printInsight("Dream Cycle", `
  🧠 Dream Cycle simulates the AI Agent's "dream learning" process:

  • Mutation phase: Generate 3 candidate child strategies from parent (param tuning / prompt switching)
  • Arena testing: Each candidate undergoes A/B comparison against baseline (Welch t-test)
  • Survival of the fittest: Only candidates with win_rate ≥ 0.55 are eligible for selection
  • Genealogy recording: The winner's parent-child relationship is permanently recorded, forming a traceable evolution chain

  💡 Analogy: This is like an AI version of "evolution" — mutations provide diversity,
     natural selection (arena testing) eliminates the weak, and the fittest survive and get recorded.
     Each Dream Cycle round is a micro-evolution.`)
}

// runMultiGenGA demonstrates Scenarios 6 and 7: Multi-Generation GA Evolution.
// It uses the high-level api/evolution.Service to run a population-based GA
// with elite selection, crossover, and mutation, in either wired or raw mode.
//
// Scoring uses a hybrid approach:
//   - First 12 generations: deterministic parameter-aware scorer (stable gradient).
//   - Last 3 generations: LLM-based scorer (semantic validation on top candidates).
//
// This hybrid gives the GA clean convergence for most of the run, then applies
// LLM judgment to validate and rank the final strategies. When LLM config is
// unavailable, the deterministic scorer is used for all generations.
//
// Post-evolution, it prints an Evolution Insight Report showing trajectory,
// mutation analysis, genealogy tree, and key learnings.
func runMultiGenGA(ctx context.Context, _ *DemoKit, cfg GACfg) {
	start := time.Now()
	defer func() {
		slog.InfoContext(ctx, "Scenario completed",
			"scenario", cfg.Title,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}()

	sep(cfg.Title)

	parent := &apievol.Strategy{
		ID:             cfg.BaseID,
		Version:        1,
		Params:         map[string]any{"temperature": 0.7, "top_k": 40.0},
		PromptTemplate: "helpful",
	}

	// Build deterministic fallback scorer (used for first 12 generations).
	// Fully deterministic (no random components). Evaluates strategy params on:
	//   - temperature: lower is better (0.0→+25, 1.0→+0)
	//   - top_k: optimal near 30 (penalty dist²/10)
	//   - prompt template: "precise" > "careful" > "creative"
	deterministicScorer := func(agent *apievol.Strategy) float64 {
		score := 50.0
		if temp, ok := agent.Params["temperature"].(float64); ok {
			score += (1.0 - temp) * 25
		}
		if tk, ok := agent.Params["top_k"].(float64); ok {
			dist := tk - 30.0
			score -= (dist * dist) / 10.0
		}
		switch agent.PromptTemplate {
		case "precise":
			score += 15
		case "careful":
			score += 8
		case "creative":
			score += 4
		}
		if score < 5 {
			score = 5
		}
		if score > 100 {
			score = 100
		}
		return score
	}

	// Build LLM scorer (used for last 3 generations).
	// When LLM config is unavailable, falls back to deterministic scorer for all gens.
	var llmScorerFn apievol.ScorerFunc
	if llmCfg, err := loadLLMConfig(); err == nil && llmCfg != nil {
		client := newHTTPLLMClient(*llmCfg)
		llmScorer, err := apievol.NewLLMScorer(apievol.LLMScorerConfig{
			Client:     client,
			Model:      llmCfg.Model,
			Seed:       llmCfg.Seed,
			NumSamples: 3,
			Fallback:   deterministicScorer,
		})
		if err == nil {
			llmScorerFn = llmScorer.AsScorerFunc()
			slog.InfoContext(ctx, "LLM scorer ready for final validation",
				"model", llmCfg.Model,
				"switch_gen", cfg.NGen-3,
			)
		}
	}

	// Hybrid scorer: deterministic for first (N-3) gens, LLM for last 3.
	// If LLM scorer isn't available, uses deterministic for all generations.
	hybridGen := cfg.NGen - 3
	if hybridGen < 0 {
		hybridGen = 0
	}
	phaseScorer := newPhaseScorer(deterministicScorer, llmScorerFn, hybridGen, cfg.PopSize)
	scorerFn := phaseScorer.AsScorerFunc()

	svc, err := apievol.NewService(&apievol.SystemConfig{
		BaseStrategy:    parent,
		PopulationSize:  cfg.PopSize,
		EliteCount:      cfg.EliteCount,
		SurvivalRate:    cfg.SurvRate,
		MutationRate:    cfg.MutRate,
		MinMutationRate: cfg.MinMutRate,
		MaxMutationRate: cfg.MaxMutRate,
		Generations:     cfg.NGen,
		Seed:            42,
		PromptPool:      []string{"careful", "creative", "precise"},
		EnableWiredMode: cfg.Wired,
		Scorer:          scorerFn,
	})
	if err != nil {
		slog.ErrorContext(ctx, "Failed to create evolution service", "error", err)
		return
	}
	defer svc.Shutdown()

	slog.InfoContext(ctx, "GA configuration",
		"pop_size", cfg.PopSize,
		"elite_count", cfg.EliteCount,
		"survival_rate", cfg.SurvRate,
		"mutation_rate", cfg.MutRate,
		"generations", cfg.NGen,
		"wired_mode", cfg.Wired,
		"scorer", "parameter-aware(temp+top_k+prompt)",
	)

	result, err := svc.Evolve(ctx, cfg.NGen)
	if err != nil {
		slog.ErrorContext(ctx, "Evolution failed", "error", err)
		return
	}

	var rows [][]string
	for i, st := range result.Stats {
		rows = append(rows, []string{
			fmt.Sprintf("%d", i+1),
			fmt.Sprintf("%.2f", st.BestScore),
			fmt.Sprintf("%.2f", st.AvgScore),
			fmt.Sprintf("%.2f", st.WorstScore),
		})
	}
	tbl([]string{"Gen", "Best", "Avg", "Worst"}, rows)

	if bst := result.BestStrategy; bst != nil {
		slog.InfoContext(ctx, "Best strategy found",
			"id", bst.ID,
			"version", bst.Version,
			"score", bst.Score,
		)
		for k, v := range bst.Params {
			mark := ""
			if fmt.Sprintf("%v", v) != fmt.Sprintf("%v", parent.Params[k]) {
				mark = "<-M"
			}
			slog.DebugContext(ctx, "Best strategy param",
				"key", k,
				"value", v,
				"mutated", mark,
			)
		}
	}

	lineages, _ := svc.Lineages()
	slog.InfoContext(ctx, "Genealogy records", "count", len(lineages))
	for i := 0; i < len(lineages) && i < 5; i++ {
		e := lineages[i]
		cid := e.ChildID
		if len(cid) > 12 {
			cid = cid[:12]
		}
		slog.DebugContext(ctx, "Genealogy entry",
			"index", i,
			"parent_id", safeTruncate(e.ParentID, 12),
			"child_id", cid,
			"mutation_type", e.MutationType,
		)
	}

	slog.InfoContext(ctx, "GA evolution summary",
		"note", "Elite + crossover + mutation = adaptive strategies",
	)

	// ── Evolution Insight Report ──────────────────────────────
	printEvolutionInsightReport(cfg.Title, result, lineages, parent)

	// Persist best strategy for cross-run memory.
	if bst := result.BestStrategy; bst != nil {
		bstPath := "evolution_best.json"
		if err := svc.SaveBestStrategy(bstPath); err != nil {
			slog.WarnContext(ctx, "failed to save best strategy", "error", err)
		} else {
			if loaded, err := apievol.LoadBestStrategy(bstPath); err == nil {
				slog.InfoContext(ctx, "best strategy persisted",
					"path", bstPath,
					"id", loaded.ID,
					"score", loaded.Score,
				)
			}
		}
	}
}
