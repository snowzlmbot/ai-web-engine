package crypto

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptRoundTripAndTamperDetection(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	ciphertext, err := Encrypt(key, []byte("secret payload"))
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := Decrypt(key, ciphertext)
	if err != nil || string(plaintext) != "secret payload" {
		t.Fatalf("round trip failed: %q, %v", plaintext, err)
	}
	ciphertext[len(ciphertext)-1] ^= 1
	if _, err := Decrypt(key, ciphertext); err == nil {
		t.Fatal("tampered ciphertext was accepted")
	}
}

func TestEnsureKeyPreservesExistingKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config", "master.key")
	first, err := EnsureKey(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EnsureKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("existing master key was replaced")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("master key mode = %o, want 600", info.Mode().Perm())
	}
}
