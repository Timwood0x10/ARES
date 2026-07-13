// Simple embedding bridge that forwards requests to Ollama's embedding API.
// Acts as a drop-in replacement for the Python embedding service.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

func main() {
	client := &http.Client{Timeout: 30 * time.Second}

	http.HandleFunc("/embed", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Text   string `json:"text"`
			Prefix string `json:"prefix"`
		}
		_ = json.Unmarshal(body, &req)

		payload := map[string]any{
			"model": "qwen3-embedding:0.6b",
			"input": req.Prefix + req.Text,
		}
		data, _ := json.Marshal(payload)
		reqCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		httpReq, _ := http.NewRequestWithContext(reqCtx, "POST",
			"http://localhost:11434/api/embed", bytes.NewReader(data))
		httpReq.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(httpReq)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		respBody, _ := io.ReadAll(resp.Body)

		var ollamaResp struct {
			Embeddings [][]float64 `json:"embeddings"`
		}
		_ = json.Unmarshal(respBody, &ollamaResp)
		if len(ollamaResp.Embeddings) == 0 {
			http.Error(w, "no embeddings", 500)
			return
		}

		result := map[string]any{"embedding": ollamaResp.Embeddings[0]}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"status":"healthy","model":"qwen3-embedding:0.6b"}`)
	})

	srv := &http.Server{
		Addr:              ":8000",
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	fmt.Println("Embedding bridge on :8000 → Ollama :11434")
	_ = srv.ListenAndServe()
}
