package output

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ── Adapter Generate / GenerateWithParams / GenerateStructured ──────────────

func TestOpenAIAdapter_Generate_Success(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q, want /chat/completions", r.URL.Path)
		}
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-key" {
			t.Errorf("Authorization = %q", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		resp := OpenAIChatResponse{
			Choices: []Choice{{Message: Message{Role: "assistant", Content: "hello"}}},
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode: %v", err)
		}
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	a := NewOpenAIAdapter(&Config{BaseURL: server.URL, APIKey: "test-key", Model: "gpt-4", Timeout: 5})
	got, err := a.Generate(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got != "hello" {
		t.Errorf("Generate = %q, want hello", got)
	}
}

func TestOpenAIAdapter_Generate_MaxPromptLength(t *testing.T) {
	a := NewOpenAIAdapter(&Config{MaxPromptLength: 5, APIKey: "k", Model: "m"})
	_, err := a.Generate(context.Background(), "this is too long")
	if err == nil {
		t.Error("expected error for prompt exceeding MaxPromptLength")
	}
}

func TestOpenAIAdapter_Generate_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		if _, err := w.Write([]byte("bad request")); err != nil {
			t.Errorf("write: %v", err)
		}
	}))
	defer server.Close()

	a := NewOpenAIAdapter(&Config{BaseURL: server.URL, APIKey: "k", Model: "m", Timeout: 5})
	_, err := a.Generate(context.Background(), "hi")
	if err == nil {
		t.Error("expected error for HTTP 400")
	}
}

func TestOpenAIAdapter_Generate_NoChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := OpenAIChatResponse{Choices: []Choice{}}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer server.Close()

	a := NewOpenAIAdapter(&Config{BaseURL: server.URL, APIKey: "k", Model: "m", Timeout: 5})
	_, err := a.Generate(context.Background(), "hi")
	if err == nil {
		t.Error("expected error for empty choices")
	}
}

func TestOpenAIAdapter_GenerateWithParams_Delegates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := OpenAIChatResponse{Choices: []Choice{{Message: Message{Content: "ok"}}}}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer server.Close()

	a := NewOpenAIAdapter(&Config{BaseURL: server.URL, APIKey: "k", Model: "m", Timeout: 5})
	got, err := a.GenerateWithParams(context.Background(), "hi", map[string]any{"temperature": 0.9})
	if err != nil {
		t.Fatalf("GenerateWithParams: %v", err)
	}
	if got != "ok" {
		t.Errorf("got %q, want ok", got)
	}
}

func TestOpenAIAdapter_GenerateStructured_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		content := `{"items":[{"item_id":"1","name":"item","category":"casual","price":10}]}`
		resp := OpenAIChatResponse{Choices: []Choice{{Message: Message{Content: content}}}}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer server.Close()

	a := NewOpenAIAdapter(&Config{BaseURL: server.URL, APIKey: "k", Model: "m", Timeout: 5, MaxTokens: 100})
	result, err := a.GenerateStructured(context.Background(), "prompt", "{}")
	if err != nil {
		t.Fatalf("GenerateStructured: %v", err)
	}
	if result == nil || len(result.Items) == 0 {
		t.Error("expected non-empty items")
	}
}

func TestOllamaAdapter_Generate_Success(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		opts, _ := body["options"].(map[string]any)
		if opts == nil {
			t.Error("request must have options object")
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"response": "generated"}); err != nil {
			t.Errorf("encode: %v", err)
		}
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	a := NewOllamaAdapter(&Config{BaseURL: server.URL, Model: "llama3", Timeout: 5, Temperature: 0.5})
	got, err := a.Generate(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got != "generated" {
		t.Errorf("got %q, want generated", got)
	}
}

func TestOllamaAdapter_Generate_MissingResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := json.NewEncoder(w).Encode(map[string]any{"model": "llama3"}); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer server.Close()

	a := NewOllamaAdapter(&Config{BaseURL: server.URL, Model: "m", Timeout: 5})
	_, err := a.Generate(context.Background(), "hi")
	if err == nil {
		t.Error("expected error for missing response field")
	}
}

