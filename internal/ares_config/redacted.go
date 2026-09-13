package ares_config

// redactedMarker is the literal every redacted secret is replaced with.
const redactedMarker = "***"

// Redacted returns a deep copy of the config with secrets replaced by the
// literal "***", safe for JSON/YAML serialization to logs or the
// /runtime/config endpoint. The receiver is not modified.
//
// Current redactions:
//   - LLM.APIKey and each LLM.Fallbacks[].APIKey
//   - LLM.Extra values (custom provider credentials ride here)
//   - Storage.Password
//   - Security.JWTSecret
//   - Introspect.Token
//   - Kernel.Chaos.StopToken (halts live fault injection)
//   - MCP servers' stdio Env values and SSE Headers values
//
// Secret-bearing maps are redacted wholesale (keys kept, values replaced):
// the config dump is a diagnostic surface, and over-redacting a harmless
// value is preferable to leaking a credential. Every map is copied before
// being rewritten so the live configuration keeps its real secrets.
//
// Storage.Password also carries json:"-" so default JSON marshaling omits it,
// but this method makes the redaction explicit and YAML-safe.
func (c *Config) Redacted() *Config {
	out := *c

	// LLM provider key (and its fallbacks).
	out.LLM = c.LLM
	out.LLM.Extra = redactStringMap(c.LLM.Extra)
	if out.LLM.APIKey != "" {
		out.LLM.APIKey = redactedMarker
	}
	if len(out.LLM.Fallbacks) > 0 {
		out.LLM.Fallbacks = make([]LLMConfig, len(c.LLM.Fallbacks))
		for i, fb := range c.LLM.Fallbacks {
			out.LLM.Fallbacks[i] = fb
			out.LLM.Fallbacks[i].Extra = redactStringMap(fb.Extra)
			if fb.APIKey != "" {
				out.LLM.Fallbacks[i].APIKey = redactedMarker
			}
		}
	}

	// Storage password.
	out.Storage = c.Storage
	if out.Storage.Password != "" {
		out.Storage.Password = redactedMarker
	}

	// JWT signing secret.
	out.Security = c.Security
	if out.Security.JWTSecret != "" {
		out.Security.JWTSecret = redactedMarker
	}

	// Introspect read-side bearer token.
	out.Introspect = c.Introspect
	if out.Introspect.Token != "" {
		out.Introspect.Token = redactedMarker
	}

	// Chaos emergency-stop token.
	out.Kernel = c.Kernel
	if out.Kernel.Chaos.StopToken != "" {
		out.Kernel.Chaos.StopToken = redactedMarker
	}

	// MCP transport credentials: stdio children receive secrets via Env, SSE
	// connections carry them in Headers. Copy each server (and its transport
	// pointers) before rewriting so the live config is untouched.
	out.MCP = c.MCP
	if len(c.MCP.Servers) > 0 {
		out.MCP.Servers = make([]MCPServerEntry, len(c.MCP.Servers))
		for i, srv := range c.MCP.Servers {
			out.MCP.Servers[i] = srv
			if srv.Transport.Stdio != nil {
				stdio := *srv.Transport.Stdio
				stdio.Env = redactStringMap(srv.Transport.Stdio.Env)
				out.MCP.Servers[i].Transport.Stdio = &stdio
			}
			if srv.Transport.SSE != nil {
				sse := *srv.Transport.SSE
				sse.Headers = redactStringMap(srv.Transport.SSE.Headers)
				out.MCP.Servers[i].Transport.SSE = &sse
			}
		}
	}

	return &out
}

// redactStringMap returns a copy of m with every value replaced by the
// redaction marker. A nil map stays nil, so a configuration that never set
// the field does not gain an empty JSON object in the dump.
func redactStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k := range m {
		out[k] = redactedMarker
	}
	return out
}
