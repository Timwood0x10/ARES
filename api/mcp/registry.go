// Package mcp — MCP Service Registry with Discovery, Tagging, Scoring, Routing.
//
// Architecture:
//
//	Discovery → Tagging → Scoring → Routing
//
// Storage is pluggable via ServiceStore interface (memory, SQLite, Postgres).
package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ── Data Structures ──────────────────────────────────────

// MCPService represents a discovered MCP server with metadata.
type MCPService struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Endpoint    string            `json:"endpoint,omitempty"` // command or URL
	Tags        map[string][]string `json:"tags"`             // level -> tags
	Metadata    map[string]any    `json:"metadata,omitempty"`
	LastSeen    time.Time         `json:"last_seen"`
	Available   bool              `json:"available"`

	// Stats (updated by runtime)
	SuccessRate float64       `json:"success_rate"`
	AvgLatency  time.Duration `json:"avg_latency"`
	CallCount   int64         `json:"call_count"`
	ErrorCount  int64         `json:"error_count"`

	// User preferences
	Favorite bool `json:"favorite,omitempty"`
	Avoid    bool `json:"avoid,omitempty"`
}

// ServiceStore is the pluggable storage interface for service registry.
type ServiceStore interface {
	Save(ctx context.Context, svc *MCPService) error
	Get(ctx context.Context, id string) (*MCPService, error)
	List(ctx context.Context) ([]*MCPService, error)
	Delete(ctx context.Context, id string) error
	UpdateStats(ctx context.Context, id string, success bool, latency time.Duration) error
}

// ── Tag System ───────────────────────────────────────────

// TagLevel defines tag hierarchy levels.
type TagLevel int

const (
	TagLevel1 TagLevel = iota + 1 // Core category (must match)
	TagLevel2                     // Capability (strong match)
	TagLevel3                     // Quality (soft match)
)

// Tag categories.
const (
	TagCategory   = "category"   // Level 1: database, search, code, web, media, analysis
	TagType       = "type"       // Level 1: read, write, execute, query
	TagCapability = "capability" // Level 2: sql-query, file-search, web-scrape
	TagDomain     = "domain"     // Level 2: finance, medical, code, general
	TagQuality    = "quality"    // Level 3: high-precision, fast, reliable
	TagLatency    = "latency"    // Level 3: low, medium, high
)

// AutoTag generates tags from service metadata.
func AutoTag(svc *MCPService) {
	if svc.Tags == nil {
		svc.Tags = make(map[string][]string)
	}

	name := strings.ToLower(svc.Name)
	desc := strings.ToLower(svc.Description)

	// Level 1: Category
	switch {
	case contains(name, "database", "db", "sql", "postgres", "mysql", "redis"):
		svc.Tags[TagCategory] = appendUnique(svc.Tags[TagCategory], "database")
	case contains(name, "search", "query", "find", "grep"):
		svc.Tags[TagCategory] = appendUnique(svc.Tags[TagCategory], "search")
	case contains(name, "code", "graph", "syntax", "lint", "compile"):
		svc.Tags[TagCategory] = appendUnique(svc.Tags[TagCategory], "code")
	case contains(name, "web", "http", "scrape", "browser", "fetch"):
		svc.Tags[TagCategory] = appendUnique(svc.Tags[TagCategory], "web")
	case contains(name, "file", "fs", "disk", "storage"):
		svc.Tags[TagCategory] = appendUnique(svc.Tags[TagCategory], "file")
	case contains(name, "memory", "knowledge", "rag", "embed"):
		svc.Tags[TagCategory] = appendUnique(svc.Tags[TagCategory], "knowledge")
	default:
		svc.Tags[TagCategory] = appendUnique(svc.Tags[TagCategory], "general")
	}

	// Level 1: Type (from description keywords)
	switch {
	case contains(desc, "read", "get", "list", "query", "search", "find"):
		svc.Tags[TagType] = appendUnique(svc.Tags[TagType], "read")
	case contains(desc, "write", "create", "update", "delete", "insert"):
		svc.Tags[TagType] = appendUnique(svc.Tags[TagType], "write")
	case contains(desc, "execute", "run", "call", "invoke"):
		svc.Tags[TagType] = appendUnique(svc.Tags[TagType], "execute")
	default:
		svc.Tags[TagType] = appendUnique(svc.Tags[TagType], "query")
	}

	// Level 2: Capability (from name patterns)
	capabilities := extractCapabilities(name, desc)
	svc.Tags[TagCapability] = capabilities

	// Level 2: Domain
	if contains(desc, "code", "programming", "software", "api") {
		svc.Tags[TagDomain] = appendUnique(svc.Tags[TagDomain], "code")
	}
	if contains(desc, "finance", "trading", "stock", "crypto") {
		svc.Tags[TagDomain] = appendUnique(svc.Tags[TagDomain], "finance")
	}

	// Level 3: Quality defaults
	if svc.SuccessRate > 0.95 {
		svc.Tags[TagQuality] = appendUnique(svc.Tags[TagQuality], "high-reliability")
	}
}

