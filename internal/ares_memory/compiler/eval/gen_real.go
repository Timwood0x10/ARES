//go:build ignore

// Command gen_real converts an exported conversation (messages only) into a
// usable eval Sample by running the REAL production extractor
// (compiler.AKGExtractor) on the messages. This keeps the sample honest:
// candidates are what the live pipeline would extract, not hand-picked nodes.
//
// Usage:
//
//	go run gen_real.go -in conversations/ourchat.json -out samples_real/ourchat.json
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Timwood0x10/ares/internal/ares_memory/compiler"
	"github.com/Timwood0x10/ares/internal/ares_memory/compiler/eval"
)

// convInput is the messages-only export we read from disk.
type convInput struct {
	ID        string         `json:"id"`
	Namespace string         `json:"namespace"`
	Messages  []eval.Message `json:"messages"`
}

func slug(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else if r >= 0x4E00 && r <= 0x9FFF {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// toCandidate maps an extracted entity to an eval candidate node. Decision /
// constraint / tradeoff / question entities become their corresponding node
// type (with a summary attribute so the validator's default branch has
// content); other entity types become plain "entity" nodes keyed by name.
func toCandidate(e compiler.ExtractedEntity) eval.SampleNode {
	attrs := map[string]any{"name": e.Name}
	nodeType := "entity"
	switch e.Type {
	case "decision":
		nodeType = "decision"
	case "constraint":
		nodeType = "constraint"
	case "tradeoff":
		nodeType = "tradeoff"
	case "question":
		nodeType = "question"
	}
	if nodeType != "entity" {
		// Validator's default branch reads summary; mirror name so content exists.
		attrs["summary"] = e.Name
	}
	if len(e.Aliases) > 0 {
		attrs["aliases"] = e.Aliases
	}
	return eval.SampleNode{
		ID:         "e-" + e.SourceID + "-" + slug(e.Name),
		Type:       nodeType,
		Attributes: attrs,
		Confidence: e.Confidence,
		Source:     e.SourceID,
	}
}

// toFactCandidate maps an extracted triple to an eval candidate fact node.
func toFactCandidate(f compiler.ExtractedFact) eval.SampleNode {
	attrs := map[string]any{
		"subject":   f.Subject,
		"predicate": f.Predicate,
		"object":    f.Object,
	}
	return eval.SampleNode{
		ID:         "f-" + f.SourceID + "-" + slug(f.Subject+f.Predicate+f.Object),
		Type:       "fact",
		Attributes: attrs,
		Confidence: f.Confidence,
		Source:     f.SourceID,
	}
}

func main() {
	inPath := flag.String("in", "", "path to exported conversation JSON (messages only)")
	outPath := flag.String("out", "", "path to write the generated eval Sample JSON")
	flag.Parse()
	if *inPath == "" || *outPath == "" {
		fmt.Fprintln(os.Stderr, "gen_real: -in and -out are required")
		os.Exit(2)
	}

	data, err := os.ReadFile(*inPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gen_real: read %q: %v\n", *inPath, err)
		os.Exit(1)
	}
	var in convInput
	if err := json.Unmarshal(data, &in); err != nil {
		fmt.Fprintf(os.Stderr, "gen_real: parse %q: %v\n", *inPath, err)
		os.Exit(1)
	}

	msgs := make([]compiler.SourceMessage, 0, len(in.Messages))
	for i, m := range in.Messages {
		msgs = append(msgs, compiler.SourceMessage{
			ID:      fmt.Sprintf("m%d", i+1),
			Role:    m.Role,
			Content: m.Content,
		})
	}

	ext := compiler.NewAKGExtractor()
	entities, facts, err := ext.Extract(context.Background(), msgs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gen_real: extract: %v\n", err)
		os.Exit(1)
	}

	cands := make([]eval.SampleNode, 0, len(entities)+len(facts))
	for _, e := range entities {
		cands = append(cands, toCandidate(e))
	}
	for _, f := range facts {
		cands = append(cands, toFactCandidate(f))
	}

	sample := eval.Sample{
		ID:         in.ID,
		Namespace:  in.Namespace,
		Messages:   in.Messages,
		Candidates: cands,
	}

	out, err := json.MarshalIndent(sample, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "gen_real: marshal: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*outPath, out, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "gen_real: write %q: %v\n", *outPath, err)
		os.Exit(1)
	}
	fmt.Printf("gen_real: wrote %s (%d candidates: %d entities+%d facts)\n",
		*outPath, len(cands), len(entities), len(facts))
}
