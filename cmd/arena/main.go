// Command arena provides the Chaos Engineering Arena CLI.
//
// Usage:
//
//	ares arena run <scenario.yaml> [--addr=localhost:8080]
//	ares arena validate <scenario.yaml>
//	ares arena list [dir]
//	ares arena serve [--addr=:8080]
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	arena "github.com/Timwood0x10/ares/internal/ares_arena"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	sub := os.Args[1]
	args := os.Args[2:]

	switch sub {
	case "run":
		if err := runRun(args); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "validate":
		if err := runValidate(args); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "list":
		if err := runList(args); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "survival":
		if err := runSurvival(args); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "inspect":
		if err := runInspect(args); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "serve":
		if err := runServe(args); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", sub)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "Usage: ares arena <command> [options]\n\n")
	fmt.Fprintf(os.Stderr, "Commands:\n")
	fmt.Fprintf(os.Stderr, "  run       Run a scenario file (remote mode via HTTP)\n")
	fmt.Fprintf(os.Stderr, "  validate  Validate a scenario file\n")
	fmt.Fprintf(os.Stderr, "  list      List available scenarios in a directory\n")
	fmt.Fprintf(os.Stderr, "  survival  Run survival mode against a remote server\n")
	fmt.Fprintf(os.Stderr, "  inspect   Inspect arena run results from a remote server\n")
	fmt.Fprintf(os.Stderr, "  serve     Start arena HTTP server\n")
}

// separateArgs splits args into flags (starting with -) and positional args.
func separateArgs(args []string) (flags []string, positional []string) {
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			flags = append(flags, args[i])
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				flags = append(flags, args[i+1])
				i++
			}
		} else {
			positional = append(positional, args[i])
		}
	}
	return flags, positional
}

// runRun handles the "run" subcommand.
// It loads a YAML/JSON scenario file and POSTs it to a remote arena server.
func runRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	addr := fs.String("addr", "http://localhost:8080", "Arena server address")

	flags, positional := separateArgs(args)
	if err := fs.Parse(flags); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	if len(positional) < 1 {
		return fmt.Errorf("scenario file path is required")
	}
	scenarioPath := positional[0]

	// Load scenario from file.
	s, err := arena.LoadScenarioFile(scenarioPath)
	if err != nil {
		return fmt.Errorf("load scenario: %w", err)
	}

	// Validate before sending.
	if err := arena.ValidateScenario(s); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	fmt.Printf("Running scenario: %s\n", s.Name)
	if s.Description != "" {
		fmt.Printf("  Description: %s\n", s.Description)
	}
	fmt.Printf("  Actions: %d\n", len(s.Actions))
	fmt.Printf("  Target:   %s\n\n", *addr)

	// POST to remote server.
	bodyData, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal scenario: %w", err)
	}

	url := *addr + "/arena/scenario/run"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(bodyData)))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("send request to %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned status %d: %s", resp.StatusCode, string(respBody))
	}

	// Pretty-print the report.
	var report arena.ScenarioReport
	if err := json.Unmarshal(respBody, &report); err != nil {
		// If we can't unmarshal as report, just print raw response.
		fmt.Println(string(respBody))
		return nil
	}

	printReport(&report)
	return nil
}

// runValidate handles the "validate" subcommand.
func runValidate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	remote := fs.Bool("remote", false, "Validate against remote server")
	addr := fs.String("addr", "http://localhost:8080", "Arena server address (used with -remote)")

	flags, positional := separateArgs(args)
	if err := fs.Parse(flags); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	if len(positional) < 1 {
		return fmt.Errorf("scenario file path is required")
	}
	scenarioPath := positional[0]

	if *remote {
		return validateRemote(scenarioPath, *addr)
	}

	s, err := arena.LoadScenarioFile(scenarioPath)
	if err != nil {
		return fmt.Errorf("load scenario: %w", err)
	}

	if err := arena.ValidateScenario(s); err != nil {
		fmt.Printf("❌ INVALID: %s\n\n", scenarioPath)
		fmt.Printf("  Error: %v\n", err)
		fmt.Printf("  Name:   %s\n", s.Name)
		fmt.Printf("  Actions: %d\n", len(s.Actions))
		os.Exit(1)
	}

	fmt.Printf("✅ VALID: %s\n", scenarioPath)
	fmt.Printf("  Name:        %s\n", s.Name)
	fmt.Printf("  Description: %s\n", s.Description)
	fmt.Printf("  Tags:        %v\n", s.Tags)
	fmt.Printf("  Actions:     %d\n", len(s.Actions))
	if s.Config.StopOnError {
		fmt.Printf("  Config:      stop_on_error=true\n")
	}
	if s.Config.Warmup > 0 {
		fmt.Printf("  Config:      warmup=%v\n", s.Config.Warmup)
	}
	if s.Config.Cooldown > 0 {
		fmt.Printf("  Config:      cooldown=%v\n", s.Config.Cooldown)
	}
	if s.Config.Timeout > 0 {
		fmt.Printf("  Config:      timeout=%v\n", s.Config.Timeout)
	}
	return nil
}

