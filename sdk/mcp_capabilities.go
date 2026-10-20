package sdk

import (
	"sort"
	"strings"
)

// mcpCapabilityKeywords maps planner capability categories (see
// internal/tools/planner/capdef.go) to the terms that select them from an MCP tool's name and
// description.
//
// The categories are the PLANNER's vocabulary, not a new one: GetToolCapabilities expands a
// broad category into the granular names its resolver understands, so inventing our own names
// here would produce caps that never resolve.
var mcpCapabilityKeywords = []struct {
	Category string
	Terms    []string
}{
	{"HTTPRequest", []string{"http", "api", "request", "fetch", "url", "网页", "网络"}},
	{"WebSearch", []string{"search", "query", "搜索", "检索"}},
	{"CodeExecution", []string{"run", "exec", "compile", "代码", "运行", "编译", "shell", "bash"}},
	{"DateTime", []string{"time", "date", "时间", "日期", "timezone", "时区"}},
	{"JSONProcessing", []string{"json", "yaml", "parse", "解析"}},
	{"PDFParsing", []string{"pdf"}},
	{"Hashing", []string{"hash", "sha", "md5", "哈希"}},
	{"Regex", []string{"regex", "regexp", "正则"}},
	{"Arithmetic", []string{"math", "calculate", "计算", "数学"}},
}

// classifyMCPToolCapabilities derives planner capabilities from an MCP tool's advertised
// description.
//
// MCP tools carry no capability metadata, only prose, so this is a keyword read of that prose.
// It is deliberately conservative in one direction: an unrecognised tool yields NO capabilities
// rather than a guess, because a wrong capability makes the planner route work to a tool that
// cannot do it, while a missing one only leaves the tool unranked (the previous behaviour for
// every MCP tool, since Capabilities() used to return nil unconditionally).
//
// The result is sorted so the same server advertises the same caps on every start — these names
// reach prompt construction, and a map-order shuffle would churn the prompt for no reason.
func classifyMCPToolCapabilities(name, description string) []string {
	text := strings.ToLower(name + " " + description)
	if text == "" {
		return nil
	}
	var out []string
	for _, entry := range mcpCapabilityKeywords {
		for _, term := range entry.Terms {
			if strings.Contains(text, term) {
				out = append(out, entry.Category)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}
