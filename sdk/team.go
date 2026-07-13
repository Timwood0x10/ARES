// Package sdk provides the top-level ARES agent runtime.
package sdk

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/Timwood0x10/ares/api/core"
)

// ── Run modes ──────────────────────────────────────────────────────────────

// RunMode defines how the team assigns sub-tasks to its members.
type RunMode int

const (
	// ModeAutoSplit instructs the leader to automatically discover files,
	// divide them among members, and verify results.
	ModeAutoSplit RunMode = iota
	// ModeExplicit uses pre-configured GroupConfig assignments.
	ModeExplicit
)

// ── Group config ───────────────────────────────────────────────────────────

// GroupConfig assigns a specific task to a subset of team members.
type GroupConfig struct {
	Name    string
	Indices []int
	Task    string
}

// ── Team config ────────────────────────────────────────────────────────────

// TeamConfig controls the team orchestration behaviour.
type TeamConfig struct {
	Mode           RunMode
	Groups         []GroupConfig
	VerifierIndex  int // -1 to skip verification
	MaxConcurrency int // 0 = unlimited
}

// DefaultTeamConfig returns sensible defaults.
func DefaultTeamConfig() TeamConfig {
	return TeamConfig{
		Mode:           ModeAutoSplit,
		VerifierIndex:  -1,
		MaxConcurrency: 0,
	}
}

// ── Team ───────────────────────────────────────────────────────────────────

// Team orchestrates a leader agent and multiple member agents.
// In ModeAutoSplit:
//  1. Leader runs with tools to discover files and plan.
//  2. File list is divided among members, who run concurrently.
//  3. Verifier reviews results (if configured).
//  4. Leader synthesises final output.
type Team struct {
	name    string
	leader  *Agent
	members []*Agent
	runtime *Runtime
	cfg     TeamConfig
}

// NewTeam creates a Team with a leader and member agents.
func (r *Runtime) NewTeam(name string, leader *Agent, members []*Agent) *Team {
	return &Team{
		name:    name,
		leader:  leader,
		members: members,
		runtime: r,
		cfg:     DefaultTeamConfig(),
	}
}

// WithTeamConfig applies a TeamConfig to the team.
func (t *Team) WithTeamConfig(cfg TeamConfig) *Team {
	t.cfg = cfg
	return t
}

// ── Result types ───────────────────────────────────────────────────────────

// SubResult holds the outcome of a single member execution.
type SubResult struct {
	MemberName string `json:"member_name"`
	Output     string `json:"output"`
	Error      string `json:"error,omitempty"`
	Duration   string `json:"duration,omitempty"`
}

// TeamResult holds the outcome of a full team execution.
type TeamResult struct {
	Plan         string        `json:"plan"`
	SubResults   []SubResult   `json:"sub_results"`
	Verification string        `json:"verification,omitempty"`
	Output       string        `json:"output"`
	Duration     time.Duration `json:"duration"`
	Passed       bool          `json:"passed"`
}

// ── Run ────────────────────────────────────────────────────────────────────

// Run executes the team orchestration:
//
// Phase 1 — Leader runs with tools to discover and plan.
// Phase 2 — Members execute concurrently with their assigned work.
// Phase 3 — Verifier checks results (if configured).
// Phase 4 — Leader synthesises final output.
func (t *Team) Run(ctx context.Context, input string) (*TeamResult, error) {
	start := time.Now()

	if t.runtime.trace {
		log.Printf("[team:trace] %s → mode=%v input=%q", t.name, t.cfg.Mode, truncate(input, 120))
	}

	// ── Phase 1: Leader discovers and plans ──────────────────────
	plan, fileList, err := t.leaderDiscover(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("team discover: %w", err)
	}

	// ── Phase 2: Build sub-task assignments from file list ───────
	assignments := t.buildAssignments(input, fileList)

	// ── Phase 3: Execute concurrently ────────────────────────────
	subResults := t.executeConcurrent(ctx, assignments)

	// ── Phase 4: Verify ──────────────────────────────────────────
	verification, passed := t.verify(ctx, input, fileList, subResults)

	// ── Phase 5: Synthesise ──────────────────────────────────────
	output, err := t.synthesize(ctx, input, plan, subResults, verification)
	if err != nil {
		return nil, fmt.Errorf("team synthesize: %w", err)
	}

	if t.runtime.trace {
		log.Printf("[team:trace] %s ✓ done (%v)", t.name, time.Since(start).Round(time.Millisecond))
	}

	return &TeamResult{
		Plan:         plan,
		SubResults:   subResults,
		Verification: verification,
		Output:       output,
		Duration:     time.Since(start),
		Passed:       passed,
	}, nil
}

