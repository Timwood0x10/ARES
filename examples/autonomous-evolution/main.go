// Package main demonstrates GoAgentX Autonomous Evolution (Dream Mode v1) workflow.
// Showcases 7 core capabilities using mock implementations — no external services required.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"strings"
	"sync"
	"time"

	"goagentx/internal/arena"
	"goagentx/internal/callbacks"
	"goagentx/internal/evolution"
	"goagentx/internal/evolution/genome"
	"goagentx/internal/evolution/mutation"
	"goagentx/internal/experience"
	storageModels "goagentx/internal/storage/postgres/models"
)

// ============================================================
// DemoKit — consolidated mock factory
// ============================================================

// DemoKit provides all mock components required by demo scenarios.
type DemoKit struct {
	Repo      *mockExperienceRepo
	Scorer    *mockScorer
	Genealogy *mockGenealogyRecorder
	Tester    *mockTester
	Mutator   *mutation.Mutator
	Crosser   *genome.Crossover
}

// NewDemoKit creates a fully initialized DemoKit with all mock dependencies.
// Args:
//   - None.
//
// Returns:
//   - *DemoKit: fully initialized demo kit with repo, scorer, genealogy, tester, mutator, and crosser.
//   - error: non-nil if any constructor fails, preventing nil-pointer panics downstream.
func NewDemoKit() (*DemoKit, error) {
	repo := newMockExperienceRepo()
	sc := newMockScorer(0.78, 0.04)

	tt, err := newMockTester(sc)
	if err != nil {
		return nil, fmt.Errorf("create tester: %w", err)
	}

	mu, err := mutation.NewMutator(mutation.WithSeed(42))
	if err != nil {
		return nil, fmt.Errorf("create mutator: %w", err)
	}

	cx, err := genome.NewCrossover(genome.WithSeed(42))
	if err != nil {
		return nil, fmt.Errorf("create crossover: %w", err)
	}

	return &DemoKit{
		Repo:      repo,
		Scorer:    sc,
		Genealogy: newMockGenealogyRecorder(),
		Tester:    tt,
		Mutator:   mu,
		Crosser:   cx,
	}, nil
}

// ============================================================
// Mock Implementations
// ============================================================

type mockExperienceRecord struct {
	id, input, output string
	score             float64
}

type mockExperienceRepo struct {
	mu    sync.RWMutex
	data  map[string]*mockExperienceRecord
	usage map[string]int
	ranks map[string]float64
}

func newMockExperienceRepo() *mockExperienceRepo {
	return &mockExperienceRepo{
		data:  make(map[string]*mockExperienceRecord),
		usage: make(map[string]int),
		ranks: make(map[string]float64),
	}
}

func (r *mockExperienceRepo) Create(_ context.Context, e *storageModels.Experience) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[e.ID] = &mockExperienceRecord{id: e.ID, input: e.Input, output: e.Output, score: e.Score}
	r.ranks[e.ID] = e.Score
	return nil
}

func (r *mockExperienceRepo) GetByID(_ context.Context, id string) (*storageModels.Experience, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.data[id]
	if !ok {
		return nil, fmt.Errorf("experience not found: %s", id)
	}
	return &storageModels.Experience{ID: rec.id, Input: rec.input, Output: rec.output, Score: rec.score}, nil
}

func (r *mockExperienceRepo) Update(_ context.Context, e *storageModels.Experience) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.data[e.ID]; !ok {
		return fmt.Errorf("experience not found: %s", e.ID)
	}
	r.data[e.ID] = &mockExperienceRecord{id: e.ID, input: e.Input, output: e.Output, score: e.Score}
	return nil
}

func (r *mockExperienceRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.data, id)
	delete(r.usage, id)
	delete(r.ranks, id)
	return nil
}

func (r *mockExperienceRepo) SearchByVector(_ context.Context, _ []float64, _ string, limit int) ([]*storageModels.Experience, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*storageModels.Experience, 0, limit)
	for _, rec := range r.data {
		if len(out) >= limit {
			break
		}
		out = append(out, &storageModels.Experience{ID: rec.id, Input: rec.input, Output: rec.output, Score: rec.score})
	}
	return out, nil
}

func (r *mockExperienceRepo) SearchByKeyword(c context.Context, q, s string, n int) ([]*storageModels.Experience, error) {
	return r.SearchByVector(c, nil, s, n)
}

