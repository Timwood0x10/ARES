# LLM Module -- Performance & Code Quality Analysis

## 1. Module Overview

The LLM module provides a unified client for multiple LLM providers (OpenAI,
OpenRouter, Ollama, Anthropic) with support for both blocking and streaming
generation, rate limiting, callbacks, tracing, and automatic failover across
provider instances.

### Key Files

| File | Purpose |
|------|---------|
| `internal/llm/client.go` | Core `Client` with `Generate`, `GenerateStream`, provider dispatch |
| `internal/llm/scorer.go` | `FailoverScorer` -- chains multiple clients with automatic failover |
| `internal/llm/output/openai.go` | `OpenAIAdapter` -- OpenAI-compatible API (blocking + streaming + tool calls) |
| `internal/llm/output/ollama.go` | `OllamaAdapter` -- Ollama local API |
| `internal/llm/output/openrouter.go` | `OpenRouterAdapter` -- OpenRouter API (OpenAI-compatible) |

---

## 2. Performance Bottlenecks

| # | Severity | Location | Problem | Proposed Fix |
|---|----------|----------|---------|--------------|
| 1 | **HIGH** | `client.go:685-700` | `GenerateStream` wrapper goroutine accumulates `fullResponse += chunk.Content` via repeated string concatenation. For a 10 KB streaming response with 200 chunks, this performs ~200 allocations and copies ~1 MB total. | Use `strings.Builder` to accumulate the full response. |
| 2 | **HIGH** | `openai.go:204` | `GenerateStream` creates a **new `http.Client`** (`streamClient`) on every call. Each new client creates a fresh connection pool with no connection reuse. Under concurrent streaming, this means N separate TCP connections to the same server. | Move `streamClient` to the adapter struct, initialize once in `NewOpenAIAdapter`. |
| 3 | **HIGH** | `ollama.go:145` | Same issue: `streamClient := &http.Client{Transport: http.DefaultTransport}` created per call. | Same fix: move to struct field. |
| 4 | **HIGH** | `openrouter.go:191` | Same issue: `streamClient` created per call. | Same fix: move to struct field. |
| 5 | **MEDIUM** | `client.go:372` | Error response bodies are read with `io.ReadAll(resp.Body)` with no size limit. A malicious or buggy server could return a multi-GB error body, causing OOM. | Use `io.LimitReader(resp.Body, maxErrorBodySize)` (e.g., 64 KB). |
| 6 | **MEDIUM** | `scorer.go:99-127` | `FailoverScorer.Generate` tries clients **sequentially**. If the primary client times out after 30s, the user waits 30s before the fallback is even attempted. | Start the next client after a shorter deadline (e.g., `min(fs.timeout, 5s)` for the primary), or race primary + first fallback with a small head start. |
| 7 | **MEDIUM** | `openai.go:85` | `io.ReadAll(resp.Body)` on error -- same unbounded read issue as `client.go`. | Use `io.LimitReader`. |
| 8 | **MEDIUM** | `ollama.go:78` | `io.ReadAll(resp.Body)` on error -- same issue. | Use `io.LimitReader`. |
| 9 | **MEDIUM** | `openrouter.go:77` | `io.ReadAll(resp.Body)` on error -- same issue. | Use `io.LimitReader`. |
| 10 | **MEDIUM** | `client.go:873-917` | `streamAnthropic` parses every line with `json.Unmarshal`, including SSE control lines that are not JSON. Each failed unmarshal allocates and discards. | Check for `event:` prefix before attempting JSON parse; only parse lines starting with `data:`. |
| 11 | **LOW** | `openai.go:253` | `json.Unmarshal([]byte(data), &chunk)` for every SSE chunk creates a new `[]byte` allocation from the string. | Use `json.NewDecoder` with a `strings.Reader` to reuse the decoder, or accept the allocation as negligible for streaming. |
| 12 | **LOW** | `client.go:162-173` | Two separate `http.Client` instances (`httpClient` with timeout, `streamClient` without) are maintained. The `streamClient` uses `http.DefaultTransport` which has a global connection pool -- concurrent clients share it, causing contention. | Create a dedicated `http.Transport` per client with tuned `MaxIdleConnsPerHost`. |
| 13 | **LOW** | `client.go:334-344` | `generateOpenRouter` and `generateAnthropic` create a new `map[string]interface{}` request body on every call. For hot paths, this allocates on every request. | Pool request body structs or use a builder pattern. |

