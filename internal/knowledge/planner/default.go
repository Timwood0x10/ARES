package planner

import (
	"context"
	"fmt"
	"sort"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/provider"
)

// NewKnowledgePlanner creates a default planner that generates requirements
// based on keyword matching against the task goal.
func NewKnowledgePlanner() KnowledgePlanner {
	return &defaultPlanner{}
}

type defaultPlanner struct{}

func (p *defaultPlanner) Plan(_ context.Context, goal string, budget knowledge.TokenBudget) (*KnowledgePlan, error) {
	if goal == "" {
		return nil, fmt.Errorf("goal cannot be empty")
	}

	reqs := generateRequirements(goal)
	plan := &KnowledgePlan{
		Requirements: reqs,
		TokenBudget:  budget,
	}
	return plan, nil
}

// generateRequirements creates KnowledgeRequirements based on task goal keywords.
// This is a simple keyword-based implementation; a production version could use
// LLM-based intent analysis for more accurate requirement generation.
func generateRequirements(goal string) []KnowledgeRequirement {
	reqs := make([]KnowledgeRequirement, 0, 3)

	// Default: always include decision and history.
	reqs = append(reqs, KnowledgeRequirement{
		Need:        NeedDecision,
		Description: fmt.Sprintf("Decisions related to: %s", goal),
		Priority:    1,
		MaxResults:  20,
	})
	reqs = append(reqs, KnowledgeRequirement{
		Need:        NeedHistory,
		Description: fmt.Sprintf("History related to: %s", goal),
		Priority:    3,
		MaxResults:  30,
	})
	reqs = append(reqs, KnowledgeRequirement{
		Need:        NeedArchitecture,
		Description: fmt.Sprintf("Architecture decisions for: %s", goal),
		Priority:    2,
		MaxResults:  15,
	})

	return reqs
}

// defaultSourceDiscovery maps KnowledgeRequirements to providers by
// scoring each provider's IntentMatch against generated intents.
type defaultSourceDiscovery struct {
	registry *provider.ProviderRegistry
	planner  QueryPlanner
}

// NewSourceDiscovery creates a SourceDiscovery with the given registry and query planner.
func NewSourceDiscovery(registry *provider.ProviderRegistry, planner QueryPlanner) SourceDiscovery {
	return &defaultSourceDiscovery{
		registry: registry,
		planner:  planner,
	}
}

func (d *defaultSourceDiscovery) Discover(ctx context.Context, reqs []KnowledgeRequirement, budget knowledge.TokenBudget) ([]PlannedSource, error) {
	if len(reqs) == 0 {
		return nil, nil
	}

	var sources []PlannedSource

	for _, req := range reqs {
		// Build an intent for each requirement.
		intent := knowledge.Intent{
			Goal: req.Description,
			Scope: knowledge.Scope{
				MaxObjects: req.MaxResults,
			},
			Budget: budget,
		}

		// Find matching providers.
		providers := d.registry.Select(intent, 0.1)
		if len(providers) == 0 {
			continue
		}

		// For each matching provider, generate a query plan.
		for _, prov := range providers {
			providerType := detectProviderType(prov.Name())
			qp, err := d.planner.PlanQuery(ctx, req, prov.Name(), providerType)
			if err != nil {
				continue
			}

			sources = append(sources, PlannedSource{
				ProviderName: prov.Name(),
				Requirement:  req,
				Query:        qp,
				Priority:     req.Priority,
				MaxResults:   req.MaxResults,
			})
		}
	}

	// Sort by priority.
	sort.Slice(sources, func(i, j int) bool {
		return sources[i].Priority < sources[j].Priority
	})

	return sources, nil
}

func detectProviderType(name string) string {
	// Simple heuristic: derive provider type from name.
	// Real implementations would register their type explicitly.
	return name
}
