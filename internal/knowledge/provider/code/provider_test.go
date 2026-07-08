package code

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
)

func TestNewCodeProvider(t *testing.T) {
	dir := t.TempDir()

	t.Run("valid provider", func(t *testing.T) {
		p, err := New("my-code", dir)
		if err != nil {
			t.Fatalf("New() error: %v", err)
		}
		if p.Name() != "my-code" {
			t.Fatalf("expected name 'my-code', got %q", p.Name())
		}
	})

	t.Run("empty name", func(t *testing.T) {
		_, err := New("", dir)
		if err == nil {
			t.Fatal("expected error for empty name")
		}
	})

	t.Run("empty root", func(t *testing.T) {
		_, err := New("test", "")
		if err == nil {
			t.Fatal("expected error for empty root")
		}
	})

	t.Run("non-existent dir", func(t *testing.T) {
		_, err := New("test", "/nonexistent/path")
		if err == nil {
			t.Fatal("expected error for non-existent dir")
		}
	})

	t.Run("file not dir", func(t *testing.T) {
		f := filepath.Join(dir, "file.txt")
		if err := os.WriteFile(f, []byte("hello"), 0644); err != nil {
			t.Fatal(err)
		}
		_, err := New("test", f)
		if err == nil {
			t.Fatal("expected error for file, not dir")
		}
	})
}

func TestIntentMatch(t *testing.T) {
	dir := t.TempDir()
	p, err := New("test", dir)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		goal    string
		wantGeq float64
	}{
		{"what is the architecture", 0.7},
		{"show me the code", 0.7},
		{"find function definitions", 0.7},
		{"list API endpoints", 0.7},
		{"struct definitions", 0.7},
		{"interface design", 0.7},
		{"dependency analysis", 0.7},
		{"hello world", 0.2},
		{"user preferences", 0.2},
	}

	for _, tt := range tests {
		t.Run(tt.goal, func(t *testing.T) {
			got := p.IntentMatch(knowledge.Intent{Goal: tt.goal})
			if got < tt.wantGeq {
				t.Errorf("IntentMatch(%q) = %.2f, want >= %.2f", tt.goal, got, tt.wantGeq)
			}
		})
	}
}

func TestStreamEmptyDir(t *testing.T) {
	dir := t.TempDir()
	p, err := New("test", dir)
	if err != nil {
		t.Fatal(err)
	}

	objCh, errCh := p.Stream(context.Background(), knowledge.Intent{Scope: knowledge.Scope{MaxObjects: 10}})
	var objs []*knowledge.KnowledgeObject
	for obj := range objCh {
		objs = append(objs, obj)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	default:
	}

	if len(objs) != 0 {
		t.Fatalf("expected 0 objects for empty dir, got %d", len(objs))
	}
}

func TestStreamGoFiles(t *testing.T) {
	dir := t.TempDir()

	// Create test Go source files.
	files := map[string]string{
		"math.go": `package util

// Add returns the sum of two integers.
func Add(a, b int) int {
	return a + b
}

// Point represents a 2D coordinate.
type Point struct {
	X, Y int
}

// Shaper defines a shape interface.
type Shaper interface {
	Area() float64
}
`,
		"strings.go": `package util

// Join concatenates strings with a separator.
func Join(parts []string, sep string) string {
	var result string
	for i, s := range parts {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}
`,
	}

	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}

	p, err := New("code", dir)
	if err != nil {
		t.Fatal(err)
	}

	objCh, errCh := p.Stream(context.Background(), knowledge.Intent{Scope: knowledge.Scope{MaxObjects: 100}})
	var objs []*knowledge.KnowledgeObject
	for obj := range objCh {
		objs = append(objs, obj)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	default:
	}

	if len(objs) != 4 {
		t.Fatalf("expected 4 objects (2 funcs + 1 struct + 1 interface), got %d", len(objs))
	}

	// Sort for deterministic assertions.
	sort.Slice(objs, func(i, j int) bool {
		return objs[i].ID < objs[j].ID
	})

	// Verify each object.
	tests := []struct {
		idSuffix string
		objType  knowledge.ObjectType
		tags     []string
	}{
		{"util.Add", knowledge.ObjectCode, []string{"function", "util"}},
		{"util.Join", knowledge.ObjectCode, []string{"function", "util"}},
		{"util.Point", knowledge.ObjectCode, []string{"struct", "util"}},
		{"util.Shaper", knowledge.ObjectCode, []string{"interface", "util"}},
	}

	for i, tt := range tests {
		expectedID := "code:" + tt.idSuffix
		if objs[i].ID != expectedID {
			t.Errorf("object %d: expected ID %q, got %q", i, expectedID, objs[i].ID)
		}
		if objs[i].Type != tt.objType {
			t.Errorf("object %d %q: expected type %q, got %q", i, objs[i].ID, tt.objType, objs[i].Type)
		}
		if objs[i].Namespace != "code" {
			t.Errorf("expected namespace 'code', got %q", objs[i].Namespace)
		}
		if objs[i].Confidence != 0.9 {
			t.Errorf("expected confidence 0.9, got %.2f", objs[i].Confidence)
		}
	}

	// Verify the Add function has its doc comment as summary.
	addObj := objs[0]
	if addObj.ID == "code:util.Add" && addObj.Summary != "Add returns the sum of two integers." {
		t.Errorf("expected doc comment as summary, got %q", addObj.Summary)
	}
}

