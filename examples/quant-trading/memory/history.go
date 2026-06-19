// Package memory provides cross-stock decision memory.
// Reads previous analysis results from JSON files and injects them
// into the Portfolio Manager's prompt for context-aware decisions.
package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// HistoryLoader reads past analysis results from JSON files.
type HistoryLoader struct {
	resultsDir string
	maxEntries int
}

// DecisionSummary is a lightweight view of a past analysis result.
type DecisionSummary struct {
	Ticker    string `json:"ticker"`
	Date      string `json:"date"`
	PMSignal  string `json:"pm_signal,omitempty"`
	PMThought string `json:"pm_thought,omitempty"`
}

// ResultFile is the on-disk structure of saved analysis JSON.
type ResultFile struct {
	Ticker     string        `json:"ticker"`
	AnalyzedAt string        `json:"analyzed_at"`
	Model      string        `json:"model"`
	Agents     []AgentOutput `json:"agents"`
}

// AgentOutput mirrors the main.go structure for reading saved files.
type AgentOutput struct {
	YamlID   string `json:"yaml_id"`
	Name     string `json:"name"`
	Analysis string `json:"analysis"`
	Status   string `json:"status"`
}

// NewHistoryLoader creates a loader that scans resultsDir for JSON files.
func NewHistoryLoader(resultsDir string, maxEntries int) *HistoryLoader {
	if maxEntries <= 0 {
		maxEntries = 10
	}
	return &HistoryLoader{resultsDir: resultsDir, maxEntries: maxEntries}
}

// LoadRecentDecisions reads the most recent analysis results for each ticker.
func (l *HistoryLoader) LoadRecentDecisions() ([]DecisionSummary, error) {
	entries, err := os.ReadDir(l.resultsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read results dir: %w", err)
	}

	var jsonFiles []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			jsonFiles = append(jsonFiles, filepath.Join(l.resultsDir, e.Name()))
		}
	}
	if len(jsonFiles) == 0 {
		return nil, nil
	}

	// Sort by modification time, newest first.
	sort.Slice(jsonFiles, func(i, j int) bool {
		fi, _ := os.Stat(jsonFiles[i])
		fj, _ := os.Stat(jsonFiles[j])
		if fi != nil && fj != nil {
			return fi.ModTime().After(fj.ModTime())
		}
		return false
	})

	// Group by ticker, take most recent per ticker.
	seen := make(map[string]bool)
	var results []DecisionSummary
	for _, path := range jsonFiles {
		if len(results) >= l.maxEntries {
			break
		}
		d, err := loadResultFile(path)
		if err != nil || d == nil {
			continue
		}
		if seen[d.Ticker] {
			continue
		}
		seen[d.Ticker] = true

		summary := DecisionSummary{
			Ticker: d.Ticker,
			Date:   d.AnalyzedAt,
		}
		for _, ag := range d.Agents {
			if ag.YamlID == "pm" && ag.Status == "completed" {
				summary.PMSignal = extractSignal(ag.Analysis)
				summary.PMThought = truncate(ag.Analysis, 300)
				break
			}
		}
		results = append(results, summary)
	}

	return results, nil
}

// BuildContext formats past decisions into a prompt fragment.
func (l *HistoryLoader) BuildContext() (string, error) {
	decisions, err := l.LoadRecentDecisions()
	if err != nil || len(decisions) == 0 {
		return "", err
	}

	var b strings.Builder
	b.WriteString("\nPast trading decisions for reference:\n")
	for _, d := range decisions {
		fmt.Fprintf(&b, "- %s (%s): %s\n", d.Ticker, d.Date, d.PMSignal)
		if d.PMThought != "" {
			fmt.Fprintf(&b, "  Rationale: %s\n", d.PMThought)
		}
	}
	b.WriteString("\nConsider past patterns, but don't overfit. Each decision should be based on current data.\n")
	return b.String(), nil
}

// loadResultFile reads and parses a saved analysis JSON file.
func loadResultFile(path string) (*ResultFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r ResultFile
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, err
	}
	if r.Ticker == "" {
		return nil, fmt.Errorf("invalid result file: %s", path)
	}
	return &r, nil
}

// extractSignal extracts the trading signal from PM analysis JSON.
func extractSignal(analysis string) string {
	analysis = strings.TrimSpace(analysis)
	analysis = strings.TrimPrefix(analysis, "```json")
	analysis = strings.TrimPrefix(analysis, "```")
	analysis = strings.TrimSuffix(analysis, "```")
	analysis = strings.TrimSpace(analysis)

	// Try to extract "signal" field from JSON.
	if strings.Contains(analysis, `"signal"`) {
		parts := strings.Split(analysis, `"signal"`)
		if len(parts) > 1 {
			after := parts[1]
			if idx := strings.Index(after, `"`); idx >= 0 {
				after = after[idx+1:]
				if endIdx := strings.Index(after, `"`); endIdx >= 0 {
					return after[:endIdx]
				}
			}
		}
	}
	return "unknown"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// TimeNow is a portable time source, overridable in tests.
var TimeNow = time.Now
