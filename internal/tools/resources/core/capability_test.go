package core

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCapabilityConstants tests all capability constants.
func TestCapabilityConstants(t *testing.T) {
	tests := []struct {
		name string
		cap  Capability
		want string
	}{
		{
			name: "math capability",
			cap:  CapabilityMath,
			want: "math",
		},
		{
			name: "knowledge capability",
			cap:  CapabilityKnowledge,
			want: "knowledge",
		},
		{
			name: "memory capability",
			cap:  CapabilityMemory,
			want: "memory",
		},
		{
			name: "text capability",
			cap:  CapabilityText,
			want: "text",
		},
		{
			name: "network capability",
			cap:  CapabilityNetwork,
			want: "network",
		},
		{
			name: "time capability",
			cap:  CapabilityTime,
			want: "time",
		},
		{
			name: "file capability",
			cap:  CapabilityFile,
			want: "file",
		},
		{
			name: "external capability",
			cap:  CapabilityExternal,
			want: "external",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.cap) != tt.want {
				t.Errorf("got %q, want %q", tt.cap, tt.want)
			}
		})
	}
}

// TestCapabilityUniqueness ensures all capabilities are unique.
func TestCapabilityUniqueness(t *testing.T) {
	caps := map[Capability]bool{
		CapabilityMath:      true,
		CapabilityKnowledge: true,
		CapabilityMemory:    true,
		CapabilityText:      true,
		CapabilityNetwork:   true,
		CapabilityTime:      true,
		CapabilityFile:      true,
		CapabilityExternal:  true,
	}

	if len(caps) != 8 {
		t.Errorf("expected 8 unique capabilities, got %d", len(caps))
	}
}

// TestCapabilityEngineNew tests creating a new CapabilityEngine.
func TestCapabilityEngineNew(t *testing.T) {
	registry := NewRegistry()
	engine := NewCapabilityEngine(registry)

	require.NotNil(t, engine)

	if engine.registry != registry {
		t.Error("engine registry should match the provided registry")
	}
}

// TestCapabilityEngineDetect tests capability detection.
func TestCapabilityEngineDetect(t *testing.T) {
	registry := NewRegistry()
	engine := NewCapabilityEngine(registry)

	tests := []struct {
		name      string
		query     string
		minCaps   int
		maxCaps   int
		checkCaps []Capability
	}{
		{
			name:    "math query in English",
			query:   "calculate the sum of 5 and 3",
			minCaps: 1,
			maxCaps: 3,
			checkCaps: []Capability{
				CapabilityMath,
			},
		},
		{
			name:    "math query in Chinese",
			query:   "计算 5 加 3",
			minCaps: 1,
			maxCaps: 3,
			checkCaps: []Capability{
				CapabilityMath,
			},
		},
		{
			name:    "knowledge query in English",
			query:   "what is the capital of France",
			minCaps: 1,
			maxCaps: 3,
			checkCaps: []Capability{
				CapabilityKnowledge,
			},
		},
		{
			name:    "knowledge query in Chinese",
			query:   "什么是法国的首都",
			minCaps: 1,
			maxCaps: 3,
			checkCaps: []Capability{
				CapabilityKnowledge,
			},
		},
		{
			name:    "multiple capabilities",
			query:   "remember the file content at 3pm",
			minCaps: 1,
			maxCaps: 5,
			checkCaps: []Capability{
				CapabilityMemory,
				CapabilityFile,
			},
		},
		{
			name:      "no capability detected",
			query:     "just some random text",
			minCaps:   0,
			maxCaps:   2,
			checkCaps: []Capability{},
		},
		{
			name:      "empty query",
			query:     "",
			minCaps:   0,
			maxCaps:   0,
			checkCaps: []Capability{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caps := engine.Detect(tt.query)
			if len(caps) < tt.minCaps || len(caps) > tt.maxCaps {
				t.Errorf("Detect() returned %d capabilities, expected between %d and %d", len(caps), tt.minCaps, tt.maxCaps)
			}

			capSet := make(map[Capability]bool)
			for _, cap := range caps {
				capSet[cap] = true
			}

			for _, checkCap := range tt.checkCaps {
				if !capSet[checkCap] {
					t.Errorf("Detect() did not detect capability %q for query %q", checkCap, tt.query)
				}
			}
		})
	}
}

