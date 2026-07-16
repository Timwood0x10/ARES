// Package core provides core abstractions and interfaces for the ARES API layer.
package core

import (
	"context"
	"time"
)

// BaseConfig represents base configuration for all API services.
type BaseConfig struct {
	// RequestTimeout is the default timeout for API requests.
	RequestTimeout time.Duration
	// MaxRetries is the maximum number of retry attempts.
	MaxRetries int
	// RetryDelay is the delay between retry attempts.
	RetryDelay time.Duration
}

// TenantContext represents tenant-specific context for multi-tenant support.
type TenantContext struct {
	// TenantID is the unique identifier for the tenant.
	TenantID string
	// UserID is the unique identifier for the user within the tenant.
	UserID string
	// TraceID is the unique trace ID for distributed tracing.
	TraceID string
}

// PaginationRequest represents pagination parameters.
type PaginationRequest struct {
	// Page is the page number (1-indexed).
	Page int
	// PageSize is the number of items per page.
	PageSize int
	// Offset is the number of items to skip.
	Offset int
	// Limit is the maximum number of items to return.
	Limit int
}

// PaginationResponse represents pagination metadata.
type PaginationResponse struct {
	// Total is the total number of items.
	Total int64
	// Page is the current page number.
	Page int
	// PageSize is the number of items per page.
	PageSize int
	// TotalPages is the total number of pages.
	TotalPages int
	// HasMore indicates if there are more pages.
	HasMore bool
}

// Metadata represents optional metadata for API requests and responses.
type Metadata map[string]interface{}

// RequestContext represents extended context for API operations.
type RequestContext struct {
	// Context is the standard Go context.
	Context context.Context
	// Tenant is the tenant context.
	Tenant *TenantContext
	// Metadata is optional metadata.
	Metadata Metadata
}

// NewRequestContext creates a new request context.
// Args:
// ctx - base Go context.
// tenantID - tenant identifier.
// Returns new request context.
func NewRequestContext(ctx context.Context, tenantID string) *RequestContext {
	return &RequestContext{
		Context: ctx,
		Tenant: &TenantContext{
			TenantID: tenantID,
		},
		Metadata: make(Metadata),
	}
}

// WithUserID adds user ID to the request context.
// Args:
// userID - user identifier.
// Returns the request context for chaining.
func (rc *RequestContext) WithUserID(userID string) *RequestContext {
	if rc.Tenant != nil {
		rc.Tenant.UserID = userID
	}
	return rc
}

// WithTraceID adds trace ID to the request context.
// Args:
// traceID - trace identifier.
// Returns the request context for chaining.
func (rc *RequestContext) WithTraceID(traceID string) *RequestContext {
	if rc.Tenant != nil {
		rc.Tenant.TraceID = traceID
	}
	return rc
}

// WithMetadata adds metadata to the request context.
// Args:
// key - metadata key.
// value - metadata value.
// Returns the request context for chaining.
func (rc *RequestContext) WithMetadata(key string, value interface{}) *RequestContext {
	if rc.Metadata == nil {
		rc.Metadata = make(Metadata)
	}
	rc.Metadata[key] = value
	return rc
}
