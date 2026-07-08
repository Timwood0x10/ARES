// Package adapters provides format conversion layer for storage operations.
// This layer converts between various input formats (JSON/YAML/CSV) and standard JSON format.
package adapters

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Timwood0x10/ares/internal/errors"
	storage_models "github.com/Timwood0x10/ares/internal/storage/postgres/models"
	"gopkg.in/yaml.v3"
)

// SecretFormat defines supported import/export formats.
type SecretFormat string

const (
	FormatJSON SecretFormat = "json"
	FormatYAML SecretFormat = "yaml"
	FormatCSV  SecretFormat = "csv"
)

// SecretAdapter handles format conversion for secret import/export operations.
// This implements the adapter pattern to unify various input formats to JSON.
type SecretAdapter struct{}

// NewSecretAdapter creates a new SecretAdapter instance.
func NewSecretAdapter() *SecretAdapter {
	return &SecretAdapter{}
}

// ImportData represents the structure for importing secrets.
// This is the internal JSON format used by the repository layer.
type ImportData struct {
	Secrets []SecretImportItem `json:"secrets" yaml:"secrets"`
}

// SecretImportItem represents a single secret for import.
type SecretImportItem struct {
	Key       string                 `json:"key" yaml:"key"`
	Value     string                 `json:"value" yaml:"value"`
	ExpiresAt string                 `json:"expires_at,omitempty" yaml:"expires_at,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty" yaml:"metadata,omitempty"`
}

// ParseFrom converts various input formats to standard JSON format.
// Args:
// data - raw data in any supported format (JSON/YAML/CSV).
// format - input format type (json/yaml/csv).
// Returns standard JSON format or error if parsing fails.
func (a *SecretAdapter) ParseFrom(data []byte, format SecretFormat) ([]byte, error) {
	switch format {
	case FormatJSON:
		return a.parseJSON(data)
	case FormatYAML:
		return a.parseYAML(data)
	case FormatCSV:
		return a.parseCSV(data)
	default:
		return nil, fmt.Errorf("unsupported format: %s", format)
	}
}

// parseJSON parses JSON input format.
func (a *SecretAdapter) parseJSON(data []byte) ([]byte, error) {
	// Validate JSON format
	var importData ImportData
	if err := json.Unmarshal(data, &importData); err != nil {
		return nil, errors.Wrap(err, "invalid JSON format")
	}

	// Return as-is (already JSON)
	return data, nil
}

// parseYAML parses YAML input format and converts to JSON using gopkg.in/yaml.v3.
// This properly handles YAML syntax (nested structures, quoted strings, multi-line
// values, etc.) instead of the fragile line-by-line approach.
func (a *SecretAdapter) parseYAML(data []byte) ([]byte, error) {
	var importData ImportData
	if err := yaml.Unmarshal(data, &importData); err != nil {
		return nil, errors.Wrap(err, "invalid YAML format")
	}

	// Convert to JSON
	return json.Marshal(importData)
}

// parseCSV parses CSV input format and converts to JSON.
func (a *SecretAdapter) parseCSV(data []byte) ([]byte, error) {
	// Parse CSV
	reader := csv.NewReader(strings.NewReader(string(data)))
	records, err := reader.ReadAll()
	if err != nil {
		return nil, errors.Wrap(err, "parse CSV")
	}

	if len(records) == 0 {
		return nil, fmt.Errorf("empty CSV data")
	}

	// Extract headers
	headers := records[0]
	if len(headers) < 2 {
		return nil, fmt.Errorf("CSV must have at least 2 columns (key, value)")
	}

	// Find column indices
	keyIdx := -1
	valueIdx := -1
	expiresAtIdx := -1

	for i, header := range headers {
		switch strings.ToLower(strings.TrimSpace(header)) {
		case "key":
			keyIdx = i
		case "value":
			valueIdx = i
		case "expires_at":
			expiresAtIdx = i
		}
	}

	if keyIdx == -1 || valueIdx == -1 {
		return nil, fmt.Errorf("CSV must contain 'key' and 'value' columns")
	}

	// Convert to JSON structure
	importData := ImportData{
		Secrets: make([]SecretImportItem, 0, len(records)-1),
	}

	for i := 1; i < len(records); i++ {
		record := records[i]
		if len(record) <= keyIdx || len(record) <= valueIdx {
			continue
		}

		item := SecretImportItem{
			Key:   strings.TrimSpace(record[keyIdx]),
			Value: strings.TrimSpace(record[valueIdx]),
		}

		if expiresAtIdx != -1 && len(record) > expiresAtIdx {
			item.ExpiresAt = strings.TrimSpace(record[expiresAtIdx])
		}

		importData.Secrets = append(importData.Secrets, item)
	}

	// Convert to JSON
	return json.Marshal(importData)
}

