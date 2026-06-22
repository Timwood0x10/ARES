package main

import (
	"fmt"
	"strings"

	apievol "goagentx/api/evolution"
)

// sep prints a visual section separator with a centered title.
func sep(title string) {
	fmt.Printf("\n%s\n  %s\n%s\n", strings.Repeat("=", 60), title, strings.Repeat("=", 60))
}

// safeTruncate returns the first n characters of s, or the full string if shorter.
func safeTruncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// tbl prints a formatted table with the given header and rows to the console.
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

// printInsight prints a conversational "What did we learn?" summary.
func printInsight(title, message string) {
	fmt.Printf("\n  💬 What did we learn? — %s\n", title)
	fmt.Println("  " + strings.Repeat("─", 56))
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
func printEvolutionInsightReport(title string, result *apievol.EvolutionResult, lineages []apievol.StrategyLineage, parent *apievol.Strategy) {
	fmt.Println("\n╔════════════════════════════════════════════════════╗")
	fmt.Println("║          🧬 Evolution Insight Report               ║")
	fmt.Println("╚════════════════════════════════════════════════════╝")

	printTrajectory(result.Stats, result.BestStrategy)
	printMutationAnalysis(lineages)
	printGenealogyTree(lineages)
	printBestStrategyDiff(result.BestStrategy, parent)
	printKeyLearnings(result, parent)
}

func printTrajectory(stats []apievol.Stats, bestStrategy *apievol.Strategy) {
	nGen := len(stats)
	if nGen == 0 {
		return
	}

	fmt.Println("\n📊 Evolution Trajectory:")
	firstBest := stats[0].BestScore
	lastBest := stats[nGen-1].BestScore
	bestAvg := stats[nGen-1].AvgScore

	improvementPct := 0.0
	if firstBest != 0 {
		improvementPct = ((lastBest - firstBest) / mathAbs(firstBest)) * 100
	}

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

	trend := "→ STAGNANT"
	if lastBest > firstBest+0.01 {
		trend = "↗ IMPROVING"
	} else if lastBest < firstBest-0.01 {
		trend = "↘ DECLINING"
	}
	fmt.Printf("\n   Trend: %s  |  Best: %.2f → %.2f  |  Generations: %d\n",
		trend, firstBest, lastBest, nGen)
}

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

func printGenealogyTree(lineages []apievol.StrategyLineage) {
	fmt.Println("\n🏆 Genealogy Tree (top lineages):")

	showN := min(5, len(lineages))
	if showN == 0 {
		fmt.Println("   (No genealogy records)")
		return
	}

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
