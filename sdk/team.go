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
	// ModeAutoSplit instructs the leader to automatically break the task
	// into sub-tasks and delegate them to members.
	ModeAutoSplit RunMode = iota
	// ModeExplicit uses pre-configured GroupConfig assignments. The leader
	// skips planning and routes each group's task directly.
	ModeExplicit
)

// ── Group config ───────────────────────────────────────────────────────────

// GroupConfig assigns a specific task description to a subset of team members.
type GroupConfig struct {
	// Name is a human-readable label (e.g. "data-collectors").
	Name string
	// Indices are the 0-based member indices belonging to this group.
	// Example: []int{0,1,2} for members[0..2].
	Indices []int
	// Task describes what this group should do. In ModeAutoSplit this is
	// optional (the leader generates it); in ModeExplicit it is required.
	Task string
}

// ── Team config ────────────────────────────────────────────────────────────

// TeamConfig controls the team orchestration behaviour.
type TeamConfig struct {
	// Mode selects auto-split or explicit assignment.
	Mode RunMode
	// Groups defines explicit sub-task assignments (ModeExplicit only).
	Groups []GroupConfig
	// VerifierIndex is the 0-based member index of the verifier agent.
	// Set to -1 to skip verification.
	VerifierIndex int
	// MaxConcurrency caps the number of members that run simultaneously.
	// 0 means unlimited (all members at once).
	MaxConcurrency int
}

// DefaultTeamConfig returns a sensible default: auto-split, no verifier,
// unlimited concurrency.
func DefaultTeamConfig() TeamConfig {
	return TeamConfig{
		Mode:           ModeAutoSplit,
		VerifierIndex:  -1,
		MaxConcurrency: 0,
	}
}

// ── Team ───────────────────────────────────────────────────────────────────

// Team orchestrates a leader agent and multiple member agents.
// The leader plans tasks, delegates to members, and synthesizes results.
// With TeamConfig you can control concurrency, grouping, and verification.
type Team struct {
	name    string
	leader  *Agent
	members []*Agent
	runtime *Runtime
	cfg     TeamConfig
}

// NewTeam creates a Team with a leader and member agents.
// Use WithTeamConfig to control orchestration behaviour.
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

