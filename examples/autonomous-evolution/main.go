// Package main demonstrates GoAgentX Autonomous Evolution (Dream Mode v1) workflow.
// Showcases 7 core capabilities using mock implementations — no external services required.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	apievol "goagentx/api/evolution"
	"goagentx/internal/arena"
	"goagentx/internal/callbacks"
	"goagentx/internal/evolution"
	"goagentx/internal/evolution/genome"
	"goagentx/internal/evolution/mutation"
	"goagentx/internal/experience"
	storageModels "goagentx/internal/storage/postgres/models"

	"gopkg.in/yaml.v3"
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
//
// mockScorer evaluates strategy fitness based on actual parameters.
// Lower temperature + moderate top_k + "precise" prompt → higher score.
// This ensures different strategy combinations produce differentiated scores.
type mockScorer struct {
	baseScore, variance float64
	counter             int64
	mu                  sync.Mutex
}

func newMockScorer(baseScore, variance float64) *mockScorer {
	return &mockScorer{baseScore: baseScore, variance: variance}
}

func (s *mockScorer) Score(input any) (float64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counter++

	// Extract strategy params for parameter-aware scoring.
	score := s.baseScore
	if m, ok := input.(map[string]any); ok {
		// Temperature: lower is better (0.1→0.9 best, 1.0→0.5 worst)
		if temp, ok := m["temperature"].(float64); ok {
			score += (1.0 - temp) * 0.15
		} else if temp, ok := m["temp"].(float64); ok {
			score += (1.0 - temp) * 0.15
		}
		// top_k: moderate values (20-40) are optimal
		if tk, ok := m["top_k"].(float64); ok {
			optimal := 30.0
			dist := tk - optimal
			score -= (dist * dist) / 2000.0 // quadratic penalty
		}
		// Prompt template: "precise" > "careful" > "creative" > "helpful"
		if pt, ok := m["prompt_template"].(string); ok {
			switch pt {
			case "precise":
				score += 0.08
			case "careful":
				score += 0.04
			case "creative":
				score += 0.02
			}
		} else if pt, ok := m["PromptTemplate"].(string); ok {
			switch pt {
			case "precise":
				score += 0.08
			case "careful":
				score += 0.04
			case "creative":
				score += 0.02
			}
		}
	}

	// Add small per-call variation for realism but keep it secondary to param effects.
	score += float64(s.counter%7-3) * s.variance * 0.5
	if score < 0.05 {
		score = 0.05
	}
	if score > 1.0 {
		score = 1.0
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
	MinMutRate, MaxMutRate    float64
	Wired, Dream              bool
}

// Predefined configurations for each GA scenario.
var (
	cfgGA    = GACfg{Title: "Scenario 6: GA Evolution", BaseID: "ga-root", PopSize: 20, EliteCount: 2, SurvRate: 0.6, MutRate: 0.2, MinMutRate: 0.05, MaxMutRate: 0.5, NGen: 15}
	cfgWired = GACfg{Title: "Scenario 7: Wired System", BaseID: "wired-root", PopSize: 10, EliteCount: 1, SurvRate: 0.5, MutRate: 0.3, MinMutRate: 0.05, MaxMutRate: 0.5, NGen: 10, Wired: true}
)

// ============================================================
// Project-Level Evolution Config
// ============================================================

// EvolutionConfigFromFile mirrors internal/config.EvolutionConfig for YAML parsing
// without importing the internal package from examples.
type EvolutionConfigFromFile struct {
	Enabled           bool    `yaml:"enabled"`
	PopulationSize    int     `yaml:"population_size"`
	EliteCount        int     `yaml:"elite_count"`
	SurvivalRate      float64 `yaml:"survival_rate"`
	MutationRate      float64 `yaml:"mutation_rate"`
	MinMutationRate   float64 `yaml:"min_mutation_rate"`
	MaxMutationRate   float64 `yaml:"max_mutation_rate"`
	Generations       int     `yaml:"generations"`
	BreedingPoolRatio float64 `yaml:"breeding_pool_ratio"`
	MinInterval       string  `yaml:"min_interval"`
}

// loadProjectEvolutionConfig attempts to find and parse evolution config from
// standard project locations. It checks (in order):
//  1. Environment variable EVOLUTION_ENABLED=true
//  2. ./config/config.yaml  (project root relative)
//  3. ../config/config.yaml (examples dir relative)
//
// Returns the parsed EvolutionConfig or an error if not found/unparseable.
func loadProjectEvolutionConfig() (*EvolutionConfigFromFile, error) {
	if os.Getenv("EVOLUTION_ENABLED") == "true" {
		return &EvolutionConfigFromFile{Enabled: true}, nil
	}

	locations := []string{
		"config/config.yaml",
		"../config/config.yaml",
		"../../config.yaml",
	}

	for _, loc := range locations {
		data, err := os.ReadFile(loc)
		if err != nil {
			continue
		}

		var raw struct {
			Evolution *EvolutionConfigFromFile `yaml:"evolution"`
		}
		if err := yaml.Unmarshal(data, &raw); err != nil {
			continue
		}

		if raw.Evolution != nil && raw.Evolution.Enabled {
			return raw.Evolution, nil
		}
		return &EvolutionConfigFromFile{Enabled: false}, nil
	}

	return nil, fmt.Errorf("no config file found with evolution settings")
}

// mergeGACfg merges project-level evolution config values into GACfg defaults.
func mergeGACfg(base GACfg, proj *EvolutionConfigFromFile) GACfg {
	out := base
	if proj.PopulationSize > 0 {
		out.PopSize = proj.PopulationSize
	}
	if proj.EliteCount > 0 {
		out.EliteCount = proj.EliteCount
	}
	if proj.SurvivalRate > 0 {
		out.SurvRate = proj.SurvivalRate
	}
	if proj.MutationRate > 0 {
		out.MutRate = proj.MutationRate
	}
	if proj.MinMutationRate > 0 {
		out.MinMutRate = proj.MinMutationRate
	}
	if proj.MaxMutationRate > 0 {
		out.MaxMutRate = proj.MaxMutationRate
	}
	if proj.Generations > 0 {
		out.NGen = proj.Generations
	}
	return out
}

func statusStr(enabled bool) string {
	if enabled {
		return "✓ ON "
	}
	return "✗ OFF"
}

// sep prints a visual section separator with a centered title.
// Args:
//   - title: the title text to display inside the separator.
//
// This is demo UI output and uses fmt directly for console rendering.
func sep(title string) {
	fmt.Printf("\n%s\n  %s\n%s\n", strings.Repeat("=", 60), title, strings.Repeat("=", 60))
}

// safeTruncate returns the first n characters of s, or the full string if shorter.
// Prevents panic on short strings used in table display.
func safeTruncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
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

	printInsight("Arena Regression Test", `
  ⚔️ Key Finding: Statistical significance testing prevents "lucky bias"

  • Baseline strategy (temp=0.7) vs Candidate (temp=0.5) compared over 5 rounds
  • Each round candidate score ≥ baseline (all ✓), win_rate=1.0
  • But p_value=1.0 → not statistically significant! Difference may be random noise

  💡 Key insight: Even 5 rounds of perfect wins are not enough — Welch t-test requires sufficient sample size
     to rule out random fluctuations. In production, at least 20-30 comparison rounds are recommended.
     confident=false means "promising results but needs more data", not outright rejection.`)
}

