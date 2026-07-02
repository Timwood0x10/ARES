package discovery

import (
	"testing"
)

func TestMergeRecords_SingleRecord(t *testing.T) {
	records := []DiscoveryRecord{
		{Source: "claude", Confidence: ConfidenceHigh, Endpoint: "codegraph"},
	}
	services := mergeRecords(records)
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}
	svc := services["codegraph"]
	if svc == nil {
		t.Fatal("expected service 'codegraph'")
	}
	if svc.BestSource != "claude" {
		t.Errorf("expected best source 'claude', got %q", svc.BestSource)
	}
}

func TestMergeRecords_SameEndpointMerged(t *testing.T) {
	records := []DiscoveryRecord{
		{Source: "claude", Confidence: ConfidenceHigh, Endpoint: "codegraph"},
		{Source: "binary-probe", Confidence: ConfidenceMedium, Endpoint: "/usr/local/bin/codegraph"},
	}
	services := mergeRecords(records)
	if len(services) != 1 {
		t.Fatalf("expected 1 service (merged), got %d", len(services))
	}
	svc := services["codegraph"]
	if len(svc.Records) != 2 {
		t.Errorf("expected 2 records, got %d", len(svc.Records))
	}
	if svc.BestSource != "claude" {
		t.Errorf("expected best source 'claude' (higher confidence), got %q", svc.BestSource)
	}
}

func TestMergeRecords_DifferentEndpointsNotMerged(t *testing.T) {
	records := []DiscoveryRecord{
		{Source: "claude", Confidence: ConfidenceHigh, Endpoint: "codegraph"},
		{Source: "cursor", Confidence: ConfidenceHigh, Endpoint: "postgres-mcp"},
	}
	services := mergeRecords(records)
	if len(services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(services))
	}
}

func TestMergeRecords_TagsCollected(t *testing.T) {
	records := []DiscoveryRecord{
		{Source: "probe", Endpoint: "tool-a", Tags: []string{"capability:search", "domain:code"}},
		{Source: "claude", Endpoint: "tool-a", Tags: []string{"capability:search", "priority:high"}},
	}
	services := mergeRecords(records)
	svc := services["tool-a"]
	if len(svc.Identity.Tags) != 3 {
		t.Errorf("expected 3 unique tags, got %d: %v", len(svc.Identity.Tags), svc.Identity.Tags)
	}
}

func TestMergeRecords_MetadataMerged(t *testing.T) {
	records := []DiscoveryRecord{
		{Source: "probe", Endpoint: "tool-a", Confidence: ConfidenceMedium, Metadata: map[string]string{"version": "1.0"}},
		{Source: "ares", Endpoint: "tool-a", Confidence: ConfidenceMax, Metadata: map[string]string{"description": "A tool"}},
	}
	services := mergeRecords(records)
	svc := services["tool-a"]
	if svc.Identity.Metadata["version"] != "1.0" {
		t.Errorf("expected version=1.0, got %q", svc.Identity.Metadata["version"])
	}
	if svc.Identity.Metadata["description"] != "A tool" {
		t.Errorf("expected description='A tool', got %q", svc.Identity.Metadata["description"])
	}
}

func TestDiffServices_Added(t *testing.T) {
	existing := map[string]*DiscoveredService{}
	newSvc := map[string]*DiscoveredService{
		"tool-a": {Identity: ServiceIdentity{ID: "tool-a"}},
	}
	added, _, removed := diffServices(existing, newSvc)
	if len(added) != 1 || added[0] != "tool-a" {
		t.Errorf("expected added=[tool-a], got %v", added)
	}
	if len(removed) != 0 {
		t.Errorf("expected no removals, got %v", removed)
	}
}

func TestDiffServices_Removed(t *testing.T) {
	existing := map[string]*DiscoveredService{
		"tool-a": {Identity: ServiceIdentity{ID: "tool-a"}},
	}
	newServices := map[string]*DiscoveredService{}
	added, _, removed := diffServices(existing, newServices)
	if len(added) != 0 {
		t.Errorf("expected no adds, got %v", added)
	}
	if len(removed) != 1 || removed[0] != "tool-a" {
		t.Errorf("expected removed=[tool-a], got %v", removed)
	}
}

