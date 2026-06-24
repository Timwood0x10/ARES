package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Timwood0x10/ares/internal/tools/resources/base"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// LogAnalyzer provides log parsing and analysis capabilities.
type LogAnalyzer struct {
	*base.BaseTool
}

// NewLogAnalyzer creates a new LogAnalyzer tool.
func NewLogAnalyzer() *LogAnalyzer {
	params := &core.ParameterSchema{
		Type: "object",
		Properties: map[string]*core.Parameter{
			"operation": {
				Type:        "string",
				Description: "Operation to perform (parse_log, find_errors, extract_metrics)",
				Enum:        []interface{}{"parse_log", "find_errors", "extract_metrics"},
			},
			"log_content": {
				Type:        "string",
				Description: "Log content to analyze",
			},
			"log_format": {
				Type:        "string",
				Description: "Log format (default: auto-detect). Supported: json, common, combined",
			},
			"error_patterns": {
				Type:        "array",
				Description: "Custom error patterns for find_errors operation",
			},
			"metric_patterns": {
				Type:        "array",
				Description: "Metric patterns for extract_metrics operation",
			},
		},
		Required: []string{"operation", "log_content"},
	}

	return &LogAnalyzer{
		BaseTool: base.NewBaseToolWithCategory("log_analyzer", "Parse logs, find errors, and extract metrics", core.CategoryCore, params),
	}
}

// Execute performs the log analysis operation.
func (t *LogAnalyzer) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	operation, ok := params["operation"].(string)
	if !ok || operation == "" {
		return core.NewErrorResult("operation is required"), nil
	}

	logContent, ok := params["log_content"].(string)
	if !ok || logContent == "" {
		return core.NewErrorResult("log_content is required"), nil
	}

	switch operation {
	case "parse_log":
		logFormat := getString(params, "log_format")
		return t.parseLog(ctx, logContent, logFormat)
	case "find_errors":
		errorPatterns := getStringSlice(params, "error_patterns")
		return t.findErrors(ctx, logContent, errorPatterns)
	case "extract_metrics":
		metricPatterns := getStringSlice(params, "metric_patterns")
		return t.extractMetrics(ctx, logContent, metricPatterns)
	default:
		return core.NewErrorResult(fmt.Sprintf("unsupported operation: %s", operation)), nil
	}
}

// parseLog parses log content into structured format.
func (t *LogAnalyzer) parseLog(ctx context.Context, logContent, logFormat string) (core.Result, error) {
	lines := strings.Split(logContent, "\n")
	parsed := make([]map[string]interface{}, 0, len(lines))

	format := strings.ToLower(logFormat)
	if format == "" {
		format = "auto"
	}

	// Auto-detect format
	if format == "auto" {
		if strings.Contains(logContent, "\"timestamp\"") || strings.Contains(logContent, "{\"") {
			format = "json"
		} else if strings.Contains(logContent, " - - ") {
			format = "common"
		} else {
			format = "simple"
		}
	}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var entry map[string]interface{}

		switch format {
		case "json":
			entry = t.parseJSONLog(line)
		case "common":
			entry = t.parseCommonLog(line)
		case "combined":
			entry = t.parseCombinedLog(line)
		default:
			entry = t.parseSimpleLog(line)
		}

		if entry != nil {
			parsed = append(parsed, entry)
		}
	}

	return core.NewResult(true, map[string]interface{}{
		"operation": "parse_log",
		"format":    format,
		"entries":   parsed,
		"count":     len(parsed),
	}), nil
}

// parseJSONLog parses JSON formatted log line.
func (t *LogAnalyzer) parseJSONLog(line string) map[string]interface{} {
	var js map[string]interface{}
	err := json.Unmarshal([]byte(line), &js)
	if err != nil {
		return map[string]interface{}{
			"raw":   line,
			"error": "failed to parse JSON",
		}
	}

	// Add parsed timestamp
	if ts, ok := js["timestamp"].(string); ok {
		if parsedTime, err := time.Parse(time.RFC3339, ts); err == nil {
			js["parsed_timestamp"] = parsedTime
		}
	}

	return js
}

// parseCommonLog parses Common Log Format (CLF).
func (t *LogAnalyzer) parseCommonLog(line string) map[string]interface{} {
	// Example: 127.0.0.1 - frank [10/Oct/2000:13:55:36 -0700] "GET /apache_pb.gif HTTP/1.0" 200 2326
	pattern := `^(\S+) (\S+) (\S+) \[([^\]]+)\] "(\S+) (\S+) (\S+)" (\d+) (\d+)$`
	re := regexp.MustCompile(pattern)
	matches := re.FindStringSubmatch(line)

	if len(matches) < 10 {
		return map[string]interface{}{
			"raw":   line,
			"error": "failed to parse common log format",
		}
	}

	return map[string]interface{}{
		"ip":        matches[1],
		"identity":  matches[2],
		"user":      matches[3],
		"timestamp": matches[4],
		"method":    matches[5],
		"path":      matches[6],
		"protocol":  matches[7],
		"status":    matches[8],
		"bytes":     matches[9],
		"raw":       line,
	}
}

// parseCombinedLog parses Combined Log Format.
func (t *LogAnalyzer) parseCombinedLog(line string) map[string]interface{} {
	// Example: 127.0.0.1 - frank [10/Oct/2000:13:55:36 -0700] "GET /apache_pb.gif HTTP/1.0" 200 2326 "http://www.example.com/start.html" "Mozilla/5.0"
	pattern := `^(\S+) (\S+) (\S+) \[([^\]]+)\] "(\S+) (\S+) (\S+)" (\d+) (\d+) "([^"]*)" "([^"]*)"$`
	re := regexp.MustCompile(pattern)
	matches := re.FindStringSubmatch(line)

	if len(matches) < 12 {
		return map[string]interface{}{
			"raw":   line,
			"error": "failed to parse combined log format",
		}
	}

	return map[string]interface{}{
		"ip":         matches[1],
		"identity":   matches[2],
		"user":       matches[3],
		"timestamp":  matches[4],
		"method":     matches[5],
		"path":       matches[6],
		"protocol":   matches[7],
		"status":     matches[8],
		"bytes":      matches[9],
		"referrer":   matches[10],
		"user_agent": matches[11],
		"raw":        line,
	}
}

