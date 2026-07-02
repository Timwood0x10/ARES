package builtin

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLogAnalyzer_New(t *testing.T) {
	la := NewLogAnalyzer()
	assert.NotNil(t, la)
	assert.Equal(t, "log_analyzer", la.Name())
}

func TestLogAnalyzer_Execute_MissingOperation(t *testing.T) {
	la := NewLogAnalyzer()
	result, err := la.Execute(context.Background(), map[string]interface{}{
		"log_content": "test log",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestLogAnalyzer_Execute_MissingLogContent(t *testing.T) {
	la := NewLogAnalyzer()
	result, err := la.Execute(context.Background(), map[string]interface{}{
		"operation": "parse_log",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestLogAnalyzer_Execute_UnsupportedOperation(t *testing.T) {
	la := NewLogAnalyzer()
	result, err := la.Execute(context.Background(), map[string]interface{}{
		"operation":   "invalid",
		"log_content": "test",
	})
	assert.NoError(t, err)
	assert.False(t, result.Success)
}

func TestLogAnalyzer_ParseLog_AutoDetectJSON(t *testing.T) {
	la := NewLogAnalyzer()
	result, err := la.Execute(context.Background(), map[string]interface{}{
		"operation":   "parse_log",
		"log_content": `{"timestamp": "2024-01-01T00:00:00Z", "level": "INFO", "msg": "started"}`,
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.Equal(t, "json", r["format"])
	assert.Equal(t, 1, r["count"])
}

func TestLogAnalyzer_ParseLog_AutoDetectCommon(t *testing.T) {
	la := NewLogAnalyzer()
	result, err := la.Execute(context.Background(), map[string]interface{}{
		"operation":   "parse_log",
		"log_content": "127.0.0.1 - - [10/Oct/2000:13:55:36 -0700] \"GET /apache_pb.gif HTTP/1.0\" 200 2326",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.Equal(t, "common", r["format"])
}

func TestLogAnalyzer_ParseLog_AutoDetectSimple(t *testing.T) {
	la := NewLogAnalyzer()
	result, err := la.Execute(context.Background(), map[string]interface{}{
		"operation":   "parse_log",
		"log_content": "2024-01-01 12:00:00 INFO Server started",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.Equal(t, "simple", r["format"])
}

func TestLogAnalyzer_ParseLog_ExplicitFormat(t *testing.T) {
	la := NewLogAnalyzer()
	result, err := la.Execute(context.Background(), map[string]interface{}{
		"operation":   "parse_log",
		"log_content": "plain text log line",
		"log_format":  "simple",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
}

func TestLogAnalyzer_ParseLog_JSONInvalid(t *testing.T) {
	la := NewLogAnalyzer()
	result, err := la.Execute(context.Background(), map[string]interface{}{
		"operation":   "parse_log",
		"log_content": `{invalid json}`,
		"log_format":  "json",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	entries := r["entries"].([]map[string]interface{})
	assert.Equal(t, 1, len(entries))
}

func TestLogAnalyzer_ParseLog_CommonLogFormat(t *testing.T) {
	la := NewLogAnalyzer()
	result, err := la.Execute(context.Background(), map[string]interface{}{
		"operation":   "parse_log",
		"log_content": `192.168.1.1 - - [01/Jan/2024:12:00:00 +0000] "POST /api/data HTTP/1.1" 200 1234`,
		"log_format":  "common",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	entries := r["entries"].([]map[string]interface{})
	assert.Equal(t, 1, len(entries))
	assert.Equal(t, "192.168.1.1", entries[0]["ip"])
	assert.Equal(t, "200", entries[0]["status"])
}

func TestLogAnalyzer_ParseLog_CombinedLogFormat(t *testing.T) {
	la := NewLogAnalyzer()
	result, err := la.Execute(context.Background(), map[string]interface{}{
		"operation":   "parse_log",
		"log_content": `127.0.0.1 - frank [10/Oct/2000:13:55:36 -0700] "GET /apache_pb.gif HTTP/1.0" 200 2326 "http://www.example.com/start.html" "Mozilla/5.0"`,
		"log_format":  "combined",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	entries := r["entries"].([]map[string]interface{})
	assert.Equal(t, 1, len(entries))
	assert.Equal(t, "http://www.example.com/start.html", entries[0]["referrer"])
}

func TestLogAnalyzer_ParseLog_CombinedLogInvalid(t *testing.T) {
	la := NewLogAnalyzer()
	result, err := la.Execute(context.Background(), map[string]interface{}{
		"operation":   "parse_log",
		"log_content": "bad line",
		"log_format":  "combined",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	entries := r["entries"].([]map[string]interface{})
	assert.Equal(t, 1, len(entries))
	assert.Contains(t, entries[0]["error"], "failed to parse")
}

func TestLogAnalyzer_ParseLog_CommonLogInvalid(t *testing.T) {
	la := NewLogAnalyzer()
	result, err := la.Execute(context.Background(), map[string]interface{}{
		"operation":   "parse_log",
		"log_content": "short line",
		"log_format":  "common",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
}

func TestLogAnalyzer_ParseLog_EmptyLines(t *testing.T) {
	la := NewLogAnalyzer()
	result, err := la.Execute(context.Background(), map[string]interface{}{
		"operation":   "parse_log",
		"log_content": "line1\n\n\nline2",
		"log_format":  "simple",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
}

func TestLogAnalyzer_FindErrors_DefaultPatterns(t *testing.T) {
	la := NewLogAnalyzer()
	result, err := la.Execute(context.Background(), map[string]interface{}{
		"operation":   "find_errors",
		"log_content": "INFO: all good\nERROR: something broke\nWARN: maybe an issue",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.Equal(t, 1, r["error_count"])
}

func TestLogAnalyzer_FindErrors_CustomPatterns(t *testing.T) {
	la := NewLogAnalyzer()
	result, err := la.Execute(context.Background(), map[string]interface{}{
		"operation":      "find_errors",
		"log_content":    "line with custom_pattern",
		"error_patterns": []interface{}{"custom_pattern"},
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.Equal(t, 1, r["error_count"])
}

func TestLogAnalyzer_FindErrors_NoMatches(t *testing.T) {
	la := NewLogAnalyzer()
	result, err := la.Execute(context.Background(), map[string]interface{}{
		"operation":   "find_errors",
		"log_content": "INFO: all good\nDEBUG: nothing wrong",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	assert.Equal(t, 0, r["error_count"])
}

func TestLogAnalyzer_ExtractMetrics_DefaultPatterns(t *testing.T) {
	la := NewLogAnalyzer()
	result, err := la.Execute(context.Background(), map[string]interface{}{
		"operation":   "extract_metrics",
		"log_content": "response time: 150ms, CPU: 45%",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	metrics := r["metrics"].(map[string][]float64)
	assert.Contains(t, metrics, "response_time_ms")
	assert.Contains(t, metrics, "cpu_percent")
}

func TestLogAnalyzer_ExtractMetrics_CustomPatterns(t *testing.T) {
	la := NewLogAnalyzer()
	result, err := la.Execute(context.Background(), map[string]interface{}{
		"operation":       "extract_metrics",
		"log_content":     "custom_value: 42",
		"metric_patterns": []interface{}{"my_metric:(\\d+)"},
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
	r := result.Data.(map[string]interface{})
	metrics := r["metrics"].(map[string][]float64)
	assert.Contains(t, metrics, "my_metric")
}

func TestLogAnalyzer_ExtractMetrics_NoData(t *testing.T) {
	la := NewLogAnalyzer()
	result, err := la.Execute(context.Background(), map[string]interface{}{
		"operation":   "extract_metrics",
		"log_content": "just some text without numbers",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
}

func TestLogAnalyzer_IsIdempotent(t *testing.T) {
	la := NewLogAnalyzer()
	assert.True(t, la.IsIdempotent())
}

func TestLogAnalyzer_ParseSimpleLog_TimestampPatterns(t *testing.T) {
	la := NewLogAnalyzer()
	result, err := la.Execute(context.Background(), map[string]interface{}{
		"operation":   "parse_log",
		"log_content": "2024/01/01 12:00:00 INFO Message here\n02/Jan/2024:10:00:00 DEBUG Another",
		"log_format":  "simple",
	})
	assert.NoError(t, err)
	assert.True(t, result.Success)
}
