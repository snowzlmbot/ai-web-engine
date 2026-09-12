package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const KeySize = 32

// EnsureKey creates a root-only AES-256 key and never replaces an existing key.
func EnsureKey(path string) ([]byte, error) {
	if data, err := os.ReadFile(path); err == nil {
		if len(data) != KeySize {
			return nil, fmt.Errorf("master key has invalid length %d", len(data))
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return nil, fmt.Errorf("chmod master key: %w", err)
		}
		return data, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read master key: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create key directory: %w", err)
	}
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate master key: %w", err)
	}
	if err := writeAtomic(path, key, 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

// Encrypt uses AES-256-GCM. The returned bytes contain nonce || ciphertext.
func Encrypt(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func Decrypt(key, envelope []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("new AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("new GCM: %w", err)
	}
	if len(envelope) < gcm.NonceSize() {
		return nil, errors.New("encrypted data is shorter than nonce")
	}
	nonce, ciphertext := envelope[:gcm.NonceSize()], envelope[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, errors.New("encrypted data authentication failed")
	}
	return plaintext, nil
}

func ProtectFile(key []byte, path string, plaintext []byte) error {
	envelope, err := Encrypt(key, plaintext)
	if err != nil {
		return err
	}
	return writeAtomic(path, envelope, 0o600)
}

func UnprotectFile(key []byte, path string) ([]byte, error) {
	envelope, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Decrypt(key, envelope)
}

func Fingerprint(key []byte) string {
	digest := sha256.Sum256(key)
	return fmt.Sprintf("%x", digest[:8])
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create parent directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ai-web-engine-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temporary file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return os.Chmod(path, mode)
}
