package memorystore

import (
	"context"
	"sync"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

// TestStoreSaveInputIsolation locks REVIEW 2.5#27: Save must store a PRIVATE
// copy of the caller's object — mutating the object after Save (or the
// slices/maps inside it) must not be observable through the store. The old
// implementation stored the caller's pointer, so post-Save mutations raced
// with concurrent readers.
func TestStoreSaveInputIsolation(t *testing.T) {
	s := New()
	obj := &knowledge.KnowledgeObject{
		ID:         "obj-1",
		Summary:    "original",
		Raw:        []byte("raw-original"),
		Metadata:   map[string]any{"k": "v"},
		Tags:       []string{"t1"},
		Confidence: 0.9,
	}
	if err := s.Save(context.Background(), obj); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Mutate every alias-bearing field after Save.
	obj.Summary = "mutated"
	obj.Raw[0] = 'X'
	obj.Metadata["k"] = "mutated"
	obj.Tags[0] = "mutated"

	got, err := s.Get(context.Background(), "obj-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Summary != "original" || string(got.Raw) != "raw-original" ||
		got.Metadata["k"] != "v" || got.Tags[0] != "t1" {
		t.Errorf("post-Save caller mutations leaked into the store: %+v", got)
	}
}

// TestStoreReturnedObjectsAreCopies verifies the other boundary: objects
// returned by Get/Query/Search/ListByStatus must be detached from store
// state, so mutating a returned object cannot corrupt the stored copy.
func TestStoreReturnedObjectsAreCopies(t *testing.T) {
	s := New()
	obj := &knowledge.KnowledgeObject{
		ID:         "obj-1",
		Type:       knowledge.ObjectMemory,
		Summary:    "searchable summary",
		Raw:        []byte("raw"),
		Metadata:   map[string]any{"k": "v"},
		Confidence: 0.9,
		Status:     knowledge.StatusActive,
	}
	if err := s.Save(context.Background(), obj); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Mutate through every read path.
	g, _ := s.Get(context.Background(), "obj-1")
	g.Summary = "via-get"
	g.Metadata["k"] = "via-get"
	g.Raw[0] = 'X'

	q, _ := s.Query(context.Background(), knowledge.Query{})
	if len(q) != 1 {
		t.Fatalf("Query: expected 1 result, got %d", len(q))
	}
	q[0].Summary = "via-query"

	sr, _ := s.Search(context.Background(), "searchable", "", 10)
	if len(sr) != 1 {
		t.Fatalf("Search: expected 1 result, got %d", len(sr))
	}
	sr[0].Summary = "via-search"

	lb, _ := s.ListByStatus(context.Background(), "", knowledge.StatusActive, 10)
	if len(lb) != 1 {
		t.Fatalf("ListByStatus: expected 1 result, got %d", len(lb))
	}
	lb[0].Summary = "via-list"

	final, _ := s.Get(context.Background(), "obj-1")
	if final.Summary != "searchable summary" || final.Metadata["k"] != "v" || final.Raw[0] != 'r' {
		t.Errorf("mutations through returned objects corrupted the stored copy: %+v", final)
	}
	// Each read path also returned distinct objects.
	if g == final || q[0] == final {
		t.Error("read paths returned the stored pointer, not copies")
	}
}

// TestStoreHybridSearchResultsAreCopies verifies the HybridSearch boundary:
// the returned ScoredObjects embed store-owned pointers that must be cloned
// before escaping the RLock.
func TestStoreHybridSearchResultsAreCopies(t *testing.T) {
	s := New()
	obj := &knowledge.KnowledgeObject{
		ID:         "obj-1",
		Type:       knowledge.ObjectMemory,
		Summary:    "alpha beta",
		Normalized: "alpha beta",
		Status:     knowledge.StatusActive,
	}
	if err := s.Save(context.Background(), obj); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.SaveRepresentation(context.Background(), &knowledge.Representation{
		ID: "rep-1", ObjectID: "obj-1", Model: "m", Vector: []float32{1, 0},
	}); err != nil {
		t.Fatalf("SaveRepresentation: %v", err)
	}

	res, err := s.HybridSearch(context.Background(), knowledge.HybridSearchRequest{
		Query: "alpha", Model: "m", QueryVector: []float32{1, 0},
	})
	if err != nil {
		t.Fatalf("HybridSearch: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res))
	}
	res[0].Object.Summary = "mutated"
	res[0].Object.Raw = []byte("junk")

	final, _ := s.Get(context.Background(), "obj-1")
	if final.Summary != "alpha beta" {
		t.Errorf("HybridSearch result mutation corrupted the stored object: %q", final.Summary)
	}
}

// TestStoreConcurrentSaveGetUnderRace hammers Save/Get/Query concurrently —
// meaningful under -race: with the old shared-pointer storage this is a
// guaranteed data race.
func TestStoreConcurrentSaveGetUnderRace(t *testing.T) {
	s := New()
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				obj := &knowledge.KnowledgeObject{
					ID:       "obj-shared",
					Summary:  "summary",
					Raw:      []byte("payload"),
					Metadata: map[string]any{"w": w, "i": i},
				}
				_ = s.Save(context.Background(), obj)
				obj.Metadata["mutated"] = true // post-Save mutation
				if got, err := s.Get(context.Background(), "obj-shared"); err == nil {
					_ = got.Summary
				}
				_, _ = s.Query(context.Background(), knowledge.Query{})
			}
		}(w)
	}
	wg.Wait()
}
