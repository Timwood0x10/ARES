// Command akg-eval runs the Phase 1 / 1.3 AKG quality-gate evaluation harness
// over a directory of JSON conversation samples and prints a JSON report.
//
// Usage:
//
//	akg-eval -dir <samples-dir> [-out report.json] [-min-confidence 0.6] \
//	  [-dedup-threshold 0.85] [-shared-store] [-no-gate] [-no-fail]
//
// Each sample file is a JSON object:
//
//	{
//	  "id": "conv-001",
//	  "namespace": "eval.conv001",
//	  "candidates": [
//	    {"id":"n1","type":"fact",
//	     "attributes":{"subject":"Rust","predicate":"compiles","object":"native code"},
//	     "confidence":0.9}
//	  ],
//	  "gold": {"facts":["rust compiles native code"], "entities":[], "references":[]}
//	}
//
// The "gold" field is optional; when present it drives the precision gate.
// With -fail-on-gate (default), the process exits 3 when a hard gate is not
// met, so the harness can gate CI before Phase 2/3.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/Timwood0x10/ares/internal/ares_memory/compiler/eval"
)

func main() {
	var (
		dir         = flag.String("dir", "", "directory of sample *.json files (required)")
		out         = flag.String("out", "", "optional path to write the JSON report")
		minConf     = flag.Float64("min-confidence", eval.DefaultHarnessConfig().MinConfidence, "AKG selector min confidence")
		maxFacts    = flag.Int("max-facts", 0, "max facts (0 = no cap)")
		dedup       = flag.Float64("dedup-threshold", eval.DefaultHarnessConfig().DedupThreshold, "Jaccard dedup threshold")
		gate        = flag.Bool("quality-gate", true, "enable L1 structural quality gate")
		sharedStore = flag.Bool("shared-store", false, "use one store across samples (models cross-session dedup)")
		failOnGate  = flag.Bool("fail-on-gate", true, "exit non-zero when a hard gate fails")
	)
	flag.Parse()

	if *dir == "" {
		fmt.Fprintln(os.Stderr, "usage: akg-eval -dir <samples-dir> [-out report.json]")
		os.Exit(2)
	}

	samples, err := eval.LoadDir(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if len(samples) == 0 {
		fmt.Fprintln(os.Stderr, "no samples found in", *dir)
		os.Exit(1)
	}

	cfg := eval.HarnessConfig{
		MinConfidence:     *minConf,
		MaxFacts:          *maxFacts,
		DedupThreshold:    *dedup,
		EnableQualityGate: *gate,
		SharedStore:       *sharedStore,
	}

	rep, err := cfg.Run(context.Background(), samples)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "error marshaling report:", err)
		os.Exit(1)
	}
	if *out != "" {
		if err := os.WriteFile(*out, data, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "error writing report:", err)
			os.Exit(1)
		}
	}
	fmt.Println(string(data))

	if *failOnGate && !rep.Gates.ReadyForPhase3 {
		fmt.Fprintf(os.Stderr,
			"GATES NOT MET: structure_rate=%.3f precision_rate=%.3f precision_skipped=%v\n",
			rep.Gates.StructureRate, rep.Gates.PrecisionRate, rep.Gates.PrecisionSkipped)
		os.Exit(3)
	}
}
