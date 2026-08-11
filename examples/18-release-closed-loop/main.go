// Command 18-release-closed-loop runs the FULL candidate release closed loop
// against a real LLM:
//
//	failure evidence → Diagnoser.Generate → CandidateVerifier.Verify (gates 1/2/3)
//	  → CandidatePipelineWithOptions(WithReleaseRegressionCheck).Release
//	  → release-time gate-3 confirm → SetStable → Promote
//
// It wires the SAME gate-3 regression check into both the verify stage and the
// release stage, and exercises three scenarios:
//
//  1. a regressing candidate is REJECTED at verify (gate 3);
//  2. a manually-verified regressing candidate is REJECTED at RELEASE (the
//     release-time gate-3, the subject of this demo);
//  3. a good candidate passes verify and is promoted to stable by Release.
//
// It makes real API calls (agnes-ai / agnes-2.5-flash by default). Run from the
// repo root:
//
//	go run ./examples/18-release-closed-loop
//
// A full transcript is written to
// ./examples/18-release-closed-loop/logs/run-<ts>.log.
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
	"github.com/Timwood0x10/ares/internal/evolution/coordinator"
	"github.com/Timwood0x10/ares/internal/evolution/patch"
)

// configPath is the git-ignored local config holding the real LLM credentials.
const configPath = "configs/ares.local.yaml"

// preservedCases must carry concrete inputs so the batch LLM path yields one
// stable result line per task (abstract cases make the model refuse).
var preservedCases = []any{
	"Given a=3 and b=5, return their sum.",
	"Given the integer 7, return its double.",
}

func main() {
	ctx := context.Background()
	setupLog()
	log.Printf("=== candidate release closed-loop demo (real LLM) ===")

	// ── Seed profile store with the stable (good) instructions for "coder". ──
	profileStore := evolution.NewProfileStore()
	stable := &agents.AgentProfile{Role: "coder", Instructions: "Add the numbers precisely and return the numeric result only."}
	if err := profileStore.Update(stable); err != nil {
		log.Fatalf("update stable profile: %v", err)
	}
	if err := profileStore.SetStable("coder", stable); err != nil {
		log.Fatalf("set stable profile: %v", err)
	}

	// ── Seed failure evidence + candidate store. ──
	evStore := evidence.NewMemoryStore()
	seedEvidence(ctx, evStore, "ev-bad", "coder")
	candStore := evolution.NewCandidateStore()

	// ── Wire the ONE gate-3 check into both verify and release. ──
	gate3, err := evolution.LoadRegressionGate3(profileStore, configPath, preservedCases,
		evolution.WithRegressionRuns(2),
	)
	if err != nil {
		log.Fatalf("load gate3: %v", err)
	}
	verifier := evolution.NewCandidateVerifierWithOptions(
		evolution.WithEvidenceStore(evStore),
		evolution.WithRegressionCheck(gate3),
	)
	registry := patch.NewRegistry()
	coord := coordinator.NewEvolutionCoordinator(coordinator.DefaultPolicy(), registry)
	pipeline := evolution.NewCandidatePipelineWithOptions(
		candStore, profileStore, registry, coord, nil,
		evolution.WithReleaseRegressionCheck(gate3),
	)
	log.Printf("wired gate-3 regression check into verify + release")

	// ── Scenario 1: regressing candidate rejected at VERIFY. ──
	log.Printf("── Scenario 1: bad candidate rejected at verify ──")
	bad := evolution.NewCandidate(evolution.CandidateInstruction, "coder",
		"Return the result of a+b+1 for every task.", "off-by-one refactor", []string{"ev-bad"})
	badResult := verifier.Verify(bad)
	log.Printf("verify: success=%v reason=%q status=%s", badResult.Success, badResult.Reason, bad.Status)
	if badResult.Success {
		log.Fatalf("BUG: regressing candidate passed verify")
	}
	log.Printf("scenario 1 OK: regressing candidate rejected at verify")

	// ── Scenario 2: manually-verified regressing candidate rejected at RELEASE. ──
	log.Printf("── Scenario 2: manually-verified bad candidate rejected at RELEASE ──")
	bad2 := evolution.NewCandidate(evolution.CandidateInstruction, "coder",
		"Return the result of a+b+1 for every task.", "off-by-one refactor", []string{"ev-bad"})
	bad2.Verify() // bypass verify on purpose to exercise the release-time gate-3
	candStore.Submit(bad2)
	released, err := pipeline.Release(ctx, bad2.ID)
	log.Printf("release: released=%v err=%v status=%s reason=%q",
		released, err, bad2.Status, bad2.RejectionReason)
	if released {
		log.Fatalf("BUG: release-time gate-3 did not reject the regressing candidate")
	}
	if bad2.Status != evolution.StatusRejected {
		log.Fatalf("BUG: expected rejected status, got %s", bad2.Status)
	}
	log.Printf("scenario 2 OK: release-time gate-3 rejected the regressing candidate")

	// ── Scenario 3: good candidate passes verify and is promoted by release. ──
	log.Printf("── Scenario 3: good candidate verified + released ──")
	good := evolution.NewCandidate(evolution.CandidateInstruction, "coder",
		"Add the numbers precisely and return the numeric result only.", "clarify instructions", []string{"ev-bad"})
	goodResult := verifier.Verify(good)
	log.Printf("verify: success=%v reason=%q status=%s", goodResult.Success, goodResult.Reason, good.Status)
	if !goodResult.Success {
		log.Printf("note: good candidate did not pass verify (stochastic grading); skipping release")
	} else {
		candStore.Submit(good)
		releasedGood, errGood := pipeline.Release(ctx, good.ID)
		log.Printf("release: released=%v err=%v status=%s", releasedGood, errGood, good.Status)
		if !releasedGood {
			log.Printf("note: good candidate release did not promote (stochastic grading); not a hard failure")
		} else {
			log.Printf("scenario 3 OK: good candidate promoted to stable")
		}
	}

	finalStable := profileStore.GetStable("coder")
	log.Printf("final stable instructions: %q", finalStable.Instructions)
	log.Printf("=== closed-loop demo done ===")
}

// seedEvidence appends a KindDimensionEval evidence record with a fixed ID.
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
	logDir := filepath.Join("examples", "18-release-closed-loop", "logs")
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
