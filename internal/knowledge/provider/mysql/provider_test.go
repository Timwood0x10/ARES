package mysql

import (
	"testing"

	"github.com/Timwood0x10/ares/internal/knowledge"
	"github.com/Timwood0x10/ares/internal/knowledge/provider"
)

func TestValidateIdentifier_Valid(t *testing.T) {
	valid := []string{"users", "user_profiles", "table123", "my_table"}
	for _, name := range valid {
		if err := validateIdentifier(name); err != nil {
			t.Errorf("expected valid: %q, got error: %v", name, err)
		}
	}
}

func TestValidateIdentifier_Invalid(t *testing.T) {
	invalid := []string{"", "user table", "drop;table", "user.name"}
	for _, name := range invalid {
		if err := validateIdentifier(name); err == nil {
			t.Errorf("expected error for: %q", name)
		}
	}
}

func TestQuoteIdentifier(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"users", "`users`"},
		{"my_table", "`my_table`"},
	}
	for _, tt := range tests {
		got := quoteIdentifier(tt.input)
		if got != tt.expected {
			t.Errorf("quoteIdentifier(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestBuildQuery(t *testing.T) {
	p := &MySQLProvider{
		config:  providerConfigForTest("test", "users"),
		mapping: columnMappingForTest("id", "name", "content", "tag", "created_at"),
	}

	query := p.buildQuery()
	expected := "SELECT `id`, `name`, `content`, `tag`, `created_at` FROM `users` LIMIT 10000"
	if query != expected {
		t.Errorf("buildQuery() = %q, want %q", query, expected)
	}
}

func TestBuildQuery_Minimal(t *testing.T) {
	p := &MySQLProvider{
		config:  providerConfigForTest("t2", "orders"),
		mapping: columnMappingForTest("id", "summary", "", "", ""),
	}

	query := p.buildQuery()
	expected := "SELECT `id`, `summary` FROM `orders` LIMIT 10000"
	if query != expected {
		t.Errorf("buildQuery() = %q, want %q", query, expected)
	}
}

func TestIntentMatch(t *testing.T) {
	p := &MySQLProvider{
		config: providerConfigForTest("test", "users"),
	}

	// Matching types.
	intent := intentForTypes("decision", "architecture")
	score := p.IntentMatch(intent)
	if score <= 0 {
		t.Errorf("expected positive score for matching intent, got %f", score)
	}
}

func TestNewMySQLProvider_Validation(t *testing.T) {
	tests := []struct {
		name    string
		cfgName string
		table   string
		idCol   string
		sumCol  string
		wantErr bool
	}{
		{"empty name", "", "users", "id", "name", true},
		{"empty table", "test", "", "id", "name", true},
		{"empty id col", "test", "users", "", "name", true},
		{"empty sum col", "test", "users", "id", "", true},
		{"invalid table", "test", "user table", "id", "name", true},
		{"invalid id col", "test", "users", "id;drop", "name", true},
		{"valid", "test", "users", "id", "name", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := providerConfigForTest(tt.cfgName, tt.table)
			mapping := columnMappingForTest(tt.idCol, tt.sumCol, "", "", "")
			_, err := NewMySQLProvider(nil, cfg, mapping)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewMySQLProvider() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

// ── test helpers ──

func providerConfigForTest(name, table string) provider.ProviderConfig {
	return provider.ProviderConfig{
		Name:       name,
		Table:      table,
		Namespace:  "test_ns",
		IntentTags: []string{"decision", "architecture"},
	}
}

func columnMappingForTest(idCol, summaryCol, contentCol, tagCol, timeCol string) provider.ColumnMapping {
	return provider.ColumnMapping{
		IDColumn:      idCol,
		SummaryColumn: summaryCol,
		ContentColumn: contentCol,
		TagColumn:     tagCol,
		TimeColumn:    timeCol,
	}
}

func intentForTypes(types ...string) knowledge.Intent {
	scope := knowledge.Scope{}
	for _, t := range types {
		scope.Types = append(scope.Types, knowledge.ObjectType(t))
	}
	return knowledge.Intent{
		Goal:  "test",
		Scope: scope,
	}
}
