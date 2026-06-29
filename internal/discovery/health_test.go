package discovery

import (
	"context"
	"testing"
	"time"
)

func TestNewMCPHealthChecker_DefaultTimeout(t *testing.T) {
	c := NewMCPHealthChecker(0)
	if c.timeout != 5*time.Second {
		t.Errorf("expected default timeout 5s, got %v", c.timeout)
	}
}

func TestNewMCPHealthChecker_CustomTimeout(t *testing.T) {
	c := NewMCPHealthChecker(10 * time.Second)
	if c.timeout != 10*time.Second {
		t.Errorf("expected timeout 10s, got %v", c.timeout)
	}
}

func TestBestEndpoint_Empty(t *testing.T) {
	svc := &DiscoveredService{}
	endpoint, args := bestEndpoint(svc)
	if endpoint != "" {
		t.Errorf("expected empty endpoint, got %q", endpoint)
	}
	if args != nil {
		t.Errorf("expected nil args, got %v", args)
	}
}

func TestBestEndpoint_HighestConfidence(t *testing.T) {
	svc := &DiscoveredService{
		Records: []DiscoveryRecord{
			{Source: "probe", Confidence: ConfidenceMedium, Endpoint: "/usr/bin/tool", Args: []string{"serve"}},
			{Source: "ares", Confidence: ConfidenceMax, Endpoint: "/opt/bin/tool", Args: []string{"--mcp"}},
		},
	}
	endpoint, args := bestEndpoint(svc)
	if endpoint != "/opt/bin/tool" {
		t.Errorf("expected '/opt/bin/tool', got %q", endpoint)
	}
	if len(args) != 1 || args[0] != "--mcp" {
		t.Errorf("expected ['--mcp'], got %v", args)
	}
}

func TestBestEndpoint_SingleRecord(t *testing.T) {
	svc := &DiscoveredService{
		Records: []DiscoveryRecord{
			{Source: "claude", Confidence: ConfidenceHigh, Endpoint: "codegraph"},
		},
	}
	endpoint, _ := bestEndpoint(svc)
	if endpoint != "codegraph" {
		t.Errorf("expected 'codegraph', got %q", endpoint)
	}
}

func TestIsURL(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"http://localhost:3000/mcp", true},
		{"https://example.com/mcp", true},
		{"/usr/bin/codegraph", false},
		{"codegraph", false},
		{"", false},
	}
	for _, tt := range tests {
		got := isURL(tt.input)
		if got != tt.want {
			t.Errorf("isURL(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestCheckHealth_NilService(t *testing.T) {
	c := NewMCPHealthChecker(5 * time.Second)
	_, err := c.CheckHealth(context.Background(), nil)
	if err == nil {
		t.Error("expected error for nil service")
	}
}

func TestCheckHealth_NoEndpoint(t *testing.T) {
	c := NewMCPHealthChecker(5 * time.Second)
	svc := &DiscoveredService{
		Identity: ServiceIdentity{Name: "empty"},
	}
	status, err := c.CheckHealth(context.Background(), svc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Healthy {
		t.Error("expected unhealthy for service with no endpoint")
	}
	if status.Message != "no endpoint" {
		t.Errorf("expected 'no endpoint', got %q", status.Message)
	}
}
