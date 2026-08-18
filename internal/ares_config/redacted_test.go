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
	}

	got := cfg.Redacted()

	// Receiver must be untouched.
	if cfg.LLM.APIKey != "sk-secret-1" || cfg.Storage.Password != "db-pass" || cfg.Security.JWTSecret != "jwt-secret" {
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

	// Fallback keys redacted.
	if got.LLM.Fallbacks[0].APIKey != "***" {
		t.Errorf("Fallbacks[0].APIKey = %q, want ***", got.LLM.Fallbacks[0].APIKey)
	}

	// Non-secret fields preserved.
	if got.LLM.Provider != "openai" || got.LLM.Model != "gpt-4" || !got.Storage.Enabled || got.Storage.Host != "db.internal" {
		t.Errorf("non-secret fields must be preserved, got %+v", got)
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
}
