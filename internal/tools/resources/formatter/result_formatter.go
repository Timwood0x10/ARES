package formatter

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Timwood0x10/goagent/internal/tools/resources/core"
)

// ResultFormatter formats tool results in user-friendly way.
type ResultFormatter struct{}

// NewResultFormatter creates a new ResultFormatter.
func NewResultFormatter() *ResultFormatter {
	return &ResultFormatter{}
}

// getStringParam safely gets a string parameter with default value.
func getStringParam(params map[string]interface{}, key string, defaultValue string) string {
	if value, ok := params[key].(string); ok {
		return value
	}
	return defaultValue
}

// getIntParam safely gets an int parameter with default value.
func getIntParam(dataMap map[string]interface{}, key string, defaultValue int) int {
	if value, ok := dataMap[key].(int); ok {
		return value
	}
	if value, ok := dataMap[key].(float64); ok {
		return int(value)
	}
	return defaultValue
}

// getInt64Param safely gets an int64 parameter with default value.
func getInt64Param(dataMap map[string]interface{}, key string, defaultValue int64) int64 {
	if value, ok := dataMap[key].(int64); ok {
		return value
	}
	if value, ok := dataMap[key].(int); ok {
		return int64(value)
	}
	if value, ok := dataMap[key].(float64); ok {
		return int64(value)
	}
	return defaultValue
}

// getFloat64Param safely gets a float64 parameter with default value.
func getFloat64Param(dataMap map[string]interface{}, key string, defaultValue float64) float64 {
	if value, ok := dataMap[key].(float64); ok {
		return value
	}
	if value, ok := dataMap[key].(int); ok {
		return float64(value)
	}
	return defaultValue
}

// getBoolParam safely gets a bool parameter with default value.
func getBoolParam(dataMap map[string]interface{}, key string, defaultValue bool) bool {
	if value, ok := dataMap[key].(bool); ok {
		return value
	}
	return defaultValue
}

// Format formats a tool result into a user-friendly string.
func (rf *ResultFormatter) Format(toolName string, params map[string]interface{}, result core.Result, duration time.Duration) string {
	// Check if result is successful
	if !result.Success {
		slog.Warn("Tool execution failed", "tool", toolName, "error", result.Error, "duration", duration)
		return fmt.Sprintf("调用工具 %s 时出错: %s", toolName, result.Error)
	}

	// Log successful execution
	slog.Info("Tool executed successfully",
		"tool", toolName,
		"duration", duration,
		"params", params,
	)

	// Format based on tool type
	formatted := rf.formatByToolType(toolName, params, result)

	slog.Info("Tool result formatted", "tool", toolName, "result", formatted)

	return formatted
}

// formatByToolType formats result based on tool type.
func (rf *ResultFormatter) formatByToolType(toolName string, params map[string]interface{}, result core.Result) string {
	switch toolName {
	case "datetime":
		return rf.formatDateTime(params, result.Data)
	case "calculator":
		return rf.formatCalculator(params, result.Data)
	case "file_tools":
		return rf.formatFileTools(params, result.Data)
	case "id_generator":
		return rf.formatIDGenerator(params, result.Data)
	case "http_request":
		return rf.formatHTTPRequest(params, result.Data)
	case "text_processor":
		return rf.formatTextProcessor(params, result.Data)
	case "json_tools":
		return rf.formatJSONTools(params, result.Data)
	case "data_validation":
		return rf.formatDataValidation(params, result.Data)
	case "data_transform":
		return rf.formatDataTransform(params, result.Data)
	case "regex_tool":
		return rf.formatRegexTool(params, result.Data)
	case "log_analyzer":
		return rf.formatLogAnalyzer(params, result.Data)
	case "code_runner":
		return rf.formatCodeRunner(params, result.Data)
	default:
		return rf.formatDefault(toolName, params, result.Data)
	}
}

// formatDateTime formats datetime tool result.
func (rf *ResultFormatter) formatDateTime(params map[string]interface{}, data interface{}) string {
	dataMap, ok := data.(map[string]interface{})
	if !ok {
		return "时间工具返回了意外的数据格式"
	}

	if formatted, exists := dataMap["formatted"]; exists {
		return fmt.Sprintf("当前时间是：%s", formatted)
	}

	return "时间工具执行完成，但无法解析返回的时间"
}

// formatCalculator formats calculator tool result.
func (rf *ResultFormatter) formatCalculator(params map[string]interface{}, data interface{}) string {
	dataMap, ok := data.(map[string]interface{})
	if !ok {
		return "计算工具返回了意外的数据格式"
	}

	expression := getStringParam(params, "expression", "")
	resultValue, exists := dataMap["result"]

	if !exists {
		return fmt.Sprintf("计算工具执行了表达式 %s，但无法获取结果", expression)
	}

	return fmt.Sprintf("计算结果 (%s): %.2f", expression, resultValue)
}