// ── Discovery ────────────────────────────────────────────

// DiscoveryConfig configures the discovery service.
type DiscoveryConfig struct {
	// ConfigPaths are additional config file paths to scan.
	ConfigPaths []string
	// ProjectDir is the project root for .claude/settings.json.
	ProjectDir string
	// RefreshInterval is how often to re-scan (0 = manual only).
	RefreshInterval time.Duration
}

// DiscoveryService finds and registers MCP servers.
type DiscoveryService struct {
	store  ServiceStore
	config DiscoveryConfig
	mu     sync.Mutex
}

// NewDiscoveryService creates a new discovery service.
func NewDiscoveryService(store ServiceStore, config DiscoveryConfig) *DiscoveryService {
	return &DiscoveryService{store: store, config: config}
}

// DiscoverNow runs immediate discovery from all sources.
func (d *DiscoveryService) DiscoverNow(ctx context.Context) ([]*MCPService, error) {
	servers := d.scanConfigFiles()
	discovered := make([]*MCPService, 0, len(servers))

	for _, sc := range servers {
		svc := &MCPService{
			ID:       sc.Name,
			Name:     sc.Name,
			Endpoint: formatEndpoint(sc),
			LastSeen: time.Now(),
			Available: true,
		}
		AutoTag(svc)

		if err := d.store.Save(ctx, svc); err != nil {
			return nil, fmt.Errorf("save %s: %w", sc.Name, err)
		}
		discovered = append(discovered, svc)
	}

	return discovered, nil
}

// StartAutoRefresh starts periodic discovery in the background.
func (d *DiscoveryService) StartAutoRefresh(ctx context.Context) {
	if d.config.RefreshInterval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(d.config.RefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				d.DiscoverNow(ctx)
			}
		}
	}()
}

func (d *DiscoveryService) scanConfigFiles() []ServerConfig {
	seen := make(map[string]bool)
	var servers []ServerConfig

	// ~/.claude.json
	if home, err := os.UserHomeDir(); err == nil {
		servers = append(servers, scanClaudeConfig(filepath.Join(home, ".claude.json"), seen)...)
	}

	// Project-level
	if d.config.ProjectDir != "" {
		servers = append(servers, scanClaudeConfig(filepath.Join(d.config.ProjectDir, ".claude", "settings.json"), seen)...)
	}

	// Additional config paths
	for _, p := range d.config.ConfigPaths {
		servers = append(servers, scanClaudeConfig(p, seen)...)
	}

	return servers
}

func formatEndpoint(sc ServerConfig) string {
	if sc.Command != "" {
		return sc.Command + " " + strings.Join(sc.Args, " ")
	}
	return sc.URL
}

// ── Scoring ──────────────────────────────────────────────

// ScoreConfig weights for scoring formula.
type ScoreConfig struct {
	Level1Weight    float64 // Core category match (default: 100)
	Level2Weight    float64 // Capability match (default: 40)
	Level3Weight    float64 // Quality match (default: 10)
	SuccessWeight   float64 // Historical success rate (default: 30)
	ReliabilityWeight float64 // Reliability score (default: 20)
	LatencyPenalty  float64 // Latency penalty factor (default: 15)
	FavoriteBonus   float64 // User favorite bonus (default: 20)
	AvoidPenalty    float64 // User avoid penalty (default: -100)
}

// DefaultScoreConfig returns default scoring weights.
func DefaultScoreConfig() ScoreConfig {
	return ScoreConfig{
		Level1Weight:      100,
		Level2Weight:      40,
		Level3Weight:      10,
		SuccessWeight:     30,
		ReliabilityWeight: 20,
		LatencyPenalty:    15,
		FavoriteBonus:     20,
		AvoidPenalty:      -100,
	}
}

// Score calculates the score for a service against a set of required tags.
func Score(svc *MCPService, requiredTags map[string][]string, cfg ScoreConfig) float64 {
	if svc.Avoid {
		return cfg.AvoidPenalty
	}

	var score float64

	// Level 1: Must match all required category/type tags
	l1Required := mergeTags(requiredTags[TagCategory], requiredTags[TagType])
	l1Actual := mergeTags(svc.Tags[TagCategory], svc.Tags[TagType])
	l1Match := tagOverlap(l1Required, l1Actual)
	if len(l1Required) > 0 && l1Match < 1.0 {
		return 0 // Level 1 mismatch = disqualified
	}
	score += l1Match * cfg.Level1Weight

	// Level 2: Capability and domain matching
	l2Required := mergeTags(requiredTags[TagCapability], requiredTags[TagDomain])
	l2Actual := mergeTags(svc.Tags[TagCapability], svc.Tags[TagDomain])
	if len(l2Required) > 0 {
		score += tagOverlap(l2Required, l2Actual) * cfg.Level2Weight
	}

	// Level 3: Quality matching
	l3Required := mergeTags(requiredTags[TagQuality], requiredTags[TagLatency])
	l3Actual := mergeTags(svc.Tags[TagQuality], svc.Tags[TagLatency])
	if len(l3Required) > 0 {
		score += tagOverlap(l3Required, l3Actual) * cfg.Level3Weight
	}

	// Historical performance
	score += svc.SuccessRate * cfg.SuccessWeight

	// Reliability (based on call count — more calls = more reliable)
	if svc.CallCount > 0 {
		reliability := float64(1.0)
		if svc.ErrorCount > 0 {
			reliability = 1.0 - float64(svc.ErrorCount)/float64(svc.CallCount)
		}
		score += reliability * cfg.ReliabilityWeight
	}

	// Latency penalty (normalized: 0-1, where 0 = instant, 1 = very slow)
	if svc.AvgLatency > 0 {
		latencyNorm := float64(svc.AvgLatency.Milliseconds()) / 5000.0 // 5s = 1.0
		if latencyNorm > 1.0 {
			latencyNorm = 1.0
		}
		score -= latencyNorm * cfg.LatencyPenalty
	}

	// User preferences
	if svc.Favorite {
		score += cfg.FavoriteBonus
	}

	return score
}

