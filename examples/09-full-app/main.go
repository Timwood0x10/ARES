// Full app — demonstrates a complete ARES application with web UI.
//
// Features:
//   - HTTP server with chat API
//   - SDK agent with tools and memory
//   - Real-time tool call tracking
//   - Simple HTML dashboard
//
// Run:
//
//	go run examples/full-app/main.go
//	then open http://localhost:8080
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/Timwood0x10/ares/api/tools"
	"github.com/Timwood0x10/ares/sdk"
)

func main() {
	rt := sdk.MustNew(
		sdk.WithOllama("llama3.2"),
		sdk.WithDefaultMemory(),
	)
	defer rt.Close()

	// Register tools.
	for _, t := range appTools {
		if err := rt.ToolRegistry().Register(t); err != nil {
			log.Printf("register: %v", err)
			return
		}
	}

	agent := rt.NewAgent("assistant",
		sdk.WithInstruction("You are a helpful assistant with tools. Use calculator for math, weather for forecasts."),
	)

	app := &appState{
		agent:   agent,
		history: make([]chatEntry, 0),
	}

	// HTTP routes.
	http.HandleFunc("/", app.handleIndex)
	http.HandleFunc("/api/chat", app.handleChat)
	http.HandleFunc("/api/stats", app.handleStats)

	addr := ":8080"
	fmt.Printf("🌐 Open http://localhost%s\n", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Printf("server: %v", err)
	}
}

// ---- app state ----

type chatEntry struct {
	Time    time.Time `json:"time"`
	Input   string    `json:"input"`
	Output  string    `json:"output"`
	Tools   int       `json:"tools"`
	Tokens  int       `json:"tokens"`
	Latency string    `json:"latency"`
}

type appState struct {
	mu      sync.Mutex
	agent   *sdk.Agent
	history []chatEntry
}

// ---- HTTP handlers ----

func (app *appState) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = fmt.Fprint(w, indexHTML)
}

func (app *appState) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	input := r.FormValue("input")
	if input == "" {
		http.Error(w, "input required", 400)
		return
	}

	result, err := app.agent.Run(r.Context(), input)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	entry := chatEntry{
		Time:    time.Now(),
		Input:   input,
		Output:  result.Output,
		Tools:   result.ToolCalls,
		Tokens:  result.TokenUsage.Total,
		Latency: result.Duration.Round(time.Millisecond).String(),
	}

	app.mu.Lock()
	app.history = append(app.history, entry)
	app.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(entry)
}

func (app *appState) handleStats(w http.ResponseWriter, r *http.Request) {
	app.mu.Lock()
	defer app.mu.Unlock()

	totalTokens := 0
	totalTools := 0
	for _, e := range app.history {
		totalTokens += e.Tokens
		totalTools += e.Tools
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"total_queries":    len(app.history),
		"total_tokens":     totalTokens,
		"total_tool_calls": totalTools,
	})
}

// ---- tools ----

var appTools = []tools.Tool{
	tools.ToolFunc{
		ToolName: "calculator",
		ToolDesc: "Evaluate a mathematical expression",
		Fn: func(_ context.Context, params map[string]any) (any, error) {
			expr, _ := params["expression"].(string)
			return fmt.Sprintf("result: %s = (demo) 42", expr), nil
		},
	},
	tools.ToolFunc{
		ToolName: "get_weather",
		ToolDesc: "Get current weather for a city",
		Fn: func(_ context.Context, params map[string]any) (any, error) {
			city, _ := params["city"].(string)
			return fmt.Sprintf(`{"city":%q,"temp":22,"condition":"sunny"}`, city), nil
		},
	},
}

// ---- HTML dashboard ----

const indexHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<title>ARES Full App</title>
<style>
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body { font: 14px/1.6 -apple-system, sans-serif; max-width: 800px; margin: 0 auto; padding: 20px; }
  h1 { margin-bottom: 8px; }
  .stats { display: flex; gap: 16px; margin: 16px 0; }
  .stat { background: #f0f4f8; border-radius: 8px; padding: 12px 20px; flex: 1; }
  .stat .val { font-size: 24px; font-weight: 700; }
  .stat .lbl { font-size: 12px; color: #666; }
  form { display: flex; gap: 8px; margin: 16px 0; }
  input[type=text] { flex: 1; padding: 10px; border: 1px solid #ccc; border-radius: 6px; font-size: 14px; }
  button { padding: 10px 20px; background: #0066ff; color: #fff; border: none; border-radius: 6px; cursor: pointer; }
  button:hover { background: #0052cc; }
  .entry { border: 1px solid #e0e0e0; border-radius: 8px; padding: 12px; margin: 8px 0; }
  .entry .q { font-weight: 600; margin-bottom: 4px; }
  .entry .a { color: #333; }
  .entry .meta { font-size: 12px; color: #888; margin-top: 4px; }
  #loading { display: none; color: #666; margin: 8px 0; }
</style>
</head>
<body>
<h1>🤖 ARES Demo</h1>
<p>Full-stack agent with tools, memory, and a web UI.</p>

<div class="stats" id="stats">
  <div class="stat"><div class="val" id="q">0</div><div class="lbl">Queries</div></div>
  <div class="stat"><div class="val" id="t">0</div><div class="lbl">Tokens</div></div>
  <div class="stat"><div class="val" id="tc">0</div><div class="lbl">Tool Calls</div></div>
</div>

<form id="form" onsubmit="return send()">
  <input type="text" id="input" placeholder="Ask something..." autofocus>
  <button type="submit">Send</button>
</form>
<div id="loading">Thinking...</div>
<div id="history"></div>

<script>
async function send() {
  const input = document.getElementById('input');
  const loading = document.getElementById('loading');
  const history = document.getElementById('history');

  loading.style.display = 'block';
  const resp = await fetch('/api/chat', {
    method: 'POST',
    headers: {'Content-Type': 'application/x-www-form-urlencoded'},
    body: 'input='+encodeURIComponent(input.value),
  });
  loading.style.display = 'none';
  if (!resp.ok) { alert(await resp.text()); return false; }
  const data = await resp.json();

  const div = document.createElement('div');
  div.className = 'entry';
  div.innerHTML = '<div class="q">🧑 '+esc(data.input)+'</div>'
    + '<div class="a">🤖 '+esc(data.output)+'</div>'
    + '<div class="meta">tools: '+data.tools+' | tokens: '+data.tokens+' | '+data.latency+'</div>';
  history.prepend(div);
  input.value = '';
  refreshStats();
  return false;
}
async function refreshStats() {
  const r = await fetch('/api/stats');
  const s = await r.json();
  document.getElementById('q').textContent = s.total_queries;
  document.getElementById('t').textContent = s.total_tokens;
  document.getElementById('tc').textContent = s.total_tool_calls;
}
function esc(s) { return s.replace(/&/g,'&amp;').replace(/</g,'&lt;'); }
refreshStats();
</script>
</body>
</html>`
