package core

import (
	"strings"
	"sync"
)

// Capability represents a task capability that tools can perform.
type Capability string

const (
	// CapabilityMath represents mathematical calculation capability.
	CapabilityMath Capability = "math"
	// CapabilityKnowledge represents knowledge retrieval and management capability.
	CapabilityKnowledge Capability = "knowledge"
	// CapabilityMemory represents memory access and storage capability.
	CapabilityMemory Capability = "memory"
	// CapabilityText represents text processing and manipulation capability.
	CapabilityText Capability = "text"
	// CapabilityNetwork represents network request and API interaction capability.
	CapabilityNetwork Capability = "network"
	// CapabilityTime represents date and time operations capability.
	CapabilityTime Capability = "time"
	// CapabilityFile represents file system operations capability.
	CapabilityFile Capability = "file"
	// CapabilityExternal represents external system interaction capability.
	CapabilityExternal Capability = "external"
)

// capabilityKeywords maps capabilities to their associated keywords.
var capabilityKeywords = map[Capability][]string{
	CapabilityMath: {
		"calculate", "sum", "multiply", "divide", "number", "compute",
		"add", "subtract", "math", "calculation", "formula",
		// Chinese keywords
		"计算", "求和", "乘", "除", "加", "减", "数字", "公式", "数学",
	},
	CapabilityKnowledge: {
		"what", "who", "explain", "information", "search", "find",
		"retrieve", "lookup", "query", "knowledge", "answer",
		// Chinese keywords
		"什么", "谁", "解释", "信息", "搜索", "查找", "查询", "知识",
	},
	CapabilityMemory: {
		"remember", "store", "recall", "profile", "history", "context",
		"memory", "previous", "past", "save",
		// Chinese keywords
		"记住", "存储", "回忆", "历史", "记忆", "保存",
	},
	CapabilityText: {
		"parse", "format", "validate", "transform", "text", "string",
		"extract", "analyze", "process", "convert",
		// Chinese keywords
		"解析", "格式", "验证", "转换", "文本", "提取", "分析", "处理",
	},
	CapabilityNetwork: {
		"api", "request", "fetch", "download", "http", "url",
		"network", "web", "endpoint", "curl",
		// Chinese keywords
		"请求", "获取", "下载", "网络", "网页", "网址",
	},
	CapabilityTime: {
		"time", "date", "schedule", "deadline", "timestamp", "calendar",
		"duration", "when", "until", "after", "before",
		// Chinese keywords
		"时间", "日期", "时刻", "时间戳", "日历", "持续", "何时",
		"几点", "现在", "当前", "今天", "昨天", "明天",
	},
	CapabilityFile: {
		"file", "directory", "read", "write", "delete", "list",
		"save", "load", "path", "folder",
		// Chinese keywords
		"文件", "目录", "读取", "写入", "删除", "列出",
		"保存", "加载", "路径", "文件夹",
	},
	CapabilityExternal: {
		"external", "system", "execute", "run", "command", "script",
		"integration", "service",
		// Chinese keywords
		"外部", "系统", "执行", "运行", "命令", "脚本",
	},
}

// CapabilityEngine provides capability detection and tool filtering.
type CapabilityEngine struct {
	registry *Registry
	mu       sync.RWMutex
	capMap   map[Capability][]Tool
}

// NewCapabilityEngine creates a new CapabilityEngine.
func NewCapabilityEngine(registry *Registry) *CapabilityEngine {
	engine := &CapabilityEngine{
		registry: registry,
		capMap:   make(map[Capability][]Tool),
	}
	engine.buildCapabilityMap()
	// Auto-rebuild when the registry changes after construction (T-04).
	registry.OnChange(func() {
		engine.Rebuild()
	})
	return engine
}

// Detect identifies capabilities from a query string.
func (e *CapabilityEngine) Detect(query string) []Capability {
	queryLower := strings.ToLower(query)

	detected := make([]Capability, 0, len(capabilityKeywords))
	detectedSet := make(map[Capability]bool)

	for cap, keywords := range capabilityKeywords {
		for _, keyword := range keywords {
			if strings.Contains(queryLower, keyword) {
				if !detectedSet[cap] {
					detectedSet[cap] = true
					detected = append(detected, cap)
				}
				break
			}
		}
	}

	return detected
}

// ToolsFor returns tools that support a specific capability.
func (e *CapabilityEngine) ToolsFor(cap Capability) []Tool {
	e.mu.RLock()
	defer e.mu.RUnlock()

	tools, exists := e.capMap[cap]
	if !exists {
		return nil
	}

	result := make([]Tool, len(tools))
	copy(result, tools)
	return result
}

// Filter returns tools that match any of the given capabilities.
func (e *CapabilityEngine) Filter(capabilities []Capability) []Tool {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if len(capabilities) == 0 {
		return nil
	}

	toolSet := make(map[string]Tool)
	for _, cap := range capabilities {
		if tools, exists := e.capMap[cap]; exists {
			for _, tool := range tools {
				toolSet[tool.Name()] = tool
			}
		}
	}

	result := make([]Tool, 0, len(toolSet))
	for _, tool := range toolSet {
		result = append(result, tool)
	}

	return result
}

// Match performs full capability matching workflow.
func (e *CapabilityEngine) Match(query string) []Tool {
	capabilities := e.Detect(query)
	if len(capabilities) == 0 {
		return nil
	}

	return e.Filter(capabilities)
}

// buildCapabilityMap constructs the capability-to-tools mapping.
func (e *CapabilityEngine) buildCapabilityMap() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.capMap = make(map[Capability][]Tool)

	tools := e.registry.List()
	for _, toolName := range tools {
		tool, exists := e.registry.Get(toolName)
		if !exists {
			continue
		}

		capabilities := tool.Capabilities()
		for _, cap := range capabilities {
			e.capMap[cap] = append(e.capMap[cap], tool)
		}
	}
}

// Rebuild rebuilds the capability map after tool registration changes.
func (e *CapabilityEngine) Rebuild() {
	e.buildCapabilityMap()
}

// GetAllCapabilities returns all available capabilities.
func (e *CapabilityEngine) GetAllCapabilities() []Capability {
	e.mu.RLock()
	defer e.mu.RUnlock()

	caps := make([]Capability, 0, len(e.capMap))
	for cap := range e.capMap {
		caps = append(caps, cap)
	}

	return caps
}

// GetCapabilitySummary returns a summary of capabilities and their tool counts.
func (e *CapabilityEngine) GetCapabilitySummary() map[Capability]int {
	e.mu.RLock()
	defer e.mu.RUnlock()

	summary := make(map[Capability]int)
	for cap, tools := range e.capMap {
		summary[cap] = len(tools)
	}

	return summary
}