func (r *mockExperienceRepo) IncrementUsageCount(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.usage[id]++
	return nil
}

func (r *mockExperienceRepo) DecrementRank(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ranks[id] *= 0.9
	return nil
}

func (r *mockExperienceRepo) ListByType(c context.Context, t, s string, n int) ([]*storageModels.Experience, error) {
	return r.SearchByVector(c, nil, s, n)
}

func (r *mockExperienceRepo) ListByAgent(c context.Context, a, s string, n int) ([]*storageModels.Experience, error) {
	return r.SearchByVector(c, nil, s, n)
}

func (r *mockExperienceRepo) getRank(id string) float64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.ranks[id]
}

func (r *mockExperienceRepo) getUsageCount(id string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.usage[id]
}

// ---- mockScorer ----

type mockScorer struct {
	baseScore, variance float64
	counter             int64
	mu                  sync.Mutex
}

func newMockScorer(baseScore, variance float64) *mockScorer {
	return &mockScorer{baseScore: baseScore, variance: variance}
}

func (s *mockScorer) Score(_ any) (float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counter++
	score := s.baseScore + float64(s.counter%5-2)*s.variance
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}
	return score, nil
}

// ---- mockGenealogyRecorder ----

type mockGenealogyRecorder struct {
	mu       sync.Mutex
	lineages []evolution.StrategyLineage
}

func newMockGenealogyRecorder() *mockGenealogyRecorder {
	return &mockGenealogyRecorder{}
}

func (g *mockGenealogyRecorder) Record(_ context.Context, l evolution.StrategyLineage) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.lineages = append(g.lineages, l)
	return nil
}

// ---- mockTester ----

type mockTester struct{ rt *arena.RegressionTester }

func newMockTester(scorer arena.Scorer) (*mockTester, error) {
	arenaSvc := arena.NewService(nil, nil)
	rt, err := arena.NewRegressionTester(arenaSvc, scorer)
	if err != nil {
		return nil, fmt.Errorf("create regression tester: %w", err)
	}
	return &mockTester{rt: rt}, nil
}

func (t *mockTester) Run(ctx context.Context, cfg evolution.RegressionConfig) (*evolution.RegressionResult, error) {
	r, err := t.rt.Run(ctx, arena.RegressionConfig{
		OldStrategy:  cfg.Baseline,
		NewStrategy:  cfg.Candidate,
		BaselineRuns: 5,
		CompareRuns:  5,
		TestSuite:    "test",
	})
	if err != nil {
		return nil, err
	}
	return &evolution.RegressionResult{
		CandidateScore: r.NewAvg,
		BaselineScore:  r.OldAvg,
		WinRate:        r.WinRate,
		TotalTasks:     r.Samples,
	}, nil
}

// ---- compositeScorer ----

type compositeScorerImpl struct {
	baselineScorer  arena.Scorer
	candidateScorer arena.Scorer
}

func (c *compositeScorerImpl) Score(input any) (float64, error) {
	m, ok := input.(map[string]any)
	if ok && m["id"] == "baseline-v1" {
		return c.baselineScorer.Score(input)
	}
	return c.candidateScorer.Score(input)
}

// ============================================================
// Abstractions
// ============================================================

// Scenario defines a single demo scenario with a name and runner function.
type Scenario struct {
	Name string
	Run  func(context.Context, *DemoKit)
}

// GACfg configures a genetic-algorithm evolution demo run.
//
// Mode selection (mutually exclusive):
//   - Dream=true  → Scenario 5: single Dream Cycle (mutate→test→select→record)
//   - Wired=true  → Scenario 7: WiredEvolutionSystem high-level API
//   - both false  → Scenario 6: standard Population-based multi-gen GA
type GACfg struct {
	Title                     string
	BaseID                    string
	PopSize, EliteCount, NGen int
	SurvRate, MutRate         float64
	Wired, Dream              bool
}

// Predefined configurations for each GA scenario.
var (
	cfgGA    = GACfg{Title: "Scenario 6: GA Evolution", BaseID: "ga-root", PopSize: 20, EliteCount: 2, SurvRate: 0.6, MutRate: 0.2, NGen: 15}
	cfgWired = GACfg{Title: "Scenario 7: Wired System", BaseID: "wired-root", PopSize: 10, EliteCount: 1, SurvRate: 0.5, MutRate: 0.3, NGen: 10, Wired: true}
)