---

## 3. Code Quality Issues

| # | Severity | Location | Problem | Proposed Fix |
|---|----------|----------|---------|--------------|
| 1 | **HIGH** | `client.go:258-267` | `Generate` dispatches `ProviderOpenRouter` to `generateOpenRouter`, but `ProviderOpenAI` also goes to `generateOpenRouter`. This means OpenAI and OpenRouter share the exact same code path, but the function is named `generateOpenRouter`, not `generateOpenAI`. Confusing for maintainers. | Rename to `generateOpenAICompatible` or extract the shared logic. |
| 2 | **MEDIUM** | `client.go:683-700` | The `GenerateStream` wrapper goroutine has a subtle bug: if `chunk.Done` is true but `chunk.Content` is also non-empty, the content is accumulated but the final `Done` chunk is **not forwarded** to the caller's channel. The `break` exits the loop before the `Done` chunk can be sent. | Send the final chunk content before breaking: check `chunk.Content` before the `Done` check. |
| 3 | **MEDIUM** | `client.go:695-699` | The wrapper goroutine sends non-Done chunks to `ch` but silently drops the final chunk if it has content. Callers relying on `Done` being the last signal will work, but callers that expect the final content chunk will lose data. | Restructure: accumulate content, then check Done after sending. |
| 4 | **MEDIUM** | `openai.go:36-41` | `NewOpenAIAdapter` creates `http.Client` with `Timeout` set from `config.Timeout`. If `config.Timeout` is 0, the timeout is 0 (immediate timeout). No default is applied. | Apply a default timeout (e.g., 60s) when `config.Timeout <= 0`. |
| 5 | **MEDIUM** | `ollama.go:38-42` | Same issue: zero timeout if `config.Timeout` is 0. | Apply default timeout. |
| 6 | **MEDIUM** | `openrouter.go:34-39` | Same issue: zero timeout if `config.Timeout` is 0. | Apply default timeout. |
| 7 | **MEDIUM** | `client.go:536-543` | `generateAnthropic` builds the result with `strings.Builder` by iterating `response.Content` blocks. This is correct but inconsistent with OpenAI's `result.Choices[0].Message.Content` pattern. | Document the difference clearly or unify the response model. |
| 8 | **LOW** | `openai.go:105-166` | `GenerateStructured` appends a schema instruction to the prompt text and also sets `response_format: json_object`. The schema instruction in the prompt is redundant when `response_format` is set, and may confuse the model. | Use only `response_format` or only the prompt instruction, not both. |
| 9 | **LOW** | `openrouter.go:68-69` | `HTTP-Referer` is hardcoded to the GitHub repo URL. This leaks repository information to OpenRouter. | Make it configurable or remove it (the comment in `client.go:359` says "Privacy: Omit referer"). |
| 10 | **LOW** | `scorer.go:64-69` | When a fallback client fails to create, it logs a warning and skips it silently. The caller has no visibility into which fallbacks are active. | Return the list of skipped configs alongside the scorer, or expose `ActiveClients()` / `SkippedClients()`. |

---

## 4. Code Snippets: Problems and Fixes

### 4.1 String Concatenation in Stream Wrapper (client.go:683-700)

**Problem:**
```go
go func() {
    defer close(ch)
    var fullResponse string
    var streamErr error
    for chunk := range rawCh {
        fullResponse += chunk.Content  // O(n^2) concatenation
        if chunk.Err != nil {
            streamErr = chunk.Err
        }
        if chunk.Done {
            break  // final content chunk may be dropped
        }
        select {
        case ch <- chunk:
        case <-ctx.Done():
            return
        }
    }
    // ...
}()
```

