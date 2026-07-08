package postgresstore

import (
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
