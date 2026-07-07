package linker

import (
	"context"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

func TestDecisionLinker(t *testing.T) {
	l := &DecisionLinker{}
	objs := []*knowledge.KnowledgeObject{
		{ID: "d1", Summary: "Decision to use Redis for caching", Tags: []string{"cache", "redis"}, Type: knowledge.ObjectDecision},
		{ID: "c1", Summary: "Redis connection pool config", Tags: []string{"cache", "redis"}, Type: knowledge.ObjectCode},
		{ID: "u1", Summary: "User login flow", Tags: []string{"auth", "user"}, Type: knowledge.ObjectCode},
	}

	edges, err := l.Link(context.Background(), objs)
	if err != nil {
		t.Fatalf("Link error: %v", err)
	}

	if len(edges) == 0 {
		t.Fatal("expected at least one decision edge")
	}

	found := false
	for _, e := range edges {
		if e.From == "d1" && e.To == "c1" && e.Name == knowledge.RelDecidedBy {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected d1 -[decided_by]--> c1 edge")
	}

	// Should NOT link d1 to u1 (different tags).
	for _, e := range edges {
		if e.From == "d1" && e.To == "u1" {
			t.Error("d1 should not link to u1")
		}
	}
}

func TestDecisionLinkerNoMatch(t *testing.T) {
	l := &DecisionLinker{}
	objs := []*knowledge.KnowledgeObject{
		{ID: "a", Summary: "hello world", Tags: []string{"general"}},
		{ID: "b", Summary: "some random text", Tags: []string{"other"}},
	}

	edges, err := l.Link(context.Background(), objs)
	if err != nil {
		t.Fatalf("Link error: %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("expected 0 edges, got %d", len(edges))
	}
}

func TestArchitectureLinker(t *testing.T) {
	l := &ArchitectureLinker{}
	objs := []*knowledge.KnowledgeObject{
		{ID: "arch1", Summary: "Cache architecture overview", Type: knowledge.ObjectDocument, Tags: []string{"cache"}},
		{ID: "code1", Summary: "Redis client wrapper", Type: knowledge.ObjectCode, Tags: []string{"cache"}},
		{ID: "code2", Summary: "User service", Type: knowledge.ObjectCode, Tags: []string{"auth"}},
	}

	edges, err := l.Link(context.Background(), objs)
	if err != nil {
		t.Fatalf("Link error: %v", err)
	}

	found := false
	for _, e := range edges {
		if e.From == "code1" && e.To == "arch1" && e.Name == knowledge.RelDependsOn {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected code1 -[depends_on]--> arch1 edge")
	}
}

func TestTimelineLinker(t *testing.T) {
	now := time.Now()
	l := &TimelineLinker{}
	objs := []*knowledge.KnowledgeObject{
		{ID: "a", Namespace: "ns1", CreatedAt: now.Add(-72 * time.Hour)},
		{ID: "b", Namespace: "ns1", CreatedAt: now.Add(-48 * time.Hour)},
		{ID: "c", Namespace: "ns1", CreatedAt: now.Add(-24 * time.Hour)},
	}

	edges, err := l.Link(context.Background(), objs)
	if err != nil {
		t.Fatalf("Link error: %v", err)
	}

	if len(edges) == 0 {
		t.Fatal("expected timeline edges")
	}

	// b -[generated_by]--> a (within 2 weeks)
	// c -[generated_by]--> b (within 2 weeks)
	// c -[supersedes]--> a (oldest to newest)
	foundGen := false
	foundSuper := false
	for _, e := range edges {
		if e.Name == knowledge.RelGeneratedBy {
			foundGen = true
		}
		if e.Name == knowledge.RelSupersedes {
			foundSuper = true
		}
	}
	if !foundGen {
		t.Error("expected generated_by edges")
	}
	if !foundSuper {
		t.Error("expected supersedes edges (oldest to newest)")
	}
}

func TestTimelineLinkerNoTimestamps(t *testing.T) {
	l := &TimelineLinker{}
	objs := []*knowledge.KnowledgeObject{
		{ID: "x", Namespace: "ns"},
		{ID: "y", Namespace: "ns"},
	}

	edges, err := l.Link(context.Background(), objs)
	if err != nil {
		t.Fatalf("Link error: %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("expected 0 edges for zero timestamps, got %d", len(edges))
	}
}

func TestTimelineLinkerSingleObject(t *testing.T) {
	l := &TimelineLinker{}
	objs := []*knowledge.KnowledgeObject{
		{ID: "x", Namespace: "ns", CreatedAt: time.Now()},
	}

	edges, err := l.Link(context.Background(), objs)
	if err != nil {
		t.Fatalf("Link error: %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("expected 0 edges for single object, got %d", len(edges))
	}
}

func TestSimilarityLinker(t *testing.T) {
	l := &SimilarityLinker{MinScore: 0.3}
	objs := []*knowledge.KnowledgeObject{
		{ID: "r1", Summary: "Redis cache configuration and setup"},
		{ID: "r2", Summary: "Redis cluster configuration guide"},
		{ID: "u1", Summary: "User authentication and login flow"},
	}

	edges, err := l.Link(context.Background(), objs)
	if err != nil {
		t.Fatalf("Link error: %v", err)
	}

	found := false
	for _, e := range edges {
		if (e.From == "r1" && e.To == "r2") || (e.From == "r2" && e.To == "r1") {
			if e.Name == knowledge.RelSimilarTo {
				found = true
				if e.Score < 0.3 {
					t.Errorf("similarity score too low: %.2f", e.Score)
				}
			}
		}
	}
	if !found {
		t.Error("expected similar_to edge between r1 and r2 (both redis related)")
	}

	// r1 and u1 should NOT be similar.
	for _, e := range edges {
		if (e.From == "r1" && e.To == "u1") || (e.From == "u1" && e.To == "r1") {
			t.Error("r1 and u1 should not be similar (different topics)")
		}
	}
}

func TestSimilarityLinkerEmptySummaries(t *testing.T) {
	l := &SimilarityLinker{}
	objs := []*knowledge.KnowledgeObject{
		{ID: "a", Summary: ""},
		{ID: "b", Summary: ""},
	}

	edges, err := l.Link(context.Background(), objs)
	if err != nil {
		t.Fatalf("Link error: %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("expected 0 edges for empty summaries, got %d", len(edges))
	}
}

func TestSimilarityLinkerBelowThreshold(t *testing.T) {
	l := &SimilarityLinker{MinScore: 0.9} // High threshold
	objs := []*knowledge.KnowledgeObject{
		{ID: "a", Summary: "Redis cache setup tutorial"},
		{ID: "b", Summary: "PostgreSQL database connection pool configuration"},
	}

	edges, err := l.Link(context.Background(), objs)
	if err != nil {
		t.Fatalf("Link error: %v", err)
	}
	if len(edges) != 0 {
		t.Errorf("expected 0 edges below threshold, got %d", len(edges))
	}
}

func TestSimilarityLinkerExactMatch(t *testing.T) {
	l := &SimilarityLinker{MinScore: 0.1}
	objs := []*knowledge.KnowledgeObject{
		{ID: "a", Summary: "hello world test"},
		{ID: "b", Summary: "hello world test"},
	}

	edges, err := l.Link(context.Background(), objs)
	if err != nil {
		t.Fatalf("Link error: %v", err)
	}
	if len(edges) == 0 {
		t.Fatal("expected an edge for near-identical summaries")
	}
	if len(edges) > 0 && edges[0].Score != 1.0 {
		t.Errorf("expected score 1.0 for identical summaries, got %.2f", edges[0].Score)
	}
}
