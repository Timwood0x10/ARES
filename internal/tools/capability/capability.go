// Package capability maps free text to the capability names tools declare, so the routing table
// lives in one place instead of in whichever caller happens to need it.
//
// It is a leaf package on purpose: the planner (which routes) and the drift test (which checks
// the table against the tools that actually declare capabilities) both need it, and putting it
// in either one would force the other to import a package it has no business importing.
package capability

import (
	"sort"
	"strings"
)

// Keywords maps a capability to the terms that select it.
//
// Capability names are the SAME vocabulary the tools use for their domain tag, so a task tag and
// a tool tag can be compared directly. "A weather request" is not a tool domain — what it needs
// is network access — and inventing a `weather` capability would have made the table describe
// tools that do not exist.
//
// Keep entries specific: broad words would route most work into one bucket and make the
// narrowing indistinguishable from no narrowing at all.
var Keywords = map[string][]string{
	"network":   {"天气", "气温", "下雨", "预报", "weather", "forecast", "http", "api", "curl", "fetch", "网页", "url", "搜索", "search"},
	"file":      {"源码", "代码", "函数", "文件", "grep", "todo", "main.go", "code", "source"},
	"math":      {"计算", "数学", "求和", "加减", "calculate"},
	"data":      {"json", "校验", "validate", "转换", "format"},
	"text":      {"文本", "摘要", "翻译", "text"},
	"git":       {"git", "commit", "diff", "branch", "仓库"},
	"shell":     {"shell", "bash", "命令", "终端"},
	"execution": {"执行", "运行", "exec", "run ", "脚本"},
	"system":    {"系统", "环境", "env", "system", "配置"},
	"embedding": {"向量", "embedding", "语义相似", "similarity", "相似度"},
	"crypto":    {"加密", "解密", "签名", "crypto", "encrypt", "decrypt"},
	"pdf":       {"pdf", "文档解析", "document"},
}

// FromText returns every capability whose keywords appear in text, sorted.
//
// Case-insensitive because the same request arrives in both cases; CJK terms are matched
// literally (there is no tokenizer here, and a substring match on a two-character term is
// precise enough for this table). Sorted so the same request always yields the same tag set —
// these names reach prompt construction, and map-order churn would rewrite the prompt for no
// reason.
func FromText(text string) []string {
	if text == "" {
		return nil
	}
	lower := strings.ToLower(text)
	var caps []string
	for cap, terms := range Keywords {
		for _, term := range terms {
			if strings.Contains(lower, term) {
				caps = append(caps, cap)
				break
			}
		}
	}
	sort.Strings(caps)
	return caps
}

// KnownDomains lists the capability names this table can produce, sorted.
func KnownDomains() []string {
	out := make([]string, 0, len(Keywords))
	for cap := range Keywords {
		out = append(out, cap)
	}
	sort.Strings(out)
	return out
}
