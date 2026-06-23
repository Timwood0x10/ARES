package formatter

import (
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// TestNewResultFormatter tests creating a new ResultFormatter.
func TestNewResultFormatter(t *testing.T) {
	formatter := NewResultFormatter()

	if formatter == nil {
		t.Fatal("NewResultFormatter() should not return nil")
	}
}

// TestFormatSuccess tests formatting successful results.
func TestFormatSuccess(t *testing.T) {
	formatter := NewResultFormatter()

	tests := []struct {
		name     string
		toolName string
		params   map[string]interface{}
		result   core.Result
		duration time.Duration
		wantErr  bool
	}{
		{
			name:     "successful result",
			toolName: "test_tool",
			params:   map[string]interface{}{"key": "value"},
			result: core.Result{
				Success: true,
				Data:    map[string]interface{}{"result": "success"},
			},
			duration: 100 * time.Millisecond,
			wantErr:  false,
		},
		{
			name:     "failed result",
			toolName: "test_tool",
			params:   map[string]interface{}{"key": "value"},
			result: core.Result{
				Success: false,
				Error:   "test error",
			},
			duration: 50 * time.Millisecond,
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			formatted := formatter.Format(tt.toolName, tt.params, tt.result, tt.duration)

			if formatted == "" {
				t.Error("Format() should not return empty string")
			}

			// For failed results, should contain error message
			if !tt.result.Success {
				if !contains(formatted, "出错") {
					t.Error("failed result format should contain error indicator")
				}
			}
		})
	}
}

// TestFormatDateTime tests formatting datetime tool results.
func TestFormatDateTime(t *testing.T) {
	formatter := NewResultFormatter()

	tests := []struct {
		name   string
		params map[string]interface{}
		data   interface{}
		want   string
	}{
		{
			name:   "valid datetime data",
			params: map[string]interface{}{},
			data: map[string]interface{}{
				"formatted": "2024-01-01 12:00:00",
			},
			want: "当前时间是：2024-01-01 12:00:00",
		},
		{
			name:   "datetime data without formatted",
			params: map[string]interface{}{},
			data: map[string]interface{}{
				"timestamp": 1234567890,
			},
			want: "时间工具执行完成，但无法解析返回的时间",
		},
		{
			name:   "invalid datetime data",
			params: map[string]interface{}{},
			data:   "invalid",
			want:   "datetime tool returned unexpected data format: string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := core.Result{
				Success: true,
				Data:    tt.data,
			}

			formatted := formatter.Format("datetime", tt.params, result, 0)

			if formatted != tt.want {
				t.Errorf("Format() = %q, want %q", formatted, tt.want)
			}
		})
	}
}

// TestFormatCalculator tests formatting calculator tool results.
func TestFormatCalculator(t *testing.T) {
	formatter := NewResultFormatter()

	tests := []struct {
		name   string
		params map[string]interface{}
		data   interface{}
		want   string
	}{
		{
			name: "valid calculation",
			params: map[string]interface{}{
				"expression": "5 + 3",
			},
			data: map[string]interface{}{
				"result": 8.0,
			},
			want: "计算结果 (5 + 3): 8.00",
		},
		{
			name: "calculation without result",
			params: map[string]interface{}{
				"expression": "5 + 3",
			},
			data: map[string]interface{}{},
			want: "计算工具执行了表达式 5 + 3，但无法获取结果",
		},
		{
			name:   "invalid calculator data",
			params: map[string]interface{}{},
			data:   "invalid",
			want:   "calculator tool returned unexpected data format: string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := core.Result{
				Success: true,
				Data:    tt.data,
			}

			formatted := formatter.Format("calculator", tt.params, result, 0)

			if formatted != tt.want {
				t.Errorf("Format() = %q, want %q", formatted, tt.want)
			}
		})
	}
}

