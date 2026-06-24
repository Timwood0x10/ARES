package eval

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strings"
	"time"
)

// AgentConfig represents configuration for a single agent under comparison.
type AgentConfig struct {
	Name         string         `yaml:"name" json:"name"`
	Model        string         `yaml:"model" json:"model"`
	Parameters   map[string]any `yaml:"parameters,omitempty" json:"parameters,omitempty"`
	SystemPrompt string         `yaml:"system_prompt,omitempty" json:"system_prompt,omitempty"`
}

// ComparisonResult holds results for one agent configuration.
type ComparisonResult struct {
	ConfigName       string             `json:"config_name"`
	Config           AgentConfig        `json:"config"`
	Results          []TestResult       `json:"results"`
	AggregatedScores map[string]float64 `json:"aggregated_scores"` // dimension -> score
	OverallScore     float64            `json:"overall_score"`
	TotalDuration    time.Duration      `json:"total_duration"`
	PassRate         float64            `json:"pass_rate"` // 0.0-1.0
	Error            string             `json:"error,omitempty"`
}

// LeaderboardEntry ranks a single configuration by overall score.
type LeaderboardEntry struct {
	Rank       int                `json:"rank"`
	ConfigName string             `json:"config_name"`
	Overall    float64            `json:"overall_score"`
	Scores     map[string]float64 `json:"scores"`
}

// ComparisonSummary provides aggregate statistics across all configurations.
type ComparisonSummary struct {
	TotalConfigs  int     `json:"total_configs"`
	TotalTests    int     `json:"total_tests"`
	BestConfig    string  `json:"best_config"`
	AvgOverall    float64 `json:"avg_overall_score"`
	ScoreVariance float64 `json:"score_variance"`
}

// ComparisonReport holds side-by-side comparison across all configurations.
type ComparisonReport struct {
	SuiteName   string             `json:"suite_name"`
	Timestamp   time.Time          `json:"timestamp"`
	Results     []ComparisonResult `json:"results"`
	Leaderboard []LeaderboardEntry `json:"leaderboard"`
	Summary     ComparisonSummary  `json:"summary"`
}

// ComparisonRunner evaluates multiple agent configurations against the same test suite.
// It uses a factory function to create a TestRunner for each configuration,
// enabling side-by-side comparison of different agent setups.
type ComparisonRunner struct {
	runnerFactory func(config AgentConfig) (TestRunner, error)
	suite         *TestSuite
}

// Compile-time interface check: ensure ComparisonRunner is usable as a value.
var _ = (*ComparisonRunner)(nil)

// NewComparisonRunner creates a new ComparisonRunner for evaluating multiple
// agent configurations against the given test suite.
//
// Args:
//   - suite: the test suite to run against each configuration (must not be nil).
//   - runnerFactory: factory function that creates a TestRunner for each AgentConfig.
//
// Returns:
//   - *ComparisonRunner: configured comparison runner instance.
func NewComparisonRunner(suite *TestSuite, runnerFactory func(AgentConfig) (TestRunner, error)) *ComparisonRunner {
	return &ComparisonRunner{
		runnerFactory: runnerFactory,
		suite:         suite,
	}
}

// Run executes the test suite against each agent configuration sequentially
// and produces a full comparison report. Within each configuration, tests are
// run according to the underlying TestRunner's concurrency settings.
//
// Args:
//   - ctx: context for cancellation and timeout control.
//   - configs: list of agent configurations to evaluate.
//
// Returns:
//   - *ComparisonReport: complete comparison results with leaderboard and summary.
//   - error: non-nil if context is cancelled or a fatal error occurs.
func (r *ComparisonRunner) Run(ctx context.Context, configs []AgentConfig) (*ComparisonReport, error) {
	if len(configs) == 0 {
		return &ComparisonReport{
			SuiteName:   r.suite.Name,
			Timestamp:   time.Now(),
			Results:     []ComparisonResult{},
			Leaderboard: []LeaderboardEntry{},
			Summary:     ComparisonSummary{TotalConfigs: 0, TotalTests: len(r.suite.TestCases)},
		}, nil
	}

	results := make([]ComparisonResult, 0, len(configs))
	for _, cfg := range configs {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		result, err := r.runSingleConfig(ctx, cfg)
		if err != nil {
			slog.Error("failed to run config",
				"config", cfg.Name,
				"error", err,
			)
			// Continue with other configs even if one fails; record the error in result.
			results = append(results, ComparisonResult{
				ConfigName: cfg.Name,
				Config:     cfg,
				Results:    []TestResult{},
				Error:      err.Error(),
			})
			continue
		}
		results = append(results, *result)
	}

	report := &ComparisonReport{
		SuiteName: r.suite.Name,
		Timestamp: time.Now(),
		Results:   results,
	}
	report.Leaderboard = GenerateLeaderboard(results)
	report.Summary = buildComparisonSummary(results)

	return report, nil
}

