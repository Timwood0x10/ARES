package builtin

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
)

func TestValidateURLEmptyURL(t *testing.T) {
	err := ValidateURL(context.Background(), "")
	if err == nil {
		t.Error("expected error for empty URL")
	}
}

func TestValidateURLUnsupportedScheme(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"ftp", "ftp://example.com"},
		{"file", "file:///etc/passwd"},
		{"javascript", "javascript:alert(1)"},
		{"data", "data:text/html,<h1>hi</h1>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateURL(context.Background(), tt.url)
			if !errors.Is(err, ErrUnsupportedScheme) {
				t.Errorf("ValidateURL(%q) err = %v, want ErrUnsupportedScheme", tt.url, err)
			}
		})
	}
}

func TestValidateURLNoHost(t *testing.T) {
	err := ValidateURL(context.Background(), "http://")
	if err == nil {
		t.Error("expected error for URL with no host")
	}
}

func TestValidateURLBlockedIPs(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"loopback_v4", "http://127.0.0.1/"},
		{"private_10", "http://10.0.0.1/"},
		{"private_172", "http://172.16.0.1/"},
		{"private_192", "http://192.168.1.1/"},
		{"link_local_metadata", "http://169.254.169.254/"},
		{"unspecified", "http://0.0.0.0/"},
		{"cgnat", "http://100.64.0.1/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateURL(context.Background(), tt.url)
			if err == nil {
				t.Errorf("ValidateURL(%q) = nil, want error (blocked IP)", tt.url)
			}
		})
	}
}

func TestIsBlockedIPClassification(t *testing.T) {
	tests := []struct {
		name string
		ip   net.IP
		want bool
	}{
		{"nil_ip", nil, true},
		{"loopback_v4", net.ParseIP("127.0.0.1"), true},
		{"loopback_v6", net.ParseIP("::1"), true},
		{"private_10", net.ParseIP("10.0.0.1"), true},
		{"private_172_16", net.ParseIP("172.16.0.1"), true},
		{"private_172_31", net.ParseIP("172.31.255.255"), true},
		{"private_192", net.ParseIP("192.168.1.1"), true},
		{"link_local_v4", net.ParseIP("169.254.0.1"), true},
		{"unspecified_v4", net.ParseIP("0.0.0.0"), true},
		{"unspecified_v6", net.ParseIP("::"), true},
		{"cgnat_low", net.ParseIP("100.64.0.1"), true},
		{"cgnat_high", net.ParseIP("100.127.255.255"), true},
		{"ipv4_mapped_loopback", net.ParseIP("::ffff:127.0.0.1"), true},
		{"ipv4_mapped_private", net.ParseIP("::ffff:10.0.0.1"), true},
		{"public_v4", net.ParseIP("8.8.8.8"), false},
		{"public_v6", net.ParseIP("2001:4860:4860::8888"), false},
		{"non_cgnat_100_0", net.ParseIP("100.0.0.1"), false},
		{"non_cgnat_100_128", net.ParseIP("100.128.0.1"), false},
		{"non_private_172_32", net.ParseIP("172.32.0.1"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isBlockedIP(tt.ip)
			if got != tt.want {
				t.Errorf("isBlockedIP(%v) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}

func TestSSRFDialerProperties(t *testing.T) {
	d := SSRFDialer()
	if d == nil {
		t.Fatal("SSRFDialer returned nil")
	}
	if d.Control == nil {
		t.Error("Control should not be nil")
	}
	if d.Timeout <= 0 {
		t.Errorf("Timeout = %v, want > 0", d.Timeout)
	}
}

func TestSSRFTransportProperties(t *testing.T) {
	tr := SSRFTransport()
	if tr == nil {
		t.Fatal("SSRFTransport returned nil")
	}
	if tr.Proxy != nil {
		t.Error("Proxy should be nil (proxy bypasses SSRF check)")
	}
	if tr.DialContext == nil {
		t.Error("DialContext should not be nil")
	}
}

func TestSSRFCheckRedirectMaxHops(t *testing.T) {
	tests := []struct {
		name      string
		viaLength int
		wantErr   bool
	}{
		{"first_hop", 0, false},
		{"second_hop", 1, false},
		{"third_hop", 2, false},
		{"exceeds_max", 3, true},
		{"far_exceeds", 10, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			via := make([]*http.Request, tt.viaLength)
			req, reqErr := http.NewRequestWithContext(context.Background(), "GET", "https://example.com", nil)
			if reqErr != nil {
				t.Fatalf("NewRequestWithContext: %v", reqErr)
			}
			err := SSRFCheckRedirect(req, via)
			if (err != nil) != tt.wantErr {
				t.Errorf("SSRFCheckRedirect(via=%d) err = %v, wantErr %v", tt.viaLength, err, tt.wantErr)
			}
		})
	}
}

func TestSSRFCheckRedirectBlocksInternal(t *testing.T) {
	req, reqErr := http.NewRequestWithContext(context.Background(), "GET", "http://169.254.169.254/latest/meta-data/", nil)
	if reqErr != nil {
		t.Fatalf("NewRequestWithContext: %v", reqErr)
	}
	err := SSRFCheckRedirect(req, []*http.Request{})
	if err == nil {
		t.Error("expected error for redirect to metadata endpoint")
	}
}

func TestMaxHTTPConstants(t *testing.T) {
	if MaxHTTPResponseBytes != 10*1024*1024 {
		t.Errorf("MaxHTTPResponseBytes = %d, want %d", MaxHTTPResponseBytes, 10*1024*1024)
	}
	if MaxHTTPRedirects != 3 {
		t.Errorf("MaxHTTPRedirects = %d, want 3", MaxHTTPRedirects)
	}
}

func TestSsrfDialControlValidation(t *testing.T) {
	tests := []struct {
		name    string
		address string
		wantErr bool
	}{
		{"valid_public", "8.8.8.8:443", false},
		{"blocked_loopback", "127.0.0.1:80", true},
		{"blocked_private", "10.0.0.1:80", true},
		{"invalid_address", "not-an-address", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ssrfDialControl("tcp", tt.address, nil)
			if (err != nil) != tt.wantErr {
				t.Errorf("ssrfDialControl(%q) err = %v, wantErr %v", tt.address, err, tt.wantErr)
			}
		})
	}
}
