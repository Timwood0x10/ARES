package builtin

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Timwood0x10/ares/internal/tools/resources/base"
	"github.com/Timwood0x10/ares/internal/tools/resources/core"
)

// DataTransform provides data transformation capabilities.
type DataTransform struct {
	*base.BaseTool
}

// NewDataTransform creates a new DataTransform tool.
func NewDataTransform() *DataTransform {
	params := &core.ParameterSchema{
		Type: "object",
		Properties: map[string]*core.Parameter{
			"operation": {
				Type:        "string",
				Description: "Operation to perform (csv_to_json, json_to_csv, flatten_json)",
				Enum:        []interface{}{"csv_to_json", "json_to_csv", "flatten_json"},
			},
			"data": {
				Type:        "string",
				Description: "Data to transform (CSV or JSON string)",
			},
			"delimiter": {
				Type:        "string",
				Description: "CSV delimiter (default: ',')",
				Default:     ",",
			},
			"has_header": {
				Type:        "boolean",
				Description: "CSV has header row (default: true)",
				Default:     true,
			},
			"separator": {
				Type:        "string",
				Description: "Flattened key separator (default: '.')",
				Default:     ".",
			},
		},
		Required: []string{"operation", "data"},
	}

	return &DataTransform{
		BaseTool: base.NewBaseToolWithCapabilities("data_transform", "Transform data between CSV, JSON, and flattened formats", core.CategoryData, []core.Capability{core.CapabilityText}, params),
	}
}

// Execute performs the data transformation operation.
func (t *DataTransform) Execute(ctx context.Context, params map[string]interface{}) (core.Result, error) {
	operation, ok := params["operation"].(string)
	if !ok || operation == "" {
		return core.NewErrorResult("operation is required"), nil
	}

	data, ok := params["data"].(string)
	if !ok || data == "" {
		return core.NewErrorResult("data is required"), nil
	}

	switch operation {
	case "csv_to_json":
		delimiter := getStringWithDefault(params, "delimiter", ",")
		hasHeader := getBoolWithDefault(params, "has_header", true)
		return t.csvToJSON(ctx, data, delimiter, hasHeader)
	case "json_to_csv":
		delimiter := getStringWithDefault(params, "delimiter", ",")
		return t.jsonToCSV(ctx, data, delimiter)
	case "flatten_json":
		separator := getStringWithDefault(params, "separator", ".")
		return t.flattenJSON(ctx, data, separator)
	default:
		return core.NewErrorResult(fmt.Sprintf("unsupported operation: %s", operation)), nil
	}
}

// csvToJSON converts CSV data to JSON array.
func (t *DataTransform) csvToJSON(ctx context.Context, csvData, delimiter string, hasHeader bool) (core.Result, error) {
	reader := csv.NewReader(strings.NewReader(csvData))
	reader.Comma = []rune(delimiter)[0]

	records, err := reader.ReadAll()
	if err != nil {
		return core.NewErrorResult(fmt.Sprintf("failed to parse CSV: %v", err)), nil
	}

	if len(records) == 0 {
		return core.NewResult(true, map[string]interface{}{
			"operation": "csv_to_json",
			"data":      []interface{}{},
			"row_count": 0,
		}), nil
	}

	var result []interface{}

	if hasHeader {
		if len(records) < 1 {
			return core.NewErrorResult("CSV is empty"), nil
		}

		headers := records[0]
		dataRows := records[1:]

		result = make([]interface{}, 0, len(dataRows))
		for _, row := range dataRows {
			obj := make(map[string]interface{})
			for i, header := range headers {
				if i < len(row) {
					obj[header] = row[i]
				} else {
					obj[header] = ""
				}
			}
			result = append(result, obj)
		}
	} else {
		result = make([]interface{}, 0, len(records))
		for _, row := range records {
			result = append(result, row)
		}
	}

	return core.NewResult(true, map[string]interface{}{
		"operation": "csv_to_json",
		"data":      result,
		"row_count": len(result),
	}), nil
}