// runSingleConfig runs the test suite for a single agent configuration
// and returns the aggregated ComparisonResult.
func (r *ComparisonRunner) runSingleConfig(ctx context.Context, cfg AgentConfig) (*ComparisonResult, error) {
	runner, err := r.runnerFactory(cfg)
	if err != nil {
		return nil, fmt.Errorf("create runner for config %q: %w", cfg.Name, err)
	}

	start := time.Now()
	results, err := runner.RunSuite(ctx, *r.suite)
	totalDuration := time.Since(start)

	if err != nil {
		return nil, fmt.Errorf("run suite for config %q: %w", cfg.Name, err)
	}

	aggScores := aggregateScores(results)
	overallScore := computeOverallScore(aggScores)
	passRate := computePassRate(results)

	return &ComparisonResult{
		ConfigName:       cfg.Name,
		Config:           cfg,
		Results:          results,
		AggregatedScores: aggScores,
		OverallScore:     overallScore,
		TotalDuration:    totalDuration,
		PassRate:         passRate,
	}, nil
}

// GenerateMarkdown produces a side-by-side Markdown comparison report.
//
// Args:
//   - report: the comparison report to render (must not be nil).
//
// Returns:
//   - string: formatted Markdown content.
//   - error: always nil (kept for future extensibility).
func (r *ComparisonRunner) GenerateMarkdown(report *ComparisonReport) (string, error) {
	var sb strings.Builder

	fmt.Fprintf(&sb, "# Agent Comparison Report: %s\n\n", report.SuiteName)
	fmt.Fprintf(&sb, "Generated: %s\n\n", report.Timestamp.Format(time.RFC3339))

	r.writeSummarySection(&sb, report)
	r.writeLeaderboardSection(&sb, report)
	r.writePerTestDetailSection(&sb, report)
	r.writeDimensionAnalysisSection(&sb, report)

	return sb.String(), nil
}

// writeSummarySection writes the summary statistics table.
func (r *ComparisonRunner) writeSummarySection(sb *strings.Builder, report *ComparisonReport) {
	s := report.Summary
	sb.WriteString("## Summary\n\n")
	sb.WriteString("| Metric | Value |\n")
	sb.WriteString("|--------|-------|\n")
	fmt.Fprintf(sb, "| Total Configurations | %d |\n", s.TotalConfigs)
	fmt.Fprintf(sb, "| Total Test Cases | %d |\n", s.TotalTests)
	fmt.Fprintf(sb, "| Best Configuration | %s |\n", s.BestConfig)
	fmt.Fprintf(sb, "| Average Overall Score | %.4f |\n", s.AvgOverall)
	fmt.Fprintf(sb, "| Score Variance | %.6f |\n\n", s.ScoreVariance)
}

// writeLeaderboardSection writes the ranked leaderboard table.
func (r *ComparisonRunner) writeLeaderboardSection(sb *strings.Builder, report *ComparisonReport) {
	if len(report.Leaderboard) == 0 {
		return
	}

	sb.WriteString("## Leaderboard\n\n")
	sb.WriteString("| Rank | Config | Overall Score")

	// Collect all dimension names from first entry that has scores.
	dimNames := r.collectDimensionNames(report.Leaderboard)
	for _, dim := range dimNames {
		fmt.Fprintf(sb, " | %s", dim)
	}
	sb.WriteString(" |\n")

	sb.WriteString("|------|--------|")
	for range dimNames {
		sb.WriteString("--------|")
	}
	sb.WriteString("\n")

	for _, entry := range report.Leaderboard {
		fmt.Fprintf(sb, "| %d | %s | %.4f", entry.Rank, entry.ConfigName, entry.Overall)
		for _, dim := range dimNames {
			val, ok := entry.Scores[dim]
			if ok {
				fmt.Fprintf(sb, " | %.4f", val)
			} else {
				sb.WriteString(" | N/A")
			}
		}
		sb.WriteString(" |\n")
	}
	sb.WriteString("\n")
}