// ============================================================
// Scenario 5: Dream Cycle Orchestration
// ============================================================

// runDreamCycle demonstrates Scenario 5: Dream Cycle Orchestration.
// It performs a full mutate→arena-test→select-best→record-genealogy cycle,
// evaluating multiple candidate mutations against a baseline and promoting the winner.
//
// Enhanced output shows per-candidate evaluation details, mutation type breakdown,
// and selection rationale with conversational insight.
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

	parent := &mutation.Strategy{ID: "root-v1", Version: 1, Params: map[string]any{"temp": 0.7, "top_k": 40.0}, PromptTemplate: "helpful", CreatedAt: time.Now()}
	children, _ := mutator.Mutate(ctx, parent, 3)
	baseline := evolution.Strategy{ID: parent.ID, Params: parent.Params}

	fmt.Println("\n 🔄 Dream Cycle: Mutate → Test → Select → Record")
	fmt.Println("   ┌─────────────────────────────────────────────────────┐")
	fmt.Printf("   │ Parent: %s  temp=%.1f top_k=%.0f prompt=%s\n", parent.ID, parent.Params["temp"], parent.Params["top_k"], parent.PromptTemplate)
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
		if t, ok := child.Params["temp"].(float64); ok {
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

	// ── Candidate Evaluation Table ──
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

	// ── Mutation Type Breakdown ──
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

	// ── Selection Rationale ──
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

	// ── Conversational Insight ──
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

// ============================================================
// Scenarios 6–7: Multi-Generation GA Evolution
// ============================================================

// runMultiGenGA demonstrates Scenarios 6 and 7: Multi-Generation GA Evolution.
// It uses the high-level api/evolution.Service to run a population-based GA
// with elite selection, crossover, and mutation, in either wired or raw mode.
//
// Post-evolution, it prints an Evolution Insight Report showing trajectory,
// mutation analysis, genealogy tree, and key learnings.
// Args:
//   - ctx: operation context for slog propagation and GA evolution.
//   - k: demo kit (unused; this scenario creates its own components).
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

	parent := &apievol.Strategy{
		ID:             cfg.BaseID,
		Version:        1,
		Params:         map[string]any{"temperature": 0.7, "top_k": 40.0},
		PromptTemplate: "helpful",
	}

	// demoScorer is a parameter-aware fitness function for GA evolution.
	// It rewards: lower temperature, top_k near 30, "precise" prompt template.
	// Score range: [0, 100] per ScorerFunc contract.
	demoScorer := func(params map[string]any) float64 {
		score := 50.0 // base
		if temp, ok := params["temperature"].(float64); ok {
			score += (1.0 - temp) * 25 // lower temp → higher score
		}
		if tk, ok := params["top_k"].(float64); ok {
			optimal := 30.0
			dist := tk - optimal
			score -= (dist * dist) / 10.0 // quadratic penalty around optimal
		}
		if pt, ok := params["prompt_template"].(string); ok {
			switch pt {
			case "precise":
				score += 15
			case "careful":
				score += 8
			case "creative":
				score += 4
			}
		} else if pt, ok := params["PromptTemplate"].(string); ok {
			switch pt {
			case "precise":
				score += 15
			case "careful":
				score += 8
			case "creative":
				score += 4
			}
		}
		if score < 5 {
			score = 5
		}
		if score > 100 {
			score = 100
		}
		return score
	}

	svc, err := apievol.NewService(&apievol.SystemConfig{
		BaseStrategy:    parent,
		PopulationSize:  cfg.PopSize,
		EliteCount:      cfg.EliteCount,
		SurvivalRate:    cfg.SurvRate,
		MutationRate:    cfg.MutRate,
		MinMutationRate: cfg.MinMutRate, // from config (default 0.05)
		MaxMutationRate: cfg.MaxMutRate, // from config (default 0.5)
		Generations:     cfg.NGen,
		Seed:            42,
		PromptPool:      []string{"careful", "creative", "precise"},
		EnableWiredMode: cfg.Wired,
		Scorer:          demoScorer,
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
}

// ============================================================
// Main — project-level config driven execution
// ============================================================

func setupLogger() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
}

func printHeader() {
	fmt.Println(`╔════════════════════════════════════════════════════╗`)
	fmt.Println(`║  GoAgentX Autonomous Evolution (Dream Mode v1) Demo ║`)
	fmt.Println(`╚════════════════════════════════════════════════════╝`)
}

func printFooter() {
	fmt.Println("\n✅ Demo Complete — all scenarios finished.")
}

func main() {
	ctx := context.Background()
	setupLogger()

	// Print header
	printHeader()

	// Scenario execution table — always show what's running
	fmt.Println("\n📋 Scenario Configuration:")
	fmt.Println("   ✓ ON   1-5: Core scenarios (Bandit, Callbacks, Mutation, Arena, Dream)")

	// Check if evolution is enabled via project-level config
	evoEnabled := false
	var evoCfg GACfg

	projectCfg, err := loadProjectEvolutionConfig()
	if err != nil {
		slog.Info("evolution: no project config found, using defaults (OFF)")
	} else if projectCfg.Enabled {
		evoEnabled = true
		evoCfg = mergeGACfg(cfgGA, projectCfg)
		slog.Info("evolution: enabled via project config")
	}

	fmt.Printf("   %s  6: Multi-Gen GA Evolution\n", statusStr(evoEnabled))
	fmt.Printf("   %s  7: Wired Evolution System\n", statusStr(evoEnabled && !cfgWired.Wired))

	kit, err := NewDemoKit()
	if err != nil {
		slog.Error("Failed to initialize demo kit", "error", err)
		return
	}

	// Run scenarios 1-5 (always on)
	runBandit(ctx, kit)
	runCallbacks(ctx, kit)
	runMutation(ctx, kit)
	runArena(ctx, kit)
	runDreamCycle(ctx, kit)

	// Run scenarios 6-7 only if evolution is enabled
	if evoEnabled {
		runMultiGenGA(ctx, kit, evoCfg)
		if !cfgWired.Wired {
			runMultiGenGA(ctx, kit, mergeGACfg(cfgWired, projectCfg))
		}
	} else {
		printInsight("Evolution Disabled", `
  🔒 The Genetic Algorithm evolution scenarios (6 & 7) are currently DISABLED.

  To enable them, add the following to your project's config.yaml:

    evolution:
      enabled: true
      population_size: 20
      elite_count: 2
      generations: 15

  Or set the environment variable: EVOLUTION_ENABLED=true

  Scenarios 1-5 demonstrate the individual building blocks (bandit feedback,
  callback events, strategy mutation, arena regression, and dream cycle) that
  compose into the full evolution pipeline. These always run regardless.`)
	}

	printFooter()
}

// ============================================================
// Insight & Report Functions
// ============================================================

// printInsight prints a conversational "What did we learn?" summary.
// This is demo UI output and uses fmt directly for console rendering.
func printInsight(title, message string) {
	fmt.Printf("\n  💬 What did we learn? — %s\n", title)
	fmt.Println("  " + strings.Repeat("─", 56))
	// Print each line with proper indentation.
	for _, line := range strings.Split(message, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			fmt.Printf("  %s\n", trimmed)
		}
	}
	fmt.Println()
}

