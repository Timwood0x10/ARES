package eval

import (
	"context"
	"fmt"
)

// dimensionAvgMetric is the metric name the dimension judge reports. It is
// preferred over "the first score" when extracting a replay verdict, because a
// runner may be wired with more than one evaluator and the accuracy number is
// specifically this one.
const dimensionAvgMetric = "llm_judge_dimension_avg"

// ReplayCase is one golden case with what the replay observed.
type ReplayCase struct {
	// Case is the golden case as the corpus declared it.
	Case TestCase
	// Result is what the executor reported for it (answer, tools, provenance).
	Result TestResult
	// Score is the judged accuracy AFTER the deterministic floor, and HasScore
	// says whether there is one at all (a judge failure leaves it false).
	Score    float64
	HasScore bool
	// GroundTruth is the deterministic layer's verdict for this case, and
	// Anchored whether that verdict was allowed to floor the judge.
	GroundTruth Verdict
	Anchored    bool
	// Err is this case's own failure — executor or judge. The run continues past
	// it, because one broken case must not discard the cases that did run.
	Err error
}

// ReplayOutcome is a whole replay's verdict, including how much of it is opinion.
type ReplayOutcome struct {
	// Cases holds one entry per replayed case, in corpus order.
	Cases []ReplayCase
	// Agreement is the judge's own judge-vs-ground-truth counter, read after the
	// run. It is NOT recomputed here: the judge keeps the single running count,
	// and a second tally would be a second rule.
	Agreement GroundTruthAgreement
	// Mean is the average judged score over the cases that produced one. Read it
	// TOGETHER WITH Scored: on its own it flatters a broken run (the average of the
	// few cases that did not error).
	Mean float64
	// Scored and Total are how many cases produced a score, of how many were
	// replayed. A Mean over a fraction of the corpus is a different claim from a
	// Mean over the corpus, and the numbers must be readable to tell them apart.
	Scored int
	Total  int
	// AnchoredShare is the share of replayed cases that had a deterministic
	// verdict. Everything else was scored by the judge alone — this is the
	// number that says how much of the replay is verifiable.
	AnchoredShare float64
	// RunErr carries the joined per-case failures from the runner, if any; the
	// outcome above is still populated for the cases that succeeded.
	RunErr error
}

// ReplayOptions configures a replay.
type ReplayOptions struct {
	// Limit caps how many cases are replayed (0 = the whole suite).
	//
	// It exists because a replay's whole purpose is COMPARABILITY: every
	// candidate genome must be scored on the SAME cases, or the comparison is
	// noise. Truncating is therefore always "the first N of the suite", never a
	// sample, and callers must hold the subset fixed across candidates.
	Limit int
}

// ReplayGolden runs a golden suite through a runtime-backed executor and the
// judge, and reports per-case outcomes plus the judge-vs-ground-truth agreement.
//
// This is the driver P0-1(ii) was missing. Until it existed, the golden corpus
// was data: the checks could be declared, but nothing ever executed them, so the
// accuracy channel's verifiable half was defined and never measured.
//
// Two properties it must keep, both inherited from the components it drives:
//   - the run is SEQUENTIAL. Provenance and the judge's "last executed case" are
//     single-case facts, so a concurrent driver would silently judge every case
//     against whichever finished last (see ProvenanceReporter).
//   - a per-case failure does not abort the run; it is recorded on the case and
//     the rest still run.
func ReplayGolden(ctx context.Context, runner *AgentTestRunner, judge Evaluator, suite TestSuite, opts ReplayOptions) (ReplayOutcome, error) {
	if runner == nil {
		return ReplayOutcome{}, fmt.Errorf("replay: runner must not be nil")
	}
	if judge == nil {
		return ReplayOutcome{}, fmt.Errorf("replay: judge must not be nil")
	}

	cases := suite.TestCases
	if opts.Limit > 0 && len(cases) > opts.Limit {
		cases = cases[:opts.Limit]
	}
	subset := TestSuite{Name: suite.Name, Description: suite.Description, TestCases: cases, Tags: suite.Tags}

	results, runErr := runner.RunSuite(ctx, subset)
	byCase := make(map[string]TestResult, len(results))
	for _, result := range results {
		byCase[result.TestCaseID] = result
	}

	outcome := ReplayOutcome{RunErr: runErr, Cases: make([]ReplayCase, 0, len(cases))}
	scoreSum := 0.0
	scored := 0
	anchored := 0
	for _, tc := range cases {
		entry := ReplayCase{Case: tc}
		result, ran := byCase[tc.ID]
		if ran {
			entry.Result = result
			if result.Error != "" {
				entry.Err = fmt.Errorf("case %s: executor reported: %s", tc.ID, result.Error)
			}
		} else {
			entry.Err = fmt.Errorf("case %s: no result produced", tc.ID)
		}

		// The deterministic layer's verdict is reported per case; whether it
		// actually floored anything was decided inside the judge, and is counted
		// there.
		gt := evaluateGroundTruth(tc, result, "")
		entry.GroundTruth = gt.Verdict
		entry.Anchored = gt.Anchored()
		if entry.Anchored {
			anchored++
		}

		if entry.Err == nil {
			scores, err := judge.Evaluate(ctx, tc, result)
			switch {
			case err != nil:
				entry.Err = fmt.Errorf("case %s: judge: %w", tc.ID, err)
			default:
				entry.Score, entry.HasScore = replayScore(scores)
			}
		}
		if entry.HasScore {
			scoreSum += entry.Score
			scored++
		}
		outcome.Cases = append(outcome.Cases, entry)
	}

	if scored > 0 {
		outcome.Mean = scoreSum / float64(scored)
	}
	if len(cases) > 0 {
		outcome.AnchoredShare = float64(anchored) / float64(len(cases))
	}
	outcome.Scored, outcome.Total = scored, len(cases)
	if reporter, ok := judge.(groundTruthAgreementReporter); ok {
		outcome.Agreement = reporter.GroundTruthAgreement()
	}
	return outcome, nil
}

// Coverage is the share of replayed cases that produced a score. A caller comparing
// candidates must check it: a genome that fails most cases can otherwise show a
// better Mean than one that answers all of them.
func (o ReplayOutcome) Coverage() float64 {
	if o.Total == 0 {
		return 0
	}
	return float64(o.Scored) / float64(o.Total)
}

// groundTruthAgreementReporter is implemented by the dimension judge, which is
// where the agreement counters live. Replay reads them; it never keeps its own.
type groundTruthAgreementReporter interface {
	GroundTruthAgreement() GroundTruthAgreement
}

// replayScore extracts the judged accuracy from a score list, preferring the
// dimension judge's own metric over positional guessing.
func replayScore(scores []EvalScore) (float64, bool) {
	for _, score := range scores {
		if score.Metric == dimensionAvgMetric {
			return score.Score, true
		}
	}
	if len(scores) > 0 {
		return scores[0].Score, true
	}
	return 0, false
}