// formatFileTools formats file tools result.
func (rf *ResultFormatter) formatFileTools(params map[string]interface{}, data interface{}) string {
	operation := getStringParam(params, "operation", "")
	dataMap, ok := data.(map[string]interface{})
	if !ok {
		return fmt.Sprintf("文件操作 (%s) 执行完成", operation)
	}

	switch operation {
	case "read":
		filePath := getStringParam(params, "file_path", "")
		if content, exists := dataMap["content"]; exists {
			if contentStr, ok := content.(string); ok {
				lineCount := getIntParam(dataMap, "line_count", 0)
				totalLines := getIntParam(dataMap, "total_lines", 0)

				var sb strings.Builder
				fmt.Fprintf(&sb, "文件: %s\n", filePath)
				fmt.Fprintf(&sb, "行数: %d/%d\n", lineCount, totalLines)
				sb.WriteString("\n内容:\n")
				sb.WriteString(contentStr)

				if totalLines > lineCount {
					fmt.Fprintf(&sb, "\n\n... (显示 %d 行，共 %d 行)", lineCount, totalLines)
				}

				return sb.String()
			}
		}
		return fmt.Sprintf("文件 %s 读取完成", filePath)
	case "write":
		bytesWritten := getIntParam(dataMap, "bytes_written", 0)
		return fmt.Sprintf("文件写入完成，写入了 %d 字节", bytesWritten)
	case "list":
		directory := getStringParam(params, "directory_path", "")
		var sb strings.Builder
		fmt.Fprintf(&sb, "目录: %s\n", directory)

		// List directories - use flexible type handling
		if dirs, exists := dataMap["directories"]; exists {
			dirList := convertToMapSlice(dirs)
			if len(dirList) > 0 {
				sb.WriteString("\n目录:\n")
				for _, dir := range dirList {
					name := getStringParam(dir, "name", "")
					fmt.Fprintf(&sb, "  📁 %s\n", name)
				}
			}
		}

		// List files - use flexible type handling
		if files, exists := dataMap["files"]; exists {
			fileList := convertToMapSlice(files)
			if len(fileList) > 0 {
				sb.WriteString("\n文件:\n")
				for _, file := range fileList {
					name := getStringParam(file, "name", "")
					size := getInt64Param(file, "size", 0)
					fmt.Fprintf(&sb, "  📄 %s (%d bytes)\n", name, size)
				}
			}
		}

		// Add summary
		if totals, exists := dataMap["totals"]; exists {
			if totalsMap, ok := totals.(map[string]interface{}); ok {
				if dirCount, ok := totalsMap["directories"].(int); ok {
					if fileCount, ok := totalsMap["files"].(int); ok {
						fmt.Fprintf(&sb, "\n总计: %d 个目录, %d 个文件", dirCount, fileCount)
					}
				}
			}
		}

		return sb.String()
	default:
		return fmt.Sprintf("文件操作 (%s) 执行完成", operation)
	}
}

// formatIDGenerator formats ID generator result.
func (rf *ResultFormatter) formatIDGenerator(params map[string]interface{}, data interface{}) string {
	dataMap, ok := data.(map[string]interface{})
	if !ok {
		return "ID生成工具执行完成"
	}

	operation := getStringParam(params, "operation", "")

	switch operation {
	case "generate_uuid":
		if id, exists := dataMap["id"]; exists {
			return fmt.Sprintf("生成的 UUID: %s", id)
		}
	case "generate_short_id":
		if id, exists := dataMap["id"]; exists {
			return fmt.Sprintf("生成的短 ID: %s", id)
		}
	}

	return "ID生成完成"
}

// formatHTTPRequest formats HTTP request result.
func (rf *ResultFormatter) formatHTTPRequest(params map[string]interface{}, data interface{}) string {
	dataMap, ok := data.(map[string]interface{})
	if !ok {
		return "HTTP 请求完成"
	}

	url := getStringParam(params, "url", "")
	statusCode := getFloat64Param(dataMap, "status_code", 0)

	if statusCode > 0 {
		return fmt.Sprintf("HTTP 请求完成: %s (状态码: %.0f)", url, statusCode)
	}

	return "HTTP 请求完成"
}

// formatTextProcessor formats text processor result.
func (rf *ResultFormatter) formatTextProcessor(params map[string]interface{}, data interface{}) string {
	operation := getStringParam(params, "operation", "")
	return fmt.Sprintf("文本处理操作 (%s) 执行完成", operation)
}

// formatJSONTools formats JSON tools result.
func (rf *ResultFormatter) formatJSONTools(params map[string]interface{}, data interface{}) string {
	operation := getStringParam(params, "operation", "")
	return fmt.Sprintf("JSON 处理操作 (%s) 执行完成", operation)
}