// ConvertTo converts standard JSON format to various output formats.
// Args:
// data - JSON format data.
// format - output format type (json/yaml/csv).
// Returns converted data or error if conversion fails.
func (a *SecretAdapter) ConvertTo(data []byte, format SecretFormat) ([]byte, error) {
	switch format {
	case FormatJSON:
		return data, nil
	case FormatYAML:
		return a.convertToYAML(data)
	case FormatCSV:
		return a.convertToCSV(data)
	default:
		return nil, fmt.Errorf("unsupported format: %s", format)
	}
}

// convertToYAML converts JSON to YAML format.
func (a *SecretAdapter) convertToYAML(data []byte) ([]byte, error) {
	var importData ImportData
	if err := json.Unmarshal(data, &importData); err != nil {
		return nil, errors.Wrap(err, "unmarshal JSON")
	}

	var yamlBuilder strings.Builder

	for _, secret := range importData.Secrets {
		yamlBuilder.WriteString("- key: ")
		yamlBuilder.WriteString(secret.Key)
		yamlBuilder.WriteString("\n  value: ")
		yamlBuilder.WriteString(secret.Value)

		if secret.ExpiresAt != "" {
			yamlBuilder.WriteString("\n  expires_at: ")
			yamlBuilder.WriteString(secret.ExpiresAt)
		}

		yamlBuilder.WriteString("\n\n")
	}

	return []byte(yamlBuilder.String()), nil
}

// convertToCSV converts JSON to CSV format.
func (a *SecretAdapter) convertToCSV(data []byte) ([]byte, error) {
	var importData ImportData
	if err := json.Unmarshal(data, &importData); err != nil {
		return nil, errors.Wrap(err, "unmarshal JSON")
	}

	var csvBuilder strings.Builder
	csvWriter := csv.NewWriter(&csvBuilder)

	// Write header
	headers := []string{"key", "value", "expires_at"}
	if err := csvWriter.Write(headers); err != nil {
		return nil, errors.Wrap(err, "write CSV header")
	}

	// Write data rows
	for _, secret := range importData.Secrets {
		record := []string{secret.Key, secret.Value}
		if secret.ExpiresAt != "" {
			record = append(record, secret.ExpiresAt)
		}
		if err := csvWriter.Write(record); err != nil {
			return nil, errors.Wrap(err, "write CSV record")
		}
	}

	csvWriter.Flush()
	if err := csvWriter.Error(); err != nil {
		return nil, errors.Wrap(err, "flush CSV writer")
	}

	return []byte(csvBuilder.String()), nil
}

// ParseImportData parses JSON format data and returns import items.
func (a *SecretAdapter) ParseImportData(data []byte) ([]SecretImportItem, error) {
	var importData ImportData
	if err := json.Unmarshal(data, &importData); err != nil {
		return nil, errors.Wrap(err, "parse import data")
	}

	return importData.Secrets, nil
}

// ConvertToExportFormat converts secret models to export data.
func (a *SecretAdapter) ConvertToExportFormat(secrets []*storage_models.Secret) ([]byte, error) {
	exportData := make([]map[string]interface{}, len(secrets))

	for i, secret := range secrets {
		exportData[i] = map[string]interface{}{
			"key":         secret.Key,
			"key_version": secret.KeyVersion,
			"algorithm":   secret.Algorithm,
			"created_at":  secret.CreatedAt,
		}

		if !secret.ExpiresAt.IsZero() {
			exportData[i]["expires_at"] = secret.ExpiresAt
		}

		if secret.Metadata != nil {
			exportData[i]["metadata"] = secret.Metadata
		}
	}

	return json.Marshal(exportData)
}

// DetectFormat automatically detects input format based on content.
func (a *SecretAdapter) DetectFormat(data []byte) SecretFormat {
	dataStr := strings.TrimSpace(string(data))

	// Check if it's JSON
	if strings.HasPrefix(dataStr, "{") || strings.HasPrefix(dataStr, "[") {
		return FormatJSON
	}

	// Check if it's CSV (contains commas and line breaks)
	if strings.Contains(dataStr, ",") && strings.Contains(dataStr, "\n") {
		return FormatCSV
	}

	// Default to YAML
	return FormatYAML
}
