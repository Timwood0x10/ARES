// Example: Evolution & Experience Data → JSONL Training Pipeline
//
// This program runs real ARES evolution and experience distillation components,
// then exports the results as JSONL ready for mlx-lm LoRA fine-tuning.
//
// Data sources:
//   1. Real evolution pipeline (WiredEvolutionSystem + RunIdleEvolution)
//      → Population.Snapshot() → top-K strategies → train_data.jsonl
//   2. Real experience distillation (TaskResults → DistillationService)
//      → Experience objects → experience_data.jsonl
//
// Output:
//   ./examples/finetune/train_data.jsonl       (text format)
//   ./examples/finetune/train_data_chat.jsonl   (chat format)
//   ./examples/finetune/experience_data.jsonl   (chat format)
//   ./examples/finetune/experience_text_data.jsonl (text format)
//
// Usage:
//   go run examples/finetune/main.go
//
// Integrated usage (after end-to-end evolution):
//   ExportPopulationToJSONL(pop, "./train_data.jsonl", 20, true)

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	evolution "github.com/Timwood0x10/ares/internal/ares_evolution"
	"github.com/Timwood0x10/ares/internal/ares_evolution/genome"
	"github.com/Timwood0x10/ares/internal/ares_evolution/mutation"
	experience "github.com/Timwood0x10/ares/internal/ares_experience"
	"github.com/Timwood0x10/ares/internal/config"
	"github.com/Timwood0x10/ares/internal/llm"
	"github.com/Timwood0x10/ares/internal/ratelimit"
)

// ──────────────────────────────────────────────
// JSONL output format (standard mlx-lm formats)
// ──────────────────────────────────────────────

// TextExample is the simplest mlx-lm format: pure text completion.
// Field: {"text": "<full text>"}
type TextExample struct {
	Text string `json:"text"`
}

// ChatExample is the instruction-tuning format.
// Field: {"messages": [{"role": "user", "content": "..."}, {"role": "assistant", "content": "..."}]}
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatExample struct {
	Messages []ChatMessage `json:"messages"`
}

// ──────────────────────────────────────────────
// Data pipeline: Strategy → JSONL
// ──────────────────────────────────────────────

func paramSummary(s *mutation.Strategy) string {
	b, _ := json.Marshal(s.Params)
	return string(b)
}

// strategyToTextExample converts one strategy into a self-contained training text.
func strategyToTextExample(s *mutation.Strategy) TextExample {
	text := fmt.Sprintf(
		"Generate an optimized agent strategy configuration.\n\n"+
			"Parameters: %s\n"+
			"Prompt template: %s\n"+
			"Mutation: %s (%s)\n\n"+
			"Optimized strategy score: %.3f\n"+
			"Parameters: %s\n"+
			"Prompt template: %s",
		paramSummary(s),
		s.PromptTemplate,
		s.StrategyMutationType.String(),
		s.MutationDesc,
		s.Score,
		paramSummary(s),
		s.PromptTemplate,
	)
	return TextExample{Text: text}
}

// strategyToChatExample converts one strategy into a chat training pair.
func strategyToChatExample(s *mutation.Strategy) ChatExample {
	userMsg := fmt.Sprintf(
		"Generate an optimized agent strategy configuration.\n\n"+
			"Available parameters: %s\n"+
			"Prompt template: %s\n"+
			"Mutation type: %s (%s)",
		paramSummary(s),
		s.PromptTemplate,
		s.StrategyMutationType.String(),
		s.MutationDesc,
	)
	assistantMsg := fmt.Sprintf(
		"Strategy score: %.3f\n"+
			"Parameters: %s\n"+
			"Prompt template: %s",
		s.Score,
		paramSummary(s),
		s.PromptTemplate,
	)
	return ChatExample{
		Messages: []ChatMessage{
			{Role: "user", Content: userMsg},
			{Role: "assistant", Content: assistantMsg},
		},
	}
}

