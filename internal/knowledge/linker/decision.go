// Package linker provides pluggable Relation generators for AKF.
// Each LinkerPlugin implements runtime.Linker and generates domain-specific edges.
package linker

import (
	"context"
	"strings"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/runtime"
)

// DecisionLinker generates decision-relation edges between objects whose
// summaries or tags contain decision-related keywords.
type DecisionLinker struct{}

func (l *DecisionLinker) Name() string { return "decision-linker" }

func (l *DecisionLinker) Link(_ context.Context, objects []*knowledge.KnowledgeObject) ([]knowledge.Relation, error) {
	// Score each object's decision relevance.
	keyTerms := []string{"decision", "decided", "choose", "select", "proposal", "rationale"}
	type scored struct {
		obj   *knowledge.KnowledgeObject
		score int
	}
	var decisionObjs []scored
	for _, obj := range objects {
		s := decisionScore(obj, keyTerms)
		if s > 0 {
			decisionObjs = append(decisionObjs, scored{obj: obj, score: s})
		}
	}

	var edges []knowledge.Relation
	// Link decision objects to related non-decision objects by shared tags.
	for _, d := range decisionObjs {
		for _, other := range objects {
			if other.ID == d.obj.ID {
				continue
			}
			if hasOverlap(d.obj.Tags, other.Tags) {
				edges = append(edges, knowledge.Relation{
					From:  d.obj.ID,
					To:    other.ID,
					Name:  knowledge.RelDecidedBy,
					Score: float64(d.score) * 0.25,
				})
			}
		}
	}
	return edges, nil
}

func decisionScore(obj *knowledge.KnowledgeObject, terms []string) int {
	text := strings.ToLower(obj.Summary + " " + strings.Join(obj.Tags, " "))
	count := 0
	for _, t := range terms {
		if strings.Contains(text, t) {
			count++
		}
	}
	return count
}

func hasOverlap(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

// Ensure DecisionLinker implements runtime.Linker at compile time.
var _ runtime.Linker = (*DecisionLinker)(nil)
