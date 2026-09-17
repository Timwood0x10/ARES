// Package adapters provides format conversion layer tests.
package adapters

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecretAdapter_ParseFrom_JSON(t *testing.T) {
	adapter := NewSecretAdapter()

	// Test JSON format
	jsonData := `{
		"secrets": [
			{
				"key": "api_key",
				"value": "secret_value_123",
				"expires_at": "2026-12-31T23:59:59Z"
			},
			{
				"key": "db_password",
				"value": "db_secret_456"
			}
		]
	}`

	result, err := adapter.ParseFrom([]byte(jsonData), FormatJSON)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify result is valid JSON
	items, err := adapter.ParseImportData(result)
	require.NoError(t, err)
	assert.Equal(t, 2, len(items))
	assert.Equal(t, "api_key", items[0].Key)
	assert.Equal(t, "secret_value_123", items[0].Value)
	assert.Equal(t, "2026-12-31T23:59:59Z", items[0].ExpiresAt)
}

func TestSecretAdapter_ParseFrom_YAML(t *testing.T) {
	adapter := NewSecretAdapter()

	// Test YAML format
	yamlData := `- key: api_key
  value: secret_value_123
  expires_at: 2026-12-31T23:59:59Z

- key: db_password
  value: db_secret_456
`

	result, err := adapter.ParseFrom([]byte(yamlData), FormatYAML)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify result
	items, err := adapter.ParseImportData(result)
	require.NoError(t, err)
	assert.Equal(t, 2, len(items))
	assert.Equal(t, "api_key", items[0].Key)
	assert.Equal(t, "secret_value_123", items[0].Value)
}

func TestSecretAdapter_ParseFrom_CSV(t *testing.T) {
	adapter := NewSecretAdapter()

	// Test CSV format
	csvData := `key,value,expires_at
api_key,secret_value_123,2026-12-31T23:59:59Z
db_password,db_secret_456,
`

	result, err := adapter.ParseFrom([]byte(csvData), FormatCSV)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify result
	items, err := adapter.ParseImportData(result)
	require.NoError(t, err)
	assert.Equal(t, 2, len(items))
	assert.Equal(t, "api_key", items[0].Key)
	assert.Equal(t, "secret_value_123", items[0].Value)
	assert.Equal(t, "2026-12-31T23:59:59Z", items[0].ExpiresAt)
}

func TestSecretAdapter_ConvertTo_YAML(t *testing.T) {
	adapter := NewSecretAdapter()

	// Test JSON to YAML conversion
	jsonData := `{
		"secrets": [
			{
				"key": "api_key",
				"value": "secret_value_123",
				"expires_at": "2026-12-31T23:59:59Z"
			}
		]
	}`

	result, err := adapter.ConvertTo([]byte(jsonData), FormatYAML)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify result contains YAML format
	resultStr := string(result)
	assert.True(t, strings.Contains(resultStr, "- key: api_key"))
	assert.True(t, strings.Contains(resultStr, "value: secret_value_123"))
}

func TestSecretAdapter_ConvertTo_CSV(t *testing.T) {
	adapter := NewSecretAdapter()

	// Test JSON to CSV conversion
	jsonData := `{
		"secrets": [
			{
				"key": "api_key",
				"value": "secret_value_123",
				"expires_at": "2026-12-31T23:59:59Z"
			},
			{
				"key": "db_password",
				"value": "db_secret_456"
			}
		]
	}`

	result, err := adapter.ConvertTo([]byte(jsonData), FormatCSV)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify result contains CSV format
	resultStr := string(result)
	assert.True(t, strings.Contains(resultStr, "key,value,expires_at"))
	assert.True(t, strings.Contains(resultStr, "api_key,secret_value_123"))
}