**Fix:**
```go
go func() {
    defer close(ch)
    var builder strings.Builder
    var streamErr error
    for chunk := range rawCh {
        if chunk.Content != "" {
            builder.WriteString(chunk.Content)
        }
        if chunk.Err != nil {
            streamErr = chunk.Err
        }
        // Forward all non-empty content chunks to caller.
        if chunk.Content != "" || chunk.Done {
            select {
            case ch <- chunk:
            case <-ctx.Done():
                return
            }
        }
        if chunk.Done {
            break
        }
    }
    fullResponse := builder.String()
    duration := time.Since(start)
    c.recordLLMCall(ctx, prompt, fullResponse, 0, start, streamErr)
    // ... emit callbacks ...
}()
```

### 4.2 Per-Call HTTP Client in Streaming (openai.go:204)

**Problem:**
```go
func (a *OpenAIAdapter) GenerateStream(ctx context.Context, prompt string) (<-chan StreamChunk, error) {
    // ...
    streamClient := &http.Client{Transport: http.DefaultTransport}  // NEW client every call
    resp, err := streamClient.Do(req)
    // ...
}
```

Each call creates a new client with a fresh connection pool. Connections are never reused.

**Fix:**
```go
// In OpenAIAdapter struct:
type OpenAIAdapter struct {
    config       *Config
    client       *http.Client  // for blocking calls (with timeout)
    streamClient *http.Client  // for streaming calls (no timeout)
}

// In NewOpenAIAdapter:
func NewOpenAIAdapter(config *Config) *OpenAIAdapter {
    // ...
    return &OpenAIAdapter{
        config: config,
        client: &http.Client{
            Timeout: time.Duration(timeout) * time.Second,
        },
        streamClient: &http.Client{
            Transport: &http.Transport{
                MaxIdleConns:        100,
                MaxIdleConnsPerHost: 10,
                IdleConnTimeout:     90 * time.Second,
            },
            // No Timeout -- controlled via context.
        },
    }
}
```

### 4.3 Unbounded Error Body Read (client.go:371-376)

**Problem:**
```go
if resp.StatusCode != http.StatusOK {
    body, readErr := io.ReadAll(resp.Body)  // no size limit
    // ...
    return "", fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(body))
}
```

**Fix:**
```go
const maxErrorBodySize = 64 * 1024 // 64 KB

if resp.StatusCode != http.StatusOK {
    limitedBody, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
    return "", fmt.Errorf("unexpected status code: %d, body: %s", resp.StatusCode, string(limitedBody))
}
```

### 4.4 FailoverScorer Sequential Timeout (scorer.go:99-127)

**Problem:**
```go
for i, client := range fs.clients {
    cctx, cancel := context.WithTimeout(ctx, fs.timeout)
    resp, err := client.Generate(cctx, prompt)
    cancel()
    if err == nil {
        return resp, nil
    }
    // ... try next client
}
```

If `fs.timeout` is 30s and the primary is unresponsive, the user waits 30s before the fallback starts.

**Fix:**
```go
func (fs *FailoverScorer) Generate(ctx context.Context, prompt string) (string, error) {
    if len(fs.clients) == 1 {
        cctx, cancel := context.WithTimeout(ctx, fs.timeout)
        defer cancel()
        return fs.clients[0].Generate(cctx, prompt)
    }

    // Race primary (with full timeout) against fallback (with short delay).
    type result struct {
        resp string
        err  error
    }

    // Start primary immediately.
    primaryCh := make(chan result, 1)
    go func() {
        cctx, cancel := context.WithTimeout(ctx, fs.timeout)
        defer cancel()
        resp, err := fs.clients[0].Generate(cctx, prompt)
        primaryCh <- result{resp, err}
    }()

    // Start fallback after a shorter delay (e.g., 5s or timeout/6).
    fallbackDelay := fs.timeout / 6
    if fallbackDelay < 3*time.Second {
        fallbackDelay = 3 * time.Second
    }

    timer := time.NewTimer(fallbackDelay)
    defer timer.Stop()

    for {
        select {
        case r := <-primaryCh:
            if r.err == nil {
                return r.resp, nil
            }
            // Primary failed; try remaining sequentially.
            return fs.generateSequential(ctx, 1, r.err)
        case <-timer.C:
            // Start fallback in parallel with primary.
            fallbackCh := make(chan result, 1)
            go func() {
                cctx, cancel := context.WithTimeout(ctx, fs.timeout)
                defer cancel()
                resp, err := fs.clients[1].Generate(cctx, prompt)
                fallbackCh <- result{resp, err}
            }()
            // Now race primary vs fallback.
            select {
            case r := <-primaryCh:
                if r.err == nil {
                    return r.resp, nil
                }
                fr := <-fallbackCh
                if fr.err == nil {
                    return fr.resp, nil
                }
                return fs.generateSequential(ctx, 2, fr.err)
            case r := <-fallbackCh:
                if r.err == nil {
                    return r.resp, nil
                }
                // Fallback failed; wait for primary.
                pr := <-primaryCh
                if pr.err == nil {
                    return pr.resp, nil
                }
                return "", fmt.Errorf("both primary and fallback failed")
            }
        case <-ctx.Done():
            return "", ctx.Err()
        }
    }
}
```

