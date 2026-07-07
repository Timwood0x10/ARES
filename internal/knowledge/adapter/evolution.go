// Package adapter provides adapters between existing ARES subsystems and AKF.
package adapter

import (
	"fmt"
	"math"
	"time"

	ares_evolution "github.com/Timwood0x10/ares/internal/ares_evolution"
	"github.com/Timwood0x10/ares/internal/knowledge"
)

// FromStrategy converts an evolution Strategy into a KnowledgeObject.
// The object type is set to ObjectDecision so it appears in decision-related queries.
func FromStrategy(s *ares_evolution.Strategy, ns string) *knowledge.KnowledgeObject {
	if s == nil {
		return nil
	}

	summary := s.Name
	if summary == "" {
		summary = fmt.Sprintf("Strategy %s (v%d)", s.ID, s.Version)
	}
	if len(summary) > 200 {
		summary = summary[:200] + "..."
	}

	tags := []string{"evolution", "strategy"}
	if s.StrategyMutationType != "" {
		tags = append(tags, s.StrategyMutationType)
	}

	return &knowledge.KnowledgeObject{
		ID:         fmt.Sprintf("evo_%s_v%d", s.ID, s.Version),
		Type:       knowledge.ObjectDecision,
		Namespace:  ns,
		Summary:    summary,
		Confidence: scoreToConfidence(s.Score),
		CreatedAt:  s.CreatedAt,
		UpdatedAt:  time.Now(),
		Tags:       tags,
		Metadata: map[string]any{
			"strategy_id":            s.ID,
			"version":                s.Version,
			"parent_id":              s.ParentID,
			"mutation_type":          s.StrategyMutationType,
			"mutation_desc":          s.MutationDesc,
			"score":                  s.Score,
			"strategy_prompt_length": len(s.PromptTemplate),
		},
	}
}

// FromStrategies converts a slice of evolution Strategies into KnowledgeObjects.
func FromStrategies(strategies []*ares_evolution.Strategy, ns string) []*knowledge.KnowledgeObject {
	objects := make([]*knowledge.KnowledgeObject, 0, len(strategies))
	for _, s := range strategies {
		if obj := FromStrategy(s, ns); obj != nil {
			objects = append(objects, obj)
		}
	}
	return objects
}

// scoreToConfidence maps an evolution score [-inf, +inf] to a [0, 1] confidence.
// Score 0 → 0.5, positive → higher, negative → lower, clamped to [0.1, 0.99].
func scoreToConfidence(score float64) float64 {
	// Sigmoid: σ(x/2) = 1 / (1 + e^(-x/2))
	// Score 0 → 0.5, Score 5 → 0.92, Score -5 → 0.08
	c := 1.0 / (1.0 + math.Exp(-score/2.0))
	if c < 0.1 {
		return 0.1
	}
	if c > 0.99 {
		return 0.99
	}
	return c
}
