package knowledge_test

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/knowledge"
	memorystore "github.com/Timwood0x10/ares/internal/knowledge/store/memory"
)

// TestAKG_BuildFromDocsArticles builds a real AKG from the project's
// docs/articles/**/*.md corpus using the embedding-only loop (no generative
// LLM): each article becomes a KnowledgeObject, rule-based RelationExtractor
// mines depends_on/calls/fixes/belongs_to edges, the quality gate scores and
// promotes candidates to active, then HybridSearch recalls them.
//
// It prints a full report (via t.Log) so the resulting AKG can be inspected:
// object counts by status, relation counts by predicate, average confidence,
// and top recall results for a few sample queries. The test is skipped when
// the docs corpus is absent (e.g. CI without the docs tree).
func TestAKG_BuildFromDocsArticles(t *testing.T) {
	ctx := context.Background()

	// docs/articles lives at <repo>/docs/articles; tests run from
	// internal/knowledge, so the relative path is ../../docs/articles.
	objects := loadDocsArticles(t, "../../docs/articles")
	if len(objects) == 0 {
		t.Skip("no docs/articles markdown found; skipping AKG build test")
	}

	store := memorystore.New()
	extractor := knowledge.NewRelationExtractor()
	gate := knowledge.DefaultQualityGateConfig()

	// Write side: extract relations, score via quality gate, save + promote.
	totalRels := 0
	predCounts := map[string]int{}
	for _, obj := range objects {
		obj.Relations = extractor.Extract(obj)
		totalRels += len(obj.Relations)
		for _, r := range obj.Relations {
			predCounts[r.Predicate]++
		}

		obj.Status = knowledge.StatusCandidate
		q := gate.Evaluate(obj)
		obj.Quality = q
		obj.Confidence = gate.ComputeFinal(q)
	}

	if err := store.Save(ctx, objects...); err != nil {
		t.Fatalf("save objects: %v", err)
	}
	promoted := 0
	for _, obj := range objects {
		if obj.Confidence >= gate.MinFinalScore {
			if err := store.Promote(ctx, obj.ID, obj.Quality); err != nil {
				t.Fatalf("promote %s: %v", obj.ID, err)
			}
			promoted++
		}
	}

	// ---- AKG build report ----
	active, _ := store.ListByStatus(ctx, "articles", knowledge.StatusActive, 1000)
	candidates, _ := store.ListByStatus(ctx, "articles", knowledge.StatusCandidate, 1000)

	var sumConf float64
	for _, obj := range objects {
		sumConf += obj.Confidence
	}
	avgConf := 0.0
	if len(objects) > 0 {
		avgConf = sumConf / float64(len(objects))
	}

	t.Logf("========== AKG BUILD REPORT ==========")
	t.Logf("source corpus : docs/articles/**/*.md")
	t.Logf("objects total : %d", len(objects))
	t.Logf("  active      : %d (promoted, Confidence >= %.2f)", len(active), gate.MinFinalScore)
	t.Logf("  candidate   : %d (below gate, not promoted)", len(candidates))
	t.Logf("  promoted    : %d", promoted)
	t.Logf("avg confidence: %.3f", avgConf)
	t.Logf("relations     : %d total", totalRels)
	t.Logf("  by predicate:")
	for _, pred := range sortedKeys(predCounts) {
		t.Logf("    %-12s : %d", pred, predCounts[pred])
	}

	// Print a few sample objects with their extracted relations.
	t.Logf("---------- sample objects (first 5 with relations) ----------")
	shown := 0
	for _, obj := range objects {
		if len(obj.Relations) == 0 {
			continue
		}
		t.Logf("  [%s] conf=%.3f  %s", obj.ID, obj.Confidence, truncateForLog(obj.Summary, 70))
		for _, r := range obj.Relations {
			t.Logf("      - %s -> %s", r.Predicate, truncateForLog(r.ObjectText, 60))
		}
		shown++
		if shown >= 5 {
			break
		}
	}

	// Read side: HybridSearch (lexical-only, no embedding service wired) for a
	// few representative queries derived from the corpus topics.
	queries := []string{
		"runtime resurrection agent",
		"memory distillation",
		"security hardening",
		"tool system",
		"knowledge graph",
		"config system",
	}
	t.Logf("---------- HybridSearch recall (lexical, top 3) ----------")
	for _, q := range queries {
		scored, err := store.HybridSearch(ctx, knowledge.HybridSearchRequest{
			Query:        q,
			Namespace:    "articles",
			TopK:         10,
			FinalK:       3,
			MinScore:     0.0,
			StatusFilter: []knowledge.ObjectStatus{knowledge.StatusActive},
		})
		if err != nil {
			t.Fatalf("HybridSearch %q: %v", q, err)
		}
		t.Logf("  query=%q  -> %d hits", q, len(scored))
		for _, s := range scored {
			if s.Object == nil {
				continue
			}
			t.Logf("      [%.3f vec=%.3f lex=%.3f] %s : %s",
				s.FinalScore, s.VectorScore, s.LexicalScore,
				s.Object.ID, truncateForLog(s.Object.Summary, 60))
		}
	}
	t.Logf("========== END AKG BUILD REPORT ==========")

	// Assertions: the AKG must actually contain something useful.
	if len(active) == 0 {
		t.Error("expected at least one active object after promotion")
	}
	if totalRels == 0 {
		t.Log("note: no relations extracted (corpus may lack 修复/依赖/调用/属于 patterns)")
	}
	// At least one query should recall something.
	anyRecall := false
	for _, q := range queries {
		scored, _ := store.HybridSearch(ctx, knowledge.HybridSearchRequest{
			Query: q, Namespace: "articles", TopK: 10, FinalK: 3,
			MinScore:     0.0,
			StatusFilter: []knowledge.ObjectStatus{knowledge.StatusActive},
		})
		if len(scored) > 0 {
			anyRecall = true
			break
		}
	}
	if !anyRecall {
		t.Error("expected at least one HybridSearch query to recall an active object")
	}
}