### 4.5 Anthropic Stream Parses Non-JSON Lines (client.go:873-917)

**Problem:**
```go
for scanner.Scan() {
    line := scanner.Text()
    if line == "" {
        continue
    }
    var event struct { ... }
    if err := json.Unmarshal([]byte(line), &event); err != nil {
        slog.Warn("Failed to unmarshal anthropic stream chunk", "error", err)
        continue
    }
    // ...
}
```

SSE lines like `event: content_block_delta` are not JSON, causing a warning log per line.

**Fix:**
```go
for scanner.Scan() {
    line := scanner.Text()
    if line == "" || !strings.HasPrefix(line, "{") {
        continue // skip SSE control lines
    }
    var event struct { ... }
    if err := json.Unmarshal([]byte(line), &event); err != nil {
        slog.Debug("skipping non-JSON SSE line", "line", line)
        continue
    }
    // ...
}
```

### 4.6 Zero Timeout Default (openai.go:36-41)

**Problem:**
```go
func NewOpenAIAdapter(config *Config) *OpenAIAdapter {
    if config == nil {
        config = &Config{}
    }
    // ...
    return &OpenAIAdapter{
        config: config,
        client: &http.Client{
            Timeout: time.Duration(config.Timeout) * time.Second,  // 0 if unset
        },
    }
}
```

**Fix:**
```go
func NewOpenAIAdapter(config *Config) *OpenAIAdapter {
    if config == nil {
        config = &Config{}
    }
    timeout := config.Timeout
    if timeout <= 0 {
        timeout = 60 // default 60 seconds
    }
    // ...
    return &OpenAIAdapter{
        config: config,
        client: &http.Client{
            Timeout: time.Duration(timeout) * time.Second,
        },
    }
}
```

---

## 5. Priority Action Items

### P0 -- Must Fix

1. **[✓] Fix per-call `http.Client` in streaming.** Added `streamClient *http.Client` to all three adapter structs, initialized once in the constructor with a tuned `Transport`.

2. **[✓] Fix `fullResponse += chunk.Content` in `client.go:685`.** Replaced with `strings.Builder`.

3. **[✓] Limit error body reads.** All `io.ReadAll(resp.Body)` calls on error paths now use `io.LimitReader(resp.Body, 64*1024)`.

### P1 -- Should Fix

4. **[✓] Fix the `Done` chunk forwarding bug** in `client.go:695-699`. Restructured to forward content-bearing chunks before checking `chunk.Done`.

5. **[✓] Apply default timeout** when `config.Timeout <= 0` in all three adapter constructors (defaults to 60s).

6. **Improve FailoverScorer latency** by racing primary + fallback after a delay, instead of sequential fallback.

7. **[✓] Fix Anthropic SSE parsing** to skip control lines (`!strings.HasPrefix(line, "{")`) and downgrade unmarshal errors to `slog.Debug`.

### P2 -- Nice to Have

8. **Rename `generateOpenRouter`** to `generateOpenAICompatible` since it handles both OpenAI and OpenRouter.

9. **Remove hardcoded `HTTP-Referer`** in `openrouter.go:68` or make it configurable.

10. **Unify `GenerateStructured` behavior** -- decide whether to use prompt-based schema instruction or `response_format`, not both.

11. **[✓] Create dedicated `http.Transport`** per LLM client instead of sharing `http.DefaultTransport`. Done as part of the stream client fix.

12. **Expose `SkippedClients()`** on `FailoverScorer` for operational visibility.