func TestDiffServices_Updated(t *testing.T) {
	existing := map[string]*DiscoveredService{
		"tool-a": {
			Identity: ServiceIdentity{ID: "tool-a", Tags: []string{"a"}},
			Records:  []DiscoveryRecord{{Source: "claude"}},
		},
	}
	newServices := map[string]*DiscoveredService{
		"tool-a": {
			Identity: ServiceIdentity{ID: "tool-a", Tags: []string{"a", "b"}},
			Records:  []DiscoveryRecord{{Source: "claude"}},
		},
	}
	added, updated, removed := diffServices(existing, newServices)
	if len(updated) != 1 || updated[0] != "tool-a" {
		t.Errorf("expected updated=[tool-a], got %v", updated)
	}
	if len(added) != 0 || len(removed) != 0 {
		t.Errorf("expected no adds/removes, got added=%v removed=%v", added, removed)
	}
}

func TestHasChanged_TagsChanged(t *testing.T) {
	old := &DiscoveredService{
		Identity: ServiceIdentity{Tags: []string{"a", "b"}},
		Records:  []DiscoveryRecord{{Source: "x"}},
	}
	new := &DiscoveredService{
		Identity: ServiceIdentity{Tags: []string{"a", "b", "c"}},
		Records:  []DiscoveryRecord{{Source: "x"}},
	}
	if !hasChanged(old, new) {
		t.Error("expected hasChanged=true when tags differ")
	}
}

func TestHasChanged_MetadataChanged(t *testing.T) {
	old := &DiscoveredService{
		Identity: ServiceIdentity{Metadata: map[string]string{"version": "1.0"}},
		Records:  []DiscoveryRecord{{Source: "x"}},
	}
	new := &DiscoveredService{
		Identity: ServiceIdentity{Metadata: map[string]string{"version": "2.0"}},
		Records:  []DiscoveryRecord{{Source: "x"}},
	}
	if !hasChanged(old, new) {
		t.Error("expected hasChanged=true when metadata differs")
	}
}

func TestHasChanged_NoChange(t *testing.T) {
	old := &DiscoveredService{
		Identity: ServiceIdentity{Tags: []string{"a"}, Metadata: map[string]string{"k": "v"}},
		Records:  []DiscoveryRecord{{Source: "x"}},
	}
	new := &DiscoveredService{
		Identity: ServiceIdentity{Tags: []string{"a"}, Metadata: map[string]string{"k": "v"}},
		Records:  []DiscoveryRecord{{Source: "x"}},
	}
	if hasChanged(old, new) {
		t.Error("expected hasChanged=false when nothing changed")
	}
}

func TestNormalizeEndpoint_ExtractsName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"codegraph", "codegraph"},
		{"/usr/local/bin/codegraph", "codegraph"},
		{"/opt/homebrew/bin/codebase-memory-mcp", "codebase-memory-mcp"},
		{"uvx blender-mcp", "uvx blender-mcp"},
		{"", "unknown"},
	}
	for _, tt := range tests {
		got := normalizeEndpoint(tt.input)
		if got != tt.want {
			t.Errorf("normalizeEndpoint(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestStringSliceEqual(t *testing.T) {
	tests := []struct {
		a, b []string
		want bool
	}{
		{nil, nil, true},
		{[]string{"a"}, []string{"a"}, true},
		{[]string{"a", "b"}, []string{"b", "a"}, true},
		{[]string{"a"}, []string{"b"}, false},
		{[]string{"a"}, nil, false},
	}
	for _, tt := range tests {
		got := stringSliceEqual(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("stringSliceEqual(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestStringMapEqual(t *testing.T) {
	tests := []struct {
		a, b map[string]string
		want bool
	}{
		{nil, nil, true},
		{map[string]string{"a": "1"}, map[string]string{"a": "1"}, true},
		{map[string]string{"a": "1"}, map[string]string{"a": "2"}, false},
		{map[string]string{"a": "1"}, map[string]string{"b": "1"}, false},
	}
	for _, tt := range tests {
		got := stringMapEqual(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("stringMapEqual(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}
