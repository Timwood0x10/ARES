package experienceadapters

import (
	"context"
	"testing"

	experience "github.com/Timwood0x10/ares/internal/llmexp"
	"github.com/Timwood0x10/ares/internal/runtime/memory/distillation"
	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
)

// ── Constants ───────────────────────────────────────────────────────────────

func TestDefaultTenant(t *testing.T) {
	if DefaultTenant == "" {
		t.Error("DefaultTenant should not be empty")
	}
}

func TestDefaultListLimit(t *testing.T) {
	if DefaultListLimit != 1000 {
		t.Errorf("DefaultListLimit = %d, want 1000", DefaultListLimit)
	}
}

func TestCountListLimit(t *testing.T) {
	if countListLimit != 10000 {
		t.Errorf("countListLimit = %d, want 10000", countListLimit)
	}
}

// ── memoryTypeToStorageType ─────────────────────────────────────────────────

func TestMemoryTypeToStorageType(t *testing.T) {
	tests := []struct {
		name       string
		memoryType experience.MemoryType
		want       string
	}{
		{"knowledge", experience.MemoryKnowledge, storage_models.ExperienceTypeSuccess},
		{"preference", experience.MemoryPreference, storage_models.ExperienceTypePattern},
		{"interaction", experience.MemoryInteraction, storage_models.ExperienceTypeSolution},
		{"profile", experience.MemoryProfile, storage_models.ExperienceTypeDistilled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := memoryTypeToStorageType(tt.memoryType)
			if got != tt.want {
				t.Errorf("memoryTypeToStorageType(%v) = %q, want %q", tt.memoryType, got, tt.want)
			}
		})
	}
}

// ── storageTypeToMemoryType ─────────────────────────────────────────────────

func TestStorageTypeToMemoryType(t *testing.T) {
	tests := []struct {
		name        string
		storageType string
		want        experience.MemoryType
	}{
		{"success", storage_models.ExperienceTypeSuccess, experience.MemoryKnowledge},
		{"pattern", storage_models.ExperienceTypePattern, experience.MemoryPreference},
		{"solution", storage_models.ExperienceTypeSolution, experience.MemoryInteraction},
		{"distilled", storage_models.ExperienceTypeDistilled, experience.MemoryProfile},
		{"unknown_defaults_to_knowledge", "unknown_type", experience.MemoryKnowledge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := storageTypeToMemoryType(tt.storageType)
			if got != tt.want {
				t.Errorf("storageTypeToMemoryType(%q) = %v, want %v", tt.storageType, got, tt.want)
			}
		})
	}
}

// ── Type mapping roundtrip ──────────────────────────────────────────────────

func TestTypeMappingRoundtrip(t *testing.T) {
	// Every memory type should roundtrip through storage type back to itself.
	memoryTypes := []experience.MemoryType{
		experience.MemoryKnowledge,
		experience.MemoryPreference,
		experience.MemoryInteraction,
		experience.MemoryProfile,
	}
	for _, mt := range memoryTypes {
		st := memoryTypeToStorageType(mt)
		back := storageTypeToMemoryType(st)
		if back != mt {
			t.Errorf("roundtrip(%v) → storage %q → %v, want original", mt, st, back)
		}
	}
}

// ── ToDistillationExperience ────────────────────────────────────────────────

func TestToDistillationExperience(t *testing.T) {
	tests := []struct {
		name         string
		input        *storage_models.Experience
		wantProblem  string
		wantSolution string
		wantConf     float64
	}{
		{
			name: "explicit_problem_solution",
			input: &storage_models.Experience{
				ID:       "e1",
				Type:     storage_models.ExperienceTypeSuccess,
				Problem:  "how to fix bug",
				Solution: "apply patch",
				Score:    0.8,
			},
			wantProblem:  "how to fix bug",
			wantSolution: "apply patch",
			wantConf:     0.8,
		},
		{
			name: "fallback_to_legacy_fields",
			input: &storage_models.Experience{
				ID:     "e2",
				Type:   storage_models.ExperienceTypeSuccess,
				Input:  "legacy problem",
				Output: "legacy solution",
				Score:  0.5,
			},
			wantProblem:  "legacy problem",
			wantSolution: "legacy solution",
			wantConf:     0.5,
		},
		{
			name: "score_clamped_above_one",
			input: &storage_models.Experience{
				ID:       "e3",
				Type:     storage_models.ExperienceTypeSuccess,
				Problem:  "p",
				Solution: "s",
				Score:    1.5,
			},
			wantProblem:  "p",
			wantSolution: "s",
			wantConf:     1.0,
		},
		{
			name: "score_clamped_below_zero",
			input: &storage_models.Experience{
				ID:       "e4",
				Type:     storage_models.ExperienceTypeSuccess,
				Problem:  "p",
				Solution: "s",
				Score:    -0.5,
			},
			wantProblem:  "p",
			wantSolution: "s",
			wantConf:     0.0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToDistillationExperience(tt.input)
			if got.Problem != tt.wantProblem {
				t.Errorf("Problem = %q, want %q", got.Problem, tt.wantProblem)
			}
			if got.Solution != tt.wantSolution {
				t.Errorf("Solution = %q, want %q", got.Solution, tt.wantSolution)
			}
			if got.Confidence != tt.wantConf {
				t.Errorf("Confidence = %v, want %v", got.Confidence, tt.wantConf)
			}
		})
	}
}