// loadDocsArticles walks root, reads every .md file, and returns one
// KnowledgeObject per article (Type=document, Namespace=articles). Normalized
// is capped so lexical Jaccard scoring stays meaningful; Summary is the first
// markdown heading.
func loadDocsArticles(t *testing.T, root string) []*knowledge.KnowledgeObject {
	t.Helper()
	if _, err := os.Stat(root); err != nil {
		return nil
	}
	var objects []*knowledge.KnowledgeObject
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(strings.ToLower(path), ".md") {
			return nil
		}
		raw, rErr := os.ReadFile(path)
		if rErr != nil {
			return rErr
		}
		base := strings.TrimSuffix(filepath.Base(path), ".md")
		content := string(raw)
		normalized := content
		if len(normalized) > 2000 {
			normalized = normalized[:2000]
		}
		// Include the language segment (zh/en) in the ID so translated twins
		// under docs/articles/{zh,en}/<same-name>.md do not collide — the
		// store dedupes by ID, which would otherwise silently drop half the
		// corpus.
		lang := languageOf(path, root)
		id := "doc:" + lang + ":" + base
		tags := tagsFromName(base)
		if lang != "" {
			tags = append(tags, lang)
		}
		objects = append(objects, &knowledge.KnowledgeObject{
			ID:         id,
			Type:       knowledge.ObjectDocument,
			Namespace:  "articles",
			Summary:    firstHeading(content, base),
			Normalized: normalized,
			Raw:        raw,
			Tags:       tags,
			Metadata:   map[string]any{"lang": lang, "path": path},
			Evidence: []knowledge.Evidence{
				{Source: "docs/articles", Ref: path, Weight: 1.0, Timestamp: time.Now()},
			},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk docs/articles: %v", err)
	}
	return objects
}

// firstHeading returns the first markdown heading line (stripped of leading #
// and whitespace), falling back to the file name when no heading is found.
func firstHeading(content, fallback string) string {
	for line := range strings.SplitSeq(content, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "#") {
			return strings.TrimSpace(strings.TrimLeft(trim, "#"))
		}
	}
	return fallback
}

// languageOf returns the language segment (e.g. "zh", "en") of a markdown path
// relative to root, or "" when the file sits directly under root. It is the
// first path component between root and the file.
func languageOf(path, root string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return ""
	}
	rel = filepath.ToSlash(rel)
	if first, _, ok := strings.Cut(rel, "/"); ok {
		return first
	}
	return ""
}

// tagsFromName splits a kebab/dot-separated file name into tags, dropping
// numeric-only leading segments (e.g. "07", "24.2").
func tagsFromName(name string) []string {
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '-' || r == '.' || r == '_'
	})
	var tags []string
	for _, p := range parts {
		if p == "" || isAllDigits(p) {
			continue
		}
		tags = append(tags, p)
	}
	return tags
}

func isAllDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func truncateForLog(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
