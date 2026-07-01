package main

import (
	"fmt"
	"math/rand"
	"time"

	exp "github.com/Timwood0x10/ares/internal/ares_evolution/experience"
)

// ── Strategy Profiles ──────────────────────────────────────────

type strategyProfile struct {
	id             string
	temperature    float64
	topK           float64
	maxTokens      float64
	freqPenalty    float64
	presentPenalty float64
	prompt         string
	// tool → success probability [0,1]
	proficiencies map[string]float64
}

var defaultProfiles = []strategyProfile{
	{
		id: "aggressive", temperature: 0.9, topK: 60, maxTokens: 4096,
		freqPenalty: 0.0, presentPenalty: 0.3, prompt: "bold",
		proficiencies: map[string]float64{
			"web_search": 0.85, "calculator": 0.45, "regex": 0.50, "json_tools": 0.55, "file_tools": 0.60,
		},
	},
	{
		id: "precise", temperature: 0.2, topK: 30, maxTokens: 2048,
		freqPenalty: 0.5, presentPenalty: 0.0, prompt: "careful",
		proficiencies: map[string]float64{
			"web_search": 0.55, "calculator": 0.92, "regex": 0.90, "json_tools": 0.88, "file_tools": 0.85,
		},
	},
	{
		id: "balanced", temperature: 0.5, topK: 40, maxTokens: 3072,
		freqPenalty: 0.2, presentPenalty: 0.1, prompt: "helpful",
		proficiencies: map[string]float64{
			"web_search": 0.75, "calculator": 0.75, "regex": 0.78, "json_tools": 0.80, "file_tools": 0.78,
		},
	},
	{
		id: "speedy", temperature: 0.3, topK: 20, maxTokens: 1024,
		freqPenalty: 0.3, presentPenalty: 0.0, prompt: "concise",
		proficiencies: map[string]float64{
			"web_search": 0.60, "calculator": 0.80, "regex": 0.85, "json_tools": 0.82, "file_tools": 0.90,
		},
	},
	{
		id: "creative", temperature: 0.8, topK: 80, maxTokens: 4096,
		freqPenalty: 0.0, presentPenalty: 0.5, prompt: "imaginative",
		proficiencies: map[string]float64{
			"web_search": 0.82, "calculator": 0.40, "regex": 0.45, "json_tools": 0.50, "file_tools": 0.55,
		},
	},
}

// ── Conversation Scenarios ──────────────────────────────────────

type toolCallTemplate struct {
	tool       string
	input      string
	output     string
	latency    int64
	latencyVar int64
	resultSize int64
	errPattern string // error code when applicable
}

type conversationScenario struct {
	name     string
	taskType string
	calls    []toolCallTemplate
}

