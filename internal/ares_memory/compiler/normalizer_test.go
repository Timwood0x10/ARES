// Package compiler tests for RuleNormalizer.
package compiler

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestRuleNormalizerName(t *testing.T) {
	n := NewRuleNormalizer()
	if got := n.Name(); got != "rule" {
		t.Errorf("Name() = %q, want %q", got, "rule")
	}
}

func TestRuleNormalizerAliasCanonicalization(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		want      string
		wantCount int
	}{
		{"rust lowercase", "rust", "Rust", 1},
		{"rust chinese", "rust语言", "Rust", 1},
		{"golang", "golang", "Go", 1},
		{"go chinese", "go语言", "Go", 1},
		{"go lowercase", "go", "Go", 1},
		{"k8s", "k8s", "Kubernetes", 1},
		{"kubernetes", "kubernetes", "Kubernetes", 1},
		{"ts", "ts", "TypeScript", 1},
		{"js", "js", "JavaScript", 1},
		{"py", "py", "Python", 1},
		{"pg", "pg", "PostgreSQL", 1},
		{"postgres typo", "postgresl", "PostgreSQL", 1},
		{"llm", "llm", "LLM", 1},
		{"case insensitive RUST", "RUST", "Rust", 1},
		{"case insensitive K8S", "K8S", "Kubernetes", 1},
		{"whitespace trimmed", "  rust  ", "Rust", 1},
		{"internal whitespace collapsed", "  foo   bar  ", "foo bar", 1},
		{"unknown preserved", "RandomTool", "RandomTool", 1},
		{"java known language", "java", "Java", 1},
		{"whitespace only dropped", "   ", "", 0},
		{"empty dropped", "", "", 0},
	}
	n := NewRuleNormalizer()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entities, _, err := n.Normalize(context.Background(),
				[]ExtractedEntity{{Name: tc.input, Type: "language", Confidence: 0.5, SourceID: "m1"}},
				nil)
			if err != nil {
				t.Fatalf("Normalize failed: %v", err)
			}
			if len(entities) != tc.wantCount {
				t.Fatalf("expected %d entities, got %d", tc.wantCount, len(entities))
			}
			if tc.wantCount > 0 && entities[0].Name != tc.want {
				t.Errorf("canonical name = %q, want %q", entities[0].Name, tc.want)
			}
		})
	}
}

func TestRuleNormalizerCoreferenceCollapse(t *testing.T) {
	n := NewRuleNormalizer()
	entities := []ExtractedEntity{
		{
			Name:       "rust",
			Type:       "language",
			Aliases:    []string{"rust lang"},
			Properties: map[string]string{"paradigm": "multi"},
			Confidence: 0.8,
			SourceID:   "m1",
		},
		{
			Name:       "Rust",
			Type:       "language",
			Aliases:    []string{"rust language"},
			Properties: map[string]string{"paradigm": "systems", "year": "2010"},
			Confidence: 0.9,
			SourceID:   "m2",
		},
	}
	out, _, err := n.Normalize(context.Background(), entities, nil)
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 collapsed entity, got %d: %+v", len(out), out)
	}
	e := out[0]
	if e.Name != "Rust" {
		t.Errorf("Name = %q, want %q", e.Name, "Rust")
	}
	if e.Confidence != 0.9 {
		t.Errorf("Confidence = %f, want 0.9 (max)", e.Confidence)
	}
	if e.SourceID != "m1" {
		t.Errorf("SourceID = %q, want %q (first non-empty)", e.SourceID, "m1")
	}
	if e.Properties["paradigm"] != "systems" {
		t.Errorf("Properties[paradigm] = %q, want %q (later wins)", e.Properties["paradigm"], "systems")
	}
	if e.Properties["year"] != "2010" {
		t.Errorf("Properties[year] = %q, want %q", e.Properties["year"], "2010")
	}
	// Aliases should be the union, excluding the canonical name.
	wantAliases := map[string]bool{"rust lang": true, "rust language": true}
	if len(e.Aliases) != len(wantAliases) {
		t.Errorf("Aliases = %v, want %d entries", e.Aliases, len(wantAliases))
	}
	for _, a := range e.Aliases {
		if !wantAliases[a] {
			t.Errorf("unexpected alias %q", a)
		}
	}
}

