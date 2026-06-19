// Package workflow provides the Bull/Bear debate loop for quant analysis.
// The debate runs N rounds: Bull argues → Bear rebuts → Bull counters → ...
// Each round injects the opponent's previous output as context.
package workflow

import (
	"fmt"
	"strings"
	"time"

	"goagentx/internal/dashboard"
)

// DebateConfig controls the debate loop.
type DebateConfig struct {
	Ticker      string
	Rounds      int
	AnalystData string // Phase 0 analyst outputs, injected into Round 1
}

// DebateResult holds the final outputs after all rounds.
type DebateResult struct {
	BullFinal string
	BearFinal string
	RoundsRun int
}

// RunDebate executes N rounds of Bull/Bear debate via the orchestrator.
// Each round, one agent sees the other's previous output and rebuts.
func RunDebate(orch *dashboard.Orchestrator, cfg DebateConfig) (*DebateResult, error) {
	if cfg.Rounds <= 0 {
		cfg.Rounds = 2
	}

	var bullOutput, bearOutput string

	for round := 1; round <= cfg.Rounds; round++ {
		// Bull argues or counters bear's last argument.
		prompt := buildBullPrompt(cfg.Rounds, cfg.Rounds, cfg.AnalystData, bearOutput)
		out, err := createAndWait(orch, agentName("Bull Researcher", round, cfg.Rounds), prompt)
		if err != nil {
			return nil, fmt.Errorf("debate round %d bull: %w", round, err)
		}
		bullOutput = out

		if round == cfg.Rounds {
			break
		}

		// Bear rebuts.
		prompt = buildBearPrompt(cfg.Rounds, cfg.Rounds, cfg.AnalystData, bullOutput)
		out, err = createAndWait(orch, agentName("Bear Researcher", round, cfg.Rounds), prompt)
		if err != nil {
			return nil, fmt.Errorf("debate round %d bear: %w", round, err)
		}
		bearOutput = out
	}

	return &DebateResult{
		BullFinal: bullOutput,
		BearFinal: bearOutput,
		RoundsRun: cfg.Rounds,
	}, nil
}

func agentName(role string, round, total int) string {
	return fmt.Sprintf("%s (Round %d/%d)", role, round, total)
}

// createAndWait creates one agent and polls until completion.
func createAndWait(orch *dashboard.Orchestrator, name, prompt string) (string, error) {
	req := dashboard.AgentRequest{
		Name:      name,
		Target:    name,
		LLMPrompt: prompt,
	}
	id, err := orch.CreateAgent(req)
	if err != nil {
		return "", fmt.Errorf("create: %w", err)
	}
	for i := 0; i < 60; i++ {
		ag, ok := orch.GetAgent(id)
		if !ok {
			time.Sleep(time.Second)
			continue
		}
		switch ag.Status {
		case "completed":
			return ag.Analysis, nil
		case "failed":
			return ag.Analysis, fmt.Errorf("failed: %s", ag.Error)
		}
		time.Sleep(time.Second)
	}
	ag, _ := orch.GetAgent(id)
	if ag != nil {
		return ag.Analysis, fmt.Errorf("timeout (status: %s)", ag.Status)
	}
	return "", fmt.Errorf("timeout")
}

// buildBullPrompt constructs the Bull Researcher prompt.
func buildBullPrompt(totalRounds, round int, analystData, opponentArg string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are a Bull Researcher. Build the strongest bullish case.\n\n")
	fmt.Fprintf(&b, "  Round %d/%d of structured debate.\n", round, totalRounds)

	if analystData != "" {
		b.WriteString("\nAnalyst reports:\n")
		trunc := analystData
		if len(trunc) > 2000 {
			trunc = trunc[:2000] + "...[truncated]"
		}
		b.WriteString(trunc)
	}

	if opponentArg != "" {
		b.WriteString("\n\nThe Bear's previous argument:\n")
		trunc := opponentArg
		if len(trunc) > 1500 {
			trunc = trunc[:1500] + "...[truncated]"
		}
		b.WriteString(trunc)
		b.WriteString("\n\nAddress each bear point and provide counter-arguments. Strengthen your bull case.")
	} else {
		b.WriteString("\n\nConstruct your initial bull case based on the analyst reports.")
	}

	b.WriteString("\n\nOutput JSON: {\"ticker\":\"...\",\"bull_score\":1-10,\"thesis\":\"...\",\"arguments\":[...],\"confidence\":0.0-1.0}")
	return b.String()
}

// buildBearPrompt constructs the Bear Researcher prompt.
func buildBearPrompt(totalRounds, round int, analystData, opponentArg string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are a Bear Researcher. Build the strongest bearish case.\n\n")
	fmt.Fprintf(&b, "  Round %d/%d of structured debate.\n", round, totalRounds)

	if analystData != "" {
		b.WriteString("\nAnalyst reports:\n")
		trunc := analystData
		if len(trunc) > 2000 {
			trunc = trunc[:2000] + "...[truncated]"
		}
		b.WriteString(trunc)
	}

	if opponentArg != "" {
		b.WriteString("\n\nThe Bull's previous argument:\n")
		trunc := opponentArg
		if len(trunc) > 1500 {
			trunc = trunc[:1500] + "...[truncated]"
		}
		b.WriteString(trunc)
		b.WriteString("\n\nAddress each bull point and provide counter-arguments. Strengthen your bear case.")
	} else {
		b.WriteString("\n\nConstruct your initial bear case based on the analyst reports.")
	}

	b.WriteString("\n\nOutput JSON: {\"ticker\":\"...\",\"bear_score\":1-10,\"thesis\":\"...\",\"arguments\":[...],\"confidence\":0.0-1.0}")
	return b.String()
}