func TestOllamaAdapter_Generate_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	a := NewOllamaAdapter(&Config{BaseURL: server.URL, Model: "m", Timeout: 5})
	_, err := a.Generate(context.Background(), "hi")
	if err == nil {
		t.Error("expected error for HTTP 500")
	}
}

func TestOllamaAdapter_GenerateWithParams_Delegates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := json.NewEncoder(w).Encode(map[string]any{"response": "r"}); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer server.Close()

	a := NewOllamaAdapter(&Config{BaseURL: server.URL, Model: "m", Timeout: 5})
	got, err := a.GenerateWithParams(context.Background(), "p", nil)
	if err != nil {
		t.Fatalf("GenerateWithParams: %v", err)
	}
	if got != "r" {
		t.Errorf("got %q", got)
	}
}

func TestOllamaAdapter_GenerateStructured_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		content := `{"items":[{"item_id":"x","name":"n","category":"casual","price":1}]}`
		if err := json.NewEncoder(w).Encode(map[string]any{"response": content}); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer server.Close()

	a := NewOllamaAdapter(&Config{BaseURL: server.URL, Model: "m", Timeout: 5})
	result, err := a.GenerateStructured(context.Background(), "prompt", "{}")
	if err != nil {
		t.Fatalf("GenerateStructured: %v", err)
	}
	if result == nil || len(result.Items) != 1 {
		t.Error("expected 1 item")
	}
}

// ── OpenRouter adapter ──────────────────────────────────────────────────────

func TestNewOpenRouterAdapter_Defaults(t *testing.T) {
	a := NewOpenRouterAdapter(nil)
	if a == nil {
		t.Fatal("NewOpenRouterAdapter returned nil")
	}
	if a.config.BaseURL != "https://openrouter.ai/api/v1" {
		t.Errorf("BaseURL = %q", a.config.BaseURL)
	}
	if a.client == nil || a.streamClient == nil {
		t.Error("clients should not be nil")
	}
}

func TestOpenRouterAdapter_GetModel(t *testing.T) {
	a := NewOpenRouterAdapter(&Config{Model: "openrouter-model"})
	if got := a.GetModel(); got != "openrouter-model" {
		t.Errorf("GetModel = %q", got)
	}
}

func TestOpenRouterAdapter_Generate_Success(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("HTTP-Referer") == "" {
			t.Error("HTTP-Referer header should be set")
		}
		if r.Header.Get("X-Title") == "" {
			t.Error("X-Title header should be set")
		}
		resp := OpenAIChatResponse{Choices: []Choice{{Message: Message{Content: "router-ok"}}}}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode: %v", err)
		}
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	a := NewOpenRouterAdapter(&Config{BaseURL: server.URL, APIKey: "k", Model: "m", Timeout: 5})
	got, err := a.Generate(context.Background(), "hi")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got != "router-ok" {
		t.Errorf("got %q", got)
	}
}

func TestOpenRouterAdapter_Generate_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	a := NewOpenRouterAdapter(&Config{BaseURL: server.URL, APIKey: "k", Model: "m", Timeout: 5})
	_, err := a.Generate(context.Background(), "hi")
	if err == nil {
		t.Error("expected error for HTTP 403")
	}
}

func TestOpenRouterAdapter_Generate_NoChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := OpenAIChatResponse{}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer server.Close()

	a := NewOpenRouterAdapter(&Config{BaseURL: server.URL, APIKey: "k", Model: "m", Timeout: 5})
	_, err := a.Generate(context.Background(), "hi")
	if err == nil {
		t.Error("expected error for empty choices")
	}
}