var scenarios = []conversationScenario{
	{
		name: "verify_math_calculation", taskType: "math_verification",
		calls: []toolCallTemplate{
			{tool: "calculator", input: "2 + 2 * 3", output: "8", latency: 45, latencyVar: 15, resultSize: 64},
			{tool: "calculator", input: "sqrt(144) + 5^2", output: "37", latency: 50, latencyVar: 20, resultSize: 72},
			{tool: "calculator", input: "integrate x^2 from 0 to 3", output: "9.0", latency: 120, latencyVar: 40, resultSize: 96},
			{tool: "web_search", input: "verify integration rules polynomial", output: "StackOverflow: power rule confirmed", latency: 1800, latencyVar: 600, resultSize: 4096},
			{tool: "calculator", input: "sin(45°) * cos(45°)", output: "0.5", latency: 55, latencyVar: 15, resultSize: 80},
		},
	},
	{
		name: "extract_log_patterns", taskType: "log_analysis",
		calls: []toolCallTemplate{
			{tool: "file_tools", input: "read /var/log/app/error.log", output: "127 lines, 3 ERROR entries", latency: 85, latencyVar: 30, resultSize: 8192},
			{tool: "regex", input: `\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}.\d+Z\s+ERROR`, output: "3 matches found", latency: 30, latencyVar: 10, resultSize: 256},
			{tool: "regex", input: `panic: .+`, output: "1 match: nil pointer dereference", latency: 25, latencyVar: 8, resultSize: 128},
			{tool: "regex", input: `(?i)(timeout|deadline)`, output: "2 matches: both in goroutine-7", latency: 28, latencyVar: 10, resultSize: 160},
			{tool: "file_tools", input: "read /var/log/app/access.log | tail -50", output: "50 lines, 200 requests total", latency: 70, latencyVar: 25, resultSize: 6144},
			{tool: "regex", input: `5\d{2}\s+\d+ms`, output: "5 HTTP 5xx responses found", latency: 32, latencyVar: 12, resultSize: 192},
		},
	},
	{
		name: "validate_api_response", taskType: "api_validation",
		calls: []toolCallTemplate{
			{tool: "json_tools", input: `{"status":"ok","data":{"id":42,"name":"test","price":19.99}}`, output: "valid JSON, 3 fields in data", latency: 15, latencyVar: 5, resultSize: 128},
			{tool: "json_tools", input: `{"items":[{"id":1},{"id":2}],"total":2}`, output: "valid JSON array with 2 items", latency: 18, latencyVar: 6, resultSize: 96},
			{tool: "web_search", input: "REST API best practices response format 2026", output: "JSON:API spec recommends envelope", latency: 2200, latencyVar: 800, resultSize: 8192},
			{tool: "json_tools", input: `[invalid json]`, output: "ERROR: invalid JSON at line 1", latency: 12, latencyVar: 4, resultSize: 64, errPattern: "INVALID_INPUT"},
			{tool: "json_tools", input: `{"nested":{"deep":{"value":true}}}`, output: "valid, depth=3", latency: 20, latencyVar: 7, resultSize: 80},
		},
	},
	{
		name: "research_new_framework", taskType: "technology_research",
		calls: []toolCallTemplate{
			{tool: "web_search", input: "best Go web framework 2026 performance comparison", output: "Top results: Chi, Echo, Gin, Fiber benchmarks", latency: 3100, latencyVar: 1200, resultSize: 16384},
			{tool: "web_search", input: "Go 1.24 new features net/http routing", output: "Go 1.24 adds enhanced ServeMux with path params", latency: 2800, latencyVar: 1000, resultSize: 12288},
			{tool: "web_search", input: "OpenTelemetry Go best practices production", output: "OTel SDK 1.30: sampling, exporters, propagators", latency: 3500, latencyVar: 1500, resultSize: 20480},
			{tool: "calculator", input: "10000 req/s * 0.95 uptime SLA", output: "8550 req/s effective capacity", latency: 40, latencyVar: 12, resultSize: 64},
			{tool: "web_search", input: "Go middleware chaining performance overhead p99", output: "Chi middleware: ~50μs per layer p99", latency: 2600, latencyVar: 900, resultSize: 10240},
		},
	},
	{
		name: "parse_config_files", taskType: "configuration",
		calls: []toolCallTemplate{
			{tool: "file_tools", input: "list /etc/app/conf.d/", output: "3 files: database.yaml, cache.yaml, auth.yaml", latency: 35, latencyVar: 15, resultSize: 512},
			{tool: "file_tools", input: "read /etc/app/conf.d/database.yaml", output: "42 lines: postgres config with read-replica", latency: 55, latencyVar: 20, resultSize: 2048},
			{tool: "regex", input: `host:\s+\S+`, output: "3 host entries found", latency: 22, latencyVar: 8, resultSize: 96},
			{tool: "json_tools", input: `{"port":5432,"host":"db-primary","pool":20}`, output: "valid, pool=20 connections", latency: 16, latencyVar: 5, resultSize: 80},
			{tool: "regex", input: `password:\s+\S+`, output: "WARNING: 1 potential secret in config", latency: 25, latencyVar: 10, resultSize: 128},
		},
	},
	{
		name: "debug_production_issue", taskType: "debugging",
		calls: []toolCallTemplate{
			{tool: "web_search", input: "Go memory leak pprof heap profile analysis", output: "Blog: tracing goroutine stacks in heap dumps", latency: 2900, latencyVar: 1100, resultSize: 14336},
			{tool: "file_tools", input: "read /proc/self/maps", output: "memory mappings: 48 regions, 2.3GB total", latency: 60, latencyVar: 20, resultSize: 4096},
			{tool: "calculator", input: "2.3GB / 48 regions", output: "~49MB per region average", latency: 35, latencyVar: 10, resultSize: 64},
			{tool: "web_search", input: "net/http connection leak keep-alive timeout", output: "DefaultTransport: 90s keep-alive, idle conns accumulate", latency: 2400, latencyVar: 800, resultSize: 8192},
			{tool: "file_tools", input: "read /var/log/app/goroutine.dump", output: "1278 goroutines, 893 stuck in net/http.(*persistConn).readLoop", latency: 95, latencyVar: 35, resultSize: 32768},
		},
	},
	{
		name: "generate_code_report", taskType: "code_analysis",
		calls: []toolCallTemplate{
			{tool: "file_tools", input: "read src/main.go --lines 200", output: "200 lines, 4 exported functions", latency: 50, latencyVar: 18, resultSize: 6144},
			{tool: "file_tools", input: "glob src/**/*.go", output: "23 Go source files found", latency: 40, latencyVar: 15, resultSize: 1024},
			{tool: "regex", input: `func\s+[A-Z]\w+`, output: "12 exported functions across codebase", latency: 28, latencyVar: 10, resultSize: 192},
			{tool: "json_tools", input: `{"files":23,"functions":47,"coverage":81.5}`, output: "valid JSON summary", latency: 14, latencyVar: 5, resultSize: 64},
			{tool: "calculator", input: "47 functions / 23 files", output: "~2.04 functions per file", latency: 30, latencyVar: 8, resultSize: 64},
			{tool: "web_search", input: "Go code coverage best practices 80% threshold", output: "Industry standard: 80% line coverage minimum", latency: 2100, latencyVar: 700, resultSize: 7168},
		},
	},
	{
		name: "benchmark_performance", taskType: "benchmarking",
		calls: []toolCallTemplate{
			{tool: "calculator", input: "950th percentile / 50th percentile", output: "2.1x p95/p50 ratio", latency: 35, latencyVar: 10, resultSize: 64},
			{tool: "web_search", input: "Go benchmark p99 latency optimization strategies", output: "Techniques: pool reuse, GC tuning, CPU pinning", latency: 3200, latencyVar: 1300, resultSize: 16384},
			{tool: "calculator", input: "mean: 245ms, stddev: 89ms, n: 1000", output: "SEM: 2.81ms, 95% CI: [239.5, 250.5]", latency: 60, latencyVar: 20, resultSize: 128},
			{tool: "web_search", input: "Go GC tuning GOMEMLIMIT GOGC 2026", output: "Go 1.24: soft memory limit, default GOGC=100", latency: 2700, latencyVar: 1000, resultSize: 10240},
			{tool: "calculator", input: "1024 * 768 * 4 bytes per pixel", output: "3.0 MB per frame", latency: 40, latencyVar: 12, resultSize: 64},
		},
	},
	{
		name: "data_migration", taskType: "data_migration",
		calls: []toolCallTemplate{
			{tool: "json_tools", input: `{"users":[{"id":1,"email":"a@b"},{"id":2,"email":"c@d"}],"version":2}`, output: "valid, 2 users in migration batch", latency: 20, latencyVar: 7, resultSize: 160},
			{tool: "file_tools", input: "write /tmp/migration/v2/users.json", output: "written 1024 bytes", latency: 45, latencyVar: 15, resultSize: 128},
			{tool: "json_tools", input: `{"batch":1,"total":500,"progress":0.2}`, output: "20% complete, 100/500 records", latency: 15, latencyVar: 5, resultSize: 80},
			{tool: "web_search", input: "PostgreSQL bulk insert performance 100k rows", output: "COPY FROM: 100k rows in 1.2s, 5x faster than INSERT", latency: 2500, latencyVar: 900, resultSize: 9216},
			{tool: "file_tools", input: "read /tmp/migration/v2/schema.sql", output: "15 lines: CREATE TABLE migration_status", latency: 38, latencyVar: 12, resultSize: 1024},
			{tool: "json_tools", input: `{"errors":[],"completed":true,"records":500}`, output: "migration complete, 0 errors", latency: 16, latencyVar: 5, resultSize: 64},
		},
	},
	{
		name: "security_audit", taskType: "security_audit",
		calls: []toolCallTemplate{
			{tool: "regex", input: `api[_-]?key\s*=\s*['"][^'"]+['"]`, output: "3 potential API keys found", latency: 35, latencyVar: 12, resultSize: 256},
			{tool: "regex", input: `(?:SELECT|INSERT|UPDATE|DELETE)\s+`, output: "7 SQL statements detected", latency: 30, latencyVar: 10, resultSize: 192},
			{tool: "web_search", input: "OWASP top 10 2026 API security", output: "BOLA, broken auth, rate limiting top risks", latency: 3400, latencyVar: 1400, resultSize: 20480},
			{tool: "regex", input: `(?:\.\./|\.\.\\)`, output: "No path traversal found", latency: 22, latencyVar: 8, resultSize: 96},
			{tool: "file_tools", input: "read /etc/app/security.yaml", output: "28 lines: rate limit, CORS, auth config", latency: 50, latencyVar: 18, resultSize: 1536},
			{tool: "web_search", input: "Go JWT middleware security best practices 2026", output: "Use jwx library, short expiry, rotate keys", latency: 2800, latencyVar: 1000, resultSize: 12288},
		},
	},
	{
		name: "deploy_kubernetes", taskType: "deployment",
		calls: []toolCallTemplate{
			{tool: "web_search", input: "Kubernetes rolling update strategy best practices", output: "maxSurge=25%, maxUnavailable=25%, minReady=10s", latency: 2600, latencyVar: 900, resultSize: 10240},
			{tool: "file_tools", input: "read k8s/deployment.yaml", output: "52 lines: deployment spec with 3 replicas", latency: 55, latencyVar: 20, resultSize: 2048},
			{tool: "regex", input: `image:\s+\S+`, output: "2 image references found", latency: 25, latencyVar: 8, resultSize: 128},
			{tool: "calculator", input: "3 replicas * 512Mi memory each", output: "1.5Gi total memory reservation", latency: 35, latencyVar: 10, resultSize: 64},
			{tool: "json_tools", input: `{"apiVersion":"apps/v1","kind":"Deployment","replicas":3}`, output: "valid K8s deployment manifest", latency: 22, latencyVar: 7, resultSize: 96},
			{tool: "web_search", input: "K8s liveness vs readiness probe differences", output: "liveness: restart container; readiness: stop traffic", latency: 2400, latencyVar: 800, resultSize: 8192},
		},
	},
	{
		name: "monitoring_setup", taskType: "observability",
		calls: []toolCallTemplate{
			{tool: "web_search", input: "Prometheus metric types counter gauge histogram summary", output: "4 metric types: counter for events, gauge for values", latency: 2200, latencyVar: 700, resultSize: 9216},
			{tool: "calculator", input: "rate(http_requests_total[5m])", output: "extrapolating: ~120 req/s average", latency: 45, latencyVar: 15, resultSize: 64},
			{tool: "json_tools", input: `{"alerts":[{"name":"HighLatency","threshold":500}],"rules":12}`, output: "valid alerting config, 12 rules", latency: 18, latencyVar: 6, resultSize: 96},
			{tool: "regex", input: `(?:alert|rule|record):\s+\w+`, output: "14 named Prometheus rules found", latency: 28, latencyVar: 10, resultSize: 192},
			{tool: "file_tools", input: "read prometheus/rules/alerts.yml", output: "38 lines: 3 alert rules with annotations", latency: 48, latencyVar: 18, resultSize: 1792},
			{tool: "web_search", input: "Grafana dashboard variable chaining best practice", output: "Template variables: namespace → pod → container chain", latency: 2800, latencyVar: 1100, resultSize: 12288},
		},
	},
	{
		name: "database_optimization", taskType: "database_tuning",
		calls: []toolCallTemplate{
			{tool: "web_search", input: "PostgreSQL query planner EXPLAIN ANALYZE interpretation", output: "Seq Scan vs Index Scan: cost estimation guide", latency: 3100, latencyVar: 1200, resultSize: 16384},
			{tool: "calculator", input: "EXPLAIN: cost=10000..45000 rows=50000", output: "~0.7ms per row, seq scan on 50k rows", latency: 50, latencyVar: 18, resultSize: 80},
			{tool: "regex", input: `Index Scan using \w+`, output: "3 index scans in query plan", latency: 28, latencyVar: 10, resultSize: 160},
			{tool: "web_search", input: "PostgreSQL partial index performance gain", output: "Partial index: 90% size reduction, 40% query speedup", latency: 2500, latencyVar: 900, resultSize: 10240},
			{tool: "json_tools", input: `{"indexes":7,"seqScans":2,"indexScans":15}`, output: "index usage ratio: 88%", latency: 20, latencyVar: 7, resultSize: 80},
			{tool: "calculator", input: "cache_hit_ratio: 0.97, 1M queries", output: "~30k cache misses per 1M queries", latency: 38, latencyVar: 12, resultSize: 64},
		},
	},
	{
		name: "incident_response", taskType: "incident_response",
		calls: []toolCallTemplate{
			{tool: "web_search", input: "PagerDuty incident response best practices runbook", output: "Triage → Mitigation → Resolution → Postmortem", latency: 2700, latencyVar: 1000, resultSize: 12288},
			{tool: "file_tools", input: "read /var/log/app/incident-20260701.log", output: "150 lines: timestamped events from alert to mitigation", latency: 80, latencyVar: 30, resultSize: 10240},
			{tool: "regex", input: `error|fatal|critical`, output: "8 critical events found", latency: 30, latencyVar: 10, resultSize: 192},
			{tool: "calculator", input: "alert_at: 14:23:00, resolved_at: 14:47:30", output: "MTTR: 24.5 minutes", latency: 40, latencyVar: 12, resultSize: 64},
			{tool: "web_search", input: "SRE error budget calculation 99.9% SLO 30 day window", output: "Monthly error budget: 43m 12s downtime allowed", latency: 2900, latencyVar: 1100, resultSize: 14336},
			{tool: "calculator", input: "current downtime 24.5m vs budget 43.2m", output: "56.7% of error budget consumed", latency: 38, latencyVar: 10, resultSize: 64},
			{tool: "file_tools", input: "write /var/log/app/postmortem.md", output: "written 2048 bytes: root cause analysis", latency: 65, latencyVar: 22, resultSize: 256},
		},
	},
	{
		name: "architecture_review", taskType: "architecture_review",
		calls: []toolCallTemplate{
			{tool: "web_search", input: "microservices vs monolith decision framework 2026", output: "Conway's Law, team topology, bounded contexts", latency: 3000, latencyVar: 1200, resultSize: 16384},
			{tool: "json_tools", input: `{"services":8,"dependencies":23,"events":45}`, output: "8 services with 23 sync + 45 async deps", latency: 22, latencyVar: 8, resultSize: 128},
			{tool: "regex", input: `(?i)(circuit.breaker|bulkhead|retry|timeout)`, output: "6 resilience patterns found in codebase", latency: 35, latencyVar: 12, resultSize: 256},
			{tool: "calculator", input: "8 services * 3 replicas * 2Gi each", output: "48Gi total compute requirement", latency: 40, latencyVar: 12, resultSize: 64},
			{tool: "web_search", input: "event-driven architecture saga pattern distributed transactions", output: "Choreography vs orchestration saga implementations", latency: 3400, latencyVar: 1400, resultSize: 20480},
			{tool: "file_tools", input: "read docs/architecture.md", output: "85 lines: C4 diagrams, ADRs, tech decisions", latency: 60, latencyVar: 20, resultSize: 4096},
		},
	},
}

