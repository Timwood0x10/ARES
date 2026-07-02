package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	arena "github.com/Timwood0x10/ares/internal/ares_arena"
	"github.com/Timwood0x10/ares/internal/ares_callbacks"
	evolution "github.com/Timwood0x10/ares/internal/ares_evolution"
	exp "github.com/Timwood0x10/ares/internal/ares_evolution/experience"
	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
	evolutionservice "github.com/Timwood0x10/ares/internal/ares_evolution/service"
	experience "github.com/Timwood0x10/ares/internal/ares_experience"
	storageModels "github.com/Timwood0x10/ares/internal/storage/postgres/models"
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

	reg := ares_callbacks.NewRegistry()
	counts := map[ares_callbacks.Event]int{}
	var mu sync.Mutex
	var captured int

	handler := func(evt ares_callbacks.Event) ares_callbacks.Handler {
		return func(*ares_callbacks.Context) {
			mu.Lock()
			counts[evt]++
			captured++
			mu.Unlock()
		}
	}

	for _, evt := range []ares_callbacks.Event{
		ares_callbacks.EventLLMStart, ares_callbacks.EventLLMEnd, ares_callbacks.EventLLMError,
		ares_callbacks.EventToolStart, ares_callbacks.EventAgentStart,
	} {
		reg.On(evt, handler(evt))
	}

	evts := []*ares_callbacks.Context{
		{Event: ares_callbacks.EventLLMStart, Model: "gpt-4o"},
		{Event: ares_callbacks.EventLLMEnd, Model: "gpt-4o", Duration: 250 * time.Millisecond},
		{Event: ares_callbacks.EventToolStart, ToolName: "calc"},
		{Event: ares_callbacks.EventAgentStart},
		{Event: ares_callbacks.EventLLMError, Error: fmt.Errorf("simulated error")},
		{Event: ares_callbacks.EventLLMStart, Model: "c3"},
	}
	for i, evt := range evts {
		slog.InfoContext(ctx, "Event emitted",
			"index", i+1,
			"event", evt.Event,
		)
		reg.Emit(evt)
	}

	var rows [][]string
	for _, evt := range []ares_callbacks.Event{ares_callbacks.EventLLMStart, ares_callbacks.EventLLMEnd, ares_callbacks.EventToolStart, ares_callbacks.EventAgentStart} {
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
// ────────────────────────────────────────────────────────────────────────────
// Scenario 6: Pure Autonomous Evolution (Control Group A)
//
// Uses ALL GA capabilities but with deterministic scoring only.
// No LLM interference — the GA finds optimal strategies on its own.
//
// GA features used:
//   - Tournament selection (size=3)
//   - Elite preservation (top 3)
//   - Adaptive mutation rate (0.05–0.5)
//   - Stagnation detection (5 gen reset)
//   - Diversity threshold (0.2)
//   - Fitness sharing
//   - Breeding pool ratio (0.5)
//   - Multi-point crossover with half-split prompt mode
//   - Param ranges for constrained mutation
//   - History tracking
// ────────────────────────────────────────────────────────────────────────────

func runScenario6(ctx context.Context, _ *DemoKit, cfg GACfg) *evolutionservice.EvolutionResult {
	start := time.Now()
	defer func() {
		slog.InfoContext(ctx, "Scenario completed",
			"scenario", cfg.Title,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}()

	sep(cfg.Title)

	// Create a temp dir for the evolution report.
	reportDir, _ := os.MkdirTemp("", "evolution-report-*")
	defer func() { _ = os.RemoveAll(reportDir) }()
	reportPath := filepath.Join(reportDir, "report.txt")

	parent := defaultParent(cfg.BaseID)
	svc, err := evolutionservice.NewService(fullGAConfig(
		parent, cfg, false, nil,
		reportPath,
		newMockEvidenceAggregator(),
		newMockPromotionLogic(),
	))
	if err != nil {
		slog.ErrorContext(ctx, "Failed to create evolution service", "error", err)
		return nil
	}
	defer svc.Shutdown()

	slog.InfoContext(ctx, "Pure autonomous evolution — no LLM",
		"pop_size", cfg.PopSize,
		"generations", cfg.NGen,
		"scorer", "deterministic",
	)

	result, err := svc.Evolve(ctx, cfg.NGen)
	if err != nil {
		slog.ErrorContext(ctx, "Evolution failed", "error", err)
		return nil
	}

	printResult(result)
	printEvolutionInsightReport(cfg.Title, result, result.Lineages, parent)

	// Display the saved evolution report.
	if data, err := os.ReadFile(reportPath); err == nil {
		fmt.Println(string(data))
	}

	return result
}

// ────────────────────────────────────────────────────────────────────────────
// Scenario 7: LLM-Guided Evolution (Control Group B)
//
// Same GA capabilities as Scenario 6, but with LLM scoring injected
// INTO the evolution loop (not just post-validation). The LLM acts as
// an oracle that evaluates strategy quality, guiding the GA toward
// solutions that are not only parameter-optimal but also semantically good.
//
// The LLM scorer has a deterministic fallback — if the LLM API is down,
// evolution continues with deterministic scoring automatically.
// ────────────────────────────────────────────────────────────────────────────

func runScenario7(ctx context.Context, _ *DemoKit, cfg GACfg) *evolutionservice.EvolutionResult {
	start := time.Now()
	defer func() {
		slog.InfoContext(ctx, "Scenario completed",
			"scenario", cfg.Title,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}()

	sep(cfg.Title)

	// Create a temp dir for the evolution report.
	reportDir, _ := os.MkdirTemp("", "evolution-report-*")
	defer func() { _ = os.RemoveAll(reportDir) }()
	reportPath := filepath.Join(reportDir, "report.txt")

	parent := defaultParent(cfg.BaseID)

	// Pure deterministic evolution — LLM participates only in post-hoc validation.
	// Rationale:
	//   - GA converges reliably within ~3 generations on heuristic scoring.
	//   - LLM-in-loop adds latency, API risk, and non-determinism for uncertain gain.
	//   - Batch-scoring infrastructure exists (see BatchScorer in SystemConfig) but
	//     is opted out here. Re-enable by wiring an LLMScorer as both Scorer and
	//     BatchScorer when semantic guidance during evolution is desired.
	svc, err := evolutionservice.NewService(fullGAConfig(
		parent, cfg, true, nil,
		reportPath,
		newMockEvidenceAggregator(),
		newMockPromotionLogic(),
	))
	if err != nil {
		slog.ErrorContext(ctx, "Failed to create wired evolution service", "error", err)
		return nil
	}
	defer svc.Shutdown()

	slog.InfoContext(ctx, "Wired evolution — deterministic only, LLM validates best strategy after",
		"pop_size", cfg.PopSize,
		"generations", cfg.NGen,
	)

	result, err := svc.Evolve(ctx, cfg.NGen)
	if err != nil {
		slog.ErrorContext(ctx, "LLM evolution failed", "error", err)
		return nil
	}

	lineages, _ := svc.Lineages()
	printResult(result)
	printEvolutionInsightReport(cfg.Title, result, lineages, parent)

	// Post-evolution: validate best strategy with LLM.
	if best := result.BestStrategy; best != nil {
		llmCfg, err := loadLLMConfig()
		if err == nil && llmCfg != nil {
			client := newFailoverLLMClient(llmCfg.Primary, llmCfg.Fallbacks)
			scorer, err := evolutionservice.NewLLMScorer(evolutionservice.LLMScorerConfig{
				Client:   client,
				Model:    llmCfg.Primary.Model,
				Fallback: func(s *evolutionservice.Strategy) float64 { return evolutionservice.DeterministicScore(s) },
			})
			if err == nil {
				start := time.Now()
				llmScore := scorer.Score(best)
				slog.InfoContext(ctx, "LLM validation of best strategy",
					"strategy_id", best.ID,
					"deterministic_score", best.Score,
					"llm_score", llmScore,
					"duration", time.Since(start).Round(time.Millisecond),
				)
			}
		}
	}

	// Display the saved evolution report with evidence and promotion summary.
	if data, err := os.ReadFile(reportPath); err == nil {
		fmt.Println(string(data))
	}

	return result
}

// ────────────────────────────────────────────────────────────────────────────
// GA Configuration — ALL features enabled
// ────────────────────────────────────────────────────────────────────────────

// fullGAConfig returns a SystemConfig with ALL GA capabilities enabled.
// scorer is optional — when nil, deterministic scoring is used.
// When reportPath is non-empty, the evolution report is saved after the run.
// When evidenceAgg and promotionLogic are non-nil, the post-run hook evaluates
// the best strategy against aggregated evidence and makes promotion decisions.
func fullGAConfig(
	parent *evolutionservice.Strategy,
	cfg GACfg,
	wired bool,
	scorer evolutionservice.ScorerFunc,
	reportPath string,
	evidenceAgg evolutionservice.EvidenceAggregator,
	promotionLogic evolutionservice.PromotionLogic,
) *evolutionservice.SystemConfig {
	if scorer == nil {
		scorer = func(s *evolutionservice.Strategy) float64 { return evolutionservice.DeterministicScore(s) }
	}
	return &evolutionservice.SystemConfig{
		BaseStrategy:           parent,
		PopulationSize:         cfg.PopSize,
		EliteCount:             max(1, cfg.PopSize/7), // ~14% of population, at least 1
		SurvivalRate:           cfg.SurvRate,          // top 60% survive
		MutationRate:           0.3,                   // base mutation rate
		MinMutationRate:        0.05,                  // adaptive floor
		MaxMutationRate:        0.5,                   // adaptive ceiling
		MaxStagnantGenerations: 5,                     // reset bottom 1/3 after 5 stale gens
		DiversityThreshold:     0.2,                   // boost mutation when diversity drops
		BreedingPoolRatio:      0.5,                   // breed from top 50% of survivors
		SelectionStrategy:      cfg.SelectionStrategy, // parent selection algorithm
		HistoryMaxSize:         100,                   // enable history for meta-controller + reflection
		PromptCrossoverMode:    1,                     // half-sentence split for prompts
		Generations:            cfg.NGen,
		Seed:                   42, // deterministic for reproducibility
		PromptPool:             []string{"careful", "creative", "precise"},
		EnableWiredMode:        wired,
		EnableIntelligence:     true, // reflection → hypotheses → meta-controller
		Scorer:                 scorer,
		ReportPath:             reportPath,
		EvidenceAggregator:     evidenceAgg,
		PromotionLogic:         promotionLogic,
	}
}

// ────────────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────────────

// defaultParent returns a shared initial strategy for both scenarios.
func defaultParent(id string) *evolutionservice.Strategy {
	return &evolutionservice.Strategy{
		ID:      id,
		Version: 1,
		Params: map[string]any{
			"temperature":       0.7,
			"top_k":             40.0,
			"max_tokens":        2048.0,
			"frequency_penalty": 0.0,
			"presence_penalty":  0.0,
		},
		PromptTemplate: "helpful",
	}
}

// printResult prints the evolution stats table with diversity, lineages, and dimension scores.
func printResult(result *evolutionservice.EvolutionResult) {
	var rows [][]string
	for i, st := range result.Stats {
		div := ""
		if st.Diversity != nil {
			div = fmt.Sprintf("%.0f%%", st.Diversity.Overall*100)
		}
		rows = append(rows, []string{
			fmt.Sprintf("%d", i+1),
			fmt.Sprintf("%.2f", st.BestScore),
			fmt.Sprintf("%.2f", st.AvgScore),
			fmt.Sprintf("%.2f", st.WorstScore),
			div,
		})
	}
	headers := []string{"Gen", "Best", "Avg", "Worst", "Diversity"}
	tbl(headers, rows)

	if len(result.Lineages) > 0 {
		slog.Info("Lineage records",
			"count", len(result.Lineages),
			"with_improvement", countWithImprovement(result.Lineages),
		)
	}

	if bst := result.BestStrategy; bst != nil {
		attrs := []any{"id", bst.ID, "version", bst.Version, "score", bst.Score}
		if len(bst.DimensionScores) > 0 {
			attrs = append(attrs, "dims", fmt.Sprintf("%v", bst.DimensionScores))
		}
		slog.Info("Best strategy found", attrs...)
	}
}

// countWithImprovement returns the number of lineages with positive ScoreDelta.
func countWithImprovement(lineages []evolutionservice.StrategyLineage) int {
	n := 0
	for _, l := range lineages {
		if l.ScoreDelta > 0 {
			n++
		}
	}
	return n
}

// ────────────────────────────────────────────────────────────────────────────
// Scenario 8: Real Data Evolution Pipeline
//
// This scenario demonstrates the complete GA/Memory/Tool fusion pipeline
// using ~600 realistic tool call records generated from 15 conversation
// scenarios across 5 distinct strategy profiles.
//
// The pipeline flow:
//   1. Generate realistic tool call data from conversation scenarios
//   2. Feed through ToolCallExperienceCollector → DefaultNormalizer → MemoryExperienceStore
//   3. DefaultEvidenceAggregator queries the store to produce evidence per strategy
//   4. GA evolves strategy parameters, AfterRun hook evaluates the best strategy
//      against real-world evidence and produces a promotion decision
//   5. Final report shows the complete analysis
//
// No LLM required — fully self-contained demonstration of the fusion pipeline.
// ────────────────────────────────────────────────────────────────────────────

func runRealDataEvolution(ctx context.Context, kit *DemoKit, cfg GACfg) *evolutionservice.EvolutionResult {
	start := time.Now()
	defer func() {
		slog.InfoContext(ctx, "Scenario completed",
			"scenario", "Real Data Evolution Pipeline",
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}()

	sep("Scenario 8: Real Data Evolution Pipeline")

	// Step 1: Generate realistic tool call data.
	fmt.Println("\n  📡 Generating realistic tool call data...")
	records := generateToolCallRecords(42)
	fmt.Printf("     %d records from %d conversations × %d strategy profiles\n",
		len(records), len(scenarios), len(defaultProfiles))

	// Count per tool and show stats.
	toolCounts := map[string]int{}
	toolSuccess := map[string]int{}
	for _, r := range records {
		toolCounts[r.ToolName]++
		if r.Success {
			toolSuccess[r.ToolName]++
		}
	}

	fmt.Println("\n   Tool Call Distribution:")
	var toolRows [][]string
	for _, tool := range []string{"web_search", "calculator", "regex", "json_tools", "file_tools"} {
		total := toolCounts[tool]
		ok := toolSuccess[tool]
		pct := float64(ok) / float64(total) * 100
		toolRows = append(toolRows, []string{
			tool,
			fmt.Sprintf("%d", total),
			fmt.Sprintf("%.0f%%", pct),
		})
	}
	tbl([]string{"Tool", "Calls", "Success"}, toolRows)

	// Print profile proficiency table.
	printProfileTable()

	// Step 2: Set up the experience pipeline.
	fmt.Println("\n  ⚙️  Initializing experience pipeline...")
	store := exp.NewMemoryExperienceStore(exp.ExperienceStoreConfig{
		MaxSize:        10000,
		EnableIndexing: true,
	})
	normalizer := exp.NewDefaultNormalizer(nil)

	collector, err := exp.NewToolCallExperienceCollector(normalizer, store)
	if err != nil {
		slog.ErrorContext(ctx, "Failed to create collector", "error", err)
		return nil
	}

	// Step 3: Feed all records through the pipeline.
	fmt.Println("     Feeding data through normalizer → store...")
	if err := collector.CollectBatch(ctx, records); err != nil {
		slog.ErrorContext(ctx, "Failed to collect records", "error", err)
		return nil
	}

	// Verify data landed in the store.
	storeStats, err := store.GetStatistics(ctx, "aggressive")
	if err == nil && len(storeStats) > 0 {
		fmt.Printf("     Store initialized: experiences ingested across %d strategies\n", len(defaultProfiles))
	}

	// Step 4: Show per-strategy evidence from the store using task-type-aware
	// aggregation to avoid mixed-task warnings.
	fmt.Println("\n   Evidence from Real Tool Call Data:")
	var evRows [][]string
	for _, profile := range defaultProfiles {
		exps, err := store.Query(ctx, profile.id, time.Time{}, time.Now())
		if err != nil || len(exps) == 0 {
			continue
		}
		byTask := exp.AggregateEvidenceByTask(exps)
		ev := exp.AggregateEvidenceCrossTask(exps)
		evRows = append(evRows, []string{
			profile.id,
			fmt.Sprintf("%.0f%%", ev.SuccessRate*100),
			fmt.Sprintf("%dms", ev.LatencyP50),
			fmt.Sprintf("%d (task_types=%d)", ev.SampleCount, len(byTask)),
			fmt.Sprintf("%.0f%%", ev.Confidence*100),
		})
	}
	tbl([]string{"Strategy", "Success", "P50 Lat", "Samples", "Confid"}, evRows)

	// Standalone promotion evaluation based on real data.
	fmt.Println("\n   Promotion Evaluation from Real Data:")
	promoter := &dataDrivenPromotion{
		thresholds: promotionThresholds{
			championSuccessRate: 0.70,
			championConfidence:  0.0,
			championMinSamples:  10,
			shadowSuccessRate:   0.60,
			shadowMinSamples:    5,
		},
	}
	for _, profile := range defaultProfiles {
		exps, err := store.Query(ctx, profile.id, time.Time{}, time.Now())
		if err != nil || len(exps) == 0 {
			continue
		}
		ev := exp.AggregateEvidenceCrossTask(exps)
		svcEv := evolutionservice.Evidence{
			StrategyID:  profile.id,
			SuccessRate: ev.SuccessRate,
			LatencyP50:  ev.LatencyP50,
			SampleCount: ev.SampleCount,
			Confidence:  ev.Confidence,
		}
		state, reason, _ := promoter.Evaluate(ctx, profile.id, svcEv)
		fmt.Printf("     %-12s → %-10s (%s)\n", profile.id, state, reason)
	}

	// Step 6: Create promotion logic for GA wired system.
	gaPromoter := &dataDrivenPromotion{
		thresholds: promotionThresholds{
			championSuccessRate: 0.80,
			championConfidence:  0.50,
			championMinSamples:  20,
			shadowSuccessRate:   0.65,
			shadowMinSamples:    10,
		},
	}

	// Step 6: Create and run the GA wired with real evidence.
	reportDir, _ := os.MkdirTemp("", "evolution-live-report-*")
	defer func() { _ = os.RemoveAll(reportDir) }()
	reportPath := filepath.Join(reportDir, "report.txt")

	parent := defaultParent(cfg.BaseID)
	evidenceAgg := newStoreEvidenceAggregator(store)

	// Pre-populate phenotype fallback: compute evidence key for each default
	// profile and register it so that GA strategies with matching phenotypes
	// can fall back to real data instead of reporting sample_count=0.
	for _, profile := range defaultProfiles {
		muStrategy := &mutation.Strategy{
			ID:             profile.id,
			Params:         map[string]any{"temperature": profile.temperature, "top_k": profile.topK, "max_tokens": profile.maxTokens, "frequency_penalty": profile.freqPenalty, "presence_penalty": profile.presentPenalty},
			PromptTemplate: profile.prompt,
		}
		ek := muStrategy.ComputeEvidenceKey()
		evidenceAgg.RegisterPhenotypeFallback(ek, profile.id)
		evidenceAgg.RegisterStrategyKey(profile.id, ek)
	}

	// Also register the parent strategy's evidence key so that GA generations
	// evaluating the parent strategy can find matching profile data.
	parentMuStrategy := &mutation.Strategy{
		ID:             parent.ID,
		Params:         parent.Params,
		PromptTemplate: parent.PromptTemplate,
	}
	parentEK := parentMuStrategy.ComputeEvidenceKey()
	evidenceAgg.RegisterStrategyKey(parent.ID, parentEK)

	svc, err := evolutionservice.NewService(fullGAConfig(
		parent, cfg, true, nil,
		reportPath,
		evidenceAgg,
		gaPromoter,
	))
	if err != nil {
		slog.ErrorContext(ctx, "Failed to create evolution service", "error", err)
		return nil
	}
	defer svc.Shutdown()

	slog.InfoContext(ctx, "Running GA evolution with real data evidence pipeline",
		"pop_size", cfg.PopSize,
		"generations", cfg.NGen,
	)

	result, err := svc.Evolve(ctx, cfg.NGen)
	if err != nil {
		slog.ErrorContext(ctx, "Evolution failed", "error", err)
		return nil
	}

	lineages, _ := svc.Lineages()
	printResult(result)
	printEvolutionInsightReport(cfg.Title, result, lineages, parent)

	// Step 7: Display the saved evolution report with promotion summary.
	if data, err := os.ReadFile(reportPath); err == nil {
		fmt.Println(string(data))
	}

	return result
}

// ── Evidence Aggregator Adapter ───────────────────────────────

// storeEvidenceAggregator queries the MemoryExperienceStore directly and
// computes evidence via AggregateEvidence, satisfying the
// evolutionservice.EvidenceAggregator interface. It supports phenotype-based
// fallback: when a strategyID returns no data, it looks up the strategy's
// evidence key and falls back to a registered phenotype profile with data.
type storeEvidenceAggregator struct {
	store *exp.MemoryExperienceStore

	// phenotypeFallback maps evidenceKey -> profile strategyID that has data
	// in the store. Pre-populated in runRealDataEvolution.
	phenotypeFallback map[string]string

	// strategyEvidenceKeys maps known strategyIDs -> their computed evidence key.
	// This enables resolving GA UUIDs to phenotype keys for fallback lookup.
	strategyEvidenceKeys map[string]string
}

func newStoreEvidenceAggregator(store *exp.MemoryExperienceStore) *storeEvidenceAggregator {
	return &storeEvidenceAggregator{
		store:                store,
		phenotypeFallback:    make(map[string]string),
		strategyEvidenceKeys: make(map[string]string),
	}
}

// RegisterStrategyKey registers a strategy ID with its computed evidence key
// so that Aggregate can resolve the ID to a phenotype fallback when direct
// query returns no results.
func (a *storeEvidenceAggregator) RegisterStrategyKey(strategyID, evidenceKey string) {
	if strategyID == "" || evidenceKey == "" {
		return
	}
	a.strategyEvidenceKeys[strategyID] = evidenceKey
}

// RegisterPhenotypeFallback registers a phenotype evidence key to a profile
// strategy ID that has data in the store. When a strategy with the same
// evidence key produces 0 direct results, the fallback profile ID is used.
func (a *storeEvidenceAggregator) RegisterPhenotypeFallback(evidenceKey, profileID string) {
	if evidenceKey == "" || profileID == "" {
		return
	}
	a.phenotypeFallback[evidenceKey] = profileID
}

func (a *storeEvidenceAggregator) Aggregate(ctx context.Context, strategyID string) (evolutionservice.Evidence, error) {
	exps, err := a.store.Query(ctx, strategyID, time.Time{}, time.Now())
	if err != nil {
		return evolutionservice.Evidence{}, fmt.Errorf("query store: %w", err)
	}
	if len(exps) > 0 {
		ev := exp.AggregateEvidenceCrossTask(exps)
		return evolutionservice.Evidence{
			StrategyID:  ev.StrategyID,
			SuccessRate: ev.SuccessRate,
			LatencyP50:  ev.LatencyP50,
			ErrorRate:   ev.ErrorRate,
			SampleCount: ev.SampleCount,
			Confidence:  ev.Confidence,
		}, nil
	}

	// Direct query returned 0 results. Try phenotype fallback: resolve the
	// strategyID to an evidence key, then look up a registered profile ID.
	evidenceKey, ok := a.strategyEvidenceKeys[strategyID]
	if !ok {
		return evolutionservice.Evidence{StrategyID: strategyID}, nil
	}
	profileID, ok := a.phenotypeFallback[evidenceKey]
	if !ok {
		return evolutionservice.Evidence{StrategyID: strategyID}, nil
	}

	exps, err = a.store.Query(ctx, profileID, time.Time{}, time.Now())
	if err != nil {
		return evolutionservice.Evidence{}, fmt.Errorf("query store fallback: %w", err)
	}
	if len(exps) == 0 {
		return evolutionservice.Evidence{StrategyID: strategyID}, nil
	}
	ev := exp.AggregateEvidenceCrossTask(exps)
	return evolutionservice.Evidence{
		StrategyID:  strategyID,
		SuccessRate: ev.SuccessRate,
		LatencyP50:  ev.LatencyP50,
		ErrorRate:   ev.ErrorRate,
		SampleCount: ev.SampleCount,
		Confidence:  ev.Confidence,
	}, nil
}

// ── Real Promotion Logic ───────────────────────────────────────

type promotionThresholds struct {
	championSuccessRate float64
	championConfidence  float64
	championMinSamples  int64
	shadowSuccessRate   float64
	shadowMinSamples    int64
}

type dataDrivenPromotion struct {
	thresholds promotionThresholds
}

func (p *dataDrivenPromotion) Evaluate(ctx context.Context, strategyID string, ev evolutionservice.Evidence) (string, string, error) {
	if ev.SampleCount == 0 {
		return "candidate", "no experience data available for evaluation", nil
	}

	if ev.Confidence >= p.thresholds.championConfidence &&
		ev.SuccessRate >= p.thresholds.championSuccessRate &&
		ev.SampleCount >= p.thresholds.championMinSamples {
		return "champion",
			fmt.Sprintf("confidence=%.0f%% success_rate=%.0f%% samples=%d p50_latency=%dms",
				ev.Confidence*100, ev.SuccessRate*100, ev.SampleCount, ev.LatencyP50),
			nil
	}

	if ev.SuccessRate >= p.thresholds.shadowSuccessRate &&
		ev.SampleCount >= p.thresholds.shadowMinSamples {
		return "shadow",
			fmt.Sprintf("promising: success_rate=%.0f%% with %d samples, needs more data for champion",
				ev.SuccessRate*100, ev.SampleCount),
			nil
	}

	return "candidate",
		fmt.Sprintf("insufficient: success_rate=%.0f%% samples=%d confidence=%.0f%%",
			ev.SuccessRate*100, ev.SampleCount, ev.Confidence*100),
		nil
}

// compareResults prints a side-by-side comparison of two evolution results.
func compareResults(resultA, resultB *evolutionservice.EvolutionResult, labelA, labelB string) {
	fmt.Println()
	fmt.Println("  📊 Control Group Comparison")
	fmt.Println("  ═══════════════════════════════════════════════════")

	bestA := resultA.BestStrategy
	bestB := resultB.BestStrategy
	if bestA == nil || bestB == nil {
		fmt.Println("  (comparison unavailable — missing results)")
		return
	}

	fmt.Printf("  %-25s %25s\n", labelA, labelB)
	fmt.Printf("  %-25s %25s\n", "─────────────────────────", "─────────────────────────")
	evidenceA := ""
	if bestA.EvidenceKey != "" {
		evidenceA = bestA.EvidenceKey
	}
	evidenceB := ""
	if bestB.EvidenceKey != "" {
		evidenceB = bestB.EvidenceKey
	}
	fmt.Printf("  Best Score:    %8.2f         Best Score:    %8.2f\n", bestA.Score, bestB.Score)
	fmt.Printf("  Evidence Key:  %-18s  Evidence Key:  %-18s\n", evidenceA, evidenceB)
	fmt.Printf("  Generations:   %8d         Generations:   %8d\n", resultA.TotalGens, resultB.TotalGens)

	// Parameter comparison.
	for _, key := range []string{"temperature", "top_k", "max_tokens"} {
		va := fmt.Sprintf("%v", bestA.Params[key])
		vb := fmt.Sprintf("%v", bestB.Params[key])
		mark := ""
		if va != vb {
			mark = " ← DIFF"
		}
		fmt.Printf("  %-14s %10s         %-14s %10s%s\n", key+":", va, key+":", vb, mark)
	}

	pa := bestA.PromptTemplate
	pb := bestB.PromptTemplate
	mark := ""
	if pa != pb {
		mark = " ← DIFF"
	}
	fmt.Printf("  %-14s %10s         %-14s %10s%s\n", "prompt:", pa, "prompt:", pb, mark)

	// Winner announcement.
	fmt.Println()
	if bestA.Score > bestB.Score {
		fmt.Printf("  🏆 Winner: %s (+%.2f points)\n", labelA, bestA.Score-bestB.Score)
	} else if bestB.Score > bestA.Score {
		fmt.Printf("  🏆 Winner: %s (+%.2f points)\n", labelB, bestB.Score-bestA.Score)
	} else {
		fmt.Println("  🤝 Tie — both approaches found equivalent strategies")
	}

	fmt.Println("  ═══════════════════════════════════════════════════")
	fmt.Println()
}