func TestToDistillationExperienceExtractionMethod(t *testing.T) {
	// With metadata containing extraction_method.
	e := &storage_models.Experience{
		ID:       "e1",
		Type:     storage_models.ExperienceTypeSuccess,
		Problem:  "p",
		Solution: "s",
		Metadata: map[string]any{"extraction_method": "llm"},
	}
	got := ToDistillationExperience(e)
	if string(got.ExtractionMethod) != "llm" {
		t.Errorf("ExtractionMethod = %q, want llm", got.ExtractionMethod)
	}

	// Without metadata — should default to ExtractionDirect.
	e2 := &storage_models.Experience{
		ID:       "e2",
		Type:     storage_models.ExperienceTypeSuccess,
		Problem:  "p",
		Solution: "s",
	}
	got2 := ToDistillationExperience(e2)
	if got2.ExtractionMethod != distillation.ExtractionDirect {
		t.Errorf("ExtractionMethod = %q, want %q", got2.ExtractionMethod, distillation.ExtractionDirect)
	}
}

// ── ToStorageExperience ─────────────────────────────────────────────────────

func TestToStorageExperience(t *testing.T) {
	tests := []struct {
		name       string
		exp        *distillation.Experience
		tenantID   string
		wantTenant string
		wantType   string
	}{
		{
			name: "knowledge_type",
			exp: &distillation.Experience{
				ID:       "e1",
				Type:     experience.MemoryKnowledge,
				Problem:  "p",
				Solution: "s",
			},
			tenantID:   "tenant-1",
			wantTenant: "tenant-1",
			wantType:   storage_models.ExperienceTypeSuccess,
		},
		{
			name: "preference_type",
			exp: &distillation.Experience{
				ID:   "e2",
				Type: experience.MemoryPreference,
			},
			tenantID:   "tenant-2",
			wantTenant: "tenant-2",
			wantType:   storage_models.ExperienceTypePattern,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToStorageExperience(tt.exp, tt.tenantID)
			if got.TenantID != tt.wantTenant {
				t.Errorf("TenantID = %q, want %q", got.TenantID, tt.wantTenant)
			}
			if got.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", got.Type, tt.wantType)
			}
		})
	}
}

func TestToStorageExperienceMirrorsLegacyFields(t *testing.T) {
	exp := &distillation.Experience{
		ID:       "e1",
		Type:     experience.MemoryKnowledge,
		Problem:  "how to fix",
		Solution: "apply patch",
	}
	got := ToStorageExperience(exp, "tenant-1")
	if got.Input != "how to fix" {
		t.Errorf("Input = %q, want mirrored Problem", got.Input)
	}
	if got.Output != "apply patch" {
		t.Errorf("Output = %q, want mirrored Solution", got.Output)
	}
}

func TestToStorageExperienceExtractionMethodMetadata(t *testing.T) {
	exp := &distillation.Experience{
		ID:               "e1",
		Type:             experience.MemoryKnowledge,
		Problem:          "p",
		Solution:         "s",
		ExtractionMethod: "llm",
	}
	got := ToStorageExperience(exp, "tenant-1")
	if got.Metadata == nil {
		t.Fatal("Metadata is nil")
	}
	method, ok := got.Metadata["extraction_method"].(string)
	if !ok || method != "llm" {
		t.Errorf("Metadata[extraction_method] = %v, want llm", got.Metadata["extraction_method"])
	}
}

// ── Adapter nil-safety ──────────────────────────────────────────────────────

func TestExperienceSearcherNilSafety(t *testing.T) {
	var s *ExperienceSearcher
	_, err := s.SearchByVector(context.TODO(), nil, "", 10)
	if err == nil {
		t.Error("expected error for nil searcher")
	}
}

func TestExperienceSearcherNilRepo(t *testing.T) {
	s := NewExperienceSearcher(nil)
	_, err := s.SearchByVector(context.TODO(), nil, "", 10)
	if err == nil {
		t.Error("expected error for nil repo")
	}
}

func TestDistillationRepoNilSafety(t *testing.T) {
	var r *DistillationRepo
	_, err := r.SearchByVector(context.TODO(), nil, "", 10)
	if err == nil {
		t.Error("expected error for nil repo")
	}

	_, err = r.CountByMemoryType(context.TODO(), "tenant", experience.MemoryKnowledge)
	if err == nil {
		t.Error("expected error for nil repo count")
	}

	err = r.Create(context.TODO(), nil)
	if err == nil {
		t.Error("expected error for nil repo create")
	}

	err = r.Update(context.TODO(), nil)
	if err == nil {
		t.Error("expected error for nil repo update")
	}
}

func TestDistillationRepoNilExperience(t *testing.T) {
	r := NewDistillationRepo(nil, "tenant")
	err := r.Create(context.TODO(), nil)
	if err == nil {
		t.Error("expected error for nil experience")
	}
	err = r.Update(context.TODO(), nil)
	if err == nil {
		t.Error("expected error for nil experience update")
	}
}

func TestNewDistillationRepoDefaultTenant(t *testing.T) {
	r := NewDistillationRepo(nil, "")
	if r.DefaultTenant != DefaultTenant {
		t.Errorf("DefaultTenant = %q, want %q", r.DefaultTenant, DefaultTenant)
	}

	r2 := NewDistillationRepo(nil, "custom-tenant")
	if r2.DefaultTenant != "custom-tenant" {
		t.Errorf("DefaultTenant = %q, want custom-tenant", r2.DefaultTenant)
	}
}

func TestDistillationRepoWriteTenant(t *testing.T) {
	r := NewDistillationRepo(nil, "default-tenant")
	// Without context tenant — should return DefaultTenant.
	got := r.writeTenant(context.TODO())
	if got != "default-tenant" {
		t.Errorf("writeTenant = %q, want default-tenant", got)
	}
}