// parseSimpleLog parses simple log format.
func (t *LogAnalyzer) parseSimpleLog(line string) map[string]interface{} {
	// Try to extract timestamp and level
	timestamp := ""
	level := "INFO"
	message := line

	// Common timestamp patterns
	timePatterns := []string{
		`\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}`,
		`\d{2}/\w{3}/\d{4}:\d{2}:\d{2}:\d{2}`,
		`\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}`,
	}

	for _, pattern := range timePatterns {
		re := regexp.MustCompile(pattern)
		if match := re.FindString(line); match != "" {
			timestamp = match
			message = strings.Replace(line, match, "", 1)
			break
		}
	}

	// Extract log level
	levelPatterns := map[string]string{
		"ERROR":   "ERROR",
		"ERR":     "ERROR",
		"FATAL":   "FATAL",
		"WARN":    "WARNING",
		"WARNING": "WARNING",
		"INFO":    "INFO",
		"DEBUG":   "DEBUG",
		"TRACE":   "TRACE",
	}

	for pattern, lvl := range levelPatterns {
		if strings.Contains(line, pattern) {
			level = lvl
			break
		}
	}

	return map[string]interface{}{
		"timestamp": timestamp,
		"level":     level,
		"message":   strings.TrimSpace(message),
		"raw":       line,
	}
}

// findErrors finds error lines in log content.
func (t *LogAnalyzer) findErrors(ctx context.Context, logContent string, customPatterns []string) (core.Result, error) {
	// Default error patterns
	defaultPatterns := []string{
		`(?i)error`,
		`(?i)exception`,
		`(?i)failed`,
		`(?i)fatal`,
		`(?i)panic`,
		`(?i)stack trace`,
		`(?i)timeout`,
		`(?i)denied`,
	}

	// Use custom patterns if provided
	patterns := defaultPatterns
	if len(customPatterns) > 0 {
		patterns = customPatterns
	}

	// Compile regex patterns
	regexes := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		re, err := regexp.Compile(pattern)
		if err != nil {
			continue
		}
		regexes = append(regexes, re)
	}

	// Find error lines
	errors := make([]map[string]interface{}, 0)
	for i, line := range strings.Split(logContent, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		for _, re := range regexes {
			if re.MatchString(line) {
				errors = append(errors, map[string]interface{}{
					"line_number": i + 1,
					"line":        line,
					"matched_by":  re.String(),
				})
				break
			}
		}
	}

	return core.NewResult(true, map[string]interface{}{
		"operation":     "find_errors",
		"error_count":   len(errors),
		"errors":        errors,
		"patterns_used": patterns,
	}), nil
}

// extractMetrics extracts metrics from log content.
func (t *LogAnalyzer) extractMetrics(ctx context.Context, logContent string, customPatterns []string) (core.Result, error) {
	// Default metric patterns
	defaultPatterns := []struct {
		name    string
		pattern string
	}{
		{"response_time_ms", `(\d+(?:\.\d+)?)\s*ms`},
		{"latency_seconds", `(\d+(?:\.\d+)?)\s*s`},
		{"request_count", `(\d+)\s*requests`},
		{"memory_mb", `(\d+(?:\.\d+)?)\s*MB`},
		{"cpu_percent", `(\d+(?:\.\d+)?)%`},
		{"throughput_rps", `(\d+(?:\.\d+)?)\s*rps`},
	}

	// Parse custom patterns if provided
	patterns := defaultPatterns
	if len(customPatterns) > 0 {
		patterns = make([]struct {
			name    string
			pattern string
		}, 0, len(customPatterns))

		for _, cp := range customPatterns {
			// Parse custom pattern: "name:pattern"
			parts := strings.SplitN(cp, ":", 2)
			if len(parts) == 2 {
				patterns = append(patterns, struct {
					name    string
					pattern string
				}{name: parts[0], pattern: parts[1]})
			}
		}
	}

	// Extract metrics
	metrics := make(map[string][]float64)

	for _, mp := range patterns {
		re, err := regexp.Compile(mp.pattern)
		if err != nil {
			continue
		}

		matches := re.FindAllStringSubmatch(logContent, -1)
		for _, match := range matches {
			if len(match) > 1 {
				var value float64
				if _, err := fmt.Sscanf(match[1], "%f", &value); err == nil {
					metrics[mp.name] = append(metrics[mp.name], value)
				}
			}
		}
	}

	// Calculate statistics
	statistics := make(map[string]interface{})
	for name, values := range metrics {
		if len(values) == 0 {
			continue
		}

		sum := 0.0
		min := values[0]
		max := values[0]

		for _, v := range values {
			sum += v
			if v < min {
				min = v
			}
			if v > max {
				max = v
			}
		}

		avg := sum / float64(len(values))

		statistics[name] = map[string]interface{}{
			"count": len(values),
			"min":   min,
			"max":   max,
			"avg":   avg,
			"sum":   sum,
		}
	}

	return core.NewResult(true, map[string]interface{}{
		"operation":  "extract_metrics",
		"metrics":    metrics,
		"statistics": statistics,
	}), nil
}

func (t *LogAnalyzer) IsIdempotent() bool { return true }
