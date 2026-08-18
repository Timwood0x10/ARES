package ares_config

// redactedMarker is the literal every redacted secret is replaced with.
const redactedMarker = "***"

// Redacted returns a deep copy of the config with secrets replaced by the
// literal "***", safe for JSON/YAML serialization to logs or the
// /runtime/config endpoint. The receiver is not modified.
//
// Current redactions:
//   - LLM.APIKey and each LLM.Fallbacks[].APIKey
//   - Storage.Password
//   - Security.JWTSecret
//
// Storage.Password also carries json:"-" so default JSON marshaling omits it,
// but this method makes the redaction explicit and YAML-safe.
func (c *Config) Redacted() *Config {
	out := *c

	// LLM provider key (and its fallbacks).
	out.LLM = c.LLM
	if out.LLM.APIKey != "" {
		out.LLM.APIKey = redactedMarker
	}
	if len(out.LLM.Fallbacks) > 0 {
		out.LLM.Fallbacks = make([]LLMConfig, len(c.LLM.Fallbacks))
		for i, fb := range c.LLM.Fallbacks {
			out.LLM.Fallbacks[i] = fb
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

	return &out
}
