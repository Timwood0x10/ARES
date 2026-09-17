//go:build integration
// +build integration

// Package repositories provides data access layer for storage system.
package repositories

import (
	"strings"
	"testing"
)

// TestSecretRepositoryExport_FailsLoudOnUndecryptableSecret is the
// end-to-end half of the backup-integrity contract: Export reports which
// keys could not be decrypted instead of returning a payload that silently
// omits or corrupts them. A backup tool that lies about what it saved is
// worse than one that refuses to run.
//
// The scenario is the restored-database case — rows written under one key,
// read back under another — which is exactly when the pre-fix fallthrough
// emitted raw ciphertext as the secret's value and let Import double-encrypt
// it into permanent unreadability.
//
// See secret_repository_export_test.go for the DB-free coverage of the same
// decision at the crypto level.
func TestSecretRepositoryExport_FailsLoudOnUndecryptableSecret(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	db := getTestDB(t)
	if db == nil {
		return
	}
	defer closeTestDB(t, db)
	defer cleanupTestDB(t, db)

	// Write a secret under key A.
	writer, err := NewSecretRepository(db, testEncryptionKey(1))
	if err != nil {
		t.Fatalf("NewSecretRepository writer: %v", err)
	}
	ctx := t.Context()
	if err := writer.Set(ctx, "foreign_key", "value-under-key-a", "tenant-1"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Re-read the same rows with a different key: the ciphertext is intact
	// but no longer decryptable.
	reader, err := NewSecretRepository(db, testEncryptionKey(200))
	if err != nil {
		t.Fatalf("NewSecretRepository reader: %v", err)
	}

	payload, err := reader.Export(ctx, "tenant-1")
	if err == nil {
		t.Fatalf("Export must fail loudly when a secret cannot be decrypted, got payload %s", payload)
	}
	if !strings.Contains(err.Error(), "foreign_key") {
		t.Fatalf("export error must name the offending key so the operator can act, got %v", err)
	}
	if strings.Contains(string(payload), "value-under-key-a") {
		t.Fatal("a failed export must not emit any secret material")
	}
}
