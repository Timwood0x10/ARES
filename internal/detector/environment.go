package detector

import (
	"context"
	"net/http"
	"os"
	"strings"
	"time"
)

// ollamaURL is the base URL of the local Ollama daemon. It is unexported so
// tests can redirect the probe at an in-process httptest server or a closed
// port without touching the real network.
var ollamaURL = "http://localhost:11434"

// ollamaProbePath is the Ollama endpoint hit to confirm the daemon is up.
const ollamaProbePath = "/api/tags"

// ollamaDefaultModel is the model assumed when Ollama is detected.
const ollamaDefaultModel = "llama3.2"

// ollamaProbeMaxTimeout caps the per-call Ollama probe so a single unreachable
// host cannot consume the whole detection budget.
const ollamaProbeMaxTimeout = 2 * time.Second

// Environment describes services detected in the local environment.
type Environment struct {
	LLMProvider     string // "ollama" | "openai" | "anthropic" | ""
	LLMEndpoint     string
	LLMModel        string
	EmbeddingModel  string
	PostgreSQLURL   string
	MCPEndpoints    []string
	HasOllama       bool
	HasOpenAIKey    bool
	HasAnthropicKey bool
}

// Detect scans the local environment for available services and returns an
// Environment describing what was found. Detection is read-only: it probes
// ports and reads environment variables but starts nothing.
//
// The timeout bounds the total detection effort. When it elapses (or the
// caller's ctx is cancelled), Detect returns whatever was detected so far
// without panicking or hanging. Each HTTP probe uses its own (shorter) timeout
// and respects ctx, so an unreachable host can never block detection.
//
// Detect never returns nil.
func Detect(ctx context.Context, timeout time.Duration) *Environment {
	if ctx == nil {
		// Defensive: a nil context would panic in context.WithTimeout; never
		// let that happen, since the package contract forbids panics.
		ctx = context.Background()
	}

	probeCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		probeCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	env := &Environment{}
	probeOllama(probeCtx, timeout, env)
	detectAPIKeys(env)
	detectPostgreSQL(env)
	detectMCP(env)
	return env
}

// probeOllama issues an HTTP GET against the Ollama tags endpoint. On HTTP 200
// it marks Ollama as available and selects it as the LLM provider. Any error
// (connection refused, timeout, non-200, malformed URL) is treated as "Ollama
// not available" and intentionally ignored — detection falls through to the
// next provider. The probe never panics.
func probeOllama(ctx context.Context, budget time.Duration, env *Environment) {
	perCall := ollamaProbeMaxTimeout
	if budget > 0 && budget < perCall {
		perCall = budget
	}
	callCtx, cancel := context.WithTimeout(ctx, perCall)
	defer cancel()

	url := strings.TrimRight(ollamaURL, "/") + ollamaProbePath
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, url, nil)
	if err != nil {
		// Malformed URL: Ollama is effectively unavailable.
		return
	}

	// A fresh client with no Timeout is safe: the request context bounds the
	// call and guarantees we never hang on a slow or absent daemon.
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		// Connection refused, timeout, or ctx cancelled: not available.
		return
	}
	defer func() {
		_ = resp.Body.Close() // best-effort; the probe only needs the status
	}()

	if resp.StatusCode != http.StatusOK {
		return
	}

	env.HasOllama = true
	env.LLMProvider = "ollama"
	env.LLMEndpoint = ollamaURL
	env.LLMModel = ollamaDefaultModel
}

// detectAPIKeys records the presence of OpenAI and Anthropic API keys and, when
// no LLM provider has been chosen yet, selects the first available key as the
// provider. OpenAI takes priority over Anthropic.
func detectAPIKeys(env *Environment) {
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		env.HasOpenAIKey = true
		if env.LLMProvider == "" {
			env.LLMProvider = "openai"
			env.LLMModel = "gpt-4o-mini"
		}
	}
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		env.HasAnthropicKey = true
		if env.LLMProvider == "" {
			env.LLMProvider = "anthropic"
			env.LLMModel = "claude-3-haiku"
		}
	}
}

// detectPostgreSQL resolves a PostgreSQL connection URL from DATABASE_URL, then
// falls back to a best-effort URL assembled from PGHOST/PGPORT/PGUSER/PGDATABASE.
// If none are set, PostgreSQLURL stays empty.
func detectPostgreSQL(env *Environment) {
	if url := os.Getenv("DATABASE_URL"); url != "" {
		env.PostgreSQLURL = url
		return
	}
	if host := os.Getenv("PGHOST"); host != "" {
		env.PostgreSQLURL = buildPgURL(host)
	}
}

// buildPgURL assembles a libpq-style postgres:// URL from the PG* environment
// variables. Missing components are omitted; the result is best-effort.
func buildPgURL(host string) string {
	port := os.Getenv("PGPORT")
	user := os.Getenv("PGUSER")
	db := os.Getenv("PGDATABASE")

	var b strings.Builder
	b.WriteString("postgres://")
	if user != "" {
		b.WriteString(user)
		b.WriteByte('@')
	}
	b.WriteString(host)
	if port != "" {
		b.WriteByte(':')
		b.WriteString(port)
	}
	if db != "" {
		b.WriteByte('/')
		b.WriteString(db)
	}
	return b.String()
}

// detectMCP parses the comma-separated MCP_ENDPOINTS environment variable into
// MCPEndpoints. Empty entries are dropped. Best-effort: no URL validation.
func detectMCP(env *Environment) {
	raw := os.Getenv("MCP_ENDPOINTS")
	if raw == "" {
		return
	}
	for _, ep := range strings.Split(raw, ",") {
		ep = strings.TrimSpace(ep)
		if ep != "" {
			env.MCPEndpoints = append(env.MCPEndpoints, ep)
		}
	}
}