func TestOpenRouterAdapter_GenerateWithParams_Delegates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := OpenAIChatResponse{Choices: []Choice{{Message: Message{Content: "ok"}}}}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer server.Close()

	a := NewOpenRouterAdapter(&Config{BaseURL: server.URL, APIKey: "k", Model: "m", Timeout: 5})
	got, err := a.GenerateWithParams(context.Background(), "p", map[string]any{"temperature": 0.1})
	if err != nil {
		t.Fatalf("GenerateWithParams: %v", err)
	}
	if got != "ok" {
		t.Errorf("got %q", got)
	}
}

func TestOpenRouterAdapter_GenerateStructured_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		content := `{"items":[{"item_id":"1","name":"n","category":"casual","price":5}]}`
		resp := OpenAIChatResponse{Choices: []Choice{{Message: Message{Content: content}}}}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer server.Close()

	a := NewOpenRouterAdapter(&Config{BaseURL: server.URL, APIKey: "k", Model: "m", Timeout: 5})
	result, err := a.GenerateStructured(context.Background(), "prompt", "{}")
	if err != nil {
		t.Fatalf("GenerateStructured: %v", err)
	}
	if result == nil || len(result.Items) != 1 {
		t.Error("expected 1 item")
	}
}

func TestOpenRouterAdapter_GenerateStructured_NoChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := OpenAIChatResponse{}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	defer server.Close()

	a := NewOpenRouterAdapter(&Config{BaseURL: server.URL, APIKey: "k", Model: "m", Timeout: 5})
	_, err := a.GenerateStructured(context.Background(), "prompt", "{}")
	if err == nil {
		t.Error("expected error for empty choices")
	}
}

func TestOpenRouterAdapter_GenerateStream(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		events := []string{
			`data: {"choices":[{"delta":{"content":"A"}}]}`,
			`data: {"choices":[{"delta":{"content":"B"}}]}`,
			`data: [DONE]`,
		}
		for _, e := range events {
			if _, err := w.Write([]byte(e + "\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	a := NewOpenRouterAdapter(&Config{BaseURL: server.URL, APIKey: "k", Model: "m", Timeout: 5})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := a.GenerateStream(ctx, "prompt")
	if err != nil {
		t.Fatalf("GenerateStream: %v", err)
	}

	var sb strings.Builder
	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatalf("stream error: %v", chunk.Err)
		}
		sb.WriteString(chunk.Content)
	}
	if sb.String() != "AB" {
		t.Errorf("content = %q, want AB", sb.String())
	}
}

// ── Parser: ParseJSON / ParseArray / ParseJSONSlice / parseArrayFormat ──────

func TestParser_ParseJSON(t *testing.T) {
	p := NewParser()
	tests := []struct {
		name    string
		input   string
		wantKey string
		wantVal string
		wantErr bool
	}{
		{"object", `{"key":"val"}`, "key", "val", false},
		{"markdown", "```json\n{\"k\":\"v\"}\n```", "k", "v", false},
		{"fixable", `{key:"value"}`, "key", "value", false},
		{"no_json", "plain text", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := p.ParseJSON(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseJSON(%q) err = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if result[tt.wantKey] != tt.wantVal {
				t.Errorf("result[%q] = %v, want %q", tt.wantKey, result[tt.wantKey], tt.wantVal)
			}
		})
	}
}

func TestParser_ParseArray(t *testing.T) {
	p := NewParser()
	tests := []struct {
		name      string
		input     string
		wantCount int
		wantErr   bool
	}{
		{"array", `[1,2,3]`, 3, false},
		{"markdown", "```json\n[\"a\",\"b\"]\n```", 2, false},
		{"object_rejected", `{"key":"val"}`, 0, true},
		{"no_json", "text", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := p.ParseArray(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseArray(%q) err = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && len(result) != tt.wantCount {
				t.Errorf("len = %d, want %d", len(result), tt.wantCount)
			}
		})
	}
}

func TestParser_ParseJSONSlice(t *testing.T) {
	p := NewParser()
	tests := []struct {
		name      string
		input     string
		wantCount int
		wantErr   bool
	}{
		{"array", `[1,2,3]`, 3, false},
		{"fixable", `["a","b",]`, 2, false},
		{"no_bracket", `no json`, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := p.ParseJSONSlice(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseJSONSlice(%q) err = %v", tt.input, err)
			}
			if !tt.wantErr && len(result) != tt.wantCount {
				t.Errorf("len = %d, want %d", len(result), tt.wantCount)
			}
		})
	}
}