// sep prints a visual section separator with a centered title.
// Args:
//   - title: the title text to display inside the separator.
//
// This is demo UI output and uses fmt directly for console rendering.
func sep(title string) {
	fmt.Printf("\n%s\n  %s\n%s\n", strings.Repeat("=", 60), title, strings.Repeat("=", 60))
}

// tbl prints a formatted table with the given header and rows to the console.
// Args:
//   - hdr: column header names.
//   - rows: table data rows, each a slice of cell strings.
//
// This is demo UI output and uses fmt directly for console rendering.
func tbl(hdr []string, rows [][]string) {
	widths := make([]int, len(hdr))
	for i, h := range hdr {
		widths[i] = len(h)
	}
	for _, r := range rows {
		for i, c := range r {
			if len(c) > widths[i] {
				widths[i] = len(c)
			}
		}
	}
	format := ""
	for _, w := range widths {
		format += fmt.Sprintf(" %%-%ds", w)
	}
	format += "\n"

	toAny := func(ss []string) []any {
		a := make([]any, len(ss))
		for i, s := range ss {
			a[i] = s
		}
		return a
	}

	sepRow := make([]string, len(widths))
	for i, w := range widths {
		sepRow[i] = strings.Repeat("-", w)
	}
	fmt.Printf(format, toAny(hdr)...)
	fmt.Println(strings.Join(sepRow, " "))
	for _, r := range rows {
		fmt.Printf(format, toAny(r)...)
	}
}

// ============================================================
// Scenarios 1–4
// ============================================================

// runBandit demonstrates Scenario 1: Bandit Feedback Loop.
// It creates experiences, simulates task executions with success/failure tracking,
// and shows how usage counts reinforce reliability while failures decay rank.
// Args:
//   - ctx: operation context for slog propagation.
//   - k: demo kit providing mock repository and services.
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
}

// runCallbacks demonstrates Scenario 2: Callback Event System.
// It registers handlers on multiple event types, emits events, and verifies
// that pub/sub dispatches correctly with panic safety and rich metadata.
// Args:
//   - ctx: operation context for slog propagation.
//   - k: demo kit (unused in this scenario but required by Scenario interface).
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
}

// runMutation demonstrates Scenario 3: Strategy Mutation Engine.
// It mutates a parent strategy into child strategies, showing deterministic
// reproducibility with the same seed and the ratio of param vs prompt mutations.
// Args:
//   - ctx: operation context for slog propagation and mutation execution.
//   - k: demo kit (unused; this scenario creates its own mutator).
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
}

// runArena demonstrates Scenario 4: Arena Regression Test.
// It runs a Welch t-test between baseline and candidate strategies,
// showing per-run comparisons and statistical significance determination.
// Args:
//   - ctx: operation context for slog propagation and arena test execution.
//   - k: demo kit (unused; this scenario creates its own tester and scorers).
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
	bs, cs := newMockScorer(0.72, 0.05), newMockScorer(0.81, 0.06)
	comp := &compositeScorerImpl{bs, cs}

	rt, _ := arena.NewRegressionTester(arena.NewService(nil, nil), comp)
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
}

// ============================================================
// Scenario 5: Dream Cycle Orchestration
// ============================================================