// TestFormatFileTools tests formatting file tools results.
func TestFormatFileTools(t *testing.T) {
	formatter := NewResultFormatter()

	tests := []struct {
		name   string
		params map[string]interface{}
		data   interface{}
		want   string
	}{
		{
			name: "read operation",
			params: map[string]interface{}{
				"operation": "read",
				"file_path": "/path/to/file.txt",
			},
			data: map[string]interface{}{
				"content":     "file content",
				"line_count":  10,
				"total_lines": 10,
			},
			want: "文件: /path/to/file.txt\n行数: 10/10\n\n内容:\nfile content",
		},
		{
			name: "write operation",
			params: map[string]interface{}{
				"operation": "write",
			},
			data: map[string]interface{}{
				"bytes_written": 1024,
			},
			want: "文件写入完成，写入了 1024 字节",
		},
		{
			name: "list operation",
			params: map[string]interface{}{
				"operation":      "list",
				"directory_path": "/path/to/dir",
			},
			data: map[string]interface{}{
				"directories": []interface{}{
					map[string]interface{}{"name": "subdir1"},
				},
				"files": []interface{}{
					map[string]interface{}{"name": "file1.txt", "size": int64(100)},
				},
				"totals": map[string]interface{}{
					"directories": 1,
					"files":       1,
				},
			},
			want: "目录: /path/to/dir\n\n目录:\n  📁 subdir1\n\n文件:\n  📄 file1.txt (100 bytes)\n\n总计: 1 个目录, 1 个文件",
		},
		{
			name: "unknown operation",
			params: map[string]interface{}{
				"operation": "unknown",
			},
			data: map[string]interface{}{},
			want: "文件操作 (unknown) 执行完成",
		},
		{
			name:   "invalid file data",
			params: map[string]interface{}{"operation": "read"},
			data:   "invalid",
			want:   "文件操作 (read) 执行完成",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := core.Result{
				Success: true,
				Data:    tt.data,
			}

			formatted := formatter.Format("file_tools", tt.params, result, 0)

			if formatted != tt.want {
				t.Errorf("Format() = %q, want %q", formatted, tt.want)
			}
		})
	}
}

// TestFormatIDGenerator tests formatting ID generator results.
func TestFormatIDGenerator(t *testing.T) {
	formatter := NewResultFormatter()

	tests := []struct {
		name   string
		params map[string]interface{}
		data   interface{}
		want   string
	}{
		{
			name: "generate UUID",
			params: map[string]interface{}{
				"operation": "generate_uuid",
			},
			data: map[string]interface{}{
				"id": "550e8400-e29b-41d4-a716-446655440000",
			},
			want: "生成的 UUID: 550e8400-e29b-41d4-a716-446655440000",
		},
		{
			name: "generate short ID",
			params: map[string]interface{}{
				"operation": "generate_short_id",
			},
			data: map[string]interface{}{
				"id": "abc123",
			},
			want: "生成的短 ID: abc123",
		},
		{
			name: "unknown operation",
			params: map[string]interface{}{
				"operation": "unknown",
			},
			data: map[string]interface{}{},
			want: "ID生成完成",
		},
		{
			name:   "invalid ID data",
			params: map[string]interface{}{"operation": "generate_uuid"},
			data:   "invalid",
			want:   "ID生成工具执行完成",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := core.Result{
				Success: true,
				Data:    tt.data,
			}

			formatted := formatter.Format("id_generator", tt.params, result, 0)

			if formatted != tt.want {
				t.Errorf("Format() = %q, want %q", formatted, tt.want)
			}
		})
	}
}