// validateRemote sends the scenario to the remote server for validation.
func validateRemote(scenarioPath, addr string) error {
	s, err := arena.LoadScenarioFile(scenarioPath)
	if err != nil {
		return fmt.Errorf("load scenario: %w", err)
	}

	bodyData, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshal scenario: %w", err)
	}

	url := addr + "/arena/scenario/validate"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(bodyData)))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("send request to %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(resp.Body)
	fmt.Println(string(respBody))
	return nil
}

// runList handles the "list" subcommand.
func runList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)

	flags, positional := separateArgs(args)
	if err := fs.Parse(flags); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	dir := "."
	if len(positional) >= 1 {
		dir = positional[0]
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read directory %s: %w", dir, err)
	}

	var scenarios []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		ext := filepath.Ext(name)
		if ext == ".yaml" || ext == ".yml" || ext == ".json" {
			scenarios = append(scenarios, name)
		}
	}

	if len(scenarios) == 0 {
		fmt.Printf("No scenario files found in %s\n", dir)
		return nil
	}

	fmt.Printf("Scenarios in %s:\n", dir)
	for i, name := range scenarios {
		fullPath := filepath.Join(dir, name)
		s, err := arena.LoadScenarioFile(fullPath)
		if err != nil {
			fmt.Printf("  %d. %s (parse error: %v)\n", i+1, name, err)
			continue
		}
		desc := s.Description
		if desc == "" {
			desc = "(no description)"
		}
		tags := ""
		if len(s.Tags) > 0 {
			tags = fmt.Sprintf("[%s]", strings.Join(s.Tags, ", "))
		}
		fmt.Printf("  %d. %-30s  %s %s\n", i+1, name, desc, tags)
	}
	return nil
}

// runServe handles the "serve" subcommand.
func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", ":8080", "Listen address")

	flags, _ := separateArgs(args)
	if err := fs.Parse(flags); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	// Create a minimal service for the HTTP handler.
	inj := arena.NewInjector(nil, nil)
	svc := arena.NewService(inj, nil)
	handler := arena.NewHandler(svc)

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	// Wrap with recovery middleware.
	wrapped := arena.RecoverMiddleware(mux)

	server := &http.Server{
		Addr:         *addr,
		Handler:      wrapped,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	slog.Info("arena: starting HTTP server", "addr", *addr)
	fmt.Printf("Arena server listening on %s\n", *addr)
	fmt.Printf("Endpoints:\n")
	fmt.Printf("  POST /arena/scenario/run       Run a scenario\n")
	fmt.Printf("  POST /arena/scenario/validate   Validate a scenario\n")
	fmt.Printf("  GET  /arena/stats               View statistics\n")
	fmt.Printf("  GET  /arena/history             View action history\n")
	fmt.Printf("  GET  /arena/stream              SSE event stream\n")
	fmt.Printf("  GET  /arena/score               Resilience score\n")
	fmt.Printf("  GET  /arena/metrics             Detailed metrics\n")
	fmt.Printf("  POST /arena/survival            Start survival test (background)\n")
	fmt.Printf("  POST /arena/survival/stop       Stop survival test\n")
	fmt.Printf("  GET  /arena/survival/status     Survival progress\n")
	fmt.Printf("  GET  /arena/flight/timeline     Flight recorder timeline\n")
	fmt.Printf("  GET  /arena/flight/diagnostics  Diagnostic records\n")

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}
	return nil
}