func TestStreamSkipTestFiles(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "helper.go"), []byte("package test\nfunc Helper() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "helper_test.go"), []byte("package test\nfunc TestHelper(t *testing.T) {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	p, err := New("test", dir)
	if err != nil {
		t.Fatal(err)
	}

	objCh, _ := p.Stream(context.Background(), knowledge.Intent{Scope: knowledge.Scope{MaxObjects: 10}})
	var objs []*knowledge.KnowledgeObject
	for obj := range objCh {
		objs = append(objs, obj)
	}

	// Only the non-test file's declaration should be found.
	if len(objs) != 1 {
		t.Fatalf("expected 1 object (skip _test.go), got %d", len(objs))
	}
	if objs[0].ID != "test:test.Helper" {
		t.Fatalf("expected Helper function, got %q", objs[0].ID)
	}
}

func TestStreamMaxResults(t *testing.T) {
	dir := t.TempDir()

	// Create a file with many functions.
	src := "package many\n"
	for i := 0; i < 20; i++ {
		src += fmt.Sprintf("func F%d() {}\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "many.go"), []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	// We need fmt for the formatted source.
	_ = fmt.Sprintf

	p, err := New("test", dir)
	if err != nil {
		t.Fatal(err)
	}

	objCh, _ := p.Stream(context.Background(), knowledge.Intent{Scope: knowledge.Scope{MaxObjects: 5}})
	var objs []*knowledge.KnowledgeObject
	for obj := range objCh {
		objs = append(objs, obj)
	}

	if len(objs) > 5 {
		t.Fatalf("expected at most 5 objects (MaxObjects=5), got %d", len(objs))
	}
}

func TestStreamCancelContext(t *testing.T) {
	dir := t.TempDir()

	var src = "package many\n"
	for i := 0; i < 100; i++ {
		src += fmt.Sprintf("func F%d() {}\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "many.go"), []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	p, err := New("test", dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	objCh, _ := p.Stream(ctx, knowledge.Intent{Scope: knowledge.Scope{MaxObjects: 1000}})
	var objs []*knowledge.KnowledgeObject
	for obj := range objCh {
		objs = append(objs, obj)
	}

	if len(objs) != 0 {
		t.Fatalf("expected 0 objects with cancelled context, got %d", len(objs))
	}
}

func TestStreamSkipVendor(t *testing.T) {
	dir := t.TempDir()

	// Create code outside vendor.
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc Main() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create code inside vendor (should be skipped).
	vendorDir := filepath.Join(dir, "vendor", "example.com", "lib")
	if err := os.MkdirAll(vendorDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vendorDir, "lib.go"), []byte("package lib\nfunc LibFunc() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}

	p, err := New("test", dir)
	if err != nil {
		t.Fatal(err)
	}

	objCh, _ := p.Stream(context.Background(), knowledge.Intent{Scope: knowledge.Scope{MaxObjects: 100}})
	var objs []*knowledge.KnowledgeObject
	for obj := range objCh {
		objs = append(objs, obj)
	}

	if len(objs) != 1 {
		t.Fatalf("expected 1 object (skip vendor), got %d", len(objs))
	}
	if objs[0].ID != "test:main.Main" {
		t.Fatalf("expected main.Main, got %q", objs[0].ID)
	}
}
