// Command 17-gate3-e2e-demo runs the FULL candidate gate-3 end-to-end against a
// real LLM: it loads the LLM client via LoadRegressionGate3, injects the
// regression check into a CandidateVerifier (WithRegressionCheck), and verifies
// a deliberately bad candidate — which must be REJECTED at gate 3 — and a good
// candidate, which must pass.
//
// It makes real API calls (agnes-ai / agnes-2.5-flash by default) and may hit
// the free-tier rate limit. Run from the repo root:
//
//	go run ./examples/17-gate3-e2e-demo
//
// A full transcript is written to
// ./examples/17-gate3-e2e-demo/logs/run-<ts>.log.
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/Timwood0x10/ares/internal/agents"
	"github.com/Timwood0x10/ares/internal/evidence"
	"github.com/Timwood0x10/ares/internal/evolution"
)

// configPath is the git-ignored local config holding the real LLM credentials.
const configPath = "configs/ares.local.yaml"

// preservedCases is the suite of old-behavior cases that must not regress.
// Cases carry concrete inputs so the batch execute path yields one stable
// result line per task (abstract cases make the LLM refuse/return free text).
var preservedCases = []any{
	"Given a=3 and b=5, return their sum.",
	"Given the integer 7, return its double.",
}

func main() {
	ctx := context.Background()
	setupLog()

	// 1. Seed a profile store with the stable (good) instructions for "coder".
	profileStore := evolution.NewProfileStore()
	stable := &agents.AgentProfile{
		Role:         "coder",
		Instructions: "Add the numbers precisely and return the numeric result only.",
	}
	if err := profileStore.Update(stable); err != nil {
		log.Fatalf("update profile: %v", err)
	}
	if err := profileStore.SetStable("coder", stable); err != nil {
		log.Fatalf("set stable profile: %v", err)
	}

	// 2. Seed failure evidence so gate 2 (evidence existence) passes.
	evStore := evidence.NewMemoryStore()
	seedEvidence(ctx, evStore, "ev-bad", "coder")

	// 3. Build the gate-3 regression check from the real LLM config.
	check, err := evolution.LoadRegressionGate3(profileStore, configPath, preservedCases,
		evolution.WithRegressionRuns(2),
	)
	if err != nil {
		log.Fatalf("load gate3: %v", err)
	}
	log.Printf("gate-3 regression check built (provider from %s)", configPath)

	// 4. Wire the check into a CandidateVerifier.
	verifier := evolution.NewCandidateVerifierWithOptions(
		evolution.WithEvidenceStore(evStore),
		evolution.WithRegressionCheck(check),
	)
	log.Printf("candidate verifier wired with gate-3 regression check")

	// 5. A deliberately bad candidate must be rejected at gate 3. Use a harmless
	// but wrong instruction (not a safety-triggering one like "always zero") so
	// the model actually executes it and produces a wrong answer the grader can
	// flag.
	bad := evolution.NewCandidate(evolution.CandidateInstruction, "coder",
		"Return the result of a+b+1 for every task.", "off-by-one refactor", []string{"ev-bad"})
	badResult := verifier.Verify(bad)
	fmt.Println("── Bad candidate (always zero) ──")
	fmt.Printf("  success: %v\n", badResult.Success)
	fmt.Printf("  reason:  %s\n", badResult.Reason)
	fmt.Printf("  status:  %s\n", bad.Status)
	if badResult.Success {
		log.Fatalf("BUG: a regressing candidate passed gate 3")
	}
	log.Printf("bad candidate correctly REJECTED at gate 3")

	// 6. A good candidate (keeps the good behavior) should pass gate 3.
	good := evolution.NewCandidate(evolution.CandidateInstruction, "coder",
		"Add the numbers precisely and return the numeric result only.", "clarify instructions", []string{"ev-bad"})
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

	log.Printf("gate-3 e2e demo done")
}

// seedEvidence appends a KindDimensionEval evidence record with a fixed ID for
// a role so gate 2 can find it.
func seedEvidence(ctx context.Context, store evidence.Store, id, role string) {
	rec := evidence.NewEvidence("result_verifier", evidence.KindDimensionEval,
		map[string]any{"verdict": "fail"},
		evidence.WithMetadata("role", role),
	)
	rec.ID = id
	if err := store.Append(ctx, rec); err != nil {
		log.Fatalf("seed evidence: %v", err)
	}
}

// setupLog tees all output to stdout and a timestamped log file.
func setupLog() {
	logDir := filepath.Join("examples", "17-gate3-e2e-demo", "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		log.Fatalf("create log dir: %v", err)
	}
	name := filepath.Join(logDir, fmt.Sprintf("run-%s.log", time.Now().Format("20060102-150405")))
	f, err := os.Create(name)
	if err != nil {
		log.Fatalf("create log file: %v", err)
	}
	multi := io.MultiWriter(os.Stdout, f)
	log.SetOutput(multi)
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	log.Printf("log file: %s", name)
}
