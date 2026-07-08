package planner

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/provider"
)

// providerSelectThreshold is the minimum IntentMatch score a provider must
// reach to be selected for a requirement. It sits above MemoryProvider's
// weak scores (0.3 for code/architecture, where memory is least useful) so
// that memory is not injected into code queries, while still including its
// strong scores (0.6–0.8 for memory/decision/issue).
const providerSelectThreshold = 0.35

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
	goalLower := strings.ToLower(goal)
	reqs := make([]KnowledgeRequirement, 0, 5)

	// Always include decision relevance.
	reqs = append(reqs, KnowledgeRequirement{
		Need:        NeedDecision,
		Description: fmt.Sprintf("Decisions related to: %s", goal),
		Priority:    1,
		MaxResults:  20,
	})

	// Match keywords to need types.
	if containsAny(goalLower, []string{"architect", "design", "stack", "infrastructure", "deploy"}) {
		reqs = append(reqs, KnowledgeRequirement{
			Need:        NeedArchitecture,
			Description: fmt.Sprintf("Architecture decisions for: %s", goal),
			Priority:    2,
			MaxResults:  15,
		})
	}
	if containsAny(goalLower, []string{"code", "implement", "function", "api", "class", "method"}) {
		reqs = append(reqs, KnowledgeRequirement{
			Need:        NeedCode,
			Description: fmt.Sprintf("Code implementation for: %s", goal),
			Priority:    2,
			MaxResults:  25,
		})
	}
	if containsAny(goalLower, []string{"bug", "fix", "issue", "problem", "error"}) {
		reqs = append(reqs, KnowledgeRequirement{
			Need:        NeedIssue,
			Description: fmt.Sprintf("Issues and fixes for: %s", goal),
			Priority:    2,
			MaxResults:  20,
		})
	}
	if containsAny(goalLower, []string{"performance", "slow", "latency", "benchmark", "optimize"}) {
		reqs = append(reqs, KnowledgeRequirement{
			Need:        NeedPerformance,
			Description: fmt.Sprintf("Performance data for: %s", goal),
			Priority:    3,
			MaxResults:  15,
		})
	}

	// Always include history as fallback.
	reqs = append(reqs, KnowledgeRequirement{
		Need:        NeedHistory,
		Description: fmt.Sprintf("History related to: %s", goal),
		Priority:    4,
		MaxResults:  30,
	})

	return reqs
}

// containsAny reports whether s contains any of the substrings.
func containsAny(s string, substrs []string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// needToObjectTypes maps a knowledge Need to the ObjectTypes it is most
// relevant to, so that type-aware providers (e.g. MemoryProvider) can score
// the requirement correctly. Without this mapping the intent carries no
// types and every provider collapses to its generic "no type" score, which
// previously caused MemoryProvider to be selected for all queries.
func needToObjectTypes(need NeedType) []knowledge.ObjectType {
	switch need {
	case NeedDecision:
		return []knowledge.ObjectType{knowledge.ObjectDecision}
	case NeedArchitecture:
		return []knowledge.ObjectType{knowledge.ObjectArchitecture}
	case NeedCode:
		return []knowledge.ObjectType{knowledge.ObjectCode}
	case NeedIssue:
		return []knowledge.ObjectType{knowledge.ObjectIssue}
	case NeedPerformance:
		return []knowledge.ObjectType{knowledge.ObjectCommit}
	case NeedHistory:
		return []knowledge.ObjectType{knowledge.ObjectMemory}
	default:
		return nil
	}
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
				Types:      needToObjectTypes(req.Need),
				MaxObjects: req.MaxResults,
			},
			Budget: budget,
		}

		// Find matching providers.
		providers := d.registry.Select(intent, providerSelectThreshold)
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

// defaultQueryPlanner is a simple QueryPlanner that generates keyword-based
// query descriptions for each requirement-provider pair.
type defaultQueryPlanner struct{}

// NewQueryPlanner creates a default query planner.
func NewQueryPlanner() QueryPlanner {
	return &defaultQueryPlanner{}
}

func (q *defaultQueryPlanner) PlanQuery(_ context.Context, req KnowledgeRequirement, providerName, providerType string) (*QueryPlan, error) {
	if req.Description == "" {
		return nil, fmt.Errorf("requirement description cannot be empty")
	}
	return &QueryPlan{
		Query:      req.Description,
		QueryType:  QueryKeyword,
		MaxResults: req.MaxResults,
		Parameters: map[string]any{
			"need":          string(req.Need),
			"provider_name": providerName,
			"provider_type": providerType,
		},
	}, nil
}
