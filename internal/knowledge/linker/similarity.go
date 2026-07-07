package linker

import (
	"context"
	"math"
	"strings"
	"unicode"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/runtime"
)

// SimilarityLinker generates similar_to relations between objects whose
// summaries share significant token overlap.
type SimilarityLinker struct {
	// MinScore is the minimum similarity score to create an edge (default 0.3).
	MinScore float64
}

func (l *SimilarityLinker) Name() string { return "similarity-linker" }

func (l *SimilarityLinker) Link(_ context.Context, objects []*knowledge.KnowledgeObject) ([]knowledge.Relation, error) {
	minScore := l.MinScore
	if minScore <= 0 {
		minScore = 0.3
	}

	var edges []knowledge.Relation
	n := len(objects)

	for i := 0; i < n; i++ {
		tokensA := tokenize(objects[i].Summary)
		if len(tokensA) == 0 {
			continue
		}

		for j := i + 1; j < n; j++ {
			tokensB := tokenize(objects[j].Summary)
			if len(tokensB) == 0 {
				continue
			}

			score := jaccardSimilarity(tokensA, tokensB)
			if score >= minScore {
				edges = append(edges, knowledge.Relation{
					From:  objects[i].ID,
					To:    objects[j].ID,
					Name:  knowledge.RelSimilarTo,
					Score: score,
				})
			}
		}
	}

	return edges, nil
}

// tokenize splits text into lowercase words, filtering out stop words
// and short tokens.
func tokenize(text string) map[string]int {
	tokens := make(map[string]int)
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	for _, w := range words {
		if len(w) < 3 || isStopWord(w) {
			continue
		}
		tokens[w]++
	}
	return tokens
}

// jaccardSimilarity computes the Jaccard index between two token bags.
func jaccardSimilarity(a, b map[string]int) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}

	intersection := 0
	for k := range a {
		if _, ok := b[k]; ok {
			intersection++
		}
	}

	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return math.Round(float64(intersection)/float64(union)*100) / 100
}

// isStopWord returns true for common English stop words.
func isStopWord(w string) bool {
	switch w {
	case "the", "and", "for", "are", "but", "not", "you", "all",
		"can", "had", "her", "was", "one", "our", "out", "has",
		"have", "been", "some", "them", "than", "that", "this",
		"very", "just", "with", "will", "each", "make", "like",
		"from", "they", "said", "what", "when", "where",
		"which", "their", "there", "would", "about", "could",
		"should", "other", "after", "still", "also", "more":
		return true
	}
	return false
}

var _ runtime.Linker = (*SimilarityLinker)(nil)
