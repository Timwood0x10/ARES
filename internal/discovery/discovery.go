// Package discovery provides a pluggable service discovery engine.
//
// Architecture:
//
//	DiscoveryProvider(s) → Identity Merge → Health Verify → EventBus
//
// Providers are plugins that find services from different sources
// (filesystem configs, HTTP endpoints, registries, etc.).
// The engine merges duplicates, verifies health, and emits events
// so the runtime can react to service changes.
package discovery

import (
	"context"
	"time"
)

// ServiceType identifies the kind of discovered service.
type ServiceType string

const (
	ServiceTypeMCP    ServiceType = "mcp"
	ServiceTypeHTTP   ServiceType = "http"
	ServiceTypeGRPC   ServiceType = "grpc"
	ServiceTypeCLI    ServiceType = "cli"
	ServiceTypeDocker ServiceType = "docker"
)

// Confidence represents how trustworthy a discovery source is.
type Confidence int

const (
	ConfidenceLow    Confidence = 60  // PATH scan, broadcast.
	ConfidenceMedium Confidence = 80  // HTTP discovery, mDNS.
	ConfidenceHigh   Confidence = 95  // Claude, Cursor, VSCode configs.
	ConfidenceMax    Confidence = 100 // ARES registry, verified.
)

// ServiceIdentity is the stable identity of a discovered service.
type ServiceIdentity struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Type        ServiceType       `json:"type"`
	Description string            `json:"description,omitempty"`
	Version     string            `json:"version,omitempty"`
	Endpoint    string            `json:"endpoint,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// DiscoveryRecord is a single observation of a service from one source.
type DiscoveryRecord struct {
	Source     string            `json:"source"`
	Confidence Confidence        `json:"confidence"`
	Endpoint   string            `json:"endpoint"`
	Args       []string          `json:"args,omitempty"`
	Tags       []string          `json:"tags,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	LastSeen   time.Time         `json:"last_seen"`
}

// DiscoveredService is the merged result of one or more discovery records.
type DiscoveredService struct {
	Identity   ServiceIdentity   `json:"identity"`
	Records    []DiscoveryRecord `json:"records"`
	Healthy    bool              `json:"healthy"`
	HealthMsg  string            `json:"health_msg,omitempty"`
	BestSource string            `json:"best_source"`
	CheckedAt  *time.Time        `json:"checked_at,omitempty"`
}

// HealthStatus represents the result of a health check.
type HealthStatus struct {
	Healthy   bool          `json:"healthy"`
	Message   string        `json:"message,omitempty"`
	Latency   time.Duration `json:"latency"`
	CheckedAt time.Time     `json:"checked_at"`
}

// DiscoveryProvider finds services from a specific source.
type DiscoveryProvider interface {
	// Name returns the provider identifier.
	Name() string
	// Confidence returns the default trust level.
	Confidence() Confidence
	// Discover scans the source and returns found services.
	Discover(ctx context.Context) ([]DiscoveryRecord, error)
}

// HealthChecker verifies if a discovered service is available.
type HealthChecker interface {
	CheckHealth(ctx context.Context, svc *DiscoveredService) (*HealthStatus, error)
}

// ServiceStore persists discovered services.
type ServiceStore interface {
	Save(ctx context.Context, svc *DiscoveredService) error
	Get(ctx context.Context, id string) (*DiscoveredService, error)
	List(ctx context.Context) ([]*DiscoveredService, error)
	Delete(ctx context.Context, id string) error
}