// ── Data Generator ────────────────────────────────────────────

// generateToolCallRecords produces realistic tool call records for each strategy
// profile across all conversation scenarios. Success/failure is determined by
// each profile's per-tool proficiency with seeded randomness for reproducibility.
func generateToolCallRecords(seed int64) []exp.ToolCallRecord {
	rng := rand.New(rand.NewSource(seed))
	now := time.Now().Add(-30 * time.Minute)

	var records []exp.ToolCallRecord

	for _, profile := range defaultProfiles {
		seq := 0
		for _, sc := range scenarios {
			for _, call := range sc.calls {
				seq++
				success := rng.Float64() < profile.proficiencies[call.tool]

				latencyJitter := int64(0)
				if call.latencyVar > 0 {
					latencyJitter = int64(rng.Float64()*float64(call.latencyVar)*2 - float64(call.latencyVar))
				}
				latency := call.latency + latencyJitter
				if latency < 5 {
					latency = 5
				}

				errCode := ""
				if !success {
					errCode = randomErrorCode(rng, call.tool)
				}

				records = append(records, exp.ToolCallRecord{
					StrategyID:      profile.id,
					TaskType:        sc.taskType,
					ToolName:        call.tool,
					InputSummary:    call.input,
					OutputSummary:   call.output,
					ErrorCode:       errCode,
					LatencyMs:       latency,
					Success:         success,
					Timestamp:       now.Add(time.Duration(seq) * time.Second),
					RetryCount:      int(rng.Int31n(3)),
					ResultSizeBytes: call.resultSize,
				})
			}
		}
	}

	return records
}