// writePerTestDetailSection writes per-test side-by-side results.
func (r *ComparisonRunner) writePerTestDetailSection(sb *strings.Builder, report *ComparisonReport) {
	if len(r.suite.TestCases) == 0 || len(report.Results) == 0 {
		return
	}

	sb.WriteString("## Per-Test Results\n\n")
	// Header row: Test ID | Name | Config1_Score | Config1_Status | ...
	sb.WriteString("| Test ID | Name")
	for _, cr := range report.Results {
		fmt.Fprintf(sb, " | %s_Score | %s_Status", cr.ConfigName, cr.ConfigName)
	}
	sb.WriteString(" |\n")

	// Separator
	sb.WriteString("|---------|-----")
	for range report.Results {
		sb.WriteString("|----------|-----------")
	}
	sb.WriteString("|\n")

	for _, tc := range r.suite.TestCases {
		fmt.Fprintf(sb, "| %s | %s", tc.ID, tc.Name)
		for _, cr := range report.Results {
			score := r.findTestScore(cr, tc.ID)
			status := r.findTestStatus(cr, tc.ID)
			fmt.Fprintf(sb, " | %.4f | %s", score, status)
		}
		sb.WriteString(" |\n")
	}
	sb.WriteString("\n")
}

// writeDimensionAnalysisSection writes per-dimension analysis across configs.
func (r *ComparisonRunner) writeDimensionAnalysisSection(sb *strings.Builder, report *ComparisonReport) {
	allDims := r.collectAllDimensions(report.Results)
	if len(allDims) == 0 {
		return
	}

	sb.WriteString("## Dimension Analysis\n\n")
	for _, dim := range allDims {
		fmt.Fprintf(sb, "### %s\n\n", dim)
		sb.WriteString("| Config | Score |\n")
		sb.WriteString("|--------|-------|\n")
		for _, cr := range report.Results {
			val, ok := cr.AggregatedScores[dim]
			if ok {
				fmt.Fprintf(sb, "| %s | %.4f |\n", cr.ConfigName, val)
			} else {
				fmt.Fprintf(sb, "| %s | N/A |\n", cr.ConfigName)
			}
		}
		sb.WriteString("\n")
	}
}

// collectDimensionNames extracts sorted dimension names from leaderboard entries.
func (r *ComparisonRunner) collectDimensionNames(entries []LeaderboardEntry) []string {
	dimSet := make(map[string]bool)
	for _, entry := range entries {
		for dim := range entry.Scores {
			dimSet[dim] = true
		}
	}
	dims := make([]string, 0, len(dimSet))
	for d := range dimSet {
		dims = append(dims, d)
	}
	sort.Strings(dims)
	return dims
}

// collectAllDimensions collects all dimension names from comparison results.
func (r *ComparisonRunner) collectAllDimensions(results []ComparisonResult) []string {
	dimSet := make(map[string]bool)
	for _, cr := range results {
		for dim := range cr.AggregatedScores {
			dimSet[dim] = true
		}
	}
	dims := make([]string, 0, len(dimSet))
	for d := range dimSet {
		dims = append(dims, d)
	}
	sort.Strings(dims)
	return dims
}

// findTestScore finds the aggregated score for a specific test case within a comparison result.
func (r *ComparisonRunner) findTestScore(cr ComparisonResult, testCaseID string) float64 {
	for _, tr := range cr.Results {
		if tr.TestCaseID == testCaseID {
			// Use the max metric value as the representative score.
			maxScore := 0.0
			for _, v := range tr.Metrics {
				if v > maxScore {
					maxScore = v
				}
			}
			return maxScore
		}
	}
	return 0.0
}

// findTestStatus returns pass/fail status for a specific test case.
func (r *ComparisonRunner) findTestStatus(cr ComparisonResult, testCaseID string) string {
	for _, tr := range cr.Results {
		if tr.TestCaseID == testCaseID {
			if tr.Error == "" {
				return "PASS"
			}
			return "FAIL"
		}
	}
	return "N/A"
}

