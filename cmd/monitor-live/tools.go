package main

import (
	"context"
	"time"

	"github.com/Timwood0x10/ares/internal/agents/sub"
	builtin_network "github.com/Timwood0x10/ares/internal/tools/resources/builtin/network"
	builtin_text "github.com/Timwood0x10/ares/internal/tools/resources/builtin/text"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// newToolBinder creates a ToolBinder with built-in tools.
func newToolBinder() sub.ToolBinder {
	binder := sub.NewToolBinder()

	// Web search (SearXNG)
	webSearch := builtin_network.NewWebSearch()
	binder.BindTool("web_search", func(ctx context.Context, args map[string]any) (any, error) {
		return webSearch.Execute(ctx, args)
	})

	// HTTP request
	httpReq := builtin_network.NewHTTPRequest()
	binder.BindTool("http_request", func(ctx context.Context, args map[string]any) (any, error) {
		return httpReq.Execute(ctx, args)
	})

	// Web scraper
	fetcher := builtin_network.NewWebFetcher(builtin_network.NewDefaultHTTPClient(30 * time.Second))
	scraper := builtin_network.NewWebScraper(fetcher)
	binder.BindTool("web_scraper", func(ctx context.Context, args map[string]any) (any, error) {
		return scraper.Execute(ctx, args)
	})

	// Regex tool
	regexTool := builtin_text.NewRegexTool()
	binder.BindTool("regex", func(ctx context.Context, args map[string]any) (any, error) {
		return regexTool.Execute(ctx, args)
	})

	// JSON tools
	jsonTools := builtin_text.NewJSONTools()
	binder.BindTool("json_tools", func(ctx context.Context, args map[string]any) (any, error) {
		return jsonTools.Execute(ctx, args)
	})

	return binder
}

// registerBuiltinTools registers built-in tools into the core.Registry
// so they appear in the /api/mcp/tools endpoint.
func registerBuiltinTools(registry *core.Registry) {
	tools := []core.Tool{
		builtin_network.NewWebSearch(),
		builtin_network.NewHTTPRequest(),
		builtin_network.NewWebScraper(builtin_network.NewWebFetcher(builtin_network.NewDefaultHTTPClient(30 * time.Second))),
		builtin_text.NewRegexTool(),
		builtin_text.NewJSONTools(),
	}
	for _, t := range tools {
		_ = registry.Register(t)
	}
}

// bridgeRegistryToToolBinder registers all tools from a core.Registry into a ToolBinder.
func bridgeRegistryToToolBinder(binder sub.ToolBinder, registry *core.Registry) {
	if registry == nil {
		return
	}
	for _, name := range registry.List() {
		tool, ok := registry.Get(name)
		if !ok || tool == nil {
			continue
		}
		t := tool // capture for closure
		binder.BindTool(name, func(ctx context.Context, args map[string]any) (any, error) {
			return t.Execute(ctx, args)
		})
	}
}