// runDreamCycle demonstrates Scenario 5: Dream Cycle Orchestration.
// It performs a full mutate→arena-test→select-best→record-genealogy cycle,
// evaluating multiple candidate mutations against a baseline and promoting the winner.
// Args:
//   - ctx: operation context for slog propagation and dream cycle operations.
//   - k: demo kit (unused; this scenario creates its own components).
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
	scorer := newMockScorer(0.78, 0.04)
	tester, _ := newMockTester(scorer)
	genealogy := newMockGenealogyRecorder()

	parent := &mutation.Strategy{ID: "root-v1", Version: 1, Params: map[string]any{"temp": 0.7, "top_k": 40}, PromptTemplate: "helpful", CreatedAt: time.Now()}
	children, _ := mutator.Mutate(ctx, parent, 3)
	baseline := evolution.Strategy{ID: parent.ID, Params: parent.Params}

	var best *evolution.Strategy
	var bestWinRate, bestImprovement float64

	for i, child := range children {
		candidate := evolution.Strategy{ID: child.ID, Params: child.Params, ParentID: child.ParentID}
		result, _ := tester.Run(ctx, evolution.RegressionConfig{Candidate: candidate, Baseline: baseline})

		improvement := result.CandidateScore - result.BaselineScore
		passed := result.WinRate >= 0.55
		slog.DebugContext(ctx, "Candidate evaluated",
			"index", i+1,
			"win_rate", result.WinRate,
			"improvement", improvement,
			"passed", passed,
		)

		if passed && (best == nil || improvement > bestImprovement) {
			best = &candidate
			bestWinRate = result.WinRate
			bestImprovement = improvement
		}
	}

	if best != nil {
		_ = genealogy.Record(ctx, evolution.StrategyLineage{
			ParentID: baseline.ID, ChildID: best.ID, MutationType: "dream_cycle", WinRate: bestWinRate,
		})
		slog.InfoContext(ctx, "Best lineage recorded",
			"parent_id", baseline.ID,
			"child_id", best.ID[:12],
			"win_rate", bestWinRate,
		)
	}
	slog.InfoContext(ctx, "Dream cycle pipeline",
		"note", "Mutate -> ArenaTest -> SelectBest -> Genealogy",
	)
}

// ============================================================
// Scenarios 6–7: Multi-Generation GA Evolution
// ============================================================

