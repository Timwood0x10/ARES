package knowledge_test

import (
	"context"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/pipeline"
)

// TestEntityResolver_UsesIdentifierMatchFirst locks the risk-ascending
// resolution order: a candidate whose Summary AND Type exactly match the
// object is matched at confidence 1.0 (step 1) before any similarity
// comparison runs (step 4).
func TestEntityResolver_UsesIdentifierMatchFirst(t *testing.T) {
	t.Parallel()

	matcher := &pipeline.DefaultEntityMatcher{MatchThreshold: 0.7}

	obj := &knowledge.KnowledgeObject{
		ID:      "obj_new",
		Type:    knowledge.ObjectMemory,
		Summary: "user prefers Rust",
	}
	candidate := &knowledge.KnowledgeObject{
		ID:      "obj_existing",
		Type:    knowledge.ObjectMemory,
		Summary: "user prefers Rust",
	}

	result, err := matcher.Match(context.TODO(), obj, []*knowledge.KnowledgeObject{candidate})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if result.IsNew {
		t.Fatal("expected identifier match to find the existing entity")
	}
	if result.MatchedObjectID != "obj_existing" {
		t.Errorf("matched %q, want %q", result.MatchedObjectID, "obj_existing")
	}
	if result.Confidence < 1.0 {
		t.Errorf("identifier match confidence = %v, want 1.0", result.Confidence)
	}
}

// TestEntityResolution_KeepsSeparateWhenUncertain locks step 5: when no
// candidate reaches the similarity threshold, the object stays a new
// entity rather than being silently merged with the closest match.
func TestEntityResolution_KeepsSeparateWhenUncertain(t *testing.T) {
	t.Parallel()

	matcher := &pipeline.DefaultEntityMatcher{MatchThreshold: 0.9}

	obj := &knowledge.KnowledgeObject{
		ID:         "obj_a",
		Type:       knowledge.ObjectMemory,
		Normalized: "the user likes Python programming language",
		Summary:    "user likes Python",
	}
	candidate := &knowledge.KnowledgeObject{
		ID:         "obj_b",
		Type:       knowledge.ObjectMemory,
		Normalized: "the user prefers Rust for systems programming",
		Summary:    "user prefers Rust",
	}

	result, err := matcher.Match(context.TODO(), obj, []*knowledge.KnowledgeObject{candidate})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if !result.IsNew {
		t.Errorf("expected IsNew=true when similarity is below threshold, got match to %q at %v",
			result.MatchedObjectID, result.Confidence)
	}
}

// TestEntityResolution_TypeConstraintBlocksCrossTypeMatch locks the type
// constraint in step 4: a "user" and a "project" with similar names must
// not be merged, even if their token overlap exceeds the threshold.
func TestEntityResolution_TypeConstraintBlocksCrossTypeMatch(t *testing.T) {
	t.Parallel()

	matcher := &pipeline.DefaultEntityMatcher{MatchThreshold: 0.1}

	obj := &knowledge.KnowledgeObject{
		ID:         "user_alice",
		Type:       knowledge.ObjectUser,
		Normalized: "alice works on the auth project",
		Summary:    "alice auth project",
	}
	candidate := &knowledge.KnowledgeObject{
		ID:         "project_auth",
		Type:       knowledge.ObjectProject,
		Normalized: "the auth project uses alice service",
		Summary:    "auth project alice",
	}

	result, err := matcher.Match(context.TODO(), obj, []*knowledge.KnowledgeObject{candidate})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if !result.IsNew {
		t.Errorf("expected IsNew=true for cross-type match (user vs project), got match to %q",
			result.MatchedObjectID)
	}
}

// TestRelationGate_RejectsTypeIncompatible locks the relation validation
// gate: a "fixes" relation from a user-typed object is rejected because
// users do not "fix" things in the knowledge graph.
func TestRelationGate_RejectsTypeIncompatible(t *testing.T) {
	t.Parallel()

	cfg := knowledge.DefaultRelationGateConfig()
	rel := knowledge.Relation{
		Predicate:  knowledge.RelFixes,
		ObjectText: "auth bug",
		Evidence:   "user fixes the auth bug",
	}

	if knowledge.ValidateRelation(rel, knowledge.ObjectUser, cfg) {
		t.Error("expected ValidateRelation to reject fixes from a user-typed object")
	}
}

// TestRelationGate_RejectsUnknownPredicate locks the gate: a relation
// with a predicate not in AllowedPredicates is rejected.
func TestRelationGate_RejectsUnknownPredicate(t *testing.T) {
	t.Parallel()

	cfg := knowledge.DefaultRelationGateConfig()
	rel := knowledge.Relation{
		Predicate:  "hates",
		ObjectText: "auth bug",
		Evidence:   "user hates the auth bug",
	}

	if knowledge.ValidateRelation(rel, knowledge.ObjectMemory, cfg) {
		t.Error("expected ValidateRelation to reject unknown predicate 'hates'")
	}
}

// TestRelationGate_AcceptsValidRelation locks the positive path: a
// "fixes" relation from a commit-typed object with evidence passes the gate.
func TestRelationGate_AcceptsValidRelation(t *testing.T) {
	t.Parallel()

	cfg := knowledge.DefaultRelationGateConfig()
	rel := knowledge.Relation{
		Predicate:  knowledge.RelFixes,
		ObjectText: "auth bug",
		Evidence:   "commit fixes the auth bug",
	}

	if !knowledge.ValidateRelation(rel, knowledge.ObjectCommit, cfg) {
		t.Error("expected ValidateRelation to accept a valid fixes relation from a commit")
	}
}

// TestRelationGate_RequireEvidenceRejection locks the evidence requirement:
// when RequireEvidence is true, a relation with no evidence span is rejected.
func TestRelationGate_RequireEvidenceRejection(t *testing.T) {
	t.Parallel()

	cfg := knowledge.RelationGateConfig{RequireEvidence: true}
	rel := knowledge.Relation{
		Predicate:  knowledge.RelFixes,
		ObjectText: "auth bug",
		Evidence:   "", // no evidence
	}

	if knowledge.ValidateRelation(rel, knowledge.ObjectCommit, cfg) {
		t.Error("expected ValidateRelation to reject relation without evidence when RequireEvidence=true")
	}
}

// TestRelationGate_FilterRelations locks the batch filter: only relations
// that pass the gate survive FilterRelations.
func TestRelationGate_FilterRelations(t *testing.T) {
	t.Parallel()

	cfg := knowledge.DefaultRelationGateConfig()
	rels := []knowledge.Relation{
		{Predicate: knowledge.RelFixes, ObjectText: "bug", Evidence: "fixes bug"},          // valid (from commit)
		{Predicate: "unknown_pred", ObjectText: "thing", Evidence: "unknown thing"},        // invalid predicate
		{Predicate: knowledge.RelDependsOn, ObjectText: "lib", Evidence: "depends on lib"}, // valid (from code)
	}

	filtered := knowledge.FilterRelations(rels, knowledge.ObjectCode, cfg)
	if len(filtered) != 2 {
		t.Errorf("FilterRelations: got %d relations, want 2", len(filtered))
	}
	for _, r := range filtered {
		if r.Predicate == "unknown_pred" {
			t.Error("unknown predicate should have been filtered out")
		}
	}
}
