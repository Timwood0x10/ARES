package builtin

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/Timwood0x10/goagent/internal/errors"
	"github.com/Timwood0x10/goagent/internal/tools/resources/base"
	"github.com/Timwood0x10/goagent/internal/tools/resources/core"
)

// WebScraper extracts and parses web page content.
type WebScraper struct {
	*base.BaseTool
	getter HTTPGetter
}

// NewWebScraper creates a new WebScraper tool.
func NewWebScraper(getter HTTPGetter) *WebScraper {
	params := &core.ParameterSchema{
		Type: "object",
		Properties: map[string]*core.Parameter{
			"url": {
				Type:        "string",
				Description: "Target URL to scrape",
			},
			"extract_title": {
				Type:        "boolean",
				Description: "Extract page title",
				Default:     true,
			},
			"extract_body": {
				Type:        "boolean",
				Description: "Extract main body content",
				Default:     true,
			},
			"extract_links": {
				Type:        "boolean",
				Description: "Extract page links",
				Default:     false,
			},
			"remove_nav": {
				Type:        "boolean",
				Description: "Remove navigation elements",
				Default:     true,
			},
			"max_length": {
				Type:        "integer",
				Description: "Maximum content length to return (0 = unlimited)",
				Default:     10000,
			},
			"timeout": {
				Type:        "integer",
				Description: "Request timeout in seconds",
				Default:     30,
			},
		},
		Required: []string{"url"},
	}

	ws := &WebScraper{
		getter: getter,
	}
	ws.BaseTool = base.NewBaseToolWithCapabilities(
		"web_scraper",
		"Extract and parse web page content. Returns structured data including title, body text, and links.",
		core.CategoryCore,
		[]core.Capability{core.CapabilityNetwork, core.CapabilityText},
		params,
	)

	return ws
}

// Execute performs the web scraping operation.
func (t *WebScraper) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	url, ok := params["url"].(string)
	if !ok || url == "" {
		return core.Result{}, fmt.Errorf("url is required")
	}

	// Parse options
	options := &ScrapeOptions{
		ExtractTitle: getBool(params, "extract_title", true),
		ExtractBody:  getBool(params, "extract_body", true),
		ExtractLinks: getBool(params, "extract_links", false),
		RemoveNav:    getBool(params, "remove_nav", true),
		MaxLength:    getIntParam(params, "max_length", 10000),
		Timeout:      time.Duration(getIntParam(params, "timeout", 30)) * time.Second,
	}

	// Validate URL
	if !isValidURL(url) {
		return core.Result{}, fmt.Errorf("invalid URL format")
	}

	// Create context with timeout
	if options.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, options.Timeout)
		defer cancel()
	}

	// Fetch page content
	slog.Info("Web scraping started", "url", url)
	startTime := time.Now()

	html, err := t.getter.Get(ctx, url)
	if err != nil {
		slog.Error("Failed to fetch page", "url", url, "error", err)
		return core.Result{}, errors.Wrap(err, "failed to fetch page")
	}

	duration := time.Since(startTime)
	slog.Info("Page fetched", "url", url, "size_bytes", len(html), "duration", duration)

	// Parse HTML content
	result := t.parseHTML(string(html), options)

	// Add metadata
	result["url"] = url
	result["fetched_at"] = time.Now().Format(time.RFC3339)
	result["size_bytes"] = len(html)
	result["fetch_duration_ms"] = duration.Milliseconds()

	return core.NewResult(true, result), nil
}

// parseHTML extracts content from HTML.
func (t *WebScraper) parseHTML(html string, options *ScrapeOptions) map[string]interface{} {
	result := make(map[string]interface{})

	// Extract title
	if options.ExtractTitle {
		result["title"] = extractTitle(html)
	}

	// Extract body content
	if options.ExtractBody {
		body := extractBody(html, options.RemoveNav)
		if options.MaxLength > 0 && len(body) > options.MaxLength {
			body = body[:options.MaxLength] + "..."
		}
		result["content"] = body
		result["content_length"] = len(body)
	}

	// Extract links
	if options.ExtractLinks {
		links := extractLinks(html)
		result["links"] = links
		result["link_count"] = len(links)
	}

	return result
}

// ScrapeOptions defines web scraping options.
type ScrapeOptions struct {
	ExtractTitle bool
	ExtractBody  bool
	ExtractLinks bool
	RemoveNav    bool
	MaxLength    int
	Timeout      time.Duration
}

// extractTitle extracts the page title from HTML.
func extractTitle(html string) string {
	// Match <title>...</title>
	re := regexp.MustCompile(`<title>(.*?)</title>`)
	matches := re.FindStringSubmatch(html)
	if len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}
	return ""
}

// extractBody extracts main body content from HTML.
func extractBody(html string, removeNav bool) string {
	// Remove script and style tags
	html = regexp.MustCompile(`<script[^>]*>.*?</script>`).ReplaceAllString(html, " ")
	html = regexp.MustCompile(`<style[^>]*>.*?</style>`).ReplaceAllString(html, " ")

	// Remove navigation elements if requested
	if removeNav {
		html = regexp.MustCompile(`<nav[^>]*>.*?</nav>`).ReplaceAllString(html, " ")
		html = regexp.MustCompile(`<header[^>]*>.*?</header>`).ReplaceAllString(html, " ")
		html = regexp.MustCompile(`<footer[^>]*>.*?</footer>`).ReplaceAllString(html, " ")
	}

	// Extract body content
	re := regexp.MustCompile(`<body[^>]*>(.*?)</body>`)
	matches := re.FindStringSubmatch(html)
	if len(matches) > 1 {
		body := matches[1]
		// Remove all HTML tags
		body = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(body, " ")
		// Clean up whitespace
		body = strings.Join(strings.Fields(body), " ")
		return body
	}

	// Fallback: remove all HTML tags from entire document
	cleanHTML := regexp.MustCompile(`<[^>]+>`).ReplaceAllString(html, " ")
	cleanHTML = strings.Join(strings.Fields(cleanHTML), " ")
	return cleanHTML
}

// extractLinks extracts all links from HTML.
func extractLinks(html string) []map[string]string {
	re := regexp.MustCompile(`<a[^>]+href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	matches := re.FindAllStringSubmatch(html, -1)

	links := make([]map[string]string, 0, len(matches))
	for _, match := range matches {
		if len(match) >= 3 {
			link := map[string]string{
				"url":  match[1],
				"text": strings.TrimSpace(match[2]),
			}
			links = append(links, link)
		}
	}

	return links
}

// isValidURL performs basic URL validation.
func isValidURL(url string) bool {
	if url == "" {
		return false
	}

	// Check for http:// or https:// prefix
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return false
	}

	return true
}

// getBool safely gets a boolean parameter with default value.
func getBool(params map[string]interface{}, key string, defaultVal bool) bool {
	if v, ok := params[key].(bool); ok {
		return v
	}
	return defaultVal
}

// getIntParam safely gets an integer parameter with default value.
func getIntParam(params map[string]interface{}, key string, defaultVal int) int {
	switch v := params[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	}
	return defaultVal
}

// SetGetter sets a custom HTTP getter.
func (t *WebScraper) SetGetter(getter HTTPGetter) {
	t.getter = getter
}