func TestRuleNormalizerFactCanonicalization(t *testing.T) {
	n := NewRuleNormalizer()
	facts := []ExtractedFact{
		{Subject: "golang", Predicate: "uses", Object: "k8s", Confidence: 0.8, SourceID: "m1"},
	}
	_, out, err := n.Normalize(context.Background(), nil, facts)
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(out))
	}
	f := out[0]
	if f.Subject != "Go" {
		t.Errorf("Subject = %q, want %q", f.Subject, "Go")
	}
	if f.Object != "Kubernetes" {
		t.Errorf("Object = %q, want %q", f.Object, "Kubernetes")
	}
	if f.Predicate != "uses" {
		t.Errorf("Predicate = %q, want %q (unchanged)", f.Predicate, "uses")
	}
	if f.Confidence != 0.8 {
		t.Errorf("Confidence = %f, want 0.8", f.Confidence)
	}
}

func TestRuleNormalizerFactDedup(t *testing.T) {
	n := NewRuleNormalizer()
	facts := []ExtractedFact{
		{Subject: "rust", Predicate: "depends on", Object: "k8s", Confidence: 0.7, SourceID: "m1"},
		{Subject: "Rust", Predicate: "depends on", Object: "Kubernetes", Confidence: 0.9, SourceID: "m2"},
		{Subject: "Rust", Predicate: "depends on", Object: "Kubernetes", Confidence: 0.6, SourceID: "m3"},
	}
	_, out, err := n.Normalize(context.Background(), nil, facts)
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 deduplicated fact, got %d: %+v", len(out), out)
	}
	f := out[0]
	if f.Subject != "Rust" {
		t.Errorf("Subject = %q, want %q", f.Subject, "Rust")
	}
	if f.Object != "Kubernetes" {
		t.Errorf("Object = %q, want %q", f.Object, "Kubernetes")
	}
	if f.Confidence != 0.9 {
		t.Errorf("Confidence = %f, want 0.9 (max)", f.Confidence)
	}
}

func TestRuleNormalizerFactDedupKeepsFirstOnEqualConfidence(t *testing.T) {
	n := NewRuleNormalizer()
	facts := []ExtractedFact{
		{Subject: "rust", Predicate: "uses", Object: "k8s", Confidence: 0.8, SourceID: "first"},
		{Subject: "Rust", Predicate: "uses", Object: "Kubernetes", Confidence: 0.8, SourceID: "second"},
	}
	_, out, err := n.Normalize(context.Background(), nil, facts)
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 fact, got %d", len(out))
	}
	// On equal confidence, the first occurrence is kept.
	if out[0].SourceID != "first" {
		t.Errorf("SourceID = %q, want %q (first kept on tie)", out[0].SourceID, "first")
	}
}

func TestRuleNormalizerEmptyInput(t *testing.T) {
	n := NewRuleNormalizer()
	t.Run("nil slices", func(t *testing.T) {
		entities, facts, err := n.Normalize(context.Background(), nil, nil)
		if err != nil {
			t.Fatalf("Normalize failed: %v", err)
		}
		if entities == nil {
			t.Error("expected non-nil entities slice")
		}
		if facts == nil {
			t.Error("expected non-nil facts slice")
		}
		if len(entities) != 0 || len(facts) != 0 {
			t.Errorf("expected empty slices, got entities=%d facts=%d", len(entities), len(facts))
		}
	})
	t.Run("empty slices", func(t *testing.T) {
		entities, facts, err := n.Normalize(context.Background(), []ExtractedEntity{}, []ExtractedFact{})
		if err != nil {
			t.Fatalf("Normalize failed: %v", err)
		}
		if len(entities) != 0 || len(facts) != 0 {
			t.Errorf("expected empty slices, got entities=%d facts=%d", len(entities), len(facts))
		}
	})
	t.Run("nil ctx", func(t *testing.T) {
		entities, facts, err := n.Normalize(context.TODO(), nil, nil)
		if err != nil {
			t.Fatalf("Normalize with nil ctx failed: %v", err)
		}
		if len(entities) != 0 || len(facts) != 0 {
			t.Errorf("expected empty slices, got entities=%d facts=%d", len(entities), len(facts))
		}
	})
}

