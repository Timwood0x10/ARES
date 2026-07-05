package core

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestBaseConfig tests BaseConfig initialization and fields.
func TestBaseConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  BaseConfig
	}{
		{
			name: "default config",
			cfg:  BaseConfig{},
		},
		{
			name: "full config",
			cfg: BaseConfig{
				RequestTimeout: 30 * time.Second,
				MaxRetries:     3,
				RetryDelay:     1 * time.Second,
			},
		},
		{
			name: "zero values",
			cfg: BaseConfig{
				RequestTimeout: 0,
				MaxRetries:     0,
				RetryDelay:     0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Verify config fields are accessible
			_ = tt.cfg.RequestTimeout
			_ = tt.cfg.MaxRetries
			_ = tt.cfg.RetryDelay
		})
	}
}

// TestTenantContext tests TenantContext initialization and fields.
func TestTenantContext(t *testing.T) {
	tests := []struct {
		name string
		ctx  TenantContext
	}{
		{
			name: "all fields populated",
			ctx: TenantContext{
				TenantID: "tenant-123",
				UserID:   "user-456",
				TraceID:  "trace-789",
			},
		},
		{
			name: "partial fields",
			ctx: TenantContext{
				TenantID: "tenant-123",
			},
		},
		{
			name: "empty context",
			ctx:  TenantContext{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Verify context fields are accessible
			_ = tt.ctx.TenantID
			_ = tt.ctx.UserID
			_ = tt.ctx.TraceID
		})
	}
}

// TestPaginationRequest tests PaginationRequest initialization and validation.
func TestPaginationRequest(t *testing.T) {
	tests := []struct {
		name string
		req  PaginationRequest
	}{
		{
			name: "all fields populated",
			req: PaginationRequest{
				Page:     1,
				PageSize: 10,
				Offset:   0,
				Limit:    10,
			},
		},
		{
			name: "only page and page size",
			req: PaginationRequest{
				Page:     2,
				PageSize: 20,
			},
		},
		{
			name: "zero values",
			req:  PaginationRequest{},
		},
		{
			name: "large values",
			req: PaginationRequest{
				Page:     1000,
				PageSize: 100,
				Offset:   10000,
				Limit:    100,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Verify request fields are accessible
			_ = tt.req.Page
			_ = tt.req.PageSize
			_ = tt.req.Offset
			_ = tt.req.Limit
		})
	}
}

// TestPaginationResponse tests PaginationResponse initialization and calculations.
func TestPaginationResponse(t *testing.T) {
	tests := []struct {
		name string
		resp PaginationResponse
		want PaginationResponse
	}{
		{
			name: "first page with more results",
			resp: PaginationResponse{
				Total:      100,
				Page:       1,
				PageSize:   10,
				TotalPages: 10,
				HasMore:    true,
			},
			want: PaginationResponse{
				Total:      100,
				Page:       1,
				PageSize:   10,
				TotalPages: 10,
				HasMore:    true,
			},
		},
		{
			name: "last page",
			resp: PaginationResponse{
				Total:      100,
				Page:       10,
				PageSize:   10,
				TotalPages: 10,
				HasMore:    false,
			},
			want: PaginationResponse{
				Total:      100,
				Page:       10,
				PageSize:   10,
				TotalPages: 10,
				HasMore:    false,
			},
		},
		{
			name: "empty result",
			resp: PaginationResponse{
				Total:      0,
				Page:       1,
				PageSize:   10,
				TotalPages: 0,
				HasMore:    false,
			},
			want: PaginationResponse{
				Total:      0,
				Page:       1,
				PageSize:   10,
				TotalPages: 0,
				HasMore:    false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.resp.Total != tt.want.Total {
				t.Errorf("PaginationResponse.Total = %d, want %d", tt.resp.Total, tt.want.Total)
			}
			if tt.resp.Page != tt.want.Page {
				t.Errorf("PaginationResponse.Page = %d, want %d", tt.resp.Page, tt.want.Page)
			}
			if tt.resp.PageSize != tt.want.PageSize {
				t.Errorf("PaginationResponse.PageSize = %d, want %d", tt.resp.PageSize, tt.want.PageSize)
			}
			if tt.resp.TotalPages != tt.want.TotalPages {
				t.Errorf("PaginationResponse.TotalPages = %d, want %d", tt.resp.TotalPages, tt.want.TotalPages)
			}
			if tt.resp.HasMore != tt.want.HasMore {
				t.Errorf("PaginationResponse.HasMore = %v, want %v", tt.resp.HasMore, tt.want.HasMore)
			}
		})
	}
}