// GenerateLeaderboard sorts comparison results by overall score descending
// and assigns ranks. Ties receive the same rank.
//
// Args:
//   - results: slice of comparison results to rank.
//
// Returns:
//   - []LeaderboardEntry: sorted and ranked entries.
func GenerateLeaderboard(results []ComparisonResult) []LeaderboardEntry {
	entries := make([]LeaderboardEntry, 0, len(results))
	for _, cr := range results {
		entries = append(entries, LeaderboardEntry{
			Rank:       0, // placeholder, assigned below
			ConfigName: cr.ConfigName,
			Overall:    cr.OverallScore,
			Scores:     copyMap(cr.AggregatedScores),
		})
	}

	// Sort by Overall descending.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Overall > entries[j].Overall
	})

	// Assign ranks (ties get same rank).
	for i := range entries {
		if i == 0 {
			entries[i].Rank = 1
		} else if entries[i].Overall == entries[i-1].Overall {
			entries[i].Rank = entries[i-1].Rank
		} else {
			entries[i].Rank = i + 1
		}
	}

	return entries
}

// buildComparisonSummary computes aggregate statistics from comparison results.
func buildComparisonSummary(results []ComparisonResult) ComparisonSummary {
	totalConfigs := len(results)
	totalTests := 0
	if len(results) > 0 && len(results[0].Results) > 0 {
		totalTests = len(results[0].Results)
	}

	bestConfig := ""
	bestScore := -1.0
	sumScore := 0.0
	scoreCount := 0
	var scores []float64

	for _, cr := range results {
		if cr.OverallScore > bestScore {
			bestScore = cr.OverallScore
			bestConfig = cr.ConfigName
		}
		if cr.Error == "" { // Only include successful runs in statistics.
			sumScore += cr.OverallScore
			scoreCount++
			scores = append(scores, cr.OverallScore)
		}
	}

	avgOverall := 0.0
	variance := 0.0
	if scoreCount > 0 {
		avgOverall = sumScore / float64(scoreCount)
		mean := avgOverall
		var sumSqDiff float64
		for _, s := range scores {
			diff := s - mean
			sumSqDiff += diff * diff
		}
		variance = sumSqDiff / float64(scoreCount)
	}

	return ComparisonSummary{
		TotalConfigs:  totalConfigs,
		TotalTests:    totalTests,
		BestConfig:    bestConfig,
		AvgOverall:    math.Round(avgOverall*10000) / 10000,
		ScoreVariance: math.Round(variance*1000000) / 1000000,
	}
}

// aggregateScores computes average scores per dimension from test results.
func aggregateScores(results []TestResult) map[string]float64 {
	dimValues := make(map[string][]float64)
	for _, tr := range results {
		for metric, val := range tr.Metrics {
			dimValues[metric] = append(dimValues[metric], val)
		}
	}

	agg := make(map[string]float64, len(dimValues))
	for metric, values := range dimValues {
		if len(values) > 0 {
			sum := 0.0
			for _, v := range values {
				sum += v
			}
			agg[metric] = sum / float64(len(values))
		}
	}
	return agg
}

// computeOverallScore calculates the overall score as the mean of all dimension scores.
func computeOverallScore(scores map[string]float64) float64 {
	if len(scores) == 0 {
		return 0.0
	}
	sum := 0.0
	for _, v := range scores {
		sum += v
	}
	return math.Round((sum/float64(len(scores)))*10000) / 10000
}

// computePassRate calculates the fraction of tests that passed (no error).
func computePassRate(results []TestResult) float64 {
	if len(results) == 0 {
		return 0.0
	}
	passed := 0
	for _, tr := range results {
		if tr.Error == "" {
			passed++
		}
	}
	return math.Round(float64(passed)/float64(len(results))*10000) / 10000
}

// copyMap creates a shallow copy of a string-to-float64 map.
func copyMap(m map[string]float64) map[string]float64 {
	if m == nil {
		return nil
	}
	cpy := make(map[string]float64, len(m))
	for k, v := range m {
		cpy[k] = v
	}
	return cpy
}