// TestFormatHTTPRequest tests formatting HTTP request results.
func TestFormatHTTPRequest(t *testing.T) {
	formatter := NewResultFormatter()

	tests := []struct {
		name   string
		params map[string]interface{}
		data   interface{}
		want   string
	}{
		{
			name: "successful request",
			params: map[string]interface{}{
				"url": "https://api.example.com/data",
			},
			data: map[string]interface{}{
				"status_code": 200.0,
			},
			want: "HTTP 请求完成: https://api.example.com/data (状态码: 200)",
		},
		{
			name: "request without status code",
			params: map[string]interface{}{
				"url": "https://api.example.com/data",
			},
			data: map[string]interface{}{},
			want: "HTTP 请求完成",
		},
		{
			name:   "invalid HTTP data",
			params: map[string]interface{}{"url": "https://api.example.com"},
			data:   "invalid",
			want:   "HTTP 请求完成",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := core.Result{
				Success: true,
				Data:    tt.data,
			}

			formatted := formatter.Format("http_request", tt.params, result, 0)

			if formatted != tt.want {
				t.Errorf("Format() = %q, want %q", formatted, tt.want)
			}
		})
	}
}

// TestFormatTextProcessor tests formatting text processor results.
func TestFormatTextProcessor(t *testing.T) {
	formatter := NewResultFormatter()

	params := map[string]interface{}{
		"operation": "parse",
	}

	result := core.Result{
		Success: true,
		Data:    map[string]interface{}{},
	}

	formatted := formatter.Format("text_processor", params, result, 0)

	if formatted == "" {
		t.Error("Format() should not return empty string")
	}

	if !contains(formatted, "文本处理操作") {
		t.Error("Format() should contain text processing indicator")
	}
}

// TestFormatJSONTools tests formatting JSON tools results.
func TestFormatJSONTools(t *testing.T) {
	formatter := NewResultFormatter()

	params := map[string]interface{}{
		"operation": "parse",
	}

	result := core.Result{
		Success: true,
		Data:    map[string]interface{}{},
	}

	formatted := formatter.Format("json_tools", params, result, 0)

	if formatted == "" {
		t.Error("Format() should not return empty string")
	}

	if !contains(formatted, "JSON 处理操作") {
		t.Error("Format() should contain JSON processing indicator")
	}
}

// TestFormatDataValidation tests formatting data validation results.
func TestFormatDataValidation(t *testing.T) {
	formatter := NewResultFormatter()

	tests := []struct {
		name string
		data interface{}
		want string
	}{
		{
			name: "valid data",
			data: map[string]interface{}{
				"valid": true,
			},
			want: "数据验证通过：格式正确",
		},
		{
			name: "invalid data",
			data: map[string]interface{}{
				"valid": false,
			},
			want: "数据验证失败：格式不正确",
		},
		{
			name: "invalid validation data",
			data: "invalid",
			want: "数据验证 (validate) 执行完成",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := map[string]interface{}{
				"operation": "validate",
			}

			result := core.Result{
				Success: true,
				Data:    tt.data,
			}

			formatted := formatter.Format("data_validation", params, result, 0)

			if formatted != tt.want {
				t.Errorf("Format() = %q, want %q", formatted, tt.want)
			}
		})
	}
}

// TestFormatDataTransform tests formatting data transform results.
func TestFormatDataTransform(t *testing.T) {
	formatter := NewResultFormatter()

	params := map[string]interface{}{
		"operation": "transform",
	}

	result := core.Result{
		Success: true,
		Data:    map[string]interface{}{},
	}

	formatted := formatter.Format("data_transform", params, result, 0)

	if formatted == "" {
		t.Error("Format() should not return empty string")
	}

	if !contains(formatted, "数据转换操作") {
		t.Error("Format() should contain data transform indicator")
	}
}

// TestFormatRegexTool tests formatting regex tool results.
func TestFormatRegexTool(t *testing.T) {
	formatter := NewResultFormatter()

	tests := []struct {
		name string
		data interface{}
		want string
	}{
		{
			name: "match found",
			data: map[string]interface{}{
				"matched": true,
			},
			want: "正则匹配成功",
		},
		{
			name: "match not found",
			data: map[string]interface{}{
				"matched": false,
			},
			want: "正则匹配失败",
		},
		{
			name: "invalid regex data",
			data: "invalid",
			want: "正则操作 (match) 执行完成",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := map[string]interface{}{
				"operation": "match",
			}

			result := core.Result{
				Success: true,
				Data:    tt.data,
			}

			formatted := formatter.Format("regex_tool", params, result, 0)

			if formatted != tt.want {
				t.Errorf("Format() = %q, want %q", formatted, tt.want)
			}
		})
	}
}