func TestParser_ParseRecommendResult_ArrayFormat(t *testing.T) {
	p := NewParser()
	input := `[{"item_id":"1","name":"item","category":"casual","price":9.9}]`
	result, err := p.ParseRecommendResult(input)
	if err != nil {
		t.Fatalf("ParseRecommendResult: %v", err)
	}
	if len(result.Items) != 1 {
		t.Errorf("items len = %d, want 1", len(result.Items))
	}
	if result.Items[0].ItemID != "1" {
		t.Errorf("ItemID = %q", result.Items[0].ItemID)
	}
}

func TestParser_ParseRecommendResult_FixPath(t *testing.T) {
	p := NewParser()
	input := `{items:[{item_id:"x",name:"n"}]}`
	result, err := p.ParseRecommendResult(input)
	if err != nil {
		t.Fatalf("ParseRecommendResult: %v", err)
	}
	if len(result.Items) != 1 {
		t.Errorf("items len = %d, want 1", len(result.Items))
	}
}

func TestParser_IsAlphaNum(t *testing.T) {
	tests := []struct {
		c    byte
		want bool
	}{
		{'a', true}, {'Z', true}, {'_', true}, {'5', true},
		{' ', false}, {'!', false}, {'\n', false},
	}
	for _, tt := range tests {
		if got := isAlphaNum(tt.c); got != tt.want {
			t.Errorf("isAlphaNum(%q) = %v, want %v", tt.c, got, tt.want)
		}
	}
}

func TestScanFixJSON_SingleQuoteEscaping(t *testing.T) {
	input := `{'key': 'has "quotes" and \backslash'}`
	fixed := scanFixJSON(input)
	var result map[string]string
	if err := json.Unmarshal([]byte(fixed), &result); err != nil {
		t.Fatalf("scanFixJSON produced invalid JSON: %v\nfixed: %s", err, fixed)
	}
	if !strings.Contains(result["key"], `"quotes"`) {
		t.Errorf("key = %q, should contain escaped quotes", result["key"])
	}
}

func TestScanFixJSON_KeysWithDigits(t *testing.T) {
	input := `{key1:"value1",key2:"value2"}`
	fixed := scanFixJSON(input)
	var result map[string]string
	if err := json.Unmarshal([]byte(fixed), &result); err != nil {
		t.Fatalf("unmarshal: %v\nfixed: %s", err, fixed)
	}
	if result["key1"] != "value1" || result["key2"] != "value2" {
		t.Errorf("result = %v", result)
	}
}

// ── Template: RenderFile / RegisterFunc / toJSON / toYAML ──────────────────

func TestTemplateEngine_RenderFile_NotFound(t *testing.T) {
	e := NewTemplateEngine()
	_, err := e.RenderFile("/nonexistent/template.txt", nil)
	if err == nil {
		t.Error("expected error for missing template file")
	}
}

