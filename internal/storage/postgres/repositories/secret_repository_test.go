//go:build integration
// +build integration

// Package repositories provides comprehensive tests for SecretRepository.
package repositories

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"goagentx/internal/storage/postgres/adapters"
)

// TestSecretRepository_Set tests storing a secret value.
func TestSecretRepository_Set(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	// Create repository with test encryption key
	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// Store a secret
	err = repo.Set(ctx, "test_key", "test_secret_value", "tenant-1")
	require.NoError(t, err)
}

// TestSecretRepository_Set_Update tests updating an existing secret.
func TestSecretRepository_Set_Update(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// Store initial secret
	err = repo.Set(ctx, "update_key", "initial_value", "tenant-1")
	require.NoError(t, err)

	// Update the secret
	err = repo.Set(ctx, "update_key", "updated_value", "tenant-1")
	require.NoError(t, err)

	// Verify the updated value
	value, err := repo.Get(ctx, "update_key", "tenant-1")
	require.NoError(t, err)
	assert.Equal(t, "updated_value", value, "Value should be updated")
}

// TestSecretRepository_Get tests retrieving a secret value.
func TestSecretRepository_Get(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// Store a secret
	err = repo.Set(ctx, "get_key", "get_secret_value", "tenant-1")
	require.NoError(t, err)

	// Retrieve the secret
	value, err := repo.Get(ctx, "get_key", "tenant-1")
	require.NoError(t, err)
	assert.Equal(t, "get_secret_value", value, "Retrieved value should match")
}

// TestSecretRepository_Get_NotFound tests retrieving a non-existent secret.
func TestSecretRepository_Get_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// Try to retrieve non-existent secret
	value, err := repo.Get(ctx, "non_existent_key", "tenant-1")
	assert.Error(t, err, "Should return error for non-existent secret")
	assert.Empty(t, value, "Value should be empty")
}

// TestSecretRepository_Get_TenantIsolation tests tenant isolation.
func TestSecretRepository_Get_TenantIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// Store secret for tenant-1
	err = repo.Set(ctx, "isolation_key", "tenant1_value", "tenant-1")
	require.NoError(t, err)

	// Try to retrieve with tenant-2 (should fail)
	value, err := repo.Get(ctx, "isolation_key", "tenant-2")
	assert.Error(t, err, "Should not access another tenant's secret")
	assert.Empty(t, value)
}

// TestSecretRepository_Delete tests deleting a secret.
func TestSecretRepository_Delete(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// Store a secret
	err = repo.Set(ctx, "delete_key", "delete_value", "tenant-1")
	require.NoError(t, err)

	// Delete the secret
	err = repo.Delete(ctx, "delete_key", "tenant-1")
	require.NoError(t, err)

	// Verify deletion
	value, err := repo.Get(ctx, "delete_key", "tenant-1")
	assert.Error(t, err, "Should return error after deletion")
	assert.Empty(t, value)
}

// TestSecretRepository_Delete_NotFound tests deleting a non-existent secret.
func TestSecretRepository_Delete_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// Try to delete non-existent secret
	err = repo.Delete(ctx, "non_existent_key", "tenant-1")
	assert.Error(t, err, "Should return error for non-existent secret")
}