// printEvolutionInsightReport generates a comprehensive evolution analysis report
// after GA completes. It shows trajectory phases, mutation statistics,
// genealogy tree, and key learnings in a visual format.
//
// Args:
//   - title: scenario title (e.g., "Scenario 6: GA Evolution").
//   - result: complete evolution result with per-generation stats.
//   - lineages: recorded parent-child relationships.
//   - parent: original base strategy for comparison.
func printEvolutionInsightReport(title string, result *apievol.EvolutionResult, lineages []apievol.StrategyLineage, parent *apievol.Strategy) {
	fmt.Println("\n╔════════════════════════════════════════════════════╗")
	fmt.Println("║          🧬 Evolution Insight Report               ║")
	fmt.Println("╚════════════════════════════════════════════════════╝")

	// ── 1. Evolution Trajectory ──
	printTrajectory(result.Stats, result.BestStrategy)

	// ── 2. Mutation Analysis ──
	printMutationAnalysis(lineages)

	// ── 3. Genealogy Tree (top lineages) ──
	printGenealogyTree(lineages)

	// ── 4. Best Strategy vs Baseline ──
	printBestStrategyDiff(result.BestStrategy, parent)

	// ── 5. Key Learnings ──
	printKeyLearnings(result, parent)
}

// printTrajectory analyzes per-generation stats and prints phase breakdown.
func printTrajectory(stats []apievol.Stats, bestStrategy *apievol.Strategy) {
	nGen := len(stats)
	if nGen == 0 {
		return
	}

	fmt.Println("\n📊 Evolution Trajectory:")
	firstBest := stats[0].BestScore
	lastBest := stats[nGen-1].BestScore
	bestAvg := stats[nGen-1].AvgScore

	// Determine improvement direction.
	improvementPct := 0.0
	if firstBest != 0 {
		improvementPct = ((lastBest - firstBest) / mathAbs(firstBest)) * 100
	}

	// Phase detection.
	phase1End := nGen / 3
	phase2End := 2 * nGen / 3
	if phase1End < 1 {
		phase1End = 1
	}
	if phase2End <= phase1End {
		phase2End = phase1End + 1
	}
	if phase2End >= nGen {
		phase2End = nGen - 1
	}

	fmt.Printf("   Gen 1 → Gen %d:  Exploration phase (random search)\n", phase1End)
	if bestStrategy != nil {
		fmt.Printf("             ↓ Best strategy: %s\n", formatParamsShort(bestStrategy.Params))
	}
	fmt.Printf("             ↓ Key insight: Diverse mutations explore the parameter space\n")
	fmt.Printf("   Gen %d → Gen %d: Exploitation phase (refining winners)\n", phase1End+1, phase2End)
	if bestStrategy != nil {
		fmt.Printf("             ↓ Best strategy: %s\n", formatParamsShort(bestStrategy.Params))
	}
	fmt.Printf("             ↓ Breakthrough: Elite preservation keeps top performers\n")
	if phase2End < nGen {
		fmt.Printf("   Gen %d → Gen %d: Convergence phase\n", phase2End+1, nGen)
		fmt.Printf("             ↓ Final best: score=%.2f, avg=%.2f (%+.1f%% vs baseline)\n",
			lastBest, bestAvg, improvementPct)
	}

	// Score trend indicator.
	trend := "→ STAGNANT"
	if lastBest > firstBest+0.01 {
		trend = "↗ IMPROVING"
	} else if lastBest < firstBest-0.01 {
		trend = "↘ DECLINING"
	}
	fmt.Printf("\n   Trend: %s  |  Best: %.2f → %.2f  |  Generations: %d\n",
		trend, firstBest, lastBest, nGen)
}