// runSurvival starts a survival test against a remote arena server.
// It sends a start request and polls the status until the test completes,
// is cancelled by the user, or the server stops responding.
// Args:
// args - command-line arguments including --addr, --duration, --interval.
// Returns:
// err - error if the request fails or the server returns an error.
func runSurvival(args []string) error {
	fs := flag.NewFlagSet("survival", flag.ContinueOnError)
	addr := fs.String("addr", "http://localhost:8080", "Arena server address")
	duration := fs.Duration("duration", 5*time.Minute, "Survival test duration")
	interval := fs.Duration("interval", 10*time.Second, "Interval between fault injections")

	flags, _ := separateArgs(args)
	if err := fs.Parse(flags); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	cfg := map[string]any{
		"duration": duration.String(),
		"interval": interval.String(),
	}
	body, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	fmt.Println(strings.Repeat("=", 59))
	fmt.Println("  Arena Survival Mode")
	fmt.Println(strings.Repeat("=", 59))
	fmt.Printf("  Duration: %s  Interval: %s\n", *duration, *interval)
	fmt.Printf("  Server:   %s\n\n", *addr)

	baseURL := strings.TrimRight(*addr, "/")

	// POST to start survival run.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		baseURL+"/arena/survival", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create start request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("start survival: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned status %d", resp.StatusCode)
	}

	fmt.Println("  Survival started. Press Ctrl+C to stop.")
	return pollSurvival(ctx, baseURL)
}

// pollSurvival polls the survival status until completion or interrupt.
// It displays live progress and fetches the final score when done.
// Args:
// ctx - parent context for HTTP requests.
// baseURL - arena server base URL.
// Returns:
// err - nil on normal completion or interrupt, error on network failure.
func pollSurvival(ctx context.Context, baseURL string) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-sigCh:
			fmt.Println("\n  Stopping survival...")
			stopReq, stopErr := http.NewRequestWithContext(ctx, http.MethodPost,
				baseURL+"/arena/survival/stop", nil)
			if stopErr == nil {
				if stopResp, doErr := http.DefaultClient.Do(stopReq); doErr == nil {
					_ = stopResp.Body.Close()
				}
			}
			printFinalScore(baseURL)
			return nil

		case <-ticker.C:
			status := getSurvivalStatus(baseURL)
			if status == nil {
				fmt.Println("  ⚠ Server not responding — retrying...")
				continue
			}

			running, _ := status["running"].(bool)
			actionsRun, _ := status["actions_run"].(float64)
			elapsed, _ := status["elapsed"].(string)

			elapsedStr := elapsed
			if d, parseErr := time.ParseDuration(elapsed); parseErr == nil {
				elapsedStr = d.Truncate(time.Second).String()
			}

			score := getScore(baseURL)
			scoreStr := "-"
			gradeStr := "-"
			if score != nil {
				if s, ok := score["score"].(float64); ok {
					scoreStr = fmt.Sprintf("%.1f", s)
				}
				if g, ok := score["grade"].(string); ok {
					gradeStr = g
				}
			}

			fmt.Printf("  Elapsed: %-12s  Actions: %-4d  Score: %s (%s)\n",
				elapsedStr, int(actionsRun), scoreStr, gradeStr)

			if !running {
				fmt.Println("\n  Survival completed!")
				printFinalScore(baseURL)
				return nil
			}
		}
	}
}

// getSurvivalStatus fetches survival status from the server.
func getSurvivalStatus(baseURL string) map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		baseURL+"/arena/survival/status", nil)
	if err != nil {
		return nil
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()

	var status map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil
	}
	return status
}

// getScore fetches the resilience score from the server.
func getScore(baseURL string) map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		baseURL+"/arena/score", nil)
	if err != nil {
		return nil
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()

	var score map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&score); err != nil {
		return nil
	}
	return score
}

// getMetrics fetches arena metrics from the server.
func getMetrics(baseURL string) map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		baseURL+"/arena/metrics", nil)
	if err != nil {
		return nil
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()

	var metrics map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&metrics); err != nil {
		return nil
	}
	return metrics
}

