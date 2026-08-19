# Record of B.ai API Request

```bash
curl -X POST "https://api.b.ai/v1/chat/completions" \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -d '{
        "model": "your-model-name",
        "messages": [{ "role": "user", "content": "如何把这个记入到pi里呢？" }],
        "temperature": 0.7
      }'
```

## How to integrate with Pi

1. Save this file in the Pi notes directory.
2. Use `symbol_search` or `module_report` to locate it later.
3. Optionally send to WeChat with `send_file_to_wechat --filePath <path>`.