// ExportStrategiesToJSONL writes top-K strategies to JSONL.
// textFormat=true → {"text": "..."}; textFormat=false → {"messages": [...]}
func ExportStrategiesToJSONL(strategies []*mutation.Strategy, path string, topK int, textFormat bool) (int, error) {
	// Sort by score descending.
	sorted := make([]*mutation.Strategy, len(strategies))
	copy(sorted, strategies)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Score > sorted[j].Score
	})
	if topK <= 0 || topK > len(sorted) {
		topK = len(sorted)
	}
	sorted = sorted[:topK]

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, fmt.Errorf("create dir %s: %w", dir, err)
	}
	f, err := os.Create(path)
	if err != nil {
		return 0, fmt.Errorf("create file %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	count := 0
	for _, s := range sorted {
		var ex any
		if textFormat {
			ex = strategyToTextExample(s)
		} else {
			ex = strategyToChatExample(s)
		}
		if err := enc.Encode(ex); err != nil {
			return count, fmt.Errorf("encode strategy %s: %w", s.ID, err)
		}
		count++
	}
	slog.Info("Strategies exported to JSONL",
		"path", path,
		"count", count,
		"format", map[bool]string{true: "text", false: "chat"}[textFormat],
		"top_k", topK,
		"best_score", sorted[0].Score,
	)
	return count, nil
}

// ──────────────────────────────────────────────
// Data pipeline: Experience → JSONL
// ──────────────────────────────────────────────

func experienceToChatExample(ex *experience.Experience) ChatExample {
	userMsg := fmt.Sprintf("Problem: %s\n\nConstraints: %s", ex.Problem, ex.Constraints)
	assistantMsg := fmt.Sprintf("Solution: %s\n\nType: %s | Score: %.3f | Success: %t",
		ex.Solution, ex.Type, ex.Score, ex.Success)
	return ChatExample{
		Messages: []ChatMessage{
			{Role: "user", Content: userMsg},
			{Role: "assistant", Content: assistantMsg},
		},
	}
}

func experienceToTextExample(ex *experience.Experience) TextExample {
	text := fmt.Sprintf(
		"Problem: %s\nConstraints: %s\n\nSolution: %s\nScore: %.3f",
		ex.Problem, ex.Constraints, ex.Solution, ex.Score,
	)
	return TextExample{Text: text}
}

// ExportExperiencesToJSONL writes experiences to JSONL.
func ExportExperiencesToJSONL(experiences []*experience.Experience, path string, textFormat bool) (int, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, fmt.Errorf("create dir %s: %w", dir, err)
	}
	f, err := os.Create(path)
	if err != nil {
		return 0, fmt.Errorf("create file %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	count := 0
	for _, ex := range experiences {
		var entry any
		if textFormat {
			entry = experienceToTextExample(ex)
		} else {
			entry = experienceToChatExample(ex)
		}
		if err := enc.Encode(entry); err != nil {
			return count, fmt.Errorf("encode experience %s: %w", ex.ID, err)
		}
		count++
	}
	slog.Info("Experiences exported to JSONL",
		"path", path,
		"count", count,
		"format", map[bool]string{true: "text", false: "chat"}[textFormat],
	)
	return count, nil
}

// ──────────────────────────────────────────────
// Real evolution: WiredEvolutionSystem
// ──────────────────────────────────────────────

// ──────────────────────────────────────────────
// LLM-based strategy scorer (rate-limited)
// ──────────────────────────────────────────────

// LLMScoreClient wraps an LLM client with a fallback heuristic scorer.
// It provides a ScorerFunc that calls the LLM to evaluate strategies,
// subject to rate limiting via the token bucket.
type LLMScoreClient struct {
	client    *llm.Client
	heuristic genome.ScorerFunc // fallback when LLM is unavailable or fails
	timeout   time.Duration     // per-call timeout
}

// NewLLMScoreClient creates an LLM scorer from the given LLM configuration.
//
// Args:
//
//	llmCfg - LLM client configuration (provider, api key, base url, model).
//	heuristic - fallback scorer used when LLM is unavailable or the call fails.
//	llmTimeout - per-call timeout for LLM scoring (e.g., 30s).
//	scorerRate - token bucket rate (requests/sec) for the LLM scorer.
//	scorerBurst - token bucket burst size.
//
// Returns:
//
//	*LLMScoreClient - ready-to-use scorer.
//	error - non-nil if LLM client creation fails.
func NewLLMScoreClient(llmCfg *llm.Config, heuristic genome.ScorerFunc, llmTimeout time.Duration, scorerRate float64, scorerBurst int) (*LLMScoreClient, error) {
	limiter := ratelimit.NewTokenBucketLimiter(&ratelimit.LimiterConfig{
		Rate:  scorerRate,
		Burst: scorerBurst,
	})

	client, err := llm.NewClient(llmCfg, llm.WithRateLimiter(limiter))
	if err != nil {
		return nil, fmt.Errorf("new LLM client: %w", err)
	}

	return &LLMScoreClient{
		client:    client,
		heuristic: heuristic,
		timeout:   llmTimeout,
	}, nil
}

// ScorerFunc returns a genome.ScorerFunc that evaluates strategies using LLM.
// It constructs a structured prompt describing the strategy parameters and
// asks the LLM to rate the strategy on a 0-100 scale. On any failure
// (timeout, parse error, empty response), it falls back to the heuristic scorer.
//
// Because genome.ScorerFunc does not accept a context parameter, this method
// uses context.Background() internally with the configured timeout.
func (ls *LLMScoreClient) ScorerFunc() genome.ScorerFunc {
	return func(s *mutation.Strategy) float64 {
		prompt := buildStrategyPrompt(s)

		ctx, cancel := context.WithTimeout(context.Background(), ls.timeout)
		defer cancel()

		resp, err := ls.client.Generate(ctx, prompt)
		if err != nil {
			slog.Warn("LLM scorer failed, falling back to heuristic",
				"strategy_id", s.ID,
				"error", err,
			)
			return ls.heuristic(s)
		}

		score, err := parseScore(resp)
		if err != nil {
			slog.Warn("LLM scorer parse error, falling back to heuristic",
				"strategy_id", s.ID,
				"response", truncate(resp, 100),
				"error", err,
			)
			return ls.heuristic(s)
		}

		slog.Debug("LLM scored strategy",
			"strategy_id", s.ID,
			"score", score,
		)
		return score
	}
}

// buildStrategyPrompt constructs a scoring prompt from a strategy.
func buildStrategyPrompt(s *mutation.Strategy) string {
	var b strings.Builder
	b.WriteString("You are an expert agent strategy evaluator. ")
	b.WriteString("Rate the following agent configuration on a scale from 0 to 100, ")
	b.WriteString("where 100 is the best possible configuration for executing complex tasks reliably.\n\n")
	b.WriteString("Strategy Configuration:\n")

	// Parameter section
	b.WriteString("Parameters:\n")
	for k, v := range s.Params {
		switch k {
		case "temperature":
			fmt.Fprintf(&b, "  - temperature: %v (lower = more deterministic)\n", v)
		case "top_k":
			fmt.Fprintf(&b, "  - top_k: %v (moderate 20-40 is best)\n", v)
		case "max_steps":
			fmt.Fprintf(&b, "  - max_steps: %v (more steps = more thorough)\n", v)
		case "memory_limit":
			fmt.Fprintf(&b, "  - memory_limit: %v (higher = better recall)\n", v)
		default:
			fmt.Fprintf(&b, "  - %s: %v\n", k, v)
		}
	}

	if s.PromptTemplate != "" {
		fmt.Fprintf(&b, "Prompt Template: %s\n", truncate(s.PromptTemplate, 200))
	}

	fmt.Fprintf(&b, "Mutation Type: %s\n", s.StrategyMutationType.String())
	if s.MutationDesc != "" {
		fmt.Fprintf(&b, "Mutation Detail: %s\n", s.MutationDesc)
	}

	b.WriteString("\n")
	b.WriteString("Respond with ONLY a single number between 0 and 100. ")
	b.WriteString("Do not include any explanation, additional text, or formatting.")
	return b.String()
}

// parseScore extracts a numeric score from the LLM's response.
// It attempts to parse the entire response as a number, then falls back
// to finding the first number in the response text.
func parseScore(resp string) (float64, error) {
	resp = strings.TrimSpace(resp)

	// Try direct parse first.
	if score, err := strconv.ParseFloat(resp, 64); err == nil {
		return clampScore(score), nil
	}

	// Fallback: find the first number sequence.
	fields := strings.Fields(resp)
	for _, field := range fields {
		cleaned := strings.TrimFunc(field, func(r rune) bool {
			return (r < '0' || r > '9') && r != '.' && r != '-'
		})
		if cleaned == "" {
			continue
		}
		if score, err := strconv.ParseFloat(cleaned, 64); err == nil {
			return clampScore(score), nil
		}
	}

	return 0, fmt.Errorf("no numeric score found in response: %q", truncate(resp, 80))
}

// clampScore clamps a score to the [0, 100] range.
func clampScore(score float64) float64 {
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

// RealScorer assigns scores to strategies based on param configuration quality.
// This is a heuristic scorer that rewards strategies with lower temperature
// and higher top_k (commonly better for task execution).
func RealScorer(s *mutation.Strategy) float64 {
	score := 50.0 // baseline

	if temp, ok := s.Params["temperature"]; ok {
		if t, ok := temp.(float64); ok {
			// Lower temperature = more deterministic = higher score.
			score += (1.0 - t) * 30.0
		}
	}
	if topK, ok := s.Params["top_k"]; ok {
		switch v := topK.(type) {
		case float64:
			// moderate top_k is best: 20-40 range.
			if v >= 20 && v <= 40 {
				score += 15.0
			}
		}
	}
	if s.PromptTemplate != "" {
		score += 5.0
	}
	return score
}

// runRealEvolution creates a real WiredEvolutionSystem, runs N generations,
// and returns the population snapshot.
func runRealEvolution(ctx context.Context, generations, popSize int, appCfg *config.Config) (*genome.Population, []*mutation.Strategy, error) {
	base := &mutation.Strategy{
		ID:             "finetune-base",
		Version:        1,
		Name:           "finetune-root-strategy",
		Params:         map[string]any{"temperature": 0.7, "top_k": 40, "max_steps": 10, "memory_limit": 5},
		PromptTemplate: "You are a helpful assistant.",
		Score:          RealScorer(&mutation.Strategy{Params: map[string]any{"temperature": 0.7, "top_k": 40}, PromptTemplate: "You are a helpful assistant."}),
		CreatedAt:      time.Now(),
	}

	cfg := evolution.DefaultSystemConfig()
	cfg.PopulationSize = popSize
	cfg.EliteCount = 2
	cfg.MutationRate = 0.3
	cfg.SurvivalRate = 0.6
	cfg.MutatorSeed = 42
	cfg.CrossoverSeed = 99
	cfg.PopulationSeed = 123
	cfg.UseDeterministicIDs = true
	cfg.EnableDreamCycle = false
	cfg.EnableScheduler = false
	cfg.HistoryMaxSize = popSize * generations
	// Try LLM-based scorer for tiered scoring pipeline.
	if appCfg.LLM.Provider != "" && appCfg.LLM.APIKey != "" {
		// Ensure LLM client timeout is sufficient for evolution workloads.
		llmTimeout := appCfg.LLM.Timeout
		if llmTimeout < 60 {
			llmTimeout = 60
			slog.Info("Increasing LLM client timeout for evolution workload",
				"original_timeout", appCfg.LLM.Timeout,
				"new_timeout", llmTimeout,
			)
		}

		llmCfg := &llm.Config{
			Provider:  appCfg.LLM.Provider,
			APIKey:    appCfg.LLM.APIKey,
			BaseURL:   appCfg.LLM.BaseURL,
			Model:     appCfg.LLM.Model,
			Timeout:   llmTimeout,
			MaxTokens: appCfg.LLM.MaxTokens, // ✅ 修复：复制 MaxTokens 配置（关键！）
			Extra:     appCfg.LLM.Extra,
		}
		// Calculate FailoverScorer timeout with minimum threshold for evolution scenarios.
		scorerTimeout := time.Duration(llmTimeout) * time.Second
		if scorerTimeout < 60*time.Second {
			scorerTimeout = 60*time.Second
		}

		llmScorer, err := NewLLMScoreClient(llmCfg, RealScorer, scorerTimeout,
			appCfg.LLM.ScorerAPIRate, appCfg.LLM.ScorerAPIBurst)
		if err != nil {
			slog.Warn("LLM scorer not available, falling back to heuristic only",
				"error", err,
			)
			cfg.Scorer = RealScorer
		} else {
			cfg.Scorer = llmScorer.ScorerFunc()
			cfg.HeuristicScorer = RealScorer
			cfg.MaxLLMCallsPerGeneration = popSize * 2 // budget: up to 2x population per gen

			slog.Info("Using tiered scoring pipeline",
				"llm_budget_per_gen", cfg.MaxLLMCallsPerGeneration,
			)
		}
	} else {
		slog.Info("No LLM config provided, using heuristic scorer only")
		cfg.Scorer = RealScorer
	}

	slog.Info("Creating real evolution system",
		"population_size", popSize,
		"generations", generations,
		"base_params", paramSummary(base),
	)

	system, err := evolution.NewWiredEvolutionSystem(base, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("create wired system: %w", err)
	}

	slog.Info("Running real evolution...")
	if err := evolution.RunIdleEvolution(ctx, system, generations); err != nil {
		return nil, nil, fmt.Errorf("run evolution: %w", err)
	}

	pop := system.Population
	strategies, gen := pop.Snapshot()
	stats := pop.Stats()

	slog.Info("Evolution complete",
		"generations_run", gen,
		"population_size", len(strategies),
		"best_score", fmt.Sprintf("%.3f", stats.BestScore),
		"avg_score", fmt.Sprintf("%.3f", stats.AvgScore),
	)

	// Log the best strategy.
	if best := pop.BestStrategy(); best != nil {
		slog.Info("Best evolved strategy",
			"id", best.ID,
			"version", best.Version,
			"score", fmt.Sprintf("%.3f", best.Score),
			"params", paramSummary(best),
			"mutation", best.StrategyMutationType.String(),
		)
	}

	return pop, strategies, nil
}

// ──────────────────────────────────────────────
// Real experience data (DistillationService)
// ──────────────────────────────────────────────

// buildRealExperiences creates TaskResults and distills them into Experiences.
// Uses the same pattern as end-to-end Phase 3: task results → distillation.
func buildRealExperiences(rng *rand.Rand) []*experience.Experience {
	// Task configurations that mirror real agent execution scenarios.
	taskResults := []*experience.TaskResult{
		{
			Task:     "Concurrent request handling: process 50 simultaneous user queries",
			Context:  "Multi-tenant environment with rate limiting and conflicting state updates",
			Result:   "Implemented conflict resolution with exponential backoff: detect collision, wait random(500ms-2s), retry with refreshed context. 98% success rate.",
			Success:  true,
			AgentID:  "agent-evolve-001",
			TenantID: "default",
		},
		{
			Task:     "Multi-step reasoning: analyze financial report and generate investment recommendations",
			Context:  "Complex document with 15 pages of financial data, requires step-by-step decomposition",
			Result:   "Decomposed into 3 sub-tasks: (1) extract key metrics, (2) trend analysis, (3) recommend. Intermediate checkpoints saved per step.",
			Success:  true,
			AgentID:  "agent-evolve-001",
			TenantID: "default",
		},
		{
			Task:     "Long conversation context management",
			Context:  "50-turn conversation exceeding 4096 token window, must preserve critical information",
			Result:   "Applied sliding window summarization: compressed turns 1-30 into 256-token summary, retaining recent 20 turns intact.",
			Success:  true,
			AgentID:  "agent-evolve-002",
			TenantID: "default",
		},
		{
			Task:     "Tool execution: query database with ambiguous schema",
			Context:  "User asks 'show me last month's data' without specifying table or date format",
			Result:   "FAILED: parameter 'date_range' ambiguous. No fallback logic triggered.",
			Success:  false,
			AgentID:  "agent-evolve-001",
			TenantID: "default",
		},
		{
			Task:     "Generate code with multiple file dependencies",
			Context:  "Need to create a REST endpoint that depends on 3 existing service interfaces",
			Result:   "Generated all 4 files: handler.go (valid), service.go (valid), repo.go (import error), router.go (valid). Partial success.",
			Success:  true,
			AgentID:  "agent-evolve-003",
			TenantID: "default",
		},
		{
			Task:     "Knowledge-grounded response: answer based on internal documentation",
			Context:  "User asks about deployment procedure not covered in training data, must reference company wiki",
			Result:   "Retrieved 3 relevant wiki pages via RAG, cross-referenced, provided step-by-step deployment guide with source citations.",
			Success:  true,
			AgentID:  "agent-evolve-002",
			TenantID: "default",
		},
		{
			Task:     "Sentiment analysis on customer feedback in mixed languages",
			Context:  "200 reviews in English + Chinese with code-switching, domain-specific terminology",
			Result:   "Applied multilingual embedding model, detected 85% negative sentiment due to shipping delays. Generated executive summary with language breakdown.",
			Success:  true,
			AgentID:  "agent-evolve-003",
			TenantID: "default",
		},
		{
			Task:     "Real-time data aggregation from 5 streaming sources",
			Context:  "High-frequency trading data: 1000 events/sec, need to aggregate within 100ms window",
			Result:   "FAILED: window processing exceeded 500ms latency target. Stream buffer overflow at 1200 events/sec peak.",
			Success:  false,
			AgentID:  "agent-evolve-001",
			TenantID: "default",
		},
	}

	// Distill each TaskResult into an Experience (mirrors Phase 3 of end-to-end).
	now := time.Now()
	experiences := make([]*experience.Experience, 0, len(taskResults))
	for i, tr := range taskResults {
		exp := distillExperience(tr, i, now, rng)
		if exp != nil {
			experiences = append(experiences, exp)
		}
	}
	return experiences
}

// distillExperience converts a TaskResult into an Experience.
// Uses the same heuristic as end-to-end's distillExperienceWithRealService but
// outputs more structured Problem/Solution content for better training data.
func distillExperience(tr *experience.TaskResult, idx int, now time.Time, rng *rand.Rand) *experience.Experience {
	expType := "success"
	if !tr.Success {
		expType = "failure"
	}

	// Build a meaningful Problem statement.
	problem := fmt.Sprintf("Agent %s encountered: %s. Context: %s",
		tr.AgentID, tr.Task, truncate(tr.Context, 120))

	// Extract constraints from the result analysis.
	constraints := "N/A"
	switch {
	case tr.Success && tr.Task == "Concurrent request handling: process 50 simultaneous user queries":
		constraints = "Must handle >=50 concurrent users with <500ms p99 latency"
	case tr.Success && tr.Task == "Multi-step reasoning: analyze financial report and generate investment recommendations":
		constraints = "Must complete within 30s total; intermediate checkpoints required"
	case tr.Success && tr.Task == "Long conversation context management":
		constraints = "Window size fixed at 4096 tokens; summary must fit 256 tokens"
	case tr.Success && tr.Task == "Generate code with multiple file dependencies":
		constraints = "Must generate valid Go code with correct import paths"
	case tr.Success && tr.Task == "Knowledge-grounded response: answer based on internal documentation":
		constraints = "Must query knowledge base within 200ms; responses must cite sources"
	case tr.Success && tr.Task == "Sentiment analysis on customer feedback in mixed languages":
		constraints = "Must handle English and Chinese; domain-specific terms must be preserved"
	case !tr.Success && tr.Task == "Tool execution: query database with ambiguous schema":
		constraints = "Must have fallback logic for ambiguous parameters; never leave user without response"
	case !tr.Success && tr.Task == "Real-time data aggregation from 5 streaming sources":
		constraints = "Window aggregation must complete within 100ms; must handle burst up to 1500 events/sec"
	}

	return &experience.Experience{
		ID:          fmt.Sprintf("exp-finetune-%03d", idx),
		TenantID:    tr.TenantID,
		Type:        expType,
		Problem:     problem,
		Solution:    tr.Result,
		Constraints: constraints,
		Score:       0.5 + rng.Float64()*0.4, // synthetic score for training
		Success:     tr.Success,
		AgentID:     tr.AgentID,
		UsageCount:  rng.Intn(30),
		CreatedAt:   now.Add(-time.Duration(rng.Intn(3600)) * time.Second),
	}
}

// ──────────────────────────────────────────────
// Integrated export: Evolution Population → JSONL
// ──────────────────────────────────────────────

// ExportPopulationToJSONL takes a real evolution Population and
// writes the top-K strategies to JSONL.
// path: output path for JSONL
// topK: number of top strategies to export (0 = all)
// textFormat: true → {"text": "..."}, false → {"messages": [...]}
func ExportPopulationToJSONL(pop *genome.Population, path string, topK int, textFormat bool) (int, error) {
	strategies, gen := pop.Snapshot()
	slog.Info("Exporting population snapshot",
		"generation", gen,
		"population_size", len(strategies),
		"top_k", topK,
	)
	return ExportStrategiesToJSONL(strategies, path, topK, textFormat)
}

// ──────────────────────────────────────────────
// Helpers
// ──────────────────────────────────────────────

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// ──────────────────────────────────────────────
// Main
// ──────────────────────────────────────────────

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	ctx := context.Background()

	// ── Load configuration ──
	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = "./examples/finetune/config/server.yaml"
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		slog.Warn("Failed to load config, using defaults",
			"path", cfgPath,
			"error", err,
		)
		cfg = &config.Config{}
	}
	_ = config.LoadFromEnv(cfg) // env vars can override YAML

	fmt.Println("╔═══════════════════════════════════════════════╗")
	fmt.Println("║  ARES → JSONL Training Data Pipeline (Real)  ║")
	fmt.Println("╚═══════════════════════════════════════════════╝")
	fmt.Println()

	// ── Step 1: Run real evolution ──
	fmt.Println("▶ Step 1: Running real evolution (WiredEvolutionSystem)...")
	fmt.Println("  Generating population via mutation + crossover + selection...")
	fmt.Println()

	pop, strategies, err := runRealEvolution(ctx, 5, 16, cfg)
	if err != nil {
		slog.Error("Evolution failed", "error", err)
		os.Exit(1)
	}
	_ = pop // kept for use with ExportPopulationToJSONL

	fmt.Printf("  ✓ Population: %d strategies, %d generations\n", len(strategies), pop.CurrentGeneration())
	if best := pop.BestStrategy(); best != nil {
		fmt.Printf("  ✓ Best score: %.3f (ID: %s, version: %d)\n",
			best.Score, best.ID, best.Version)
	}
	fmt.Println()

	// ── Step 2: Export evolution strategies to JSONL ──
	fmt.Println("▶ Step 2: Exporting evolution strategies to JSONL...")

	count1, err := ExportStrategiesToJSONL(strategies, "./examples/finetune/train_data.jsonl", 0, true)
	if err != nil {
		slog.Error("Export strategies failed", "error", err)
		os.Exit(1)
	}
	count2, err := ExportStrategiesToJSONL(strategies, "./examples/finetune/train_data_chat.jsonl", 0, false)
	if err != nil {
		slog.Error("Export chat strategies failed", "error", err)
		os.Exit(1)
	}
	fmt.Printf("  → %d text examples  (train_data.jsonl)\n", count1)
	fmt.Printf("  → %d chat examples  (train_data_chat.jsonl)\n", count2)
	fmt.Println()

	// ── Step 3: Build real experience data via distillation ──
	fmt.Println("▶ Step 3: Building experience data (TaskResult → Experience)...")
	experiences := buildRealExperiences(rng)
	fmt.Printf("  → %d experiences distilled from %d task results\n", len(experiences), 8)
	fmt.Println()

	// Show a sample experience.
	if len(experiences) > 0 {
		b, _ := json.MarshalIndent(experiences[0], "  ", "  ")
		fmt.Printf("  Sample experience:\n  %s\n\n", string(b))
	}

	// ── Step 4: Export experience data to JSONL ──
	fmt.Println("▶ Step 4: Exporting experiences to JSONL...")
	count3, err := ExportExperiencesToJSONL(experiences, "./examples/finetune/experience_data.jsonl", false)
	if err != nil {
		slog.Error("Export experiences failed", "error", err)
		os.Exit(1)
	}
	count4, err := ExportExperiencesToJSONL(experiences, "./examples/finetune/experience_text_data.jsonl", true)
	if err != nil {
		slog.Error("Export experience text failed", "error", err)
		os.Exit(1)
	}
	fmt.Printf("  → %d chat examples  (experience_data.jsonl)\n", count3)
	fmt.Printf("  → %d text examples  (experience_text_data.jsonl)\n", count4)
	fmt.Println()

	// ── Step 5: Preview ──
	fmt.Println("▶ Step 5: Preview output")
	previewFile("./examples/finetune/train_data.jsonl", "train_data.jsonl (text)")
	previewFile("./examples/finetune/train_data_chat.jsonl", "train_data_chat.jsonl (chat)")
	previewFile("./examples/finetune/experience_data.jsonl", "experience_data.jsonl (chat)")
	previewFile("./examples/finetune/experience_text_data.jsonl", "experience_text_data.jsonl (text)")

	fmt.Println()
	fmt.Println("✅ Pipeline complete.")
	fmt.Printf("  Total: %d strategy + %d experience examples\n", count1+count2, count3+count4)
	fmt.Println()
	fmt.Println("Next step: Use with mlx-lm")
	fmt.Println("  mlx_lm.lora \\")
	fmt.Println("    --model Qwen/Qwen2.5-7B-Instruct \\")
	fmt.Println("    --train \\")
	fmt.Println("    --data ./examples/finetune/ \\")
	fmt.Println("    --iters 500")
}

func previewFile(path, label string) {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Printf("  [%s] (error: %v)\n", label, err)
		return
	}
	lines := 0
	for _, b := range data {
		if b == '\n' {
			lines++
		}
	}
	if lines == 0 {
		fmt.Printf("  [%s] (empty)\n", label)
		return
	}
	// Show first line.
	var firstLine []byte
	for _, b := range data {
		firstLine = append(firstLine, b)
		if b == '\n' {
			break
		}
	}
	fmt.Printf("  [%s] %d lines\n", label, lines)
	fmt.Printf("    %s\n", string(firstLine))
}
