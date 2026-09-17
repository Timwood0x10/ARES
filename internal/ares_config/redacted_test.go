package ares_config

import "testing"

// TestConfigRedacted verifies Redacted() replaces secrets without mutating
// the receiver, and leaves non-secret values intact.
func TestConfigRedacted(t *testing.T) {
	cfg := &Config{
		LLM: LLMConfig{
			Provider: "openai",
			APIKey:   "sk-secret-1",
			Model:    "gpt-4",
			Fallbacks: []LLMConfig{
				{Provider: "ollama", APIKey: "sk-fallback", Model: "llama3"},
			},
		},
		Storage: StorageConfig{
			Enabled:  true,
			Host:     "db.internal",
			Password: "db-pass",
		},
		Security: SecurityConfig{
			JWTSecret: "jwt-secret",
		},
		Introspect: IntrospectConfig{
			Token: "panel-token",
		},
	}

	got := cfg.Redacted()

	// Receiver must be untouched.
	if cfg.LLM.APIKey != "sk-secret-1" || cfg.Storage.Password != "db-pass" || cfg.Security.JWTSecret != "jwt-secret" || cfg.Introspect.Token != "panel-token" {
		t.Fatal("Redacted must not mutate the receiver")
	}

	// Secrets redacted.
	if got.LLM.APIKey != "***" {
		t.Errorf("LLM.APIKey = %q, want ***", got.LLM.APIKey)
	}
	if got.Storage.Password != "***" {
		t.Errorf("Storage.Password = %q, want ***", got.Storage.Password)
	}
	if got.Security.JWTSecret != "***" {
		t.Errorf("Security.JWTSecret = %q, want ***", got.Security.JWTSecret)
	}
	if got.Introspect.Token != "***" {
		t.Errorf("Introspect.Token = %q, want ***", got.Introspect.Token)
	}

	// Fallback keys redacted.
	if got.LLM.Fallbacks[0].APIKey != "***" {
		t.Errorf("Fallbacks[0].APIKey = %q, want ***", got.LLM.Fallbacks[0].APIKey)
	}

	// Non-secret fields preserved.
	if got.LLM.Provider != "openai" || got.LLM.Model != "gpt-4" || !got.Storage.Enabled || got.Storage.Host != "db.internal" {
		t.Errorf("non-secret fields must be preserved, got %+v", got)
	}
}

// TestConfigRedactedChaosAndMCPSecrets verifies every secret the config dump
// endpoint can reach is redacted, not just the four original fields.
//
// Regression: /api/runtime/config serialized Redacted(), but the chaos stop
// token (which can halt live fault injection) and MCP transport credentials
// (stdio env / SSE headers) plus LLM.Extra passed through in clear text.
func TestConfigRedactedChaosAndMCPSecrets(t *testing.T) {
	cfg := &Config{
		LLM:      LLMConfig{Provider: "openai", Extra: map[string]string{"org": "sk-extra"}},
		Security: SecurityConfig{JWTSecret: "jwt-secret", ArenaAPIKey: "arena-key"},
		Kernel:   KernelConfig{Chaos: ChaosConfig{StopToken: "chaos-token"}},
		MCP: MCPConfig{Servers: []MCPServerEntry{
			{
				Name: "stdio-srv",
				Transport: TransportEntry{
					Type:  "stdio",
					Stdio: &StdioEntry{Command: "npx", Env: map[string]string{"API_KEY": "stdio-secret"}},
				},
			},
			{
				Name: "sse-srv",
				Transport: TransportEntry{
					Type: "sse",
					SSE:  &SSEEntry{URL: "https://example.invalid", Headers: map[string]string{"Authorization": "Bearer sse-secret"}},
				},
			},
		}},
	}

	got := cfg.Redacted()

	// The receiver must not be mutated: redaction has to deep-copy the maps
	// before overwriting values, otherwise the live config loses its secrets.
	if cfg.Security.ArenaAPIKey != "arena-key" {
		t.Error("Redacted must not mutate Security.ArenaAPIKey")
	}
	if cfg.Kernel.Chaos.StopToken != "chaos-token" {
		t.Error("Redacted must not mutate Kernel.Chaos.StopToken")
	}
	if cfg.LLM.Extra["org"] != "sk-extra" {
		t.Error("Redacted must not mutate LLM.Extra")
	}
	if cfg.MCP.Servers[0].Transport.Stdio.Env["API_KEY"] != "stdio-secret" {
		t.Error("Redacted must not mutate MCP stdio env")
	}
	if cfg.MCP.Servers[1].Transport.SSE.Headers["Authorization"] != "Bearer sse-secret" {
		t.Error("Redacted must not mutate MCP sse headers")
	}

	// Secrets redacted.
	if got.Security.JWTSecret != "***" {
		t.Errorf("Security.JWTSecret = %q, want ***", got.Security.JWTSecret)
	}
	if got.Security.ArenaAPIKey != "***" {
		t.Errorf("Security.ArenaAPIKey = %q, want ***", got.Security.ArenaAPIKey)
	}
	if got.Kernel.Chaos.StopToken != "***" {
		t.Errorf("Kernel.Chaos.StopToken = %q, want ***", got.Kernel.Chaos.StopToken)
	}
	if got.LLM.Extra["org"] != "***" {
		t.Errorf("LLM.Extra[org] = %q, want ***", got.LLM.Extra["org"])
	}
	if got.MCP.Servers[0].Transport.Stdio.Env["API_KEY"] != "***" {
		t.Errorf("stdio env = %q, want ***", got.MCP.Servers[0].Transport.Stdio.Env["API_KEY"])
	}
	if got.MCP.Servers[1].Transport.SSE.Headers["Authorization"] != "***" {
		t.Errorf("sse header = %q, want ***", got.MCP.Servers[1].Transport.SSE.Headers["Authorization"])
	}

	// Non-secret transport fields must survive so the dump stays useful.
	if got.MCP.Servers[0].Transport.Stdio.Command != "npx" || got.MCP.Servers[1].Transport.SSE.URL != "https://example.invalid" {
		t.Error("non-secret MCP transport fields must be preserved")
	}
}

// TestConfigRedactedEmptySecrets verifies Redacted handles empty secrets
// without injecting "***" (no false redaction of empty values).
func TestConfigRedactedEmptySecrets(t *testing.T) {
	cfg := &Config{
		LLM:     LLMConfig{Provider: "ollama"},
		Storage: StorageConfig{Enabled: false},
	}
	got := cfg.Redacted()
	if got.LLM.APIKey != "" {
		t.Errorf("empty APIKey must stay empty, got %q", got.LLM.APIKey)
	}
	if got.Storage.Password != "" {
		t.Errorf("empty password must stay empty, got %q", got.Storage.Password)
	}
	if got.Introspect.Token != "" {
		t.Errorf("empty Introspect.Token must stay empty, got %q", got.Introspect.Token)
	}
}