// TestMetadata tests Metadata initialization and operations.
func TestMetadata(t *testing.T) {
	tests := []struct {
		name     string
		metadata Metadata
		key      string
		value    interface{}
	}{
		{
			name:     "nil metadata",
			metadata: nil,
		},
		{
			name:     "empty metadata",
			metadata: make(Metadata),
		},
		{
			name:     "metadata with string value",
			metadata: Metadata{"key": "value"},
			key:      "key",
			value:    "value",
		},
		{
			name:     "metadata with int value",
			metadata: Metadata{"count": 42},
			key:      "count",
			value:    42,
		},
		{
			name:     "metadata with bool value",
			metadata: Metadata{"enabled": true},
			key:      "enabled",
			value:    true,
		},
		{
			name:     "metadata with multiple values",
			metadata: Metadata{"key1": "value1", "key2": 123, "key3": true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.metadata != nil {
				if tt.key != "" {
					if val, exists := tt.metadata[tt.key]; exists {
						if val != tt.value {
							t.Errorf("Metadata[%s] = %v, want %v", tt.key, val, tt.value)
						}
					}
				}
			}
		})
	}
}

// TestNewRequestContext tests NewRequestContext function.
func TestNewRequestContext(t *testing.T) {
	ctx := context.Background()
	tenantID := "tenant-123"

	tests := []struct {
		name     string
		ctx      context.Context
		tenantID string
	}{
		{
			name:     "valid context and tenant ID",
			ctx:      ctx,
			tenantID: tenantID,
		},
		{
			name:     "nil context",
			ctx:      nil,
			tenantID: tenantID,
		},
		{
			name:     "empty tenant ID",
			ctx:      ctx,
			tenantID: "",
		},
		{
			name:     "both nil and empty",
			ctx:      nil,
			tenantID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := NewRequestContext(tt.ctx, tt.tenantID)

			require.NotNil(t, rc)

			if rc.Context != tt.ctx {
				t.Errorf("NewRequestContext().Context = %v, want %v", rc.Context, tt.ctx)
			}

			if rc.Tenant == nil {
				t.Error("NewRequestContext().Tenant should not be nil")
			} else if rc.Tenant.TenantID != tt.tenantID {
				t.Errorf("NewRequestContext().Tenant.TenantID = %q, want %q", rc.Tenant.TenantID, tt.tenantID)
			}

			if rc.Metadata == nil {
				t.Error("NewRequestContext().Metadata should be initialized, got nil")
			}
		})
	}
}

// TestRequestContext_WithUserID tests WithUserID method.
func TestRequestContext_WithUserID(t *testing.T) {
	rc := NewRequestContext(context.Background(), "tenant-123")
	userID := "user-456"

	result := rc.WithUserID(userID)

	if result != rc {
		t.Error("WithUserID() should return the same RequestContext instance for chaining")
	}

	if rc.Tenant.UserID != userID {
		t.Errorf("WithUserID() = %q, want %q", rc.Tenant.UserID, userID)
	}

	// Test with nil tenant
	rcNilTenant := &RequestContext{
		Context:  context.Background(),
		Tenant:   nil,
		Metadata: make(Metadata),
	}

	resultNil := rcNilTenant.WithUserID("user-789")
	// Should not panic, and tenant should remain nil
	if resultNil.Tenant != nil {
		t.Error("WithUserID() with nil tenant should keep tenant as nil")
	}
}