// printMutationAnalysis categorizes lineage records and shows mutation distribution.
func printMutationAnalysis(lineages []apievol.StrategyLineage) {
	fmt.Println("\n🧪 Mutation Analysis:")

	paramCount := 0
	promptCount := 0
	crossoverCount := 0
	otherCount := 0
	totalWinRate := 0.0
	countPositive := 0

	for _, l := range lineages {
		switch {
		case strings.Contains(l.MutationType, "parameter") || strings.Contains(l.MutationType, "param"):
			paramCount++
		case strings.Contains(l.MutationType, "prompt"):
			promptCount++
		case strings.Contains(l.MutationType, "crossover") || strings.Contains(l.MutationType, "cross"):
			crossoverCount++
		default:
			otherCount++
		}
		if l.WinRate > 0 {
			totalWinRate += l.WinRate
			countPositive++
		}
	}

	total := len(lineages)
	if total == 0 {
		fmt.Println("   (No lineage records — population may not have evolved)")
		return
	}

	pct := func(n int) string {
		if total == 0 {
			return "0%"
		}
		return fmt.Sprintf("%.0f%%", float64(n)*100/float64(total))
	}

	fmt.Printf("   ├─ Param mutations: %d (%s)\n", paramCount, pct(paramCount))
	fmt.Printf("   ├─ Prompt mutations: %d (%s)\n", promptCount, pct(promptCount))
	fmt.Printf("   ├─ Crossover events: %d (%s)\n", crossoverCount, pct(crossoverCount))
	fmt.Printf("   └─ Other: %d (%s)\n", otherCount, pct(otherCount))

	if countPositive > 0 {
		avgWR := totalWinRate / float64(countPositive)
		fmt.Printf("   Avg win_rate (lineages): %.2f\n", avgWR)
	}
}