func TestSecretAdapter_DetectFormat(t *testing.T) {
	adapter := NewSecretAdapter()

	tests := []struct {
		name     string
		data     string
		expected SecretFormat
	}{
		{
			name:     "JSON object",
			data:     `{"secrets": [{"key": "test", "value": "value"}]}`,
			expected: FormatJSON,
		},
		{
			name:     "JSON array",
			data:     `[{"key": "test", "value": "value"}]`,
			expected: FormatJSON,
		},
		{
			name:     "CSV format",
			data:     "key,value\napi_key,secret_value",
			expected: FormatCSV,
		},
		{
			name:     "YAML format",
			data:     "- key: api_key\n  value: secret_value",
			expected: FormatYAML,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := adapter.DetectFormat([]byte(tt.data))
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSecretAdapter_ParseFrom_UnsupportedFormat(t *testing.T) {
	adapter := NewSecretAdapter()

	_, err := adapter.ParseFrom([]byte("test"), SecretFormat("xml"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported format")
}

func TestSecretAdapter_ParseFrom_EmptyData(t *testing.T) {
	adapter := NewSecretAdapter()

	_, err := adapter.ParseFrom([]byte(""), FormatJSON)
	assert.Error(t, err)
}

func TestSecretAdapter_ParseFrom_InvalidJSON(t *testing.T) {
	adapter := NewSecretAdapter()

	_, err := adapter.ParseFrom([]byte("{invalid json}"), FormatJSON)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid JSON format")
}

func TestSecretAdapter_ParseFrom_InvalidCSV(t *testing.T) {
	adapter := NewSecretAdapter()

	// CSV with missing required columns
	csvData := `name,description
test,description
`

	_, err := adapter.ParseFrom([]byte(csvData), FormatCSV)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must contain 'key' and 'value' columns")
}

func TestSecretAdapter_ConvertTo_UnsupportedFormat(t *testing.T) {
	adapter := NewSecretAdapter()

	jsonData := `{"secrets": [{"key": "test", "value": "value"}]}`

	_, err := adapter.ConvertTo([]byte(jsonData), SecretFormat("xml"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported format")
}

func TestSecretAdapter_ParseImportData_EmptySecrets(t *testing.T) {
	adapter := NewSecretAdapter()

	jsonData := `{"secrets": []}`

	items, err := adapter.ParseImportData([]byte(jsonData))
	require.NoError(t, err)
	assert.Equal(t, 0, len(items))
}

func TestSecretAdapter_ParseImportData_InvalidJSON(t *testing.T) {
	adapter := NewSecretAdapter()

	_, err := adapter.ParseImportData([]byte("{invalid}"))
	assert.Error(t, err)
}

func TestSecretAdapter_CSV_EmptyData(t *testing.T) {
	adapter := NewSecretAdapter()

	_, err := adapter.ParseFrom([]byte(""), FormatCSV)
	assert.Error(t, err)
}

func TestSecretAdapter_CSV_InvalidFormat(t *testing.T) {
	adapter := NewSecretAdapter()

	// CSV with only header, no data
	csvData := `key,value`

	result, err := adapter.ParseFrom([]byte(csvData), FormatCSV)
	require.NoError(t, err)

	items, err := adapter.ParseImportData(result)
	require.NoError(t, err)
	assert.Equal(t, 0, len(items))
}

func TestSecretAdapter_YAML_Comments(t *testing.T) {
	adapter := NewSecretAdapter()

	// YAML with comments
	yamlData := `# This is a comment
- key: api_key
  value: secret_value_123

# Another comment
- key: db_password
  value: db_secret_456
`

	result, err := adapter.ParseFrom([]byte(yamlData), FormatYAML)
	require.NoError(t, err)

	items, err := adapter.ParseImportData(result)
	require.NoError(t, err)
	assert.Equal(t, 2, len(items))
	assert.Equal(t, "api_key", items[0].Key)
	assert.Equal(t, "db_password", items[1].Key)
}

func TestSecretAdapter_ConvertTo_YAMLQuotesHostileValues(t *testing.T) {
	adapter := NewSecretAdapter()

	// Values that break hand-rolled YAML: mapping indicators, comments,
	// newlines, quotes and leading special characters. The old writer
	// emitted them raw, producing structurally invalid YAML.
	jsonData := `{
		"secrets": [
			{
				"key": "db_url",
				"value": "postgres://user:p@ss:word@host:5432/db #prod",
				"expires_at": "2026-12-31T23:59:59Z"
			},
			{
				"key": "multiline",
				"value": "line1\nline2: with colon"
			},
			{
				"key": "quoted",
				"value": "\"quoted value\""
			}
		]
	}`

	yamlOut, err := adapter.ConvertTo([]byte(jsonData), FormatYAML)
	require.NoError(t, err)

	// Round-trip: the generated YAML must parse back to the same secrets.
	back, err := adapter.ParseFrom(yamlOut, FormatYAML)
	require.NoError(t, err)

	var parsed ImportData
	require.NoError(t, json.Unmarshal(back, &parsed))
	require.Len(t, parsed.Secrets, 3)

	byKey := map[string]SecretImportItem{}
	for _, s := range parsed.Secrets {
		byKey[s.Key] = s
	}
	assert.Equal(t, "postgres://user:p@ss:word@host:5432/db #prod", byKey["db_url"].Value)
	assert.Equal(t, "line1\nline2: with colon", byKey["multiline"].Value)
	assert.Equal(t, "\"quoted value\"", byKey["quoted"].Value)
	assert.Equal(t, "2026-12-31T23:59:59Z", byKey["db_url"].ExpiresAt)
}
