package builtin

import (
	"context"
	"io"
	"net/http"
	"time"
)

// HTTPClient defines the interface for making HTTP requests.
// This allows for dependency injection and testing.
type HTTPClient interface {
	// Do sends an HTTP request and returns an HTTP response.
	Do(req *http.Request) (*http.Response, error)
}

// DefaultHTTPClient provides a standard HTTP client implementation.
type DefaultHTTPClient struct {
	client *http.Client
}

// NewDefaultHTTPClient creates a new default HTTP client with reasonable defaults.
func NewDefaultHTTPClient(timeout time.Duration) *DefaultHTTPClient {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	return &DefaultHTTPClient{
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

// Do executes an HTTP request.
func (c *DefaultHTTPClient) Do(req *http.Request) (*http.Response, error) { // #nosec G704
	return c.client.Do(req)
}

// HTTPGetter defines the interface for fetching web content.
type HTTPGetter interface {
	// Get fetches content from a URL.
	Get(ctx context.Context, url string) ([]byte, error)
}

// WebFetcher implements HTTPGetter using an HTTPClient.
type WebFetcher struct {
	client    HTTPClient
	userAgent string
}

// NewWebFetcher creates a new WebFetcher with the given HTTP client.
func NewWebFetcher(client HTTPClient) *WebFetcher {
	return &WebFetcher{
		client:    client,
		userAgent: "Mozilla/5.0 (compatible; GoAgent/1.0; +https://github.com/Timwood0x10/ares)",
	}
}

// SetUserAgent sets a custom user agent string.
func (f *WebFetcher) SetUserAgent(userAgent string) {
	f.userAgent = userAgent
}

// Get fetches content from a URL.
func (f *WebFetcher) Get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil) // #nosec G704
	if err != nil {
		return nil, err
	}

	// Set User-Agent header to avoid being blocked by websites
	if f.userAgent != "" {
		req.Header.Set("User-Agent", f.userAgent)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			// Log error if needed
			log.Error("Failed to close response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPError{
			StatusCode: resp.StatusCode,
			Message:    resp.Status,
		}
	}

	return io.ReadAll(resp.Body)
}

// HTTPError represents an HTTP request error.
type HTTPError struct {
	StatusCode int
	Message    string
}

// Error returns the error message.
func (e *HTTPError) Error() string {
	return e.Message
}
