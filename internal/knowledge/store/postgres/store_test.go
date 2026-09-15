package postgresstore

import (
	"strings"
	"testing"
)

func TestPqStringArrayScan_Empty(t *testing.T) {
	var a pqStringArray
	if err := a.Scan(nil); err != nil {
		t.Fatalf("Scan nil: %v", err)
	}
	if len(a) != 0 {
		t.Errorf("expected empty, got %v", a)
	}
}

func TestPqStringArrayScan_Single(t *testing.T) {
	var a pqStringArray
	if err := a.Scan("{hello}"); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(a) != 1 || a[0] != "hello" {
		t.Errorf("expected [hello], got %v", a)
	}
}

func TestPqStringArrayScan_Multiple(t *testing.T) {
	var a pqStringArray
	if err := a.Scan("{a,b,c}"); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(a) != 3 || a[0] != "a" || a[1] != "b" || a[2] != "c" {
		t.Errorf("expected [a b c], got %v", a)
	}
}

func TestPqStringArrayScan_Quoted(t *testing.T) {
	var a pqStringArray
	if err := a.Scan(`{"hello world","foo,bar"}`); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(a) != 2 {
		t.Errorf("expected 2 elements, got %d: %v", len(a), a)
	}
}

func TestPqStringArrayScan_ByteSlice(t *testing.T) {
	var a pqStringArray
	if err := a.Scan([]byte("{x,y,z}")); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(a) != 3 {
		t.Errorf("expected 3 elements, got %d", len(a))
	}
}

func TestPqFloat32ArrayScan_Empty(t *testing.T) {
	var a pqFloat32Array
	if err := a.Scan(nil); err != nil {
		t.Fatalf("Scan nil: %v", err)
	}
	if len(a) != 0 {
		t.Errorf("expected empty, got %v", a)
	}
}

func TestPqFloat32ArrayScan_Values(t *testing.T) {
	var a pqFloat32Array
	if err := a.Scan("{1.5,2.0,3.5}"); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(a) != 3 {
		t.Errorf("expected 3 elements, got %d", len(a))
	}
	if a[0] != 1.5 || a[1] != 2.0 || a[2] != 3.5 {
		t.Errorf("unexpected values: %v", a)
	}
}

func TestPqFloat32ArrayScan_ByteSlice(t *testing.T) {
	var a pqFloat32Array
	if err := a.Scan([]byte("{0.5,1.0}")); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(a) != 2 || a[0] != 0.5 || a[1] != 1.0 {
		t.Errorf("unexpected values: %v", a)
	}
}

// TestSaveUpsertSQLGuardsNamespace pins the tenant-ownership guard in the
// Save statement (the postgres package has no live-database test harness, so
// the SQL shape is asserted directly, same approach as
// TestHybridSearchCandidateQueryShape). Objects are keyed by id alone; without
// `WHERE akf_objects.namespace = EXCLUDED.namespace` on the DO UPDATE branch,
// an upsert carrying another tenant's id would overwrite that row and
// re-stamp it with the caller's namespace — the cross-tenant migration that
// StoreAdapter.UpdateKnowledge relies on Save to refuse.
func TestSaveUpsertSQLGuardsNamespace(t *testing.T) {
	if !strings.Contains(saveUpsertSQL, "ON CONFLICT (id) DO UPDATE SET") {
		t.Fatal("saveUpsertSQL must remain an upsert on the id key")
	}
	if !strings.Contains(saveUpsertSQL, "WHERE akf_objects.namespace = EXCLUDED.namespace") {
		t.Fatal("saveUpsertSQL lost the namespace ownership guard on the update branch")
	}
}