// ── Phase 1: Leader discover ───────────────────────────────────────────────

// leaderDiscover runs the leader as a real agent (with tool execution) to
// discover files and produce a plan. Returns the plan text and the parsed
// file list.
func (t *Team) leaderDiscover(ctx context.Context, input string) (plan string, files []string, err error) {
	if t.cfg.Mode == ModeExplicit {
		return t.explicitPlan(input)
	}

	// Run the leader with its tools to discover files.
	leaderResult, err := t.leader.Run(ctx, input)
	if err != nil {
		return "", nil, fmt.Errorf("leader run: %w", err)
	}
	plan = leaderResult.Output

	// Parse the leader's output to extract file paths.
	// The leader should have called read_directory and listed files.
	// We extract any lines that look like file paths (contain .md).
	files = extractFilePaths(plan)

	// If no files found in the leader's output, use the raw input.
	if len(files) == 0 {
		if t.runtime.trace {
			log.Printf("[team:trace] %s → leader returned no files, using raw input", t.name)
		}
	}

	return plan, files, nil
}

func (t *Team) explicitPlan(input string) (string, []string, error) {
	var b strings.Builder
	for _, g := range t.cfg.Groups {
		names := make([]string, len(g.Indices))
		for j, idx := range g.Indices {
			if idx < len(t.members) {
				names[j] = t.members[idx].name
			}
		}
		b.WriteString(fmt.Sprintf("Group %q (%s): %s\n", g.Name, strings.Join(names, ", "), g.Task))
	}
	return b.String(), nil, nil
}

// ── Phase 2: Build assignments ─────────────────────────────────────────────

func (t *Team) buildAssignments(input string, files []string) []memberAssignment {
	var assignments []memberAssignment

	if t.cfg.Mode == ModeExplicit && len(t.cfg.Groups) > 0 {
		for _, g := range t.cfg.Groups {
			for _, idx := range g.Indices {
				if idx >= len(t.members) {
					continue
				}
				assignments = append(assignments, memberAssignment{
					member: t.members[idx],
					task:   g.Task,
				})
			}
		}
		return assignments
	}

	// Auto-split: divide files among members, skipping the verifier.
	available := make([]*Agent, 0, len(t.members))
	for i, m := range t.members {
		if i != t.cfg.VerifierIndex {
			available = append(available, m)
		}
	}

	if len(files) == 0 {
		// No files found — each member works on the original input.
		for _, m := range available {
			assignments = append(assignments, memberAssignment{member: m, task: input})
		}
		return assignments
	}

	// Distribute files round-robin.
	for i, f := range files {
		idx := i % len(available)
		task := fmt.Sprintf("Read and import the file: %s\n\nOriginal task: %s", f, input)
		assignments = append(assignments, memberAssignment{member: available[idx], task: task})
	}

	return assignments
}

type memberAssignment struct {
	member *Agent
	task   string
}

// ── Phase 3: Concurrent execution ──────────────────────────────────────────

func (t *Team) executeConcurrent(ctx context.Context, assignments []memberAssignment) []SubResult {
	var (
		mu       sync.Mutex
		results  = make([]SubResult, 0, len(assignments))
		sem      = make(chan struct{}, t.semaphoreSize())
		eg, ectx = errgroup.WithContext(ctx)
	)

	for _, a := range assignments {
		a := a
		eg.Go(func() error {
			sem <- struct{}{}
			defer func() { <-sem }()

			subStart := time.Now()
			if t.runtime.trace {
				log.Printf("[team:trace] %s → executing %s", t.name, a.member.name)
			}

			messages := []*core.LLMMessage{
				{Role: roleSystem, Content: a.member.instruction},
				{Role: roleUser, Content: a.task},
			}
			coreTools := a.member.toCoreTools(a.member.tools)

			resp, err := t.runtime.llmSvc.Generate(ectx, &core.GenerateRequest{
				Messages: messages,
				Tools:    coreTools,
			})

			var output string
			if err != nil {
				output = fmt.Sprintf("Error: %v", err)
			} else {
				output = resp.Content
				for _, tc := range resp.ToolCalls {
					if t.runtime.trace {
						log.Printf("[team:trace] %s → tool call: %s(%s)", t.name, tc.Function.Name, tc.Function.Arguments)
					}
					args := parseArgs(tc.Function.Arguments)
					result, tErr := t.runtime.toolReg.Execute(ectx, tc.Function.Name, args)
					if tErr != nil {
						output += fmt.Sprintf("\n[tool %s] error: %v", tc.Function.Name, tErr)
					} else {
						output += fmt.Sprintf("\n[tool %s] %v", tc.Function.Name, result.Data)
					}
				}
			}

			mu.Lock()
			results = append(results, SubResult{
				MemberName: a.member.name,
				Output:     output,
				Duration:   time.Since(subStart).Round(time.Millisecond).String(),
			})
			mu.Unlock()
			return nil
		})
	}

	_ = eg.Wait()
	return results
}

