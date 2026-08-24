# web_search 被自家 SSRF 防护锁死；LLM 流式响应被静默截断

日期：2026-08-24
范围：api/tools、internal/llm

## 1. web_search 在默认配置下完全不可用

`newWebSearchTool` 把所有请求都路由过 `ssrfSafeDialContext`，而该拨号器拒绝
回环/私网地址——但文档写明的默认端点正是 `http://localhost:5605`
（SearXNG Docker 部署）。所有默认调用都失败于
"ssrf: refusing to dial private/loopback address"。没有任何测试发现它：
web_search 测试全部注入自己的 `http.Client`，于是防护逻辑——唯一有价值的
代码路径——从未被执行。

修复：按信任边界拆成两个 client。操作员配置的 baseURL（构造默认值 /
t.baseURL）走普通 client；LLM 可控的请求参数 `searxng_base_url` 保留
SSRF 防护拨号器。新增测试同时覆盖两条路径（可信回环可达；私网覆盖被拒）。

## 2. 流式包装器对慢消费者丢块

GenerateStream 包装器用非阻塞发送转发数据块并配 `default: return`：一旦
消费速度落后于 64 槽缓冲，协程直接返回并关闭 channel——不带 Done、不带
Err。截断的回答与完整的回答无法区分。

另外三个 provider 协程在正常结束时从不发送 `{Done: true}`：依赖 Done 标记
（而非 channel 关闭）的消费者在正常完成后会永远等待。

修复：改为带 ctx.Done 逃逸的阻塞发送（停止读取的调用方必须取消 ctx——标准
流式契约）；并在原始流未给出终止标记时补发 `{Done: true}`。

## 回归测试

- `TestWebSearchToolDefaultLoopbackReachable`、
  `TestWebSearchToolOverrideBlocksPrivateTarget`（api/tools/tools_test.go）。
- `TestClient_GenerateStream_SlowConsumerNoTruncation`
  （internal/llm/client_stream_test.go）：200 个数据块 + 终止 Done 全部送达
  刻意放慢的读者。