func TestRuleNormalizerExtraAliases(t *testing.T) {
	t.Run("adds aliases", func(t *testing.T) {
		n, err := NewRuleNormalizerWithAliases(map[string]string{
			"react": "React",
			"vue":   "Vue",
		})
		if err != nil {
			t.Fatalf("NewRuleNormalizerWithAliases failed: %v", err)
		}
		entities, _, err := n.Normalize(context.Background(),
			[]ExtractedEntity{
				{Name: "react", Type: "library", Confidence: 0.9, SourceID: "m1"},
				{Name: "vue", Type: "library", Confidence: 0.9, SourceID: "m2"},
			}, nil)
		if err != nil {
			t.Fatalf("Normalize failed: %v", err)
		}
		if len(entities) != 2 {
			t.Fatalf("expected 2 entities, got %d", len(entities))
		}
		// Defaults must still work alongside the extras.
		dEntities, _, dErr := n.Normalize(context.Background(),
			[]ExtractedEntity{{Name: "rust", Confidence: 0.5, SourceID: "m1"}}, nil)
		if dErr != nil {
			t.Fatalf("Normalize defaults failed: %v", dErr)
		}
		if len(dEntities) != 1 || dEntities[0].Name != "Rust" {
			t.Errorf("default alias rust->Rust broken by extra aliases: %+v", dEntities)
		}
		foundReact, foundVue := false, false
		for _, e := range entities {
			if e.Name == "React" {
				foundReact = true
			}
			if e.Name == "Vue" {
				foundVue = true
			}
		}
		if !foundReact {
			t.Error("expected React entity from extra alias")
		}
		if !foundVue {
			t.Error("expected Vue entity from extra alias")
		}
	})
	t.Run("overrides defaults", func(t *testing.T) {
		// Extra alias with the same key as a default overrides it.
		n, err := NewRuleNormalizerWithAliases(map[string]string{
			"rust": "RustLang",
		})
		if err != nil {
			t.Fatalf("NewRuleNormalizerWithAliases failed: %v", err)
		}
		entities, _, err := n.Normalize(context.Background(),
			[]ExtractedEntity{{Name: "rust", Confidence: 0.5, SourceID: "m1"}}, nil)
		if err != nil {
			t.Fatalf("Normalize failed: %v", err)
		}
		if len(entities) != 1 || entities[0].Name != "RustLang" {
			t.Errorf("expected RustLang, got %+v", entities)
		}
	})
	t.Run("error on empty canonical", func(t *testing.T) {
		_, err := NewRuleNormalizerWithAliases(map[string]string{
			"foo": "",
		})
		if err == nil {
			t.Fatal("expected error for empty canonical name")
		}
		if !errors.Is(err, errEmptyCanonical) {
			t.Errorf("expected errEmptyCanonical, got %v", err)
		}
	})
	t.Run("error on whitespace canonical", func(t *testing.T) {
		_, err := NewRuleNormalizerWithAliases(map[string]string{
			"foo": "   ",
		})
		if err == nil {
			t.Fatal("expected error for whitespace-only canonical name")
		}
		if !errors.Is(err, errEmptyCanonical) {
			t.Errorf("expected errEmptyCanonical, got %v", err)
		}
	})
	t.Run("nil extra is fine", func(t *testing.T) {
		n, err := NewRuleNormalizerWithAliases(nil)
		if err != nil {
			t.Fatalf("NewRuleNormalizerWithAliases(nil) failed: %v", err)
		}
		entities, _, err := n.Normalize(context.Background(),
			[]ExtractedEntity{{Name: "k8s", Confidence: 0.5, SourceID: "m1"}}, nil)
		if err != nil {
			t.Fatalf("Normalize failed: %v", err)
		}
		if len(entities) != 1 || entities[0].Name != "Kubernetes" {
			t.Errorf("expected Kubernetes, got %+v", entities)
		}
	})
}

