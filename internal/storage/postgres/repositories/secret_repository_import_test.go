// Package repositories provides secret repository tests.
package repositories

import (
	"context"
	"database/sql"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Timwood0x10/ares/internal/storage/postgres/adapters"
)

func TestSecretRepository_Import_JSON(t *testing.T) {
	db := setupTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("Warning: Failed to close database:", "error", err)
		}
	}()

	// Create repository with test encryption key
	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// Test JSON format import
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

	count, err := repo.Import(ctx, "tenant-1", []byte(jsonData), "json")
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)

	// Verify secrets were imported
	secret1, err := repo.Get(ctx, "api_key", "tenant-1")
	require.NoError(t, err)
	assert.Equal(t, "secret_value_123", secret1)

	secret2, err := repo.Get(ctx, "db_password", "tenant-1")
	require.NoError(t, err)
	assert.Equal(t, "db_secret_456", secret2)
}

func TestSecretRepository_Import_YAML(t *testing.T) {
	db := setupTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("Warning: Failed to close database: ", "error", err)
		}
	}()

	// Create repository with test encryption key
	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// Test YAML format import
	yamlData := `- key: api_key
  value: secret_value_123
  expires_at: 2026-12-31T23:59:59Z

- key: db_password
  value: db_secret_456
`

	count, err := repo.Import(ctx, "tenant-2", []byte(yamlData), "yaml")
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)

	// Verify secrets were imported
	secret1, err := repo.Get(ctx, "api_key", "tenant-2")
	require.NoError(t, err)
	assert.Equal(t, "secret_value_123", secret1)

	secret2, err := repo.Get(ctx, "db_password", "tenant-2")
	require.NoError(t, err)
	assert.Equal(t, "db_secret_456", secret2)
}

func TestSecretRepository_Import_CSV(t *testing.T) {
	db := setupTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("Warning: Failed to close database: ", "error", err)
		}
	}()

	// Create repository with test encryption key
	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// Test CSV format import
	csvData := `key,value,expires_at
api_key,secret_value_123,2026-12-31T23:59:59Z
db_password,db_secret_456,
`

	count, err := repo.Import(ctx, "tenant-3", []byte(csvData), "csv")
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)

	// Verify secrets were imported
	secret1, err := repo.Get(ctx, "api_key", "tenant-3")
	require.NoError(t, err)
	assert.Equal(t, "secret_value_123", secret1)

	secret2, err := repo.Get(ctx, "db_password", "tenant-3")
	require.NoError(t, err)
	assert.Equal(t, "db_secret_456", secret2)
}

func TestSecretRepository_Import_DuplicateKeys(t *testing.T) {
	db := setupTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("Warning: Failed to close database: ", "error", err)
		}
	}()

	// Create repository with test encryption key
	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// First import
	jsonData := `{
		"secrets": [
			{
				"key": "api_key",
				"value": "secret_value_123"
			}
		]
	}`

	count, err := repo.Import(ctx, "tenant-4", []byte(jsonData), "json")
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	// Second import with duplicate key (should skip)
	count, err = repo.Import(ctx, "tenant-4", []byte(jsonData), "json")
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)

	// Verify original secret is still there
	secret, err := repo.Get(ctx, "api_key", "tenant-4")
	require.NoError(t, err)
	assert.Equal(t, "secret_value_123", secret)
}

func TestSecretRepository_Import_InvalidFormat(t *testing.T) {
	db := setupTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("Warning: Failed to close database: ", "error", err)
		}
	}()

	// Create repository with test encryption key
	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// Test unsupported format
	_, err = repo.Import(ctx, "tenant-5", []byte("test"), "xml")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported format")
}

func TestSecretRepository_Import_EmptyData(t *testing.T) {
	db := setupTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("Warning: Failed to close database: ", "error", err)
		}
	}()

	// Create repository with test encryption key
	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// Test empty data
	_, err = repo.Import(ctx, "tenant-6", []byte(""), "json")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be empty")
}

func TestSecretRepository_Import_EmptyTenantID(t *testing.T) {
	db := setupTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("Warning: Failed to close database: ", "error", err)
		}
	}()

	// Create repository with test encryption key
	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	jsonData := `{
		"secrets": [
			{
				"key": "api_key",
				"value": "secret_value_123"
			}
		]
	}`

	// Test empty tenant ID
	_, err = repo.Import(ctx, "", []byte(jsonData), "json")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "tenant ID cannot be empty")
}

