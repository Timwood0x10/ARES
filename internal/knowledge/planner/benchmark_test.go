package planner

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

func BenchmarkKnowledgePlanner_Plan(b *testing.B) {
	p := NewKnowledgePlanner()
	ctx := context.Background()
	budget := knowledge.TokenBudget{
		MaxTokens: 4000,
		Reserved:  1000,
		ForGraph:  2000,
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := p.Plan(ctx, "Why did we choose Redis for session caching?", budget)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkKnowledgePlanner_PlanComplexQuery(b *testing.B) {
	p := NewKnowledgePlanner()
	ctx := context.Background()
	budget := knowledge.TokenBudget{
		MaxTokens: 8000,
		Reserved:  2000,
		ForGraph:  4000,
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := p.Plan(ctx,
			"What decisions were made about the caching layer, how does the auth flow depend on it, and what code implements session refresh?",
			budget)
		if err != nil {
			b.Fatal(err)
		}
	}
}