func TestRuleNormalizerCancelledContext(t *testing.T) {
	n := NewRuleNormalizer()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := n.Normalize(ctx,
		[]ExtractedEntity{{Name: "rust", Confidence: 0.5, SourceID: "m1"}},
		[]ExtractedFact{{Subject: "rust", Predicate: "uses", Object: "k8s", Confidence: 0.7}})
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestRuleNormalizerIdempotency(t *testing.T) {
	n := NewRuleNormalizer()
	entities := []ExtractedEntity{
		{Name: "rust", Type: "language", Aliases: []string{"rust lang"}, Confidence: 0.8, SourceID: "m1"},
		{Name: "golang", Type: "language", Confidence: 0.9, SourceID: "m2"},
		{Name: "k8s", Type: "platform", Confidence: 0.7, SourceID: "m3"},
	}
	facts := []ExtractedFact{
		{Subject: "rust", Predicate: "uses", Object: "k8s", Confidence: 0.7, SourceID: "m1"},
		{Subject: "golang", Predicate: "integrates", Object: "k8s", Confidence: 0.8, SourceID: "m2"},
	}
	out1Entities, out1Facts, err := n.Normalize(context.Background(), entities, facts)
	if err != nil {
		t.Fatalf("first Normalize failed: %v", err)
	}
	out2Entities, out2Facts, err := n.Normalize(context.Background(), out1Entities, out1Facts)
	if err != nil {
		t.Fatalf("second Normalize failed: %v", err)
	}
	if !reflect.DeepEqual(out1Entities, out2Entities) {
		t.Errorf("entities not idempotent:\nfirst:  %+v\nsecond: %+v", out1Entities, out2Entities)
	}
	if !reflect.DeepEqual(out1Facts, out2Facts) {
		t.Errorf("facts not idempotent:\nfirst:  %+v\nsecond: %+v", out1Facts, out2Facts)
	}
}

func TestRuleNormalizerMixedPipeline(t *testing.T) {
	// Exercise the full pipeline: entities and facts together, with
	// coreference collapse, fact canonicalization, and dedup interplay.
	n := NewRuleNormalizer()
	entities := []ExtractedEntity{
		{Name: "golang", Type: "language", Confidence: 0.9, SourceID: "m1"},
		{Name: "Go", Type: "language", Aliases: []string{"go lang"}, Confidence: 0.8, SourceID: "m2"},
		{Name: "   ", Type: "language", Confidence: 0.5, SourceID: "m3"}, // dropped
	}
	facts := []ExtractedFact{
		{Subject: "golang", Predicate: "uses", Object: "k8s", Confidence: 0.7, SourceID: "m1"},
		{Subject: "Go", Predicate: "uses", Object: "Kubernetes", Confidence: 0.9, SourceID: "m2"},
		{Subject: "golang", Predicate: "compiles to", Object: "binary", Confidence: 0.6, SourceID: "m3"},
	}
	outEntities, outFacts, err := n.Normalize(context.Background(), entities, facts)
	if err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	if len(outEntities) != 1 {
		t.Fatalf("expected 1 entity (coreference collapse + drop), got %d: %+v", len(outEntities), outEntities)
	}
	if outEntities[0].Name != "Go" {
		t.Errorf("entity Name = %q, want %q", outEntities[0].Name, "Go")
	}
	if outEntities[0].Confidence != 0.9 {
		t.Errorf("entity Confidence = %f, want 0.9 (max)", outEntities[0].Confidence)
	}
	// Two distinct facts: "Go uses Kubernetes" and "Go compiles to binary".
	if len(outFacts) != 2 {
		t.Fatalf("expected 2 facts, got %d: %+v", len(outFacts), outFacts)
	}
	uses := outFacts[0]
	if uses.Subject != "Go" || uses.Predicate != "uses" || uses.Object != "Kubernetes" {
		t.Errorf("uses fact = %+v, want {Go uses Kubernetes}", uses)
	}
	if uses.Confidence != 0.9 {
		t.Errorf("uses fact Confidence = %f, want 0.9 (max)", uses.Confidence)
	}
	compiles := outFacts[1]
	if compiles.Subject != "Go" || compiles.Predicate != "compiles to" || compiles.Object != "binary" {
		t.Errorf("compiles fact = %+v, want {Go compiles to binary}", compiles)
	}
}
