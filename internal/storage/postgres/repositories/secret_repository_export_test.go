// Package repositories provides data access layer for storage system.
package repositories

import (
	"testing"
)

// testEncryptionKey returns a deterministic 32-byte AES-256 key.
func testEncryptionKey(seed byte) []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = seed + byte(i)
	}
	return key
}

// newSecretRepoForCrypto builds a SecretRepository that only needs its
// encryption machinery — the DB handle is never touched by encrypt/decrypt,
// so nil is enough for the crypto-level tests below.
func newSecretRepoForCrypto(t *testing.T, key []byte) *SecretRepository {
	t.Helper()
	repo, err := NewSecretRepository(nil, key)
	if err != nil {
		t.Fatalf("NewSecretRepository: %v", err)
	}
	return repo
}

// TestSecretRepositoryExportableValue_RoundTrip locks the happy path: a value
// encrypted under the same key exports as its plaintext, not as ciphertext
// or a base64 blob of it.
func TestSecretRepositoryExportableValue_RoundTrip(t *testing.T) {
	repo := newSecretRepoForCrypto(t, testEncryptionKey(1))

	ciphertext, err := repo.encrypt([]byte("super-secret"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	value, ok := repo.exportableValue(ciphertext)
	if !ok {
		t.Fatal("same-key ciphertext must export")
	}
	if value != "super-secret" {
		t.Fatalf("want plaintext round trip, got %q", value)
	}
}

// TestSecretRepositoryExportableValue_RejectsForeignCiphertext locks the
// backup-integrity contract. When the process key no longer matches what is
// stored (key rotated outside RotateKey, partial rotation, a database
// restored onto a deployment with a different key), the ciphertext MUST NOT
// be emitted as the secret's value.
//
// Pre-fix, Export wrote the raw ciphertext bytes into the backup file on
// decrypt failure. Import re-encrypts whatever it reads, so feeding that
// backup back in double-encrypted already-encrypted material and produced a
// permanently unreadable secret — silent, undetected data loss on the exact
// path that exists to prevent data loss. It also leaked recoverable secret
// material into a backup artifact.
func TestSecretRepositoryExportableValue_RejectsForeignCiphertext(t *testing.T) {
	writer := newSecretRepoForCrypto(t, testEncryptionKey(1))
	reader := newSecretRepoForCrypto(t, testEncryptionKey(200))

	ciphertext, err := writer.encrypt([]byte("super-secret"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	value, ok := reader.exportableValue(ciphertext)
	if ok {
		t.Fatalf("undecryptable ciphertext must be rejected, got ok with value %q", value)
	}
	if value != "" {
		t.Fatalf("rejected ciphertext must not surface any value, got %q", value)
	}
}

// TestSecretRepositoryExportableValue_RejectsTruncatedCiphertext locks the
// other undecryptable shape: a blob shorter than the GCM nonce, which the
// decrypt path rejects before it even reaches Open.
func TestSecretRepositoryExportableValue_RejectsTruncatedCiphertext(t *testing.T) {
	repo := newSecretRepoForCrypto(t, testEncryptionKey(1))

	if value, ok := repo.exportableValue([]byte{1, 2, 3}); ok {
		t.Fatalf("truncated ciphertext must be rejected, got ok with value %q", value)
	}
}

// TestSecretRepositoryExportableValue_RejectsTamperedCiphertext locks that a
// flipped bit (GCM auth failure) is rejected rather than exported: AES-GCM's
// tag is the whole point of using it for secrets at rest.
func TestSecretRepositoryExportableValue_RejectsTamperedCiphertext(t *testing.T) {
	repo := newSecretRepoForCrypto(t, testEncryptionKey(1))

	ciphertext, err := repo.encrypt([]byte("super-secret"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	ciphertext[len(ciphertext)-1] ^= 0xFF

	if value, ok := repo.exportableValue(ciphertext); ok {
		t.Fatalf("tampered ciphertext must be rejected, got ok with value %q", value)
	}
}