// TestCapabilityEngineToolsFor tests getting tools for a capability.
func TestCapabilityEngineToolsFor(t *testing.T) {
	registry := NewRegistry()

	// Register a mock tool with specific capabilities
	mockTool := &MockTool{
		name:        "test_tool",
		description: "A test tool",
		category:    CategoryCore,
	}

	_ = registry.Register(mockTool)

	engine := NewCapabilityEngine(registry)

	tests := []struct {
		name        string
		cap         Capability
		expectSlice bool
		expectNil   bool
	}{
		{
			name:        "get tools for math capability",
			cap:         CapabilityMath,
			expectSlice: true,
			expectNil:   false,
		},
		{
			name:        "get tools for knowledge capability",
			cap:         CapabilityKnowledge,
			expectSlice: false,
			expectNil:   true,
		},
		{
			name:        "get tools for unknown capability",
			cap:         Capability("unknown"),
			expectSlice: false,
			expectNil:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tools := engine.ToolsFor(tt.cap)

			if tt.expectNil {
				if tools != nil {
					t.Errorf("ToolsFor() should return nil for capability %q, got non-nil", tt.cap)
				}
			} else {
				if tools == nil {
					t.Errorf("ToolsFor() should return a slice for capability %q, got nil", tt.cap)
				}
				if tt.expectSlice && len(tools) == 0 {
					t.Logf("ToolsFor() returned empty slice for capability %q", tt.cap)
				}
			}
		})
	}
}

// TestCapabilityEngineFilter tests filtering tools by capabilities.
func TestCapabilityEngineFilter(t *testing.T) {
	registry := NewRegistry()

	// Register mock tools
	mockTool1 := &MockTool{
		name:        "tool1",
		description: "Tool 1",
		category:    CategoryCore,
	}
	mockTool2 := &MockTool{
		name:        "tool2",
		description: "Tool 2",
		category:    CategoryCore,
	}

	_ = registry.Register(mockTool1)
	_ = registry.Register(mockTool2)

	engine := NewCapabilityEngine(registry)

	tests := []struct {
		name         string
		capabilities []Capability
	}{
		{
			name: "filter by single capability",
			capabilities: []Capability{
				CapabilityMath,
			},
		},
		{
			name: "filter by multiple capabilities",
			capabilities: []Capability{
				CapabilityMath,
				CapabilityKnowledge,
			},
		},
		{
			name:         "filter by empty capabilities",
			capabilities: []Capability{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tools := engine.Filter(tt.capabilities)
			if len(tt.capabilities) == 0 {
				if tools != nil {
					t.Errorf("Filter() should return nil for empty capabilities")
				}
			} else {
				if tools == nil {
					t.Errorf("Filter() should not return nil for non-empty capabilities")
				}
			}
		})
	}
}

// TestCapabilityEngineMatch tests full capability matching workflow.
func TestCapabilityEngineMatch(t *testing.T) {
	registry := NewRegistry()
	engine := NewCapabilityEngine(registry)

	tests := []struct {
		name        string
		query       string
		expectNil   bool
		description string
	}{
		{
			name:        "match math query",
			query:       "calculate 5 + 3",
			expectNil:   false,
			description: "Should detect math capability and return tools (possibly empty if no tools registered)",
		},
		{
			name:        "match knowledge query",
			query:       "what is the weather today",
			expectNil:   false,
			description: "Should detect knowledge capability and return tools (possibly empty if no tools registered)",
		},
		{
			name:        "match no capability",
			query:       "random text without keywords",
			expectNil:   false,
			description: "May detect some capability due to keyword overlap, returns empty slice if no tools",
		},
		{
			name:        "empty query",
			query:       "",
			expectNil:   true,
			description: "Empty query should return nil (no capabilities detected)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tools := engine.Match(tt.query)

			if tt.expectNil {
				if tools != nil {
					t.Errorf("%s: Match() should return nil for query %q, got non-nil", tt.description, tt.query)
				}
			} else {
				if tools == nil {
					t.Errorf("%s: Match() should return a slice for query %q, got nil", tt.description, tt.query)
				}
				// Even if tools is empty slice, that's acceptable if no tools registered for detected capabilities
			}
		})
	}
}

// TestCapabilityEngineGetAllCapabilities tests getting all capabilities.
func TestCapabilityEngineGetAllCapabilities(t *testing.T) {
	registry := NewRegistry()

	// Register a mock tool to ensure capability map is built
	mockTool := &MockTool{
		name:        "test_tool",
		description: "A test tool",
		category:    CategoryCore,
	}
	_ = registry.Register(mockTool)

	engine := NewCapabilityEngine(registry)

	caps := engine.GetAllCapabilities()

	if caps == nil {
		t.Error("GetAllCapabilities() should not return nil")
	}

	capSet := make(map[Capability]bool)
	for _, cap := range caps {
		capSet[cap] = true
	}

	// Verify that at least one capability exists (from the mock tool)
	if len(capSet) == 0 {
		t.Error("Expected at least one capability from registered tools")
	}
}