// printFinalScore fetches and prints the final score and metrics report.
func printFinalScore(baseURL string) {
	score := getScore(baseURL)
	metrics := getMetrics(baseURL)

	fmt.Println()
	fmt.Println(strings.Repeat("=", 59))
	fmt.Println("  Final Report")
	fmt.Println(strings.Repeat("=", 59))

	if score != nil {
		s, _ := score["score"].(float64)
		g, _ := score["grade"].(string)
		rr, _ := score["recovery_rate"].(float64)
		fmt.Printf("  Score:         %.1f (%s)\n", s, g)
		fmt.Printf("  Recovery Rate: %.1f%%\n", rr)

		if av, ok := score["availability_score"].(float64); ok {
			fmt.Printf("  Availability:  %.1f\n", av)
		}
		if cs, ok := score["consistency_score"].(float64); ok {
			fmt.Printf("  Consistency:   %.1f\n", cs)
		}
	}

	if metrics != nil {
		total, _ := metrics["total_actions"].(float64)
		success, _ := metrics["successful_actions"].(float64)
		failed, _ := metrics["failed_actions"].(float64)
		fmt.Printf("\n  Faults:        %.0f\n", total)
		fmt.Printf("  Recovered:     %.0f\n", success)
		fmt.Printf("  Failed:        %.0f\n", failed)

		if avg, ok := metrics["avg_recovery_time"].(string); ok && avg != "" && avg != "0" {
			fmt.Printf("  Avg Recovery:  %s\n", avg)
		}
	}
	fmt.Println()
}

// runInspect inspects arena run results from a remote server.
// It fetches score, metrics, timeline, and diagnostics to produce
// a comprehensive post-mortem report.
// Args:
// args - command-line arguments including --addr and display flags.
// Returns:
// err - error if the request fails or the server returns an error.
func runInspect(args []string) error {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	addr := fs.String("addr", "http://localhost:8080", "Arena server address")
	showTimeline := fs.Bool("timeline", true, "Show timeline events in report")
	showDiagnostics := fs.Bool("diagnostics", true, "Show diagnostics breakdown")

	flags, _ := separateArgs(args)
	if err := fs.Parse(flags); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	baseURL := strings.TrimRight(*addr, "/")

	fmt.Println(strings.Repeat("=", 59))
	fmt.Println("  Arena Inspection Report")
	fmt.Println(strings.Repeat("=", 59))

	// Score.
	score := getScore(baseURL)
	if score != nil {
		s, _ := score["score"].(float64)
		g, _ := score["grade"].(string)
		rr, _ := score["recovery_rate"].(float64)
		totalF, _ := score["total_faults"].(float64)
		recF, _ := score["recovered_faults"].(float64)
		failF, _ := score["failed_faults"].(float64)

		fmt.Printf("\n  Score:          %.1f (%s)\n", s, g)
		fmt.Printf("  Recovery Rate:  %.1f%%\n", rr)
		fmt.Printf("  Faults:         %.0f total, %.0f recovered, %.0f failed\n",
			totalF, recF, failF)

		if av, ok := score["availability_score"].(float64); ok {
			fmt.Printf("  Availability:   %.1f\n", av)
		}
		if cs, ok := score["consistency_score"].(float64); ok {
			fmt.Printf("  Consistency:    %.1f\n", cs)
		}
	} else {
		fmt.Println("  ⚠ Score data unavailable")
	}

	// Metrics.
	metrics := getMetrics(baseURL)
	if metrics != nil {
		fmt.Print("\n  Metrics:\n")
		if avg, ok := metrics["avg_recovery_time"].(string); ok && avg != "" && avg != "0" {
			fmt.Printf("    Avg Recovery Time: %s\n", avg)
		}
		if minR, ok := metrics["min_recovery_time"].(string); ok && minR != "" {
			fmt.Printf("    Min Recovery Time: %s\n", minR)
		}
		if maxR, ok := metrics["max_recovery_time"].(string); ok && maxR != "" {
			fmt.Printf("    Max Recovery Time: %s\n", maxR)
		}
		if fc, ok := metrics["failover_count"].(float64); ok && fc > 0 {
			fmt.Printf("    Failovers:         %.0f\n", fc)
		}
		if dr, ok := metrics["data_consistency_rate"].(float64); ok && dr > 0 {
			fmt.Printf("    Data Consistency:  %.1f%%\n", dr)
		}
	}

	// Timeline.
	if *showTimeline {
		printInspectTimeline(baseURL)
	}

	// Diagnostics.
	if *showDiagnostics {
		printInspectDiagnostics(baseURL)
	}

	fmt.Println()
	return nil
}