// ── Routing ──────────────────────────────────────────────

// RouteRequest describes what the caller needs.
type RouteRequest struct {
	// Tags are the required tags (level -> values).
	Tags map[string][]string `json:"tags"`
	// Description is a natural language description (for future LLM-based matching).
	Description string `json:"description,omitempty"`
	// TopN is how many results to return (default: 3).
	TopN int `json:"top_n,omitempty"`
}

// RouteResult is a single candidate with its score.
type RouteResult struct {
	Service *MCPService `json:"service"`
	Score   float64     `json:"score"`
	Reason  string      `json:"reason"`
}

// Router finds the best MCP service for a given request.
type Router struct {
	store      ServiceStore
	scoreCfg   ScoreConfig
}

// NewRouter creates a new router.
func NewRouter(store ServiceStore, cfg ScoreConfig) *Router {
	return &Router{store: store, scoreCfg: cfg}
}

// Route finds the top-N matching services for a request.
func (r *Router) Route(ctx context.Context, req RouteRequest) ([]RouteResult, error) {
	services, err := r.store.List(ctx)
	if err != nil {
		return nil, err
	}

	topN := req.TopN
	if topN <= 0 {
		topN = 3
	}

	results := make([]RouteResult, 0, len(services))
	for _, svc := range services {
		if !svc.Available {
			continue
		}
		score := Score(svc, req.Tags, r.scoreCfg)
		if score > 0 {
			results = append(results, RouteResult{
				Service: svc,
				Score:   score,
				Reason:  buildReason(svc, req.Tags),
			})
		}
	}

	// Sort by score descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if len(results) > topN {
		results = results[:topN]
	}
	return results, nil
}

// ── Helpers ──────────────────────────────────────────────

func contains(s string, keywords ...string) bool {
	for _, k := range keywords {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

func appendUnique(slice []string, item string) []string {
	for _, s := range slice {
		if s == item {
			return slice
		}
	}
	return append(slice, item)
}

func mergeTags(slices ...[]string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, s := range slices {
		for _, tag := range s {
			if !seen[tag] {
				seen[tag] = true
				result = append(result, tag)
			}
		}
	}
	return result
}

func tagOverlap(required, actual []string) float64 {
	if len(required) == 0 {
		return 1.0
	}
	actualSet := make(map[string]bool, len(actual))
	for _, t := range actual {
		actualSet[t] = true
	}
	matched := 0
	for _, t := range required {
		if actualSet[t] {
			matched++
		}
	}
	return float64(matched) / float64(len(required))
}

func extractCapabilities(name, desc string) []string {
	var caps []string
	patterns := map[string][]string{
		"sql":       {"sql-query"},
		"query":     {"data-query"},
		"search":    {"full-text-search"},
		"file":      {"file-io"},
		"web":       {"web-access"},
		"scrape":    {"web-scrape"},
		"graph":     {"graph-query"},
		"knowledge": {"knowledge-retrieval"},
		"memory":    {"memory-ops"},
		"code":      {"code-analysis"},
		"lint":      {"code-lint"},
		"exec":      {"code-execution"},
	}
	combined := name + " " + desc
	for keyword, cap := range patterns {
		if strings.Contains(combined, keyword) {
			caps = append(caps, cap...)
		}
	}
	return caps
}

func buildReason(svc *MCPService, requiredTags map[string][]string) string {
	var reasons []string
	for level, tags := range requiredTags {
		for _, tag := range tags {
			if containsTag(svc.Tags[level], tag) {
				reasons = append(reasons, fmt.Sprintf("%s:%s ✓", level, tag))
			}
		}
	}
	if svc.Favorite {
		reasons = append(reasons, "user:favorite ✓")
	}
	return strings.Join(reasons, ", ")
}

func containsTag(tags []string, target string) bool {
	for _, t := range tags {
		if t == target {
			return true
		}
	}
	return false
}