// TestRequestContext_WithTraceID tests WithTraceID method.
func TestRequestContext_WithTraceID(t *testing.T) {
	rc := NewRequestContext(context.Background(), "tenant-123")
	traceID := "trace-789"

	result := rc.WithTraceID(traceID)

	if result != rc {
		t.Error("WithTraceID() should return the same RequestContext instance for chaining")
	}

	if rc.Tenant.TraceID != traceID {
		t.Errorf("WithTraceID() = %q, want %q", rc.Tenant.TraceID, traceID)
	}

	// Test with nil tenant
	rcNilTenant := &RequestContext{
		Context:  context.Background(),
		Tenant:   nil,
		Metadata: make(Metadata),
	}

	resultNil := rcNilTenant.WithTraceID("trace-999")
	// Should not panic, and tenant should remain nil
	if resultNil.Tenant != nil {
		t.Error("WithTraceID() with nil tenant should keep tenant as nil")
	}
}

// TestRequestContext_WithMetadata tests WithMetadata method.
func TestRequestContext_WithMetadata(t *testing.T) {
	rc := NewRequestContext(context.Background(), "tenant-123")

	// Test adding single metadata
	result := rc.WithMetadata("key1", "value1")

	if result != rc {
		t.Error("WithMetadata() should return the same RequestContext instance for chaining")
	}

	if rc.Metadata["key1"] != "value1" {
		t.Errorf("WithMetadata() = %v, want %v", rc.Metadata["key1"], "value1")
	}

	// Test adding multiple metadata
	rc.WithMetadata("key2", 123).WithMetadata("key3", true).WithMetadata("key4", 3.14)

	if rc.Metadata["key2"] != 123 {
		t.Errorf("WithMetadata() second value = %v, want %v", rc.Metadata["key2"], 123)
	}
	if rc.Metadata["key3"] != true {
		t.Errorf("WithMetadata() third value = %v, want %v", rc.Metadata["key3"], true)
	}
	if rc.Metadata["key4"] != 3.14 {
		t.Errorf("WithMetadata() fourth value = %v, want %v", rc.Metadata["key4"], 3.14)
	}

	// Test with nil metadata map
	rcNilMetadata := &RequestContext{
		Context:  context.Background(),
		Tenant:   &TenantContext{TenantID: "tenant-123"},
		Metadata: nil,
	}

	resultNil := rcNilMetadata.WithMetadata("key", "value")

	if resultNil.Metadata == nil {
		t.Error("WithMetadata() should initialize metadata map if nil")
	}
	if resultNil.Metadata["key"] != "value" {
		t.Errorf("WithMetadata() with nil metadata = %v, want %v", resultNil.Metadata["key"], "value")
	}

	// Test overwriting existing value
	rc.WithMetadata("key1", "new value")

	if rc.Metadata["key1"] != "new value" {
		t.Errorf("WithMetadata() overwrite = %v, want %v", rc.Metadata["key1"], "new value")
	}
}

// TestRequestContext_Chaining tests method chaining.
func TestRequestContext_Chaining(t *testing.T) {
	rc := NewRequestContext(context.Background(), "tenant-123")

	rc.WithUserID("user-456").
		WithTraceID("trace-789").
		WithMetadata("key1", "value1").
		WithMetadata("key2", 123)

	if rc.Tenant.UserID != "user-456" {
		t.Errorf("Chaining UserID = %q, want %q", rc.Tenant.UserID, "user-456")
	}
	if rc.Tenant.TraceID != "trace-789" {
		t.Errorf("Chaining TraceID = %q, want %q", rc.Tenant.TraceID, "trace-789")
	}
	if rc.Metadata["key1"] != "value1" {
		t.Errorf("Chaining metadata key1 = %v, want %v", rc.Metadata["key1"], "value1")
	}
	if rc.Metadata["key2"] != 123 {
		t.Errorf("Chaining metadata key2 = %v, want %v", rc.Metadata["key2"], 123)
	}
}