func (t *Team) semaphoreSize() int {
	if t.cfg.MaxConcurrency <= 0 {
		return len(t.members)
	}
	return t.cfg.MaxConcurrency
}

// ── Phase 4: Verification ──────────────────────────────────────────────────

func (t *Team) verify(ctx context.Context, input string, files []string, results []SubResult) (string, bool) {
	if t.cfg.VerifierIndex < 0 || t.cfg.VerifierIndex >= len(t.members) {
		return "", true
	}

	verifier := t.members[t.cfg.VerifierIndex]

	var b strings.Builder
	b.WriteString("Review the following team execution results.\n\n")
	b.WriteString("Original task: ")
	b.WriteString(input)
	if len(files) > 0 {
		b.WriteString("\n\nFiles to import:\n")
		for _, f := range files {
			fmt.Fprintf(&b, "  - %s\n", f)
		}
	}
	b.WriteString("\n\nResults:\n")
	for _, r := range results {
		d := r.Output
		if len(d) > 300 {
			d = d[:300] + "..."
		}
		fmt.Fprintf(&b, "\n[%s]\n%s", r.MemberName, d)
	}
	b.WriteString("\n\nRespond with PASS or FAIL followed by your reasoning.")

	resp, err := t.runtime.llmSvc.Generate(ctx, &core.GenerateRequest{
		Messages: []*core.LLMMessage{
			{Role: roleSystem, Content: verifier.instruction},
			{Role: roleUser, Content: b.String()},
		},
	})
	if err != nil {
		return fmt.Sprintf("verification error: %v", err), false
	}

	content := resp.Content
	passed := strings.Contains(strings.ToUpper(content), "PASS") &&
		!strings.Contains(strings.ToUpper(content), "FAIL")
	return content, passed
}

// ── Phase 5: Synthesize ────────────────────────────────────────────────────

func (t *Team) synthesize(ctx context.Context, input, plan string, results []SubResult, verification string) (string, error) {
	var b strings.Builder
	b.WriteString("Sub-results:\n")
	for _, r := range results {
		fmt.Fprintf(&b, "\n[%s]", r.MemberName)
		if r.Error != "" {
			fmt.Fprintf(&b, " ERROR: %s", r.Error)
		} else {
			fmt.Fprintf(&b, " %s", r.Output)
		}
	}
	if verification != "" {
		fmt.Fprintf(&b, "\n\nVerification:\n%s", verification)
	}

	synthResp, err := t.runtime.llmSvc.Generate(ctx, &core.GenerateRequest{
		Messages: []*core.LLMMessage{
			{Role: roleSystem, Content: t.leader.instruction},
			{Role: roleUser, Content: fmt.Sprintf(
				"Synthesize the following sub-results into a final answer.\n\nOriginal task: %s\n\n%s",
				input, b.String())},
		},
	})
	if err != nil {
		return "", err
	}
	return synthResp.Content, nil
}

// ── Helpers ────────────────────────────────────────────────────────────────

func extractFilePaths(s string) []string {
	lines := strings.Split(s, "\n")
	var files []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Look for lines containing .md (likely file paths from read_directory).
		if strings.Contains(line, ".md") && !strings.HasPrefix(line, "[") && !strings.HasPrefix(line, "(") {
			// Extract the path part before " (N bytes)" if present.
			if idx := strings.Index(line, " ("); idx > 0 {
				line = line[:idx]
			}
			// Clean up markdown list markers.
			line = strings.TrimLeft(line, "- *")
			line = strings.TrimSpace(line)
			if line != "" && strings.Contains(line, ".") {
				files = append(files, line)
			}
		}
	}
	return files
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