// formatDataValidation formats data validation result.
func (rf *ResultFormatter) formatDataValidation(params map[string]interface{}, data interface{}) string {
	operation := getStringParam(params, "operation", "")
	dataMap, ok := data.(map[string]interface{})
	if !ok {
		return fmt.Sprintf("数据验证 (%s) 执行完成", operation)
	}

	valid := getBoolParam(dataMap, "valid", false)

	if valid {
		return "数据验证通过：格式正确"
	}

	return "数据验证失败：格式不正确"
}

// formatDataTransform formats data transform result.
func (rf *ResultFormatter) formatDataTransform(params map[string]interface{}, data interface{}) string {
	operation := getStringParam(params, "operation", "")
	return fmt.Sprintf("数据转换操作 (%s) 执行完成", operation)
}

// formatRegexTool formats regex tool result.
func (rf *ResultFormatter) formatRegexTool(params map[string]interface{}, data interface{}) string {
	operation := getStringParam(params, "operation", "")
	dataMap, ok := data.(map[string]interface{})
	if !ok {
		return fmt.Sprintf("正则操作 (%s) 执行完成", operation)
	}

	if operation == "match" {
		matched := getBoolParam(dataMap, "matched", false)
		if matched {
			return "正则匹配成功"
		}
		return "正则匹配失败"
	}

	return fmt.Sprintf("正则操作 (%s) 执行完成", operation)
}

// formatLogAnalyzer formats log analyzer result.
func (rf *ResultFormatter) formatLogAnalyzer(params map[string]interface{}, data interface{}) string {
	operation := getStringParam(params, "operation", "")
	dataMap, ok := data.(map[string]interface{})
	if !ok {
		return fmt.Sprintf("日志分析操作 (%s) 执行完成", operation)
	}

	switch operation {
	case "parse_log":
		if logType, exists := dataMap["log_type"]; exists {
			return fmt.Sprintf("日志解析完成，类型：%s", logType)
		}
		return "日志解析完成"
	case "find_errors":
		count := getIntParam(dataMap, "error_count", 0)
		if count > 0 {
			return fmt.Sprintf("发现 %d 个错误", count)
		}
		return "错误查找完成"
	case "extract_metrics":
		if metrics, exists := dataMap["metrics"]; exists {
			if metricList, ok := metrics.(map[string]interface{}); ok {
				return fmt.Sprintf("提取了 %d 个指标", len(metricList))
			}
		}
		return "指标提取完成"
	default:
		return fmt.Sprintf("日志分析操作 (%s) 执行完成", operation)
	}
}

// formatCodeRunner formats code runner result.
func (rf *ResultFormatter) formatCodeRunner(params map[string]interface{}, data interface{}) string {
	operation := getStringParam(params, "operation", "")
	dataMap, ok := data.(map[string]interface{})
	if !ok {
		return fmt.Sprintf("代码执行 (%s) 完成", operation)
	}

	switch operation {
	case "run_python":
		if output, exists := dataMap["output"]; exists {
			if outputStr, ok := output.(string); ok {
				if len(outputStr) > 100 {
					return fmt.Sprintf("Python 执行输出（前100字符）:\n%s...", outputStr[:100])
				}
				return fmt.Sprintf("Python 执行输出:\n%s", outputStr)
			}
		}
		return "Python 代码执行完成"
	case "run_js":
		if output, exists := dataMap["output"]; exists {
			if outputStr, ok := output.(string); ok {
				if len(outputStr) > 100 {
					return fmt.Sprintf("JavaScript 执行输出（前100字符）:\n%s...", outputStr[:100])
				}
				return fmt.Sprintf("JavaScript 执行输出:\n%s", outputStr)
			}
		}
		return "JavaScript 代码执行完成"
	}

	return "代码执行完成"
}

// formatDefault formats result in default way.
func (rf *ResultFormatter) formatDefault(toolName string, params map[string]interface{}, data interface{}) string {
	return fmt.Sprintf("工具 %s 执行完成", toolName)
}

// convertToMapSlice converts any slice type to []map[string]interface{}.
func convertToMapSlice(data interface{}) []map[string]interface{} {
	// Try direct conversion
	if slice, ok := data.([]map[string]interface{}); ok {
		return slice
	}

	// Try []interface{} conversion
	if slice, ok := data.([]interface{}); ok {
		result := make([]map[string]interface{}, 0, len(slice))
		for _, item := range slice {
			if m, ok := item.(map[string]interface{}); ok {
				result = append(result, m)
			}
		}
		return result
	}

	// Try JSON marshaling/unmarshaling
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return nil
	}

	var result []map[string]interface{}
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		return nil
	}

	return result
}