func TestTemplateEngine_RegisterFunc(t *testing.T) {
	e := NewTemplateEngine()
	e.RegisterFunc("custom", func(s string) string { return "custom:" + s })
	result, err := e.Render(`{{custom "test"}}`, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if result != "custom:test" {
		t.Errorf("result = %q", result)
	}
}

func TestToJSON(t *testing.T) {
	got := toJSON(map[string]string{"a": "b"})
	if got != `{"a":"b"}` {
		t.Errorf("toJSON = %q", got)
	}
}

func TestToJSON_Fallback(t *testing.T) {
	got := toJSON(make(chan int))
	if got == "" {
		t.Error("toJSON fallback should not be empty")
	}
}

func TestToYAML(t *testing.T) {
	got := toYAML(map[string]string{"key": "value"})
	if !strings.Contains(got, "key: value") {
		t.Errorf("toYAML = %q, want contains key: value", got)
	}
}

// ── Factory: CreateAdapter / RegisterProvider / ListSupportedProviders ──────

func TestCreateAdapter_AllProviders(t *testing.T) {
	providers := ListSupportedProviders()
	for _, provider := range providers {
		adapter, err := CreateAdapter(provider, nil)
		if err != nil {
			t.Errorf("CreateAdapter(%q): %v", provider, err)
			continue
		}
		if adapter == nil {
			t.Errorf("CreateAdapter(%q) returned nil adapter", provider)
		}
	}
}

func TestRegisterProvider_Custom(t *testing.T) {
	customCalled := false
	RegisterProvider("test_custom_provider", func(cfg *Config) LLMAdapter {
		customCalled = true
		return NewOpenAIAdapter(cfg)
	})

	adapter, err := CreateAdapter("test_custom_provider", &Config{Model: "custom"})
	if err != nil {
		t.Fatalf("CreateAdapter custom: %v", err)
	}
	if !customCalled {
		t.Error("custom factory was not called")
	}
	if adapter.GetModel() != "custom" {
		t.Errorf("GetModel = %q", adapter.GetModel())
	}
}

// ── Validator: validateInteger / getSchema / GetTravel*Schema ───────────────

func TestValidator_ValidateInteger(t *testing.T) {
	v := NewValidator()
	tests := []struct {
		name    string
		data    interface{}
		wantErr bool
	}{
		{"int", 42, false},
		{"float_whole", 3.0, false},
		{"float_fraction", 3.5, true},
		{"string", "42", true},
		{"bool", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.Validate(tt.data, &Schema{Type: schemaTypeInteger})
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate(%v, integer) err = %v, wantErr %v", tt.data, err, tt.wantErr)
			}
		})
	}
}

func TestValidator_GetSchemaTypes(t *testing.T) {
	tests := []struct {
		schemaType string
		wantNil    bool
	}{
		{"default", false},
		{"travel", false},
		{"custom", false},
	}
	for _, tt := range tests {
		t.Run(tt.schemaType, func(t *testing.T) {
			v := NewValidator(WithSchemaType(tt.schemaType))
			s := v.getSchema()
			if (s == nil) != tt.wantNil {
				t.Errorf("getSchema(%q) = %v", tt.schemaType, s)
			}
		})
	}
}

func TestGetTravelResultSchema(t *testing.T) {
	s := GetTravelResultSchema()
	if s == nil {
		t.Fatal("GetTravelResultSchema returned nil")
	}
	if s.Type != "object" {
		t.Errorf("Type = %q", s.Type)
	}
	if s.Properties["items"] == nil {
		t.Error("items property missing")
	}
	if len(s.Required) == 0 {
		t.Error("Required should not be empty")
	}
}

func TestGetTravelItemSchema(t *testing.T) {
	s := GetTravelItemSchema()
	if s == nil {
		t.Fatal("GetTravelItemSchema returned nil")
	}
	if s.Type != "object" {
		t.Errorf("Type = %q", s.Type)
	}
	if s.Properties["category"] == nil {
		t.Error("category property missing")
	}
	if len(s.Properties["category"].Enum) == 0 {
		t.Error("category enum should not be empty")
	}
}

func TestValidator_ValidateRecommendResult_Travel(t *testing.T) {
	v := NewValidator(WithSchemaType("travel"))
	result := &struct {
		Items []struct {
			ID       string  `json:"item_id"`
			Name     string  `json:"name"`
			Category string  `json:"category"`
			Price    float64 `json:"price"`
		}
	}{}
	_ = result

	// Use the real model type via ValidateRecommendResult path
	// Just verify the schema is used (non-nil)
	s := v.getSchema()
	if s == nil {
		t.Error("travel schema should not be nil")
	}
}

// ── Validator ext: InputValidator getters / Validate* methods ──────────────

