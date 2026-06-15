// Command arena provides the Chaos Engineering Arena CLI.
//
// Usage:
//
//	goagentx arena run <scenario.yaml> [--addr=localhost:8080]
//	goagentx arena validate <scenario.yaml>
//	goagentx arena list [dir]
//	goagentx arena serve [--addr=:8080]
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"goagentx/internal/arena"
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
	fmt.Fprintf(os.Stderr, "Usage: goagentx arena <command> [options]\n\n")
	fmt.Fprintf(os.Stderr, "Commands:\n")
	fmt.Fprintf(os.Stderr, "  run       Run a scenario file (remote mode via HTTP)\n")
	fmt.Fprintf(os.Stderr, "  validate  Validate a scenario file\n")
	fmt.Fprintf(os.Stderr, "  list      List available scenarios in a directory\n")
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
	defer resp.Body.Close()

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
	defer resp.Body.Close()

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

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}
	return nil
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
	fmt.Printf("  Results:    %d passed, %d failed, %d skipped\n",
		report.Passed, report.Failed, report.Skipped)
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
