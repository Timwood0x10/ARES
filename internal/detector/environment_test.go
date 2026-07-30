package detector

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

// setOllamaURL redirects the Ollama probe at url for the duration of the test,
// restoring the previous value on cleanup. Tests must use this instead of
// writing ollamaURL directly so cleanup is guaranteed even on failure.
func setOllamaURL(t *testing.T, url string) {
	t.Helper()
	prev := ollamaURL
	ollamaURL = url
	t.Cleanup(func() { ollamaURL = prev })
}

// closedPortURL returns an http:// URL whose TCP port is closed at call time
// (nothing is listening), so a probe fails fast with connection refused. Used
// to deterministically simulate "Ollama not running".
func closedPortURL(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for closed port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() // best-effort; we want the port closed, not held
	return "http://" + addr
}

func TestDetect_OllamaRunning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ollamaProbePath {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"models":[]}`))
	}))
	t.Cleanup(srv.Close)
	setOllamaURL(t, srv.URL)

	env := Detect(context.Background(), 5*time.Second)
	if !env.HasOllama {
		t.Fatal("expected HasOllama=true, got false")
	}
	if env.LLMProvider != "ollama" {
		t.Errorf("LLMProvider=%q, want \"ollama\"", env.LLMProvider)
	}
	if env.LLMEndpoint != srv.URL {
		t.Errorf("LLMEndpoint=%q, want %q", env.LLMEndpoint, srv.URL)
	}
	if env.LLMModel != ollamaDefaultModel {
		t.Errorf("LLMModel=%q, want %q", env.LLMModel, ollamaDefaultModel)
	}
}

func TestDetect_OnlyOpenAIKey(t *testing.T) {
	setOllamaURL(t, closedPortURL(t))
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("ANTHROPIC_API_KEY", "")

	env := Detect(context.Background(), 5*time.Second)
	if env.HasOllama {
		t.Error("expected HasOllama=false")
	}
	if !env.HasOpenAIKey {
		t.Error("expected HasOpenAIKey=true")
	}
	if env.LLMProvider != "openai" {
		t.Errorf("LLMProvider=%q, want \"openai\"", env.LLMProvider)
	}
	if env.LLMModel != "gpt-4o-mini" {
		t.Errorf("LLMModel=%q, want \"gpt-4o-mini\"", env.LLMModel)
	}
}

func TestDetect_OnlyAnthropicKey(t *testing.T) {
	setOllamaURL(t, closedPortURL(t))
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")

	env := Detect(context.Background(), 5*time.Second)
	if env.HasOllama {
		t.Error("expected HasOllama=false")
	}
	if !env.HasAnthropicKey {
		t.Error("expected HasAnthropicKey=true")
	}
	if env.LLMProvider != "anthropic" {
		t.Errorf("LLMProvider=%q, want \"anthropic\"", env.LLMProvider)
	}
	if env.LLMModel != "claude-3-haiku" {
		t.Errorf("LLMModel=%q, want \"claude-3-haiku\"", env.LLMModel)
	}
}

func TestDetect_NothingAvailable(t *testing.T) {
	setOllamaURL(t, closedPortURL(t))
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("PGHOST", "")
	t.Setenv("MCP_ENDPOINTS", "")

	env := Detect(context.Background(), 5*time.Second)
	if env.HasOllama || env.HasOpenAIKey || env.HasAnthropicKey {
		t.Errorf("expected no providers, got %+v", env)
	}
	if env.LLMProvider != "" {
		t.Errorf("LLMProvider=%q, want empty", env.LLMProvider)
	}
}

func TestDetect_Timeout(t *testing.T) {
	// A server that blocks until the request is cancelled, so the per-call
	// timeout is the only thing that can complete the probe.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	setOllamaURL(t, srv.URL)
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")

	// Run Detect in a goroutine with a watchdog so a regression that hangs
	// fails the test fast instead of waiting for the global test timeout.
	done := make(chan *Environment, 1)
	go func() {
		done <- Detect(context.Background(), 1*time.Millisecond)
	}()

	select {
	case env := <-done:
		if env == nil {
			t.Fatal("expected non-nil Environment")
		}
		if env.HasOllama {
			t.Error("expected HasOllama=false after timeout")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Detect hung past the watchdog timeout")
	}
}

func TestDetect_PostgreSQLURL(t *testing.T) {
	tests := []struct {
		name string
		set  func(t *testing.T)
		want string
	}{
		{
			name: "database_url_set",
			set: func(t *testing.T) {
				t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/app")
				t.Setenv("PGHOST", "")
			},
			want: "postgres://u:p@localhost:5432/app",
		},
		{
			name: "pghost_fallback",
			set: func(t *testing.T) {
				t.Setenv("DATABASE_URL", "")
				t.Setenv("PGHOST", "db.local")
				t.Setenv("PGPORT", "5432")
				t.Setenv("PGUSER", "app")
				t.Setenv("PGDATABASE", "appdb")
			},
			want: "postgres://app@db.local:5432/appdb",
		},
		{
			name: "neither_set",
			set: func(t *testing.T) {
				t.Setenv("DATABASE_URL", "")
				t.Setenv("PGHOST", "")
			},
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setOllamaURL(t, closedPortURL(t))
			tt.set(t)
			env := Detect(context.Background(), 5*time.Second)
			if env.PostgreSQLURL != tt.want {
				t.Errorf("PostgreSQLURL=%q, want %q", env.PostgreSQLURL, tt.want)
			}
		})
	}
}

func TestDetect_MCPEndpoints(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want []string
	}{
		{"three_endpoints", "a,b,c", []string{"a", "b", "c"}},
		{"with_spaces", "a, b ,c", []string{"a", "b", "c"}},
		{"empty_entries_dropped", "a,,c,", []string{"a", "c"}},
		{"unset", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setOllamaURL(t, closedPortURL(t))
			t.Setenv("MCP_ENDPOINTS", tt.env)
			got := Detect(context.Background(), 5*time.Second).MCPEndpoints
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("MCPEndpoints=%v, want %v", got, tt.want)
			}
		})
	}
}