// printInspectTimeline fetches and prints arena timeline events.
func printInspectTimeline(baseURL string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		baseURL+"/arena/flight/timeline", nil)
	if err != nil {
		return
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Println("  ⚠ Timeline data unavailable")
		return
	}
	defer func() { _ = resp.Body.Close() }()

	var events []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil || len(events) == 0 {
		return
	}

	fmt.Printf("\n  Timeline Events: %d\n", len(events))

	// Show last 10 events.
	start := 0
	if len(events) > 10 {
		start = len(events) - 10
	}
	for i := start; i < len(events); i++ {
		ev := events[i]
		name, _ := ev["name"].(string)
		success, _ := ev["metadata"].(map[string]any)["success"].(bool)
		actionType, _ := ev["metadata"].(map[string]any)["action_type"].(string)
		icon := "✓"
		if !success {
			icon = "✗"
		}

		label := name
		if actionType != "" {
			label = actionType
		}
		fmt.Printf("    %s %s\n", icon, label)
	}
}

// printInspectDiagnostics fetches and prints arena diagnostic records.
func printInspectDiagnostics(baseURL string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		baseURL+"/arena/flight/diagnostics", nil)
	if err != nil {
		return
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Println("  ⚠ Diagnostics data unavailable")
		return
	}
	defer func() { _ = resp.Body.Close() }()

	var diags []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&diags); err != nil || len(diags) == 0 {
		fmt.Println("\n  Diagnostics: none (no failures recorded)")
		return
	}

	// Categorize by category.
	categories := make(map[string]int)
	for _, d := range diags {
		cat, _ := d["category"].(string)
		if cat == "" {
			cat = "unknown"
		}
		categories[cat]++
	}

	fmt.Printf("\n  Diagnostics: %d failures\n", len(diags))
	fmt.Println("  Category Breakdown:")

	total := len(diags)
	for cat, count := range categories {
		pct := float64(count) / float64(total) * 100
		fmt.Printf("    %-25s %d (%5.1f%%)\n", cat, count, pct)
	}

	// Show first 3 suggestions.
	fmt.Println("\n  Top Suggestions:")
	shown := 0
	for _, d := range diags {
		if shown >= 3 {
			break
		}
		if sug, ok := d["suggestion"].(string); ok && sug != "" {
			cat, _ := d["category"].(string)
			agent, _ := d["agent_id"].(string)
			fmt.Printf("    [%s] %s: %s\n", cat, agent, sug)
			shown++
		}
	}
}

// printReport prints a human-readable scenario execution report.
func printReport(report *arena.ScenarioReport) {
	fmt.Println("=" + strings.Repeat("=", 59))
	fmt.Printf("  Scenario Report: %s\n", report.ScenarioName)
	fmt.Println("=" + strings.Repeat("=", 59))

	if report.Description != "" {
		fmt.Printf("  Description: %s\n", report.Description)
	}

	fmt.Printf("  Started:    %s\n", report.StartedAt.Format(time.RFC3339))
	fmt.Printf("  Finished:   %s\n", report.FinishedAt.Format(time.RFC3339))
	fmt.Printf("  Duration:   %s\n", report.Duration.Truncate(time.Millisecond))
	fmt.Println()
	fmt.Printf("  Results:    %d passed, %d failed\n",
		report.Passed, report.Failed)
	fmt.Printf("  Score:      %.1f (%s)\n", report.Score.Score, report.Score.Grade)
	fmt.Printf("  Verified:   %t\n", report.Verified)
	fmt.Println()

	if len(report.Results) > 0 {
		fmt.Println("  Action Details:")
		fmt.Println("  " + strings.Repeat("-", 59))
		for i, r := range report.Results {
			status := "✅ PASS"
			if !r.Success {
				status = "❌ FAIL"
			}
			actionType := string(r.Action.Type)
			label := ""
			if r.Action.Metadata != nil {
				if l, ok := r.Action.Metadata["label"].(string); ok {
					label = l
				}
			}
			if label != "" {
				fmt.Printf("    %d. [%s] %s (%s) - %s\n",
					i+1, status, actionType, label, r.Duration.Truncate(time.Millisecond))
			} else {
				fmt.Printf("    %d. [%s] %s - %s\n",
					i+1, status, actionType, r.Duration.Truncate(time.Millisecond))
			}
			if r.Error != "" {
				fmt.Printf("       Error: %s\n", r.Error)
			}
		}
	}

	fmt.Println()
	fmt.Printf("  Recovery Rate: %.1f%%\n", report.Score.RecoveryRate)
	if report.Score.AvgRecoveryTime > 0 {
		fmt.Printf("  Avg Recovery: %s\n", report.Score.AvgRecoveryTime.Truncate(time.Millisecond))
	}
	fmt.Println()
}
