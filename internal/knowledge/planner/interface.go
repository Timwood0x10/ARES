// Package planner provides knowledge planning, source discovery, and query planning.
//
// Architecture (v3):
//
//	KnowledgePlanner → KnowledgeRequirement → SourceDiscovery → QueryPlanner → Provider
//
// Planner outputs only "what knowledge is needed" (not "where to get it"),
// SourceDiscovery maps needs to providers, and QueryPlanner translates
// needs into concrete queries (SQL, Cypher, Vector, etc.).
package planner

import (
	"context"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// NeedType describes a category of knowledge required by the task.
type NeedType string

const (
	NeedArchitecture NeedType = "architecture"
	NeedDecision     NeedType = "decision"
	NeedHistory      NeedType = "history"
	NeedCode         NeedType = "code"
	NeedIssue        NeedType = "issue"
	NeedPerformance  NeedType = "performance"
)

// KnowledgeRequirement describes a single knowledge need, independent
// of any specific data source or provider.
type KnowledgeRequirement struct {
	Need        NeedType `json:"need"`
	Description string   `json:"description"`
	Priority    int      `json:"priority"`
	MaxResults  int      `json:"max_results"`
}

// KnowledgePlan is the output of KnowledgePlanner. It describes what
// knowledge is needed but NOT where to get it — SourceDiscovery handles that.
type KnowledgePlan struct {
	Requirements []KnowledgeRequirement `json:"requirements"`
	TokenBudget  knowledge.TokenBudget  `json:"token_budget"`
}

// KnowledgePlanner analyzes a task and generates a KnowledgePlan.
// It is decoupled from specific providers — new data sources can be added
// without modifying the planner.
type KnowledgePlanner interface {
	// Plan generates a knowledge plan from a task description.
	Plan(ctx context.Context, goal string, budget knowledge.TokenBudget) (*KnowledgePlan, error)
}

// PlannedSource is the output of SourceDiscovery: a provider selected
// to fulfill a specific KnowledgeRequirement, along with a query plan.
type PlannedSource struct {
	ProviderName string               `json:"provider_name"`
	Requirement  KnowledgeRequirement `json:"requirement"`
	Query        *QueryPlan           `json:"query,omitempty"`
	Priority     int                  `json:"priority"`
	MaxResults   int                  `json:"max_results"`
}

// QueryType identifies the type of query a QueryPlan represents.
type QueryType string

const (
	QuerySQL     QueryType = "sql"
	QueryCypher  QueryType = "cypher"
	QueryVector  QueryType = "vector"
	QueryMemory  QueryType = "memory"
	QueryKeyword QueryType = "keyword"
)

// QueryPlan is a concrete query that a Provider can execute.
// The QueryPlanner generates this from a KnowledgeRequirement + Provider pair.
type QueryPlan struct {
	Query      string         `json:"query"`
	QueryType  QueryType      `json:"query_type"`
	Parameters map[string]any `json:"parameters,omitempty"`
	MaxResults int            `json:"max_results"`
}

// SourceDiscovery maps KnowledgeRequirements to concrete providers.
// v3: This layer decouples Planner from Provider knowledge.
type SourceDiscovery interface {
	// Discover finds providers that can fulfill the given requirements.
	// Returns a list of PlannedSources, each pairing a requirement with a
	// selected provider and its query plan.
	Discover(ctx context.Context, reqs []KnowledgeRequirement, budget knowledge.TokenBudget) ([]PlannedSource, error)
}

// QueryPlanner translates a KnowledgeRequirement + Provider pair into
// a concrete query plan. Different provider types produce different
// query types (SQL for PG/Mysql, Log for Git, Vector for Memory, etc.).
type QueryPlanner interface {
	// PlanQuery generates a query plan for a specific provider.
	PlanQuery(ctx context.Context, req KnowledgeRequirement, providerName string, providerType string) (*QueryPlan, error)
}