// TestFormatLogAnalyzer tests formatting log analyzer results.
func TestFormatLogAnalyzer(t *testing.T) {
	formatter := NewResultFormatter()

	tests := []struct {
		name   string
		params map[string]interface{}
		data   interface{}
		want   string
	}{
		{
			name: "parse log",
			params: map[string]interface{}{
				"operation": "parse_log",
			},
			data: map[string]interface{}{
				"log_type": "access",
			},
			want: "日志解析完成，类型：access",
		},
		{
			name: "find errors",
			params: map[string]interface{}{
				"operation": "find_errors",
			},
			data: map[string]interface{}{
				"error_count": 5,
			},
			want: "发现 5 个错误",
		},
		{
			name: "extract metrics",
			params: map[string]interface{}{
				"operation": "extract_metrics",
			},
			data: map[string]interface{}{
				"metrics": map[string]interface{}{
					"cpu":    80.0,
					"memory": 60.0,
				},
			},
			want: "提取了 2 个指标",
		},
		{
			name: "unknown operation",
			params: map[string]interface{}{
				"operation": "unknown",
			},
			data: map[string]interface{}{},
			want: "日志分析操作 (unknown) 执行完成",
		},
		{
			name:   "invalid log data",
			params: map[string]interface{}{"operation": "parse_log"},
			data:   "invalid",
			want:   "日志分析操作 (parse_log) 执行完成",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := core.Result{
				Success: true,
				Data:    tt.data,
			}

			formatted := formatter.Format("log_analyzer", tt.params, result, 0)

			if formatted != tt.want {
				t.Errorf("Format() = %q, want %q", formatted, tt.want)
			}
		})
	}
}

// TestFormatCodeRunner tests formatting code runner results.
func TestFormatCodeRunner(t *testing.T) {
	formatter := NewResultFormatter()

	tests := []struct {
		name   string
		params map[string]interface{}
		data   interface{}
		want   string
	}{
		{
			name: "run python with short output",
			params: map[string]interface{}{
				"operation": "run_python",
			},
			data: map[string]interface{}{
				"output": "Hello, World!",
			},
			want: "Python 执行输出:\nHello, World!",
		},
		{
			name: "run python with long output",
			params: map[string]interface{}{
				"operation": "run_python",
			},
			data: map[string]interface{}{
				"output": string(make([]byte, 200)),
			},
			want: "Python 执行输出（前100字符）:\n" + string(make([]byte, 100)) + "...",
		},
		{
			name: "run javascript with short output",
			params: map[string]interface{}{
				"operation": "run_js",
			},
			data: map[string]interface{}{
				"output": "console.log('Hello');",
			},
			want: "JavaScript 执行输出:\nconsole.log('Hello');",
		},
		{
			name: "run javascript with long output",
			params: map[string]interface{}{
				"operation": "run_js",
			},
			data: map[string]interface{}{
				"output": string(make([]byte, 200)),
			},
			want: "JavaScript 执行输出（前100字符）:\n" + string(make([]byte, 100)) + "...",
		},
		{
			name: "unknown operation",
			params: map[string]interface{}{
				"operation": "unknown",
			},
			data: map[string]interface{}{},
			want: "代码执行完成",
		},
		{
			name:   "invalid code data",
			params: map[string]interface{}{"operation": "run_python"},
			data:   "invalid",
			want:   "代码执行 (run_python) 完成",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := core.Result{
				Success: true,
				Data:    tt.data,
			}

			formatted := formatter.Format("code_runner", tt.params, result, 0)

			if formatted != tt.want {
				t.Errorf("Format() = %q, want %q", formatted, tt.want)
			}
		})
	}
}