// TestCapabilityEngineGetCapabilitySummary tests getting capability summary.
func TestCapabilityEngineGetCapabilitySummary(t *testing.T) {
	registry := NewRegistry()
	engine := NewCapabilityEngine(registry)

	summary := engine.GetCapabilitySummary()

	if summary == nil {
		t.Error("GetCapabilitySummary() should not return nil")
	}

	// Verify summary is a map
	_ = summary
}

// TestCapabilityEngineRebuild tests rebuilding the capability map.
func TestCapabilityEngineRebuild(t *testing.T) {
	registry := NewRegistry()
	engine := NewCapabilityEngine(registry)

	// Rebuild should not panic
	engine.Rebuild()

	// Register a new tool and rebuild
	mockTool := &MockTool{
		name:        "new_tool",
		description: "New tool after rebuild",
		category:    CategoryCore,
	}
	_ = registry.Register(mockTool)
	engine.Rebuild()

	// Should still work after rebuild
	summary := engine.GetCapabilitySummary()
	if summary == nil {
		t.Error("GetCapabilitySummary() should work after rebuild")
	}
}

// TestCapabilityKeywords tests capability keywords are properly defined.
func TestCapabilityKeywords(t *testing.T) {
	tests := []struct {
		name string
		cap  Capability
	}{
		{
			name: "math keywords",
			cap:  CapabilityMath,
		},
		{
			name: "knowledge keywords",
			cap:  CapabilityKnowledge,
		},
		{
			name: "memory keywords",
			cap:  CapabilityMemory,
		},
		{
			name: "text keywords",
			cap:  CapabilityText,
		},
		{
			name: "network keywords",
			cap:  CapabilityNetwork,
		},
		{
			name: "time keywords",
			cap:  CapabilityTime,
		},
		{
			name: "file keywords",
			cap:  CapabilityFile,
		},
		{
			name: "external keywords",
			cap:  CapabilityExternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keywords, exists := capabilityKeywords[tt.cap]
			if !exists {
				t.Errorf("Capability %q should have keywords defined", tt.cap)
			}
			if len(keywords) == 0 {
				t.Errorf("Capability %q should have at least one keyword", tt.cap)
			}
		})
	}
}

// TestCapabilityDetectCaseInsensitive tests that detection is case-insensitive.
func TestCapabilityDetectCaseInsensitive(t *testing.T) {
	registry := NewRegistry()
	engine := NewCapabilityEngine(registry)

	tests := []struct {
		name  string
		query string
	}{
		{
			name:  "uppercase",
			query: "CALCULATE THE SUM",
		},
		{
			name:  "mixed case",
			query: "Calculate The Sum",
		},
		{
			name:  "lowercase",
			query: "calculate the sum",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caps := engine.Detect(tt.query)
			if len(caps) == 0 {
				t.Errorf("Detect() should find math capability for query %q", tt.query)
			}
		})
	}
}

// TestCapabilityDetectChineseKeywords tests Chinese keyword detection.
func TestCapabilityDetectChineseKeywords(t *testing.T) {
	registry := NewRegistry()
	engine := NewCapabilityEngine(registry)

	tests := []struct {
		name        string
		query       string
		wantCap     Capability
		description string
	}{
		{
			name:        "Chinese math keyword",
			query:       "计算两个数的和",
			wantCap:     CapabilityMath,
			description: "应该检测到数学能力",
		},
		{
			name:        "Chinese time keyword",
			query:       "现在几点了",
			wantCap:     CapabilityTime,
			description: "应该检测到时间能力",
		},
		{
			name:        "Chinese file keyword",
			query:       "读取文件内容",
			wantCap:     CapabilityFile,
			description: "应该检测到文件能力",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caps := engine.Detect(tt.query)
			if len(caps) == 0 {
				t.Errorf("Detect() should find capability for query %q", tt.query)
			}

			found := false
			for _, cap := range caps {
				if cap == tt.wantCap {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Detect() should find capability %q for query %q", tt.wantCap, tt.query)
			}
		})
	}
}

// TestMockToolCapabilities tests that MockTool implements capabilities properly.
func TestMockToolCapabilities(t *testing.T) {
	mock := &MockTool{
		name:        "test_tool",
		description: "A test tool",
		category:    CategoryCore,
	}

	caps := mock.Capabilities()
	if caps == nil {
		t.Error("Capabilities() should not return nil")
	}
	if len(caps) == 0 {
		t.Error("Capabilities() should return at least one capability")
	}
}

// TestCapabilityConcurrentAccess tests concurrent access to capability engine.
func TestCapabilityConcurrentAccess(t *testing.T) {
	registry := NewRegistry()
	engine := NewCapabilityEngine(registry)

	done := make(chan bool)

	// Concurrent reads
	for i := 0; i < 10; i++ {
		go func() {
			engine.Detect("calculate sum")
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Should not panic or have race conditions
	_ = engine.GetAllCapabilities()
}