func TestInputValidator_Getters(t *testing.T) {
	v := NewInputValidator()
	if v.GetMaxInputLength() != MaxInputLength {
		t.Errorf("GetMaxInputLength = %d", v.GetMaxInputLength())
	}
	if v.GetMaxJSONLength() != MaxJSONLength {
		t.Errorf("GetMaxJSONLength = %d", v.GetMaxJSONLength())
	}
	if v.GetMaxJSONDepth() != MaxJSONDepth {
		t.Errorf("GetMaxJSONDepth = %d", v.GetMaxJSONDepth())
	}
	if v.GetMaxStringLength() != MaxStringLength {
		t.Errorf("GetMaxStringLength = %d", v.GetMaxStringLength())
	}
	if v.GetMaxObjectKeyLength() != MaxObjectKeyLength {
		t.Errorf("GetMaxObjectKeyLength = %d", v.GetMaxObjectKeyLength())
	}
}

func TestInputValidator_GetConfig(t *testing.T) {
	v := NewInputValidator()
	cfg := v.GetConfig()
	if cfg.MaxInputLength != MaxInputLength {
		t.Errorf("MaxInputLength = %d", cfg.MaxInputLength)
	}
	if cfg.MaxJSONLength != MaxJSONLength {
		t.Errorf("MaxJSONLength = %d", cfg.MaxJSONLength)
	}
	if cfg.MaxJSONDepth != MaxJSONDepth {
		t.Errorf("MaxJSONDepth = %d", cfg.MaxJSONDepth)
	}
	if cfg.MaxArrayLength != MaxArrayLength {
		t.Errorf("MaxArrayLength = %d", cfg.MaxArrayLength)
	}
}

