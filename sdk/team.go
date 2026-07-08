package sdk

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/api/core"
)

// Team orchestrates a leader agent and multiple member agents.
// The leader plans tasks, delegates to members, and synthesizes results.
type Team struct {
	name    string
	leader  *Agent
	members []*Agent
	runtime *Runtime
}

// NewTeam creates a Team with a leader and member agents.
func (r *Runtime) NewTeam(name string, leader *Agent, members []*Agent) *Team {
	return &Team{
		name:    name,
		leader:  leader,
		members: members,
		runtime: r,
	}
}

// TeamResult holds the outcome of a team execution.
type TeamResult struct {
	Plan       string        `json:"plan"`
	SubResults []string      `json:"sub_results"`
	Output     string        `json:"output"`
	Duration   time.Duration `json:"duration"`
}

// Run executes the team orchestration:
//
//  1. Leader plans the task.
//  2. Sub-tasks are delegated to members.
//  3. Results are collected.
//  4. Leader synthesizes the final result.
func (t *Team) Run(ctx context.Context, input string) (*TeamResult, error) {
	start := time.Now()

	if t.runtime.trace {
		log.Printf("[team:trace] %s → planning for: %s", t.name, input)
	}

	// ── 1. Leader plans ────────────────────────────────────────
	memberNames := make([]string, len(t.members))
	for i, m := range t.members {
		memberNames[i] = m.name
	}

	planPrompt := fmt.Sprintf(
		`You are the team leader "%s". Break down the following task into sub-tasks for your team members: %s.
Task: %s
Respond with a numbered plan, one sub-task per member.`, t.name, strings.Join(memberNames, ", "), input)

	planMsg := []*core.LLMMessage{
		{Role: roleSystem, Content: t.leader.instruction},
		{Role: roleUser, Content: planPrompt},
	}

	planResp, err := t.runtime.llmSvc.Generate(ctx, &core.GenerateRequest{
		Messages: planMsg,
	})
	if err != nil {
		return nil, fmt.Errorf("team plan: %w", err)
	}
	plan := planResp.Content

	if t.runtime.trace {
		log.Printf("[team:trace] %s → plan:\n%s", t.name, plan)
	}

	// ── 2. Delegate to members ─────────────────────────────────
	var subResults []string
	for i, member := range t.members {
		taskPrompt := fmt.Sprintf(
			`You are "%s". Execute your assigned part of the plan.
Plan:
%s

Your task (#%d): Focus on what you can contribute based on the plan above.`,
			member.name, plan, i+1)

		taskMsg := []*core.LLMMessage{
			{Role: roleSystem, Content: member.instruction},
			{Role: roleUser, Content: taskPrompt},
		}

		if t.runtime.trace {
			log.Printf("[team:trace] %s → delegating to %s", t.name, member.name)
		}

		resp, err := t.runtime.llmSvc.Generate(ctx, &core.GenerateRequest{
			Messages: taskMsg,
			Tools:    member.toCoreTools(member.tools),
		})
		if err != nil {
			subResults = append(subResults, fmt.Sprintf("[%s] error: %v", member.name, err))
			continue
		}
		subResults = append(subResults, fmt.Sprintf("[%s] %s", member.name, resp.Content))
	}

	// ── 3. Leader synthesizes ──────────────────────────────────
	synthPrompt := fmt.Sprintf(
		`You are the team leader "%s". Synthesize the following sub-results into a final answer.

Original task: %s

Plan:
%s

Sub-results:
%s

Provide a concise, unified final answer.`,
		t.name, input, plan, strings.Join(subResults, "\n"))

	synthMsg := []*core.LLMMessage{
		{Role: roleSystem, Content: t.leader.instruction},
		{Role: roleUser, Content: synthPrompt},
	}

	synthResp, err := t.runtime.llmSvc.Generate(ctx, &core.GenerateRequest{
		Messages: synthMsg,
	})
	if err != nil {
		return nil, fmt.Errorf("team synthesize: %w", err)
	}

	if t.runtime.trace {
		log.Printf("[team:trace] %s ✓ done (%v)", t.name, time.Since(start).Round(time.Millisecond))
	}

	return &TeamResult{
		Plan:       plan,
		SubResults: subResults,
		Output:     synthResp.Content,
		Duration:   time.Since(start),
	}, nil
}
