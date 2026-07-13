// Simple embedding bridge that forwards requests to Ollama's embedding API.
// Acts as a drop-in replacement for the Python embedding service.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

func main() {
	http.HandleFunc("/embed", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Text   string `json:"text"`
			Prefix string `json:"prefix"`
		}
		json.Unmarshal(body, &req)

		payload := map[string]any{
			"model": "qwen3-embedding:0.6b",
			"input": req.Prefix + req.Text,
		}
		data, _ := json.Marshal(payload)
		resp, err := http.Post("http://localhost:11434/api/embed", "application/json", bytes.NewReader(data))
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)

		var ollamaResp struct {
			Embeddings [][]float64 `json:"embeddings"`
		}
		json.Unmarshal(respBody, &ollamaResp)
		if len(ollamaResp.Embeddings) == 0 {
			http.Error(w, "no embeddings", 500)
			return
		}

		result := map[string]any{"embedding": ollamaResp.Embeddings[0]}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"status":"healthy","model":"qwen3-embedding:0.6b"}`)
	})

	fmt.Println("Embedding bridge on :8000 → Ollama :11434")
	http.ListenAndServe(":8000", nil)
}