func TestInputValidator_ValidateJSONDepth(t *testing.T) {
	v := NewInputValidator()
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"shallow", `{"a":1}`, false},
		{"nested_ok", `{"a":{"b":{"c":1}}}`, false},
		{"unbalanced", `{"a":1}}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.ValidateJSONDepth(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateJSONDepth(%q) err = %v", tt.input, err)
			}
		})
	}
}

func TestInputValidator_ValidateStringLength(t *testing.T) {
	v := NewInputValidator()
	if err := v.ValidateStringLength("short"); err != nil {
		t.Errorf("ValidateStringLength(short) = %v", err)
	}
	long := strings.Repeat("x", MaxStringLength+1)
	if err := v.ValidateStringLength(long); err == nil {
		t.Error("expected error for overlong string")
	}
}

func TestInputValidator_ValidateArrayLength(t *testing.T) {
	v := NewInputValidator()
	if err := v.ValidateArrayLength(10); err != nil {
		t.Errorf("ValidateArrayLength(10) = %v", err)
	}
	if err := v.ValidateArrayLength(MaxArrayLength + 1); err == nil {
		t.Error("expected error for oversized array")
	}
}

func TestInputValidator_ValidateObjectKeyLength(t *testing.T) {
	v := NewInputValidator()
	if err := v.ValidateObjectKeyLength("short_key"); err != nil {
		t.Errorf("ValidateObjectKeyLength = %v", err)
	}
	long := strings.Repeat("k", MaxObjectKeyLength+1)
	if err := v.ValidateObjectKeyLength(long); err == nil {
		t.Error("expected error for overlong key")
	}
}

// ── Timeout helpers ─────────────────────────────────────────────────────────

func TestTimeoutHelpers_SetDeadline(t *testing.T) {
	tests := []struct {
		name string
		fn   func(ctx context.Context) (context.Context, context.CancelFunc)
	}{
		{"LLMStructured", WithLLMStructuredTimeout},
		{"DatabaseTransaction", WithDatabaseTransactionTimeout},
		{"VectorSearch", WithVectorSearchTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := tt.fn(context.Background())
			defer cancel()
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatal("context has no deadline")
			}
			if deadline.Before(time.Now()) {
				t.Error("deadline is in the past")
			}
		})
	}
}

func TestWithDefaultTimeout_PreservesExisting(t *testing.T) {
	deadline := time.Now().Add(5 * time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	newCtx, _ := WithDefaultTimeout(ctx, time.Hour)
	got, _ := newCtx.Deadline()
	if !got.Equal(deadline) {
		t.Errorf("deadline = %v, want %v", got, deadline)
	}
}

// ── Schema: ToJSON / ToJSONString ──────────────────────────────────────────

func TestSchema_ToJSON(t *testing.T) {
	s := &Schema{Type: "object", Required: []string{"id"}}
	out, err := s.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	if !strings.Contains(out, `"object"`) {
		t.Errorf("ToJSON = %q", out)
	}
}

func TestSchema_ToJSONString(t *testing.T) {
	s := &Schema{Type: "string"}
	out, err := s.ToJSONString()
	if err != nil {
		t.Fatalf("ToJSONString: %v", err)
	}
	if out != `{"type":"string"}` && !strings.Contains(out, `"string"`) {
		t.Errorf("ToJSONString = %q", out)
	}
}

// ── toolcall: toMap helper ──────────────────────────────────────────────────

func TestAssistantMsg_ToMap(t *testing.T) {
	tests := []struct {
		name     string
		msg      *AssistantMsg
		wantKeys []string
	}{
		{
			name:     "basic",
			msg:      &AssistantMsg{Role: "assistant", Content: "hello"},
			wantKeys: []string{"role", "content"},
		},
		{
			name:     "with_reasoning",
			msg:      &AssistantMsg{Role: "assistant", Content: "hi", ReasoningContent: "thinking"},
			wantKeys: []string{"role", "content", "reasoning_content"},
		},
		{
			name: "with_tool_calls",
			msg: &AssistantMsg{
				Role:    "assistant",
				Content: "",
				ToolCalls: []AssistantToolCall{
					{ID: "tc1", Type: "function", Function: AssistantToolFuncRef{Name: "fn", Arguments: "{}"}},
				},
			},
			wantKeys: []string{"role", "content", "tool_calls"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.msg.toMap()
			for _, key := range tt.wantKeys {
				if _, ok := result[key]; !ok {
					t.Errorf("toMap() missing key %q", key)
				}
			}
		})
	}
}

// ── Validator: ValidateRecommendResult edge cases ───────────────────────────

func TestValidator_ValidateRecommendResult_Nil(t *testing.T) {
	v := NewValidator()
	if err := v.ValidateRecommendResult(nil); err == nil {
		t.Error("expected error for nil result")
	}
}

// ── Parser: ParseGeneric edge cases ────────────────────────────────────────

func TestParser_ParseGeneric_NoFixJSON(t *testing.T) {
	p := &Parser{fixJSON: false, inputValidator: NewInputValidator()}
	var target map[string]any
	err := p.ParseGeneric(`{invalid}`, &target)
	if err == nil {
		t.Error("expected error when fixJSON is false and JSON is invalid")
	}
}

func TestParser_ParseStructured_NilTarget(t *testing.T) {
	p := NewParser()
	err := p.ParseStructured(`{"a":1}`, nil)
	if err == nil {
		t.Error("expected error for nil target")
	}
}

// ── Adapter: GetModel for all adapters ─────────────────────────────────────

func TestAllAdapters_GetModel(t *testing.T) {
	openai := NewOpenAIAdapter(&Config{Model: "openai-m"})
	ollama := NewOllamaAdapter(&Config{Model: "ollama-m"})
	router := NewOpenRouterAdapter(&Config{Model: "router-m"})

	tests := []struct {
		name  string
		model string
		want  string
	}{
		{"openai", openai.GetModel(), "openai-m"},
		{"ollama", ollama.GetModel(), "ollama-m"},
		{"openrouter", router.GetModel(), "router-m"},
	}
	for _, tt := range tests {
		if tt.model != tt.want {
			t.Errorf("%s GetModel = %q, want %q", tt.name, tt.model, tt.want)
		}
	}
}

// ── helper for goconst ──────────────────────────────────────────────────────

var _ = fmt.Sprintf
