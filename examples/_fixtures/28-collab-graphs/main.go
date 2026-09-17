// Collaboration graphs — submit an explicit DAG over HTTP and let the kernel
// execute it (fusion plan Phase C4: ares.Graph shapes reachable from ops).
//
// The endpoint reuses the same kernel fabric + scheduler as every other
// submission path. Validation happens BEFORE execution: unknown capability →
// 400 with the available list; cycles → 400; edges capped at 4096.
//
// Prerequisites:
//
//  1. Start a peer runtime in another terminal:
//     ares serve                       (reads ./ares.yaml)
//  2. This example then POSTs two graphs:
//     - pipeline: collect → shape
//     - orchestrate: root fans out to two workers, join validates
//     Node capabilities are tool/* names — the scheduler-visible set since
//     the B3 convergence (see the note in main).
//
// Core APIs used: net/http (the OPERATIONS surface) plus ares.LoadConfigFile
// to read llm.api_key from ares.yaml — the single credential entry point
// (no environment variable).
//
// Run:
//
//	go run examples/_fixtures/28-collab-graphs/main.go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/Timwood0x10/ares/api"
)

const baseURL = "http://localhost:8080"

type node struct {
	ID         string `json:"id"`
	Capability string `json:"capability"`
	Input      any    `json:"input,omitempty"`
}

type edge struct{ From, To string }

type graphReq struct {
	SchemaVersion int    `json:"schema_version"`
	Nodes         []node `json:"nodes"`
	Edges         []edge `json:"edges,omitempty"`
}

func submit(apiKey string, req graphReq) map[string]any {
	b, _ := json.Marshal(req)
	httpReq, _ := http.NewRequest(http.MethodPost, baseURL+"/api/graphs", bytes.NewReader(b))
	httpReq.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ POST /api/graphs: %v\n   → is the peer runtime running? start it with: ares serve\n", err)
		os.Exit(1)
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // best-effort body close
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	fmt.Printf("HTTP %d\n", resp.StatusCode)
	return out
}

func main() {
	// The control-plane credential is llm.api_key from ares.yaml — the same
	// value `ares serve` enforces on destructive endpoints. The config file
	// is the only entry point; no environment variable is read.
	cfg, err := ares.LoadConfigFile("ares.yaml")
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ load ares.yaml: %v\n", err)
		os.Exit(1)
	}
	apiKey := cfg.LLM.APIKey

	// Node capabilities must be SCHEDULER-VISIBLE. Since the B3 convergence
	// every peer is a self-contained L2 executor registered with the shared
	// capability set (ares/root, ares/plan, ares/answer, tool/<name> for each
	// built-in tool) — a bespoke capability like "research" declared in
	// ares.yaml is identity metadata only and the graph validator rejects it
	// with 400 + the available list. These local, IO-free tools keep the DAG
	// demo deterministic while exercising the same pipeline/orchestrate shapes.
	fmt.Println("═══ 1. Pipeline graph (collect → shape) ═══")
	r1 := submit(apiKey, graphReq{
		SchemaVersion: 1,
		Nodes: []node{
			{ID: "s1", Capability: "tool/id_generator", Input: "run"},
			{ID: "s2", Capability: "tool/text_processor", Input: "draft from s1"},
		},
		Edges: []edge{{From: "s1", To: "s2"}},
	})
	printResult(r1)

	fmt.Println("\n═══ 2. Orchestration graph (root → workers → join) ═══")
	r2 := submit(apiKey, graphReq{
		SchemaVersion: 1,
		Nodes: []node{
			{ID: "root", Capability: "tool/id_generator"},
			{ID: "w1", Capability: "tool/text_processor"},
			{ID: "w2", Capability: "tool/string_utils"},
			{ID: "join", Capability: "tool/data_validation"},
		},
		Edges: []edge{
			{From: "root", To: "w1"}, {From: "root", To: "w2"},
			{From: "w1", To: "join"}, {From: "w2", To: "join"},
		},
	})
	printResult(r2)

	// Give the scheduler a moment if nodes are still draining, then show
	// final outputs again for the second graph.
	time.Sleep(5 * time.Second)
	fmt.Println("\n── final outputs (graph 2) ──")
	printResult(map[string]any{"outputs": r2["outputs"]})
}

func printResult(r map[string]any) {
	if err, ok := r["error"].(string); ok && err != "" {
		fmt.Println("  error:", err)
		if caps, ok := r["available_capabilities"].([]any); ok {
			fmt.Println("  available:", caps)
		}
		return
	}
	if ids, ok := r["task_ids"].(map[string]any); ok {
		fmt.Println("  task_ids:", ids)
	}
	if outs, ok := r["outputs"].(map[string]any); ok {
		for id, v := range outs {
			s := fmt.Sprint(v)
			if len(s) > 80 {
				s = s[:80] + "…"
			}
			fmt.Printf("  [%s] %s\n", id, s)
		}
	}
}