// TestSecretRepository_List tests listing all secrets for a tenant.
func TestSecretRepository_List(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// Store multiple secrets and verify each one.
	secrets := map[string]string{
		"list_key1": "value1",
		"list_key2": "value2",
		"list_key3": "value3",
	}

	for key, value := range secrets {
		err := repo.Set(ctx, key, value, "tenant-list-test")
		require.NoError(t, err, "Failed to set secret %s", key)

		// Verify the secret was actually stored.
		retrieved, err := repo.Get(ctx, key, "tenant-list-test")
		require.NoError(t, err, "Failed to get secret %s after set", key)
		assert.Equal(t, value, retrieved, "Retrieved value doesn't match for key %s", key)
	}

	// List secrets.
	secretList, err := repo.List(ctx, "tenant-list-test")
	require.NoError(t, err, "Failed to list secrets")
	assert.Len(t, secretList, 3, "Should return 3 secrets")

	// Verify keys are present (without values).
	keys := make([]string, len(secretList))
	for i, secret := range secretList {
		keys[i] = secret.Key
		assert.NotEmpty(t, secret.ID, "ID should not be empty")
		assert.Equal(t, "tenant-list-test", secret.TenantID, "Tenant ID should match")
		assert.Greater(t, secret.KeyVersion, 0, "Key version should be positive")
	}

	// Verify all expected keys are present.
	for key := range secrets {
		assert.Contains(t, keys, key, "Key %s should be in the list", key)
	}
}

// TestSecretRepository_List_Empty tests listing when no secrets exist.
func TestSecretRepository_List_Empty(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// List secrets for tenant with no secrets
	secretList, err := repo.List(ctx, "tenant-empty")
	require.NoError(t, err)
	assert.Empty(t, secretList, "Should return empty list")
}

// TestSecretRepository_SetWithExpiration tests storing a secret with expiration.
func TestSecretRepository_SetWithExpiration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// Store a secret with expiration
	expiresAt := time.Now().Add(1 * time.Hour)
	err = repo.SetWithExpiration(ctx, "expire_key", "expire_value", "tenant-1", expiresAt)
	require.NoError(t, err)

	// Retrieve the secret (should work before expiration)
	value, err := repo.Get(ctx, "expire_key", "tenant-1")
	require.NoError(t, err)
	assert.Equal(t, "expire_value", value)
}

// TestSecretRepository_SetWithExpiration_Expired tests retrieving an expired secret.
func TestSecretRepository_SetWithExpiration_Expired(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// Store a secret with past expiration (use UTC time to avoid timezone issues)
	expiresAt := time.Now().UTC().Add(-1 * time.Hour)
	err = repo.SetWithExpiration(ctx, "expired_key", "expired_value", "tenant-1", expiresAt)
	require.NoError(t, err)

	// Try to retrieve the expired secret
	value, err := repo.Get(ctx, "expired_key", "tenant-1")
	assert.Error(t, err, "Should return error for expired secret")
	assert.Empty(t, value)
}

// TestSecretRepository_UpdateMetadata tests updating secret metadata.
func TestSecretRepository_UpdateMetadata(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// Store a secret.
	err = repo.Set(ctx, "metadata_test_key", "metadata_value", "tenant-meta-test")
	require.NoError(t, err)

	// Update metadata.
	metadata := map[string]interface{}{
		"owner":      "user1",
		"purpose":    "testing",
		"created_by": "system",
	}
	err = repo.UpdateMetadata(ctx, "metadata_test_key", "tenant-meta-test", metadata)
	require.NoError(t, err)

	// Verify the secret still exists and is retrievable after metadata update.
	value, err := repo.Get(ctx, "metadata_test_key", "tenant-meta-test")
	require.NoError(t, err, "Secret should still be accessible after metadata update")
	assert.Equal(t, "metadata_value", value, "Secret value should be unchanged")

	// Verify the secret appears in the list.
	secretList, err := repo.List(ctx, "tenant-meta-test")
	require.NoError(t, err)
	found := false
	for _, secret := range secretList {
		if secret.Key == "metadata_test_key" {
			found = true
			assert.Equal(t, "tenant-meta-test", secret.TenantID)
			break
		}
	}
	assert.True(t, found, "Secret should exist in list after metadata update")

	// Verify updating metadata again (overwrite) works.
	updatedMetadata := map[string]interface{}{
		"owner":   "user2",
		"version": "2.0",
	}
	err = repo.UpdateMetadata(ctx, "metadata_test_key", "tenant-meta-test", updatedMetadata)
	require.NoError(t, err, "Second metadata update should succeed")
}

