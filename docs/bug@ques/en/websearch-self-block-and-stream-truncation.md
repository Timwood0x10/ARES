# web_search Self-Blocked by Its Own SSRF Guard; LLM Stream Silently Truncated

Date: 2026-08-24
Scope: api/tools, internal/llm

## 1. web_search dead in default configuration

`newWebSearchTool` routed EVERY request through `ssrfSafeDialContext`, which
refuses loopback/private targets — while the documented default endpoint is
`http://localhost:5605` (SearXNG Docker deployment). Every default call failed
with "ssrf: refusing to dial private/loopback address". No test caught it:
all web_search tests injected their own `http.Client`, so the guard — the only
interesting logic — never executed.

Fix: two clients by TRUST BOUNDARY. The operator-configured base URL
(constructor default / t.baseURL) dials with a plain client; the
LLM-controllable request param `searxng_base_url` keeps the SSRF-guarded
dialer. New tests execute both paths (loopback reachable via trusted;
private override refused with an ssrf error).

## 2. Streaming wrapper dropped chunks for slow consumers

The GenerateStream wrapper forwarded chunks with a NON-BLOCKING send and a
`default: return`: once the consumer fell behind the 64-slot buffer, the
goroutine returned and closed the channel without Done or Err — truncated
answers were indistinguishable from complete ones.

Additionally none of the three provider goroutines ever emitted a success
`{Done: true}`; consumers watching for Done (instead of channel close) would
wait forever on normal completion.

Fix: blocking send escaping on ctx.Done (caller that stops reading must cancel
ctx — standard streaming contract), plus a guaranteed terminal `{Done: true}`
marker when the raw stream closes without one.

## Regression Tests

- `TestWebSearchToolDefaultLoopbackReachable`,
  `TestWebSearchToolOverrideBlocksPrivateTarget` (api/tools/tools_test.go).
- `TestClient_GenerateStream_SlowConsumerNoTruncation`
  (internal/llm/client_stream_test.go): 200 chunks + terminal Done delivered
  to a deliberately slow reader.
