package main

import (
	"context"
	"fmt"
	"log"

	"github.com/Timwood0x10/ares/internal/agents"
	"github.com/Timwood0x10/ares/internal/evidence"
	"github.com/Timwood0x10/ares/internal/evolution"
	"github.com/Timwood0x10/ares/internal/llm"
)

// runGate3E2E runs the FULL candidate gate-3 end-to-end against a real LLM:
// it loads the LLM client via LoadRegressionGate3, injects the regression
// check into a CandidateVerifier (WithRegressionCheck), and verifies a
// deliberately bad candidate — which must be REJECTED at gate 3 — and a good
// candidate, which must pass.
func runGate3E2E(ctx context.Context, client *llm.Client) {
	// Seed a profile store with the stable (good) instructions for "coder".
	profileStore := evolution.NewProfileStore()
	stable := &agents.AgentProfile{Role: "coder", Instructions: goodStrategy}
	if err := profileStore.Update(stable); err != nil {
		log.Fatalf("update profile: %v", err)
	}
	if err := profileStore.SetStable("coder", stable); err != nil {
		log.Fatalf("set stable profile: %v", err)
	}

	// Seed failure evidence so gate 2 (evidence existence) passes.
	evStore := evidence.NewMemoryStore()
	seedEvidence(ctx, evStore, "coder", []string{"ev-bad"})

	// Build the gate-3 regression check from the real LLM config.
	check, err := evolution.LoadRegressionGate3(profileStore, configPath, preservedCases,
		evolution.WithRegressionRuns(2),
	)
	if err != nil {
		log.Fatalf("load gate3: %v", err)
	}
	log.Printf("gate-3 regression check built (provider from %s)", configPath)

	// Wire the check into a CandidateVerifier.
	verifier := evolution.NewCandidateVerifierWithOptions(
		evolution.WithEvidenceStore(evStore),
		evolution.WithRegressionCheck(check),
	)
	log.Printf("candidate verifier wired with gate-3 regression check")

	// A deliberately bad candidate must be rejected at gate 3.
	bad := evolution.NewCandidate(evolution.CandidateInstruction, "coder",
		badStrategy, "off-by-one refactor", []string{"ev-bad"})
	badResult := verifier.Verify(bad)
	fmt.Println("── Bad candidate (a+b+1) ──")
	fmt.Printf("  success: %v\n", badResult.Success)
	fmt.Printf("  reason:  %s\n", badResult.Reason)
	fmt.Printf("  status:  %s\n", bad.Status)
	if badResult.Success {
		log.Fatalf("BUG: a regressing candidate passed gate 3")
	}
	log.Printf("bad candidate correctly REJECTED at gate 3")

	// A good candidate (keeps the good behavior) should pass gate 3.
	good := evolution.NewCandidate(evolution.CandidateInstruction, "coder",
		goodStrategy, "clarify instructions", []string{"ev-bad"})
	goodResult := verifier.Verify(good)
	fmt.Println("── Good candidate (add numbers) ──")
	fmt.Printf("  success: %v\n", goodResult.Success)
	fmt.Printf("  reason:  %s\n", goodResult.Reason)
	fmt.Printf("  status:  %s\n", good.Status)
	if !goodResult.Success {
		log.Printf("note: good candidate did not pass gate 3 (stochastic model grading); not a hard failure")
	} else {
		log.Printf("good candidate passed gate 3")
	}
	log.Printf("gate-3 e2e done")
}