// randomErrorCode returns a realistic error code for a failed tool call.
func randomErrorCode(rng *rand.Rand, tool string) string {
	errors := map[string][]string{
		"calculator": {"SYNTAX_ERROR", "DIVISION_BY_ZERO", "UNDEFINED_VARIABLE"},
		"regex":      {"INVALID_PATTERN", "COMPILE_ERROR", "BACKTRACK_LIMIT"},
		"json_tools": {"PARSE_ERROR", "UNEXPECTED_TOKEN", "DEPTH_LIMIT"},
		"web_search": {"TIMEOUT", "RATE_LIMIT", "NO_RESULTS", "BLOCKED"},
		"file_tools": {"NOT_FOUND", "PERMISSION_DENIED", "TOO_LARGE"},
	}
	errs := errors[tool]
	if errs == nil {
		return "UNKNOWN"
	}
	return errs[rng.Intn(len(errs))]
}

// printProfileTable displays the strategy profile proficiency matrix.
func printProfileTable() {
	fmt.Println("\n   Strategy Profile Proficiencies (success rate per tool):")
	fmt.Printf("   %-14s %-12s %-12s %-12s %-12s %-12s\n",
		"Profile", "web_search", "calculator", "regex", "json_tools", "file_tools")
	fmt.Println("   " + repeat("-", 74))
	for _, p := range defaultProfiles {
		fmt.Printf("   %-14s %-12.0f %-12.0f %-12.0f %-12.0f %-12.0f\n",
			p.id,
			p.proficiencies["web_search"]*100,
			p.proficiencies["calculator"]*100,
			p.proficiencies["regex"]*100,
			p.proficiencies["json_tools"]*100,
			p.proficiencies["file_tools"]*100,
		)
	}
}

// repeat returns a string of n copies of s.
func repeat(s string, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = s[0]
	}
	return string(b)
}