// jsonToCSV converts JSON array to CSV.
func (t *DataTransform) jsonToCSV(ctx context.Context, jsonData, delimiter string) (core.Result, error) {
	var js interface{}
	if err := json.Unmarshal([]byte(jsonData), &js); err != nil {
		return core.NewErrorResult(fmt.Sprintf("invalid JSON: %v", err)), nil
	}

	// Expect array of objects
	records, ok := js.([]interface{})
	if !ok {
		return core.NewErrorResult("JSON must be an array of objects"), nil
	}

	if len(records) == 0 {
		return core.NewResult(true, map[string]interface{}{
			"operation": "json_to_csv",
			"data":      "",
			"row_count": 0,
		}), nil
	}

	// Extract all unique keys
	keySet := make(map[string]bool)
	for _, record := range records {
		obj, ok := record.(map[string]interface{})
		if !ok {
			continue
		}
		for key := range obj {
			keySet[key] = true
		}
	}

	// Sort keys for consistent output (the comment used to say "sort" while
	// the code kept map order — nondeterministic columns across runs).
	keys := make([]string, 0, len(keySet))
	for key := range keySet {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	// Build CSV
	var csvBuilder strings.Builder

	// Write header. Keys go through the SAME quoting rule as values: a key
	// containing the delimiter (or a quote/newline) previously produced a
	// header with more columns than every data row — silently corrupted CSV.
	header := make([]string, len(keys))
	for i, key := range keys {
		header[i] = csvField(key, delimiter)
	}
	csvBuilder.WriteString(strings.Join(header, delimiter) + "\n")

	// Write data rows
	for _, record := range records {
		obj, ok := record.(map[string]interface{})
		if !ok {
			continue
		}

		row := make([]string, len(keys))
		for i, key := range keys {
			value := ""
			if val, exists := obj[key]; exists {
				value = fmt.Sprintf("%v", val)
			}
			row[i] = csvField(value, delimiter)
		}

		csvBuilder.WriteString(strings.Join(row, delimiter) + "\n")
	}

	return core.NewResult(true, map[string]interface{}{
		"operation": "json_to_csv",
		"data":      csvBuilder.String(),
		"row_count": len(records),
	}), nil
}

// csvField applies RFC 4180 quoting to one CSV field: fields containing the
// delimiter, a double quote, or a line break are wrapped in double quotes
// with embedded quotes doubled. (Newlines previously broke the row layout
// unquoted.)
func csvField(value, delimiter string) string {
	if strings.ContainsAny(value, delimiter+"\"\r\n") {
		return "\"" + strings.ReplaceAll(value, "\"", "\"\"") + "\""
	}
	return value
}

// flattenJSON flattens nested JSON object.
func (t *DataTransform) flattenJSON(ctx context.Context, jsonData, separator string) (core.Result, error) {
	var js interface{}
	if err := json.Unmarshal([]byte(jsonData), &js); err != nil {
		return core.NewErrorResult(fmt.Sprintf("invalid JSON: %v", err)), nil
	}

	flattened := t.flatten(js, "", separator)

	return core.NewResult(true, map[string]interface{}{
		"operation": "flatten_json",
		"data":      flattened,
		"separator": separator,
	}), nil
}

// flatten recursively flattens a nested structure.
func (t *DataTransform) flatten(data interface{}, prefix string, separator string) map[string]interface{} {
	result := make(map[string]interface{})

	switch v := data.(type) {
	case map[string]interface{}:
		for key, value := range v {
			newKey := key
			if prefix != "" {
				newKey = prefix + separator + key
			}
			for k, val := range t.flatten(value, newKey, separator) {
				result[k] = val
			}
		}

	case []interface{}:
		for i, value := range v {
			newKey := fmt.Sprintf("%d", i)
			if prefix != "" {
				newKey = prefix + separator + newKey
			}
			for k, val := range t.flatten(value, newKey, separator) {
				result[k] = val
			}
		}

	default:
		result[prefix] = v
	}

	return result
}

func (t *DataTransform) IsIdempotent() bool { return true }

// getStringWithDefault safely gets a string parameter with default.
func getStringWithDefault(params map[string]interface{}, key, defaultVal string) string {
	if val, ok := params[key].(string); ok && val != "" {
		return val
	}
	return defaultVal
}

// getBoolWithDefault safely gets a boolean parameter with default.
func getBoolWithDefault(params map[string]interface{}, key string, defaultVal bool) bool {
	if val, ok := params[key].(bool); ok {
		return val
	}
	return defaultVal
}