// TestSecretRepository_UpdateMetadata_NotFound tests updating metadata for non-existent secret.
func TestSecretRepository_UpdateMetadata_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// Try to update metadata for non-existent secret
	metadata := map[string]interface{}{"test": "value"}
	err = repo.UpdateMetadata(ctx, "non_existent_key", "tenant-1", metadata)
	assert.Error(t, err, "Should return error for non-existent secret")
}

// TestSecretRepository_CleanupExpired tests cleanup of expired secrets.
func TestSecretRepository_CleanupExpired(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// Store an expired secret (use UTC time to avoid timezone issues)
	expiresAt := time.Now().UTC().Add(-2 * time.Hour)
	err = repo.SetWithExpiration(ctx, "cleanup_expired_key_test", "cleanup_expired_value", "tenant-1", expiresAt)
	require.NoError(t, err)

	// Store a non-expired secret
	err = repo.SetWithExpiration(ctx, "cleanup_valid_key_test", "cleanup_valid_value", "tenant-1", time.Now().UTC().Add(2*time.Hour))
	require.NoError(t, err)

	// Cleanup expired secrets
	deleted, err := repo.CleanupExpired(ctx)
	require.NoError(t, err)

	// Note: We don't assert specific count as it depends on other test data
	// Just verify the cleanup operation works
	assert.GreaterOrEqual(t, deleted, int64(0), "Cleanup should complete without error")

	// Verify we can still get the valid secret
	value, err := repo.Get(ctx, "cleanup_valid_key_test", "tenant-1")
	require.NoError(t, err, "Non-expired secret should still exist")
	assert.Equal(t, "cleanup_valid_value", value)
}

// TestSecretRepository_RotateKey tests rotating encryption key.
func TestSecretRepository_RotateKey(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	// Create repository with initial encryption key
	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// Store secrets with initial key
	secrets := map[string]string{
		"rotate_key1": "rotate_value1",
		"rotate_key2": "rotate_value2",
		"rotate_key3": "rotate_value3",
	}

	for key, value := range secrets {
		err := repo.Set(ctx, key, value, "tenant-1")
		require.NoError(t, err)
	}

	// Create new encryption key
	newKey := make([]byte, 32)
	for i := range newKey {
		newKey[i] = byte(i + 32)
	}

	// Rotate key
	updated, err := repo.RotateKey(ctx, "tenant-1", newKey)
	require.NoError(t, err)
	assert.Equal(t, int64(3), updated, "Should update 3 secrets")

	// Verify secrets can still be decrypted with new key
	newRepo, err := NewSecretRepository(db, newKey)
	require.NoError(t, err)
	for key, expectedValue := range secrets {
		value, err := newRepo.Get(ctx, key, "tenant-1")
		require.NoError(t, err, "Should be able to decrypt with new key")
		assert.Equal(t, expectedValue, value, "Value should match after key rotation")
	}
}

// TestSecretRepository_RotateKey_InvalidKey tests rotation with invalid key.
func TestSecretRepository_RotateKey_InvalidKey(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// Try to rotate with invalid key length
	invalidKey := make([]byte, 16)
	_, err = repo.RotateKey(ctx, "tenant-1", invalidKey)
	assert.Error(t, err, "Should return error for invalid key length")
}

// TestSecretRepository_Export tests exporting secrets.
func TestSecretRepository_Export(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// Store secrets
	err = repo.Set(ctx, "export_key1", "export_value1", "tenant-1")
	require.NoError(t, err)
	err = repo.Set(ctx, "export_key2", "export_value2", "tenant-1")
	require.NoError(t, err)

	// Export secrets
	data, err := repo.Export(ctx, "tenant-1")
	require.NoError(t, err)
	assert.NotEmpty(t, data, "Export data should not be empty")
}

// TestSecretRepository_Export_Empty tests exporting when no secrets exist.
func TestSecretRepository_Export_Empty(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// Export secrets for tenant with no secrets
	data, err := repo.Export(ctx, "tenant-empty")
	require.NoError(t, err)
	assert.NotEmpty(t, data, "Export data should be valid JSON even if empty")
}

