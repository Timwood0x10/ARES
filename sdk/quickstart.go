package sdk

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/Timwood0x10/ares/internal/detector"
)

// detectFn is the environment detection function used by MustNew. It is a
// package-level variable so tests can inject a deterministic detector without
// touching the network. Defaults to detector.Detect.
var detectFn = detector.Detect

// MustNew is the zero-parameter quickstart entry point. It auto-detects the
// local environment (Ollama / OPENAI_API_KEY / ANTHROPIC_API_KEY), enables
// default memory (compression-only when no embedding service is available),
// and returns a ready-to-use Runtime.
//
// MustNew panics when no LLM provider can be detected or when the Runtime
// cannot be constructed, mirroring regexp.MustCompile's fail-fast philosophy.
// Use New for production code that needs to handle errors gracefully.
//
// Quick start:
//
//	ares := sdk.MustNew()
//	defer ares.Close()
//	agent := ares.NewAgent("assistant", sdk.WithInstruction("You are helpful."))
//	result, _ := agent.Run(ctx, "hello")
//
// The detector probes localhost:11434 for Ollama and reads OPENAI_API_KEY /
// ANTHROPIC_API_KEY from the environment. PostgreSQL and MCP endpoints detected
// by the detector are currently ignored (no URL-string SDK option exists for
// them); wire them explicitly via WithPostgres / WithMCP when needed.
func MustNew() *Runtime {
	env := detectFn(context.Background(), 5*time.Second)
	opts, err := buildOptsFromEnv(env)
	if err != nil {
		panic("ares: " + err.Error())
	}
	rt, err := New(opts...)
	if err != nil {
		panic("ares: " + err.Error())
	}
	return rt
}

// buildOptsFromEnv maps a detected Environment onto the existing SDK With*
// options. It returns an error (not a panic) when no LLM provider is
// available, so callers that prefer graceful handling can call it directly.
//
// Memory is left at its defaultConfig value (Enabled: true), so the returned
// options yield a Runtime with memEnabled == true without an explicit
// WithDefaultMemory call.
//
// Detected PostgreSQL URLs and MCP endpoint URLs are intentionally skipped:
//   - PostgreSQL: WithPostgres takes a structured DatabaseFileConfig, not a
//     URL string. Parse the URL and call WithPostgres explicitly when needed.
//   - MCP: WithMCP takes a Command + Args pair, not an HTTP endpoint. Use
//     WithMCP explicitly for each MCP server.
func buildOptsFromEnv(env *detector.Environment) ([]Option, error) {
	if env == nil {
		return nil, errors.New("no LLM provider detected; start Ollama on localhost:11434 or set OPENAI_API_KEY/ANTHROPIC_API_KEY")
	}

	var opts []Option
	switch env.LLMProvider {
	case providerOllama:
		opts = append(opts, WithOllama(env.LLMModel))
		if env.LLMEndpoint != "" {
			opts = append(opts, WithBaseURL(env.LLMEndpoint))
		}
	case providerOpenAI:
		opts = append(opts,
			WithOpenAI(env.LLMModel),
			WithAPIKey(os.Getenv("OPENAI_API_KEY")),
		)
	case providerAnthropic:
		opts = append(opts,
			WithAnthropic(env.LLMModel),
			WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")),
		)
	default:
		return nil, errors.New("no LLM provider detected; start Ollama on localhost:11434 or set OPENAI_API_KEY/ANTHROPIC_API_KEY")
	}

	// Memory defaults to enabled (see defaultConfig in options.go); no
	// explicit option is appended. The detector's PostgreSQLURL and
	// MCPEndpoints fields are intentionally ignored (see doc comment).
	return opts, nil
}