// Run executes the team orchestration.
//
// In ModeAutoSplit:
//  1. Leader plans the task.
//  2. Sub-tasks are delegated to members (optionally grouped).
//  3. Groups execute concurrently.
//  4. Verifier reviews results (if configured).
//  5. Leader synthesises the final result.
//
// In ModeExplicit:
//  1. Pre-configured groups receive their tasks directly.
//  2. Groups execute concurrently.
//  3. Verifier reviews results (if configured).
//  4. Leader synthesises the final result.
func (t *Team) Run(ctx context.Context, input string) (*TeamResult, error) {
	start := time.Now()

	if t.runtime.trace {
		log.Printf("[team:trace] %s → mode=%v input=%q", t.name, t.cfg.Mode, truncate(input, 120))
	}

	// ── 1. Plan ──────────────────────────────────────────────────
	plan, err := t.plan(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("team plan: %w", err)
	}

	// ── 2. Build sub-task assignments ────────────────────────────
	assignments, err := t.buildAssignments(ctx, input, plan)
	if err != nil {
		return nil, fmt.Errorf("team build assignments: %w", err)
	}

	// ── 3. Execute concurrently ─────────────────────────────────
	subResults := t.executeConcurrent(ctx, assignments)

	// ── 4. Verify ────────────────────────────────────────────────
	verification, passed := t.verify(ctx, input, plan, subResults)

	// ── 5. Synthesise ────────────────────────────────────────────
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

// ── Plan ───────────────────────────────────────────────────────────────────

func (t *Team) plan(ctx context.Context, input string) (string, error) {
	if t.cfg.Mode == ModeExplicit {
		// In explicit mode the leader still produces a lightweight
		// coordination plan, but groups already know their tasks.
		groupSummary := make([]string, len(t.cfg.Groups))
		for i, g := range t.cfg.Groups {
			names := make([]string, len(g.Indices))
			for j, idx := range g.Indices {
				if idx < len(t.members) {
					names[j] = t.members[idx].name
				}
			}
			groupSummary[i] = fmt.Sprintf("Group %q (%s): %s", g.Name, strings.Join(names, ", "), g.Task)
		}
		return strings.Join(groupSummary, "\n"), nil
	}

	// Auto-split: leader plans the task.
	memberNames := make([]string, len(t.members))
	for i, m := range t.members {
		memberNames[i] = m.name
	}

	planPrompt := fmt.Sprintf(
		`You are the team leader "%s". Break down the following task into sub-tasks for your team members: %s.
Task: %s
Respond with a numbered plan, one sub-task per member.`, t.name, strings.Join(memberNames, ", "), input)

	planResp, err := t.runtime.llmSvc.Generate(ctx, &core.GenerateRequest{
		Messages: []*core.LLMMessage{
			{Role: roleSystem, Content: t.leader.instruction},
			{Role: roleUser, Content: planPrompt},
		},
	})
	if err != nil {
		return "", err
	}
	return planResp.Content, nil
}

// ── Build assignments ──────────────────────────────────────────────────────

type memberAssignment struct {
	member *Agent
	task   string
}

func (t *Team) buildAssignments(ctx context.Context, input, plan string) ([]memberAssignment, error) {
	if t.cfg.Mode == ModeExplicit && len(t.cfg.Groups) > 0 {
		var assignments []memberAssignment
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
		return assignments, nil
	}

	// Auto-split: each member gets the plan and their index.
	var assignments []memberAssignment
	for i, member := range t.members {
		// Skip verifier during delegation.
		if i == t.cfg.VerifierIndex {
			continue
		}
		taskPrompt := fmt.Sprintf(
			`You are "%s". Execute your assigned part of the plan.
Plan:
%s

Your task (#%d): Focus on what you can contribute based on the plan above.`,
			member.name, plan, i+1)
		assignments = append(assignments, memberAssignment{member: member, task: taskPrompt})
	}
	return assignments, nil
}

// ── Concurrent execution ───────────────────────────────────────────────────

func (t *Team) executeConcurrent(ctx context.Context, assignments []memberAssignment) []SubResult {
	var (
		mu       sync.Mutex
		results  = make([]SubResult, 0, len(assignments))
		sem      = make(chan struct{}, t.semaphoreSize())
		eg, ectx = errgroup.WithContext(ctx)
	)

	for _, a := range assignments {
		a := a // capture
		eg.Go(func() error {
			sem <- struct{}{}
			defer func() { <-sem }()

			subStart := time.Now()
			if t.runtime.trace {
				log.Printf("[team:trace] %s → executing %s", t.name, a.member.name)
			}

			resp, err := t.runtime.llmSvc.Generate(ectx, &core.GenerateRequest{
				Messages: []*core.LLMMessage{
					{Role: roleSystem, Content: a.member.instruction},
					{Role: roleUser, Content: a.task},
				},
				Tools: a.member.toCoreTools(a.member.tools),
			})

			mu.Lock()
			sr := SubResult{
				MemberName: a.member.name,
				Duration:   time.Since(subStart).Round(time.Millisecond).String(),
			}
			if err != nil {
				sr.Error = err.Error()
			} else {
				sr.Output = resp.Content
			}
			results = append(results, sr)
			mu.Unlock()
			return nil
		})
	}

	_ = eg.Wait() // Collect all errors; individual errors are recorded per SubResult.
	return results
}

func (t *Team) semaphoreSize() int {
	if t.cfg.MaxConcurrency <= 0 {
		return len(t.members)
	}
	return t.cfg.MaxConcurrency
}

// ── Verification ───────────────────────────────────────────────────────────

func (t *Team) verify(ctx context.Context, input, plan string, results []SubResult) (string, bool) {
	if t.cfg.VerifierIndex < 0 || t.cfg.VerifierIndex >= len(t.members) {
		return "", true
	}

	verifier := t.members[t.cfg.VerifierIndex]

	var b strings.Builder
	b.WriteString("Review the following team execution results and decide if they pass.\n\n")
	b.WriteString("Original task: ")
	b.WriteString(input)
	b.WriteString("\n\nPlan:\n")
	b.WriteString(plan)
	b.WriteString("\n\nResults:\n")
	for _, r := range results {
		b.WriteString(fmt.Sprintf("\n[%s]", r.MemberName))
		if r.Error != "" {
			b.WriteString(fmt.Sprintf(" ERROR: %s", r.Error))
		} else {
			b.WriteString(fmt.Sprintf(" %s", truncate(r.Output, 300)))
		}
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

// ── Synthesize ─────────────────────────────────────────────────────────────

func (t *Team) synthesize(ctx context.Context, input, plan string, results []SubResult, verification string) (string, error) {
	// Collect output lines.
	var b strings.Builder
	b.WriteString("Sub-results:\n")
	for _, r := range results {
		b.WriteString(fmt.Sprintf("\n[%s]", r.MemberName))
		if r.Error != "" {
			b.WriteString(fmt.Sprintf(" ERROR: %s", r.Error))
		} else {
			b.WriteString(fmt.Sprintf(" %s", r.Output))
		}
	}
	if verification != "" {
		b.WriteString(fmt.Sprintf("\n\nVerification:\n%s", verification))
	}
	subResultsBlock := b.String()

	synthPrompt := fmt.Sprintf(
		`You are the team leader "%s". Synthesize the following sub-results into a final answer.

Original task: %s

Plan:
%s

%s

Provide a concise, unified final answer.`,
		t.name, input, plan, subResultsBlock)

	synthResp, err := t.runtime.llmSvc.Generate(ctx, &core.GenerateRequest{
		Messages: []*core.LLMMessage{
			{Role: roleSystem, Content: t.leader.instruction},
			{Role: roleUser, Content: synthPrompt},
		},
	})
	if err != nil {
		return "", err
	}
	return synthResp.Content, nil
}

// ── Helpers ────────────────────────────────────────────────────────────────

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}