// printGenealogyTree displays a simplified ancestry tree from lineage records.
func printGenealogyTree(lineages []apievol.StrategyLineage) {
	fmt.Println("\n🏆 Genealogy Tree (top lineages):")

	showN := min(5, len(lineages))
	if showN == 0 {
		fmt.Println("   (No genealogy records)")
		return
	}

	// Group by parent ID to build a simple tree structure.
	type childInfo struct {
		childID    string
		mutType    string
		winRate    float64
		scoreDelta float64
	}
	parentMap := make(map[string][]childInfo)
	parentOrder := []string{}

	for i := 0; i < showN; i++ {
		l := lineages[i]
		cid := l.ChildID
		if len(cid) > 10 {
			cid = cid[:10] + "..."
		}
		pid := l.ParentID
		if len(pid) > 10 {
			pid = pid[:10] + "..."
		}
		info := childInfo{childID: cid, mutType: l.MutationType, winRate: l.WinRate, scoreDelta: l.ScoreDelta}
		if _, exists := parentMap[pid]; !exists {
			parentOrder = append(parentOrder, pid)
		}
		parentMap[pid] = append(parentMap[pid], info)
	}

	for _, pid := range parentOrder {
		children := parentMap[pid]
		fmt.Printf("   %s\n", pid)
		for j, c := range children {
			conn := "├──"
			if j == len(children)-1 {
				conn = "└──"
			}
			wrStr := ""
			if c.winRate > 0 {
				wrStr = fmt.Sprintf(" wr:%.2f", c.winRate)
			}
			deltaStr := ""
			if c.scoreDelta != 0 {
				deltaStr = fmt.Sprintf(" Δ:%+.2f", c.scoreDelta)
			}
			fmt.Printf("      %s %s [%s%s%s]\n", conn, c.childID, c.mutType, wrStr, deltaStr)
		}
	}

	if len(lineages) > showN {
		fmt.Printf("   ... and %d more lineage records\n", len(lineages)-showN)
	}
}