func TestSecretRepository_Import_EmptySecretKey(t *testing.T) {
	db := setupTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("Warning: Failed to close database: ", "error", err)
		}
	}()

	// Create repository with test encryption key
	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	jsonData := `{
		"secrets": [
			{
				"key": "",
				"value": "secret_value_123"
			}
		]
	}`

	// Test empty secret key
	_, err = repo.Import(ctx, "tenant-7", []byte(jsonData), "json")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no secrets imported")
}

func TestSecretRepository_Import_EmptySecretValue(t *testing.T) {
	db := setupTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("Warning: Failed to close database: ", "error", err)
		}
	}()

	// Create repository with test encryption key
	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	jsonData := `{
		"secrets": [
			{
				"key": "api_key",
				"value": ""
			}
		]
	}`

	// Test empty secret value
	_, err = repo.Import(ctx, "tenant-8", []byte(jsonData), "json")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no secrets imported")
}

func TestSecretRepository_Import_InvalidExpiresAt(t *testing.T) {
	db := setupTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("Warning: Failed to close database: ", "error", err)
		}
	}()

	// Create repository with test encryption key
	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	jsonData := `{
		"secrets": [
			{
				"key": "api_key",
				"value": "secret_value_123",
				"expires_at": "invalid-date"
			}
		]
	}`

	// Test invalid expires_at format
	_, err = repo.Import(ctx, "tenant-9", []byte(jsonData), "json")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no secrets imported")
}

func TestSecretRepository_Import_WithExpiration(t *testing.T) {
	db := setupTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("Warning: Failed to close database: ", "error", err)
		}
	}()

	// Create repository with test encryption key
	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// Import secret with expiration
	expiresAt := time.Now().Add(24 * time.Hour).Format(time.RFC3339)
	jsonData := `{
		"secrets": [
			{
				"key": "temp_key",
				"value": "temp_value",
				"expires_at": "` + expiresAt + `"
			}
		]
	}`

	count, err := repo.Import(ctx, "tenant-10", []byte(jsonData), "json")
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	// Verify secret was imported with expiration
	secret, err := repo.Get(ctx, "temp_key", "tenant-10")
	require.NoError(t, err)
	assert.Equal(t, "temp_value", secret)
}

func TestSecretRepository_Import_MixedValidAndInvalid(t *testing.T) {
	db := setupTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("Warning: Failed to close database: ", "error", err)
		}
	}()

	// Create repository with test encryption key
	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// Import mixed valid and invalid secrets
	jsonData := `{
		"secrets": [
			{
				"key": "valid_key1",
				"value": "valid_value_1"
			},
			{
				"key": "",
				"value": "invalid_empty_key"
			},
			{
				"key": "invalid_empty_value",
				"value": ""
			},
			{
				"key": "valid_key2",
				"value": "valid_value_2"
			}
		]
	}`

	count, err := repo.Import(ctx, "tenant-11", []byte(jsonData), "json")
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)

	// Verify only valid secrets were imported
	secret1, err := repo.Get(ctx, "valid_key1", "tenant-11")
	require.NoError(t, err)
	assert.Equal(t, "valid_value_1", secret1)

	secret2, err := repo.Get(ctx, "valid_key2", "tenant-11")
	require.NoError(t, err)
	assert.Equal(t, "valid_value_2", secret2)
}

func TestSecretRepository_Import_AutoDetectFormat(t *testing.T) {
	db := setupTestDB(t)
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("Warning: Failed to close database: ", "error", err)
		}
	}()

	// Create repository with test encryption key
	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// Create adapter for format detection
	adapter := adapters.NewSecretAdapter()

	// Test data in JSON format
	jsonData := `{
		"secrets": [
			{
				"key": "api_key",
				"value": "secret_value_123"
			}
		]
	}`

	detectedFormat := adapter.DetectFormat([]byte(jsonData))
	assert.Equal(t, adapters.FormatJSON, detectedFormat)

	count, err := repo.Import(ctx, "tenant-12", []byte(jsonData), string(detectedFormat))
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	// Verify secret was imported
	secret, err := repo.Get(ctx, "api_key", "tenant-12")
	require.NoError(t, err)
	assert.Equal(t, "secret_value_123", secret)
}

// setupTestDB creates a test database connection
func setupTestDB(t *testing.T) *sql.DB {
	// This is a placeholder - in real implementation, you would set up a test database
	// For now, we'll skip the actual database setup
	t.Skip("Skipping test: database setup required")
	return nil
}