// runMultiGenGA demonstrates Scenarios 6 and 7: Multi-Generation GA Evolution.
// It runs a population-based genetic algorithm over multiple generations with
// elite selection, crossover, and mutation. In Wired mode it uses the high-level
// WiredEvolutionSystem API; otherwise it uses the genome package directly.
// Args:
//   - ctx: operation context for slog propagation and GA evolution.
//   - k: demo kit (unused; this scenario creates its own population/mutator).
//   - cfg: GA configuration specifying population size, generations, rates, and mode.
func runMultiGenGA(ctx context.Context, _ *DemoKit, cfg GACfg) {
	start := time.Now()
	defer func() {
		slog.InfoContext(ctx, "Scenario completed",
			"scenario", cfg.Title,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	}()

	sep(cfg.Title)

	parent := &mutation.Strategy{ID: cfg.BaseID, Version: 1, Params: map[string]any{"temp": 0.7, "top_k": 40}, PromptTemplate: "helpful", CreatedAt: time.Now()}
	pool := []string{"careful", "creative", "precise"}

	rawMut, _ := mutation.NewMutator(mutation.WithPromptPool(pool), mutation.WithSeed(42))

	var pop *genome.Population
	var mut genome.MutatorInterface = rawMut
	var ws *evolution.WiredEvolutionSystem

	if cfg.Wired {
		sysCfg := evolution.DefaultSystemConfig()
		sysCfg.PopulationSize = cfg.PopSize
		sysCfg.EliteCount = cfg.EliteCount
		sysCfg.SurvivalRate = cfg.SurvRate
		sysCfg.MutationRate = cfg.MutRate

		var err error
		ws, err = evolution.NewWiredEvolutionSystem(parent, sysCfg)
		if err != nil {
			slog.ErrorContext(ctx, "Failed to create wired system", "error", err)
			return
		}
		defer evolution.Shutdown(ws)
		pop = ws.Population

		gm, _ := evolution.NewGenomeMutatorAdapter(rawMut)
		mut = gm
	} else {
		var err error
		pop, err = genome.NewPopulation(ctx, parent, mut,
			genome.WithPopulationSize(cfg.PopSize),
			genome.WithEliteCount(cfg.EliteCount),
			genome.WithMutationRate(cfg.MutRate),
			genome.WithSurvivalRate(cfg.SurvRate),
		)
		if err != nil {
			slog.ErrorContext(ctx, "Failed to create population", "error", err)
			return
		}
	}

	cx, _ := genome.NewCrossover(genome.WithSeed(42))
	rng := rand.New(rand.NewSource(99))

	slog.InfoContext(ctx, "GA configuration",
		"pop_size", cfg.PopSize,
		"elite_count", cfg.EliteCount,
		"survival_rate", cfg.SurvRate,
		"mutation_rate", cfg.MutRate,
		"generations", cfg.NGen,
		"wired_mode", cfg.Wired,
	)

	var rows [][]string
	for gen := 1; gen <= cfg.NGen; gen++ {
		scorePop(pop, rng)
		_ = pop.EvolveOnIdle(ctx, mut, cx)

		if cfg.Wired && gen > 1 {
			_, _ = evolution.RecordPopulationLineage(ctx, pop, ws.Genealogy, gen-1)
		}

		st := pop.Stats()
		lineageCount := 0
		if cfg.Wired {
			lineageCount = ws.Genealogy.Count()
		}
		rows = append(rows, []string{
			fmt.Sprintf("%d", gen),
			fmt.Sprintf("%.2f", st.BestScore),
			fmt.Sprintf("%.2f", st.AvgScore),
			fmt.Sprintf("%.2f", st.WorstScore),
			fmt.Sprintf("%d", lineageCount),
		})

		slog.InfoContext(ctx, "Generation completed",
			"generation", gen,
			"best_score", st.BestScore,
			"avg_score", st.AvgScore,
			"worst_score", st.WorstScore,
			"lineage_count", lineageCount,
		)
	}
	tbl([]string{"Gen", "Best", "Avg", "Worst", "Lin"}, rows)

	if bst := pop.BestStrategy(); bst != nil {
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

	if cfg.Wired {
		lines := ws.Genealogy.Lineages()
		slog.InfoContext(ctx, "Genealogy records", "count", len(lines))
		for i := 0; i < len(lines) && i < 5; i++ {
			e := lines[i]
			cid := e.ChildID
			if len(cid) > 12 {
				cid = cid[:12]
			}
			slog.DebugContext(ctx, "Genealogy entry",
				"index", i,
				"parent_id", e.ParentID[:12],
				"child_id", cid,
				"mutation_type", e.MutationType,
			)
		}
	}
	slog.InfoContext(ctx, "GA evolution summary",
		"note", "Elite + crossover + mutation = adaptive strategies",
	)
}

// scorePop assigns fitness scores to all agents in the population based on
// temperature proximity to the optimal value (0.7) plus random noise.
// Args:
//   - pop: the population whose agents will be scored.
//   - rng: random number generator for stochastic fitness variation.
func scorePop(pop *genome.Population, rng *rand.Rand) {
	agents, _ := pop.Snapshot()
	for _, agent := range agents {
		temp := 0.7
		if v, ok := agent.Params["temperature"].(float64); ok {
			temp = v
		}
		proximity := 1 - abs(temp-0.7)*2.5
		agent.Score = 50 + rng.Float64()*30 + proximity*20
		if agent.Score > 100 {
			agent.Score = 100
		}
		if agent.Score < 0 {
			agent.Score = 0
		}
	}
}

// abs returns the absolute value of a float64.
// Args:
//   - x: the input value.
//
// Returns the non-negative absolute value of x.
func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// ============================================================
// Main — table-driven execution
// ============================================================

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	fmt.Println(`╔════════════════════════════════════════════════════╗`)
	fmt.Println(`║  GoAgentX Autonomous Evolution (Dream Mode v1) Demo ║`)
	fmt.Println(`╚════════════════════════════════════════════════════╝`)

	kit, err := NewDemoKit()
	if err != nil {
		slog.Error("Failed to initialize demo kit", "error", err)
		return
	}

	scenarios := []Scenario{
		{"1: Bandit Feedback Loop", runBandit},
		{"2: Callback Event System", runCallbacks},
		{"3: Strategy Mutation Engine", runMutation},
		{"4: Arena Regression Test", runArena},
		{"5: Dream Cycle Orchestration", runDreamCycle},
		{"6: Multi-Gen GA Evolution", func(ctx context.Context, kit *DemoKit) { runMultiGenGA(ctx, kit, cfgGA) }},
		{"7: Wired Evolution System", func(ctx context.Context, kit *DemoKit) { runMultiGenGA(ctx, kit, cfgWired) }},
	}

	ctx := context.Background()
	for _, s := range scenarios {
		sep(s.Name)
		s.Run(ctx, kit)
	}

	sep("Demo Complete")
	fmt.Println("All 7 scenarios done! Bandit->Callbacks->Mutation->Arena->DreamCycle->GA->WiredSystem")
}