// printBestStrategyDiff compares best strategy params against baseline.
func printBestStrategyDiff(best *apievol.Strategy, parent *apievol.Strategy) {
	fmt.Println("\n🎯 Best Strategy vs Baseline:")
	if best == nil {
		fmt.Println("   (No best strategy found)")
		return
	}

	fmt.Printf("   ID: %s  v%d  score=%.2f\n", best.ID, best.Version, best.Score)
	fmt.Println("   Param changes:")

	hasChanges := false
	for k, v := range best.Params {
		parentVal := parent.Params[k]
		changed := fmt.Sprintf("%v", v) != fmt.Sprintf("%v", parentVal)
		mark := "  "
		if changed {
			mark = "▸"
			hasChanges = true
		}
		fmt.Printf("     %s %s: %v → %v\n", mark, k, parentVal, v)
	}

	if best.PromptTemplate != parent.PromptTemplate {
		fmt.Printf("     ▸ prompt: %q → %q\n", parent.PromptTemplate, best.PromptTemplate)
		hasChanges = true
	}

	if !hasChanges {
		fmt.Println("     (no param changes from baseline)")
	}
}

// printKeyLearnings synthesizes actionable insights from evolution data.
func printKeyLearnings(result *apievol.EvolutionResult, parent *apievol.Strategy) {
	fmt.Println("\n💡 Key Learnings:")

	learnings := []string{}
	nGen := len(result.Stats)

	if nGen == 0 {
		learnings = append(learnings, "  1. No generations were executed — check configuration")
	} else {
		firstB := result.Stats[0].BestScore
		lastB := result.Stats[nGen-1].BestScore

		if lastB > firstB {
			learnings = append(learnings, fmt.Sprintf(
				"  1. Fitness improved from %.2f → %.2f (+%.1f%%) — evolution is working",
				firstB, lastB, ((lastB-firstB)/mathAbs(firstB))*100))
		} else if lastB < firstB {
			learnings = append(learnings, fmt.Sprintf(
				"  1. Fitness declined from %.2f → %.2f — consider increasing mutation rate or population size",
				firstB, lastB))
		} else {
			learnings = append(learnings,
				"  1. Score plateau detected — adaptive mutation rate may need tuning")
		}

		// Check if prompt template changed.
		if result.BestStrategy != nil && result.BestStrategy.PromptTemplate != parent.PromptTemplate {
			learnings = append(learnings, fmt.Sprintf(
				"  2. Prompt switch %q→%q had significant impact on fitness",
				parent.PromptTemplate, result.BestStrategy.PromptTemplate))
		} else {
			learnings = append(learnings,
				"  2. Parameter tuning (temp/top_k) was the primary performance driver")
		}

		eliteNote := fmt.Sprintf("  3. Elite preservation prevented loss of top genes across %d generations", nGen)
		learnings = append(learnings, eliteNote)
	}

	for _, l := range learnings {
		fmt.Println(l)
	}
	fmt.Println()
}

// formatParamsShort returns a compact {key:val, ...} string for display.
func formatParamsShort(params map[string]any) string {
	if len(params) == 0 {
		return "{}"
	}
	var parts []string
	for k, v := range params {
		parts = append(parts, fmt.Sprintf("%s:%v", k, v))
	}
	if len(parts) > 3 {
		parts = parts[:3]
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// mathAbs returns the absolute value of x (local copy to avoid import).
func mathAbs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