// TestFormatDefault tests formatting unknown tool results.
func TestFormatDefault(t *testing.T) {
	formatter := NewResultFormatter()

	params := map[string]interface{}{
		"key": "value",
	}

	result := core.Result{
		Success: true,
		Data:    map[string]interface{}{"result": "data"},
	}

	formatted := formatter.Format("unknown_tool", params, result, 0)

	if formatted == "" {
		t.Error("Format() should not return empty string")
	}

	if !contains(formatted, "unknown_tool") {
		t.Error("Format() should contain tool name")
	}

	if !contains(formatted, "执行完成") {
		t.Error("Format() should contain completion indicator")
	}
}

// TestConvertToMapSlice tests converting slice types.
func TestConvertToMapSlice(t *testing.T) {
	tests := []struct {
		name    string
		data    interface{}
		wantLen int
		wantNil bool
	}{
		{
			name: "direct map slice",
			data: []map[string]interface{}{
				{"key": "value1"},
				{"key": "value2"},
			},
			wantLen: 2,
			wantNil: false,
		},
		{
			name: "interface slice",
			data: []interface{}{
				map[string]interface{}{"key": "value1"},
				map[string]interface{}{"key": "value2"},
			},
			wantLen: 2,
			wantNil: false,
		},
		{
			name:    "empty slice",
			data:    []interface{}{},
			wantLen: 0,
			wantNil: false,
		},
		{
			name:    "nil data",
			data:    nil,
			wantLen: 0,
			wantNil: true,
		},
		{
			name:    "invalid type",
			data:    "invalid",
			wantLen: 0,
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertToMapSlice(tt.data)

			if tt.wantNil {
				if result != nil {
					t.Error("convertToMapSlice() should return nil")
				}
			} else {
				if result == nil {
					t.Error("convertToMapSlice() should not return nil")
				}

				if len(result) != tt.wantLen {
					t.Errorf("convertToMapSlice() length = %d, want %d", len(result), tt.wantLen)
				}
			}
		})
	}
}

// TestFormatWithMetadata tests formatting results with metadata.
func TestFormatWithMetadata(t *testing.T) {
	formatter := NewResultFormatter()

	result := core.Result{
		Success: true,
		Data:    map[string]interface{}{"key": "value"},
		Metadata: map[string]interface{}{
			"custom_field": "custom_value",
		},
	}

	formatted := formatter.Format("test_tool", map[string]interface{}{}, result, 0)

	if formatted == "" {
		t.Error("Format() should not return empty string")
	}
}

// TestFormatWithNilData tests formatting results with nil data.
func TestFormatWithNilData(t *testing.T) {
	formatter := NewResultFormatter()

	result := core.Result{
		Success: true,
		Data:    nil,
	}

	formatted := formatter.Format("test_tool", map[string]interface{}{}, result, 0)

	if formatted == "" {
		t.Error("Format() should not return empty string")
	}
}

// TestFormatWithEmptyParams tests formatting results with empty parameters.
func TestFormatWithEmptyParams(t *testing.T) {
	formatter := NewResultFormatter()

	result := core.Result{
		Success: true,
		Data:    map[string]interface{}{"key": "value"},
	}

	formatted := formatter.Format("test_tool", map[string]interface{}{}, result, 0)

	if formatted == "" {
		t.Error("Format() should not return empty string")
	}
}

// TestFormatWithNilParams tests formatting results with nil parameters.
func TestFormatWithNilParams(t *testing.T) {
	formatter := NewResultFormatter()

	result := core.Result{
		Success: true,
		Data:    map[string]interface{}{"key": "value"},
	}

	formatted := formatter.Format("test_tool", nil, result, 0)

	if formatted == "" {
		t.Error("Format() should not return empty string")
	}
}

// contains checks if a string contains a substring.
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