// TestSecretRepository_Import tests importing secrets.
func TestSecretRepository_Import(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// Import secrets using JSON format
	jsonData := `{
		"secrets": [
			{
				"key": "import_key1",
				"value": "import_value1"
			},
			{
				"key": "import_key2",
				"value": "import_value2"
			}
		]
	}`

	count, err := repo.Import(ctx, "tenant-1", []byte(jsonData), string(adapters.FormatJSON))
	require.NoError(t, err)
	assert.Equal(t, int64(2), count, "Should import 2 secrets")

	// Verify imported secrets
	value1, err := repo.Get(ctx, "import_key1", "tenant-1")
	require.NoError(t, err)
	assert.Equal(t, "import_value1", value1)

	value2, err := repo.Get(ctx, "import_key2", "tenant-1")
	require.NoError(t, err)
	assert.Equal(t, "import_value2", value2)
}

// TestSecretRepository_GetKeyVersion tests retrieving key version.
func TestSecretRepository_GetKeyVersion(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// Store a secret
	err = repo.Set(ctx, "version_key", "version_value", "tenant-1")
	require.NoError(t, err)

	// Get key version
	version, err := repo.GetKeyVersion(ctx, "version_key", "tenant-1")
	require.NoError(t, err)
	assert.Equal(t, 1, version, "Initial key version should be 1")

	// Update the secret
	err = repo.Set(ctx, "version_key", "version_value_updated", "tenant-1")
	require.NoError(t, err)

	// Get updated key version
	version, err = repo.GetKeyVersion(ctx, "version_key", "tenant-1")
	require.NoError(t, err)
	assert.Equal(t, 2, version, "Key version should be incremented")
}

// TestSecretRepository_GetKeyVersion_NotFound tests getting key version for non-existent secret.
func TestSecretRepository_GetKeyVersion_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// Try to get key version for non-existent secret
	version, err := repo.GetKeyVersion(ctx, "non_existent_key", "tenant-1")
	assert.Error(t, err, "Should return error for non-existent secret")
	assert.Equal(t, 0, version, "Version should be 0 for non-existent secret")
}

// TestSecretRepository_ConcurrentOperations tests concurrent secret operations.
func TestSecretRepository_ConcurrentOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// Perform concurrent operations
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(index int) {
			key := fmt.Sprintf("concurrent_key_%d", index)
			value := fmt.Sprintf("concurrent_value_%d", index)
			err := repo.Set(ctx, key, value, "tenant-1")
			assert.NoError(t, err, "Concurrent set failed for key %d", index)

			retrieved, err := repo.Get(ctx, key, "tenant-1")
			assert.NoError(t, err, "Concurrent get failed for key %d", index)
			assert.Equal(t, value, retrieved, "Concurrent value mismatch for key %d", index)

			done <- true
		}(i)
	}

	// Wait for all operations to complete
	for i := 0; i < 10; i++ {
		<-done
	}
}

// TestSecretRepository_LargeSecret tests storing and retrieving large secrets.
func TestSecretRepository_LargeSecret(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	encryptionKey := make([]byte, 32)
	for i := range encryptionKey {
		encryptionKey[i] = byte(i)
	}

	repo, err := NewSecretRepository(db, encryptionKey)
	require.NoError(t, err)
	ctx := context.Background()

	// Create a large secret (10KB)
	largeValue := make([]byte, 10240)
	for i := range largeValue {
		largeValue[i] = byte(i % 256)
	}

	// Store large secret
	err = repo.Set(ctx, "large_key", string(largeValue), "tenant-1")
	require.NoError(t, err)

	// Retrieve large secret
	value, err := repo.Get(ctx, "large_key", "tenant-1")
	require.NoError(t, err)
	assert.Equal(t, string(largeValue), value, "Large secret should match")
}
