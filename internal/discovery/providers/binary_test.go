package providers

import (
	"context"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/discovery"
)

func TestBinaryProbeProvider_Name(t *testing.T) {
	p := NewBinaryProbeProvider()
	if p.Name() != "binary-probe" {
		t.Errorf("expected name 'binary-probe', got %q", p.Name())
	}
}

func TestBinaryProbeProvider_Confidence(t *testing.T) {
	p := NewBinaryProbeProvider()
	if p.Confidence() != discovery.ConfidenceMedium {
		t.Errorf("expected confidence %d, got %d", discovery.ConfidenceMedium, p.Confidence())
	}
}

func TestBinaryProbeProvider_Discover(t *testing.T) {
	p := NewBinaryProbeProvider()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	records, err := p.Discover(ctx)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}

	// May find 0+ MCP binaries in PATH.
	for _, r := range records {
		if r.Source != "binary-probe" {
			t.Errorf("expected source 'binary-probe', got %q", r.Source)
		}
		if r.Endpoint == "" {
			t.Error("expected non-empty endpoint")
		}
		if r.Confidence != discovery.ConfidenceMedium {
			t.Errorf("expected confidence %d, got %d", discovery.ConfidenceMedium, r.Confidence)
		}
	}
}

func TestExtractTags(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"search for code patterns", []string{"capability:search", "domain:code"}},
		{"database query tool", []string{"capability:database", "capability:query"}},
		{"file system operations", []string{"capability:file"}},
		{"nothing relevant", nil},
	}
	for _, tt := range tests {
		got := extractTags(tt.input)
		gotSet := make(map[string]bool)
		for _, tag := range got {
			gotSet[tag] = true
		}
		for _, want := range tt.want {
			if !gotSet[want] {
				t.Errorf("extractTags(%q): missing %q, got %v", tt.input, want, got)
			}
		}
	}
}

func TestProbeBinary_Nonexistent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	metadata := probeBinary(ctx, "/nonexistent/binary")
	if metadata.isMCP {
		t.Error("expected isMCP=false for nonexistent binary")
	}
}
