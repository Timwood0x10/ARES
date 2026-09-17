package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	llmcore "github.com/Timwood0x10/ares/internal/llmcore"
)

// TestExtraHeadersSent locks the llm.extra contract (independent-review
// F-16): Extra entries ride as custom HTTP headers on outgoing provider
// requests. It was previously a dead config — ares_config passed it into
// Config.Extra but no request path read it, so proxy/gateway headers
// (HTTP-Referer, X-Title, custom auth) silently vanished.
func TestExtraHeadersSent(t *testing.T) {
	const (
		extraKey   = "X-Gateway-Token"
		extraValue = "secret-token-123"
	)
	var gotHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get(extraKey)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer server.Close()

	client, err := NewClient(&Config{
		Provider: "openai",
		APIKey:   "test-key",
		BaseURL:  server.URL,
		Model:    "gpt-4",
		Extra:    map[string]string{extraKey: extraValue},
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	t.Run("chat_path_sends_extra_headers", func(t *testing.T) {
		gotHeader = ""
		_, err := client.Chat(context.Background(),
			[]*llmcore.LLMMessage{{Role: "user", Content: "hi"}}, nil, nil)
		if err != nil {
			t.Fatalf("Chat() error = %v", err)
		}
		if gotHeader != extraValue {
			t.Errorf("Chat() extra header %s = %q, want %q", extraKey, gotHeader, extraValue)
		}
	})

	t.Run("generate_path_sends_extra_headers", func(t *testing.T) {
		gotHeader = ""
		if _, err := client.Generate(context.Background(), "hi"); err != nil {
			t.Fatalf("Generate() error = %v", err)
		}
		if gotHeader != extraValue {
			t.Errorf("Generate() extra header %s = %q, want %q", extraKey, gotHeader, extraValue)
		}
	})
}

// TestExtraOverridesReservedHeader documents that Extra wins over the
// built-in auth header: a proxy in front of the provider may demand its own
// Authorization scheme, which is exactly the "custom provider credentials
// ride here" use case the config comment promises.
func TestExtraOverridesReservedHeader(t *testing.T) {
	const wantAuth = "Gateway abc-123"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != wantAuth {
			t.Errorf("Authorization = %q, want %q (Extra must override built-in auth)", got, wantAuth)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`)
	}))
	defer server.Close()

	client, err := NewClient(&Config{
		Provider: "openai",
		APIKey:   "test-key",
		BaseURL:  server.URL,
		Model:    "gpt-4",
		Extra:    map[string]string{"Authorization": wantAuth},
	})
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := client.Generate(context.Background(), "hi"); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
}
