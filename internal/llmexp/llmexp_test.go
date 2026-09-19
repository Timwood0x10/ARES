package llmexp

import (
	"context"
	"testing"
	"time"
)

// ── MemoryType.String ───────────────────────────────────────────────────────

func TestMemoryTypeString(t *testing.T) {
	tests := []struct {
		name string
		give MemoryType
		want string
	}{
		{"knowledge", MemoryKnowledge, "fact"},
		{"preference", MemoryPreference, "preference"},
		{"interaction", MemoryInteraction, "solution"},
		{"profile", MemoryProfile, "profile"},
		{"unknown_passthrough", MemoryType("custom"), "custom"},
		{"empty_passthrough", MemoryType(""), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.give.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ── MemoryType constants ────────────────────────────────────────────────────

func TestMemoryTypeConstants(t *testing.T) {
	consts := []struct {
		name string
		val  MemoryType
		want string
	}{
		{"knowledge", MemoryKnowledge, "knowledge"},
		{"preference", MemoryPreference, "preference"},
		{"interaction", MemoryInteraction, "interaction"},
		{"profile", MemoryProfile, "profile"},
	}
	for _, tt := range consts {
		if string(tt.val) != tt.want {
			t.Errorf("%s = %q, want %q", tt.name, tt.val, tt.want)
		}
	}
}

// ── ExtractionMethod constants ──────────────────────────────────────────────

func TestExtractionMethodConstants(t *testing.T) {
	if string(ExtractionDirect) != "direct" {
		t.Errorf("ExtractionDirect = %q", ExtractionDirect)
	}
	if string(ExtractionCrossTurn) != "cross-turn" {
		t.Errorf("ExtractionCrossTurn = %q", ExtractionCrossTurn)
	}
}

// ── ResolutionStrategy constants ────────────────────────────────────────────

func TestResolutionStrategyConstants(t *testing.T) {
	consts := []struct {
		name string
		val  ResolutionStrategy
		want string
	}{
		{"replace", ReplaceOld, "replace"},
		{"keep_old", KeepOld, "keep_old"},
		{"keep_both", KeepBoth, "version"},
		{"merge", Merge, "merge"},
	}
	for _, tt := range consts {
		if string(tt.val) != tt.want {
			t.Errorf("%s = %q, want %q", tt.name, tt.val, tt.want)
		}
	}
}

// ── Experience struct ───────────────────────────────────────────────────────

func TestExperienceFields(t *testing.T) {
	exp := Experience{
		ID:               "exp-1",
		Type:             MemoryKnowledge,
		Problem:          "how to optimize",
		Solution:         "use caching",
		Confidence:       0.9,
		ExtractionMethod: ExtractionDirect,
		Vector:           []float64{0.1, 0.2, 0.3},
	}

	if exp.ID != "exp-1" {
		t.Errorf("ID = %q", exp.ID)
	}
	if exp.Type != MemoryKnowledge {
		t.Errorf("Type = %q", exp.Type)
	}
	if exp.Problem != "how to optimize" {
		t.Errorf("Problem = %q", exp.Problem)
	}
	if exp.Solution != "use caching" {
		t.Errorf("Solution = %q", exp.Solution)
	}
	if exp.Confidence != 0.9 {
		t.Errorf("Confidence = %v", exp.Confidence)
	}
	if exp.ExtractionMethod != ExtractionDirect {
		t.Errorf("ExtractionMethod = %q", exp.ExtractionMethod)
	}
	if len(exp.Vector) != 3 {
		t.Errorf("Vector len = %d", len(exp.Vector))
	}
}

func TestExperienceZeroValue(t *testing.T) {
	var exp Experience
	if exp.ID != "" {
		t.Errorf("ID = %q", exp.ID)
	}
	if exp.Type != "" {
		t.Errorf("Type = %q", exp.Type)
	}
	if exp.Confidence != 0 {
		t.Errorf("Confidence = %v", exp.Confidence)
	}
	if exp.Vector != nil {
		t.Error("Vector should be nil")
	}
}

// ── StoredExperience struct ─────────────────────────────────────────────────

func TestStoredExperienceFields(t *testing.T) {
	se := StoredExperience{
		TenantID: "tenant-1",
		Type:     "solution",
		Problem:  "problem",
		Solution: "solution",
		Score:    0.85,
		Source:   "distiller",
		Metadata: map[string]interface{}{"key": "value"},
	}

	if se.TenantID != "tenant-1" {
		t.Errorf("TenantID = %q", se.TenantID)
	}
	if se.Type != "solution" {
		t.Errorf("Type = %q", se.Type)
	}
	if se.Score != 0.85 {
		t.Errorf("Score = %v", se.Score)
	}
	if se.Metadata["key"] != "value" {
		t.Errorf("Metadata[key] = %v", se.Metadata["key"])
	}
}

// ── Memory struct ───────────────────────────────────────────────────────────

func TestMemoryFields(t *testing.T) {
	now := time.Now()
	ttl := 24 * time.Hour
	mem := Memory{
		ID:         "mem-1",
		Type:       MemoryPreference,
		Content:    "user prefers dark mode",
		Importance: 0.7,
		Source:     "conversation-42",
		Vector:     []float64{0.4, 0.5},
		TTL:        ttl,
		CreatedAt:  now,
		ExpiresAt:  now.Add(ttl),
		Metadata:   map[string]interface{}{"session": "s1"},
	}

	if mem.ID != "mem-1" {
		t.Errorf("ID = %q", mem.ID)
	}
	if mem.Type != MemoryPreference {
		t.Errorf("Type = %q", mem.Type)
	}
	if mem.Content != "user prefers dark mode" {
		t.Errorf("Content = %q", mem.Content)
	}
	if mem.Importance != 0.7 {
		t.Errorf("Importance = %v", mem.Importance)
	}
	if mem.TTL != ttl {
		t.Errorf("TTL = %v", mem.TTL)
	}
	if mem.ExpiresAt.Before(mem.CreatedAt) {
		t.Error("ExpiresAt should be after CreatedAt")
	}
	if mem.Metadata["session"] != "s1" {
		t.Errorf("Metadata[session] = %v", mem.Metadata["session"])
	}
}

// ── ExperienceStore interface (compile-time check) ──────────────────────────

type mockExperienceStore struct {
	created []*StoredExperience
}

func (m *mockExperienceStore) Create(_ context.Context, exp *StoredExperience) error {
	m.created = append(m.created, exp)
	return nil
}

func TestExperienceStoreInterface(t *testing.T) {
	var store ExperienceStore = &mockExperienceStore{}

	exp := &StoredExperience{TenantID: "t1", Type: "solution", Problem: "p", Solution: "s"}
	if err := store.Create(context.Background(), exp); err != nil {
		t.Fatalf("Create: %v", err)
	}

	ms := store.(*mockExperienceStore)
	if len(ms.created) != 1 {
		t.Errorf("created len = %d, want 1", len(ms.created))
	}
	if ms.created[0].TenantID != "t1" {
		t.Errorf("TenantID = %q", ms.created[0].TenantID)
	}
}

// ── ExperienceRepository interface (compile-time check) ─────────────────────

type mockExpRepo struct{}

func (m *mockExpRepo) SearchByVector(_ context.Context, _ []float64, _ string, _ int) ([]Experience, error) {
	return nil, nil
}
func (m *mockExpRepo) GetByMemoryType(_ context.Context, _ string, _ MemoryType) ([]Experience, error) {
	return nil, nil
}
func (m *mockExpRepo) CountByMemoryType(_ context.Context, _ string, _ MemoryType) (int, error) {
	return 0, nil
}
func (m *mockExpRepo) Update(_ context.Context, _ *Experience) error   { return nil }
func (m *mockExpRepo) Delete(_ context.Context, _ string) error        { return nil }
func (m *mockExpRepo) DeleteBatch(_ context.Context, _ []string) error { return nil }
func (m *mockExpRepo) Create(_ context.Context, _ *Experience) error   { return nil }

func TestExperienceRepositoryInterface(t *testing.T) {
	var repo ExperienceRepository = &mockExpRepo{}

	ctx := context.Background()

	// Exercise all interface methods
	if _, err := repo.SearchByVector(ctx, []float64{0.1}, "t", 10); err != nil {
		t.Errorf("SearchByVector: %v", err)
	}
	if _, err := repo.GetByMemoryType(ctx, "t", MemoryKnowledge); err != nil {
		t.Errorf("GetByMemoryType: %v", err)
	}
	if _, err := repo.CountByMemoryType(ctx, "t", MemoryKnowledge); err != nil {
		t.Errorf("CountByMemoryType: %v", err)
	}
	if err := repo.Update(ctx, &Experience{ID: "e1"}); err != nil {
		t.Errorf("Update: %v", err)
	}
	if err := repo.Delete(ctx, "e1"); err != nil {
		t.Errorf("Delete: %v", err)
	}
	if err := repo.DeleteBatch(ctx, []string{"e1", "e2"}); err != nil {
		t.Errorf("DeleteBatch: %v", err)
	}
	if err := repo.Create(ctx, &Experience{ID: "e2"}); err != nil {
		t.Errorf("Create: %v", err)
	}
}

// ── MemoryType roundtrip through String ─────────────────────────────────────

func TestMemoryTypeStringRoundtrip(t *testing.T) {
	types := []MemoryType{MemoryKnowledge, MemoryPreference, MemoryInteraction, MemoryProfile}
	for _, mt := range types {
		s := mt.String()
		if s == "" {
			t.Errorf("String(%q) returned empty", mt)
		}
	}
}
