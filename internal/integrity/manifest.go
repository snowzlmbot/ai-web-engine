package integrity

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	Format              = 1
	ScriptInit          = "init.sh"
	ScriptStart         = "start.sh"
	ScriptStop          = "stop.sh"
	ScriptRollback      = "rollback.sh"
	PublicKeyEnv        = "AI_WEB_ENGINE_SCRIPT_PUBLIC_KEY"
	ExpectedScriptCount = 4
)

// DefaultPublicKeyBase64 is the release trust anchor. It is public data; the
// matching Ed25519 private key remains outside the repository and CI logs.
const DefaultPublicKeyBase64 = "xV4IRjWBAG1CZqxH8fVqbRAr9K1E1dm+HQONwZ+HVdg="

// PublicKeyBase64 may be replaced at release build time with the same public
// key through -ldflags. A fixed default prevents unsigned local manifests from
// being accepted by a production binary.
var PublicKeyBase64 = DefaultPublicKeyBase64

var RequiredScripts = []string{ScriptInit, ScriptStart, ScriptStop, ScriptRollback}

type Manifest struct {
	Format  int               `json:"format"`
	Version string            `json:"version"`
	Scripts map[string]string `json:"scripts"`
}

func BuildManifest(version, scriptsDir string) (Manifest, error) {
	if strings.TrimSpace(version) == "" {
		return Manifest{}, errors.New("integrity manifest version is empty")
	}
	manifest := Manifest{Format: Format, Version: version, Scripts: make(map[string]string, ExpectedScriptCount)}
	for _, name := range RequiredScripts {
		digest, err := fileSHA256(filepath.Join(scriptsDir, name))
		if err != nil {
			return Manifest{}, fmt.Errorf("hash script %s: %w", name, err)
		}
		manifest.Scripts[name] = digest
	}
	return manifest, nil
}

func MarshalManifest(manifest Manifest) ([]byte, error) {
	data, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	if _, err := LoadManifest(strings.NewReader(string(data))); err != nil {
		return nil, err
	}
	return json.MarshalIndent(manifest, "", "  ")
}

func LoadManifest(r io.Reader) (Manifest, error) {
	var manifest Manifest
	decoder := json.NewDecoder(io.LimitReader(r, 64<<10))
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode integrity manifest: %w", err)
	}
	if manifest.Format != Format {
		return Manifest{}, fmt.Errorf("unsupported integrity manifest format %d", manifest.Format)
	}
	if strings.TrimSpace(manifest.Version) == "" {
		return Manifest{}, errors.New("integrity manifest version is empty")
	}
	if len(manifest.Scripts) != ExpectedScriptCount {
		return Manifest{}, fmt.Errorf("integrity manifest must contain exactly %d scripts", ExpectedScriptCount)
	}
	for _, name := range RequiredScripts {
		digest := strings.ToLower(strings.TrimSpace(manifest.Scripts[name]))
		if len(digest) != sha256.Size*2 {
			return Manifest{}, fmt.Errorf("invalid SHA-256 for %s", name)
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return Manifest{}, fmt.Errorf("invalid SHA-256 for %s: %w", name, err)
		}
		manifest.Scripts[name] = digest
	}
	for name := range manifest.Scripts {
		if !contains(RequiredScripts, name) {
			return Manifest{}, fmt.Errorf("unexpected script in integrity manifest: %s", name)
		}
	}
	return manifest, nil
}

func CanonicalBytes(manifest Manifest) []byte {
	names := append([]string(nil), RequiredScripts...)
	sort.Strings(names)
	var b strings.Builder
	fmt.Fprintf(&b, "format=%d\nversion=%s\n", manifest.Format, manifest.Version)
	for _, name := range names {
		fmt.Fprintf(&b, "script=%s sha256=%s\n", name, strings.ToLower(manifest.Scripts[name]))
	}
	return []byte(b.String())
}

func SignManifest(manifest Manifest, privatePEM []byte) ([]byte, ed25519.PublicKey, error) {
	block, _ := pem.Decode(privatePEM)
	if block == nil {
		return nil, nil, errors.New("signing key is not PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse Ed25519 private key: %w", err)
	}
	privateKey, ok := key.(ed25519.PrivateKey)
	if !ok || len(privateKey) != ed25519.PrivateKeySize {
		return nil, nil, errors.New("signing key is not Ed25519")
	}
	publicKey := privateKey.Public().(ed25519.PublicKey)
	return ed25519.Sign(privateKey, CanonicalBytes(manifest)), publicKey, nil
}

func EncodeSignature(signature []byte) []byte {
	return []byte(base64.StdEncoding.EncodeToString(signature) + "\n")
}

func EncodePublicKeyFile(key ed25519.PublicKey) []byte {
	return []byte(EncodePublicKey(key) + "\n")
}

func EncodePublicKey(key ed25519.PublicKey) string {
	return base64.StdEncoding.EncodeToString(key)
}

func DecodePublicKey(data []byte) (ed25519.PublicKey, error) {
	trimmed := strings.TrimSpace(string(data))
	if decoded, err := base64.StdEncoding.DecodeString(trimmed); err == nil {
		data = decoded
	} else {
		data = bytesTrimSpace(data)
	}
	if len(data) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("Ed25519 public key must be %d bytes", ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(append([]byte(nil), data...)), nil
}

func DecodeSignature(data []byte) ([]byte, error) {
	trimmed := strings.TrimSpace(string(data))
	if decoded, err := base64.StdEncoding.DecodeString(trimmed); err == nil {
		data = decoded
	} else {
		data = bytesTrimSpace(data)
	}
	if len(data) != ed25519.SignatureSize {
		return nil, fmt.Errorf("Ed25519 signature must be %d bytes", ed25519.SignatureSize)
	}
	return append([]byte(nil), data...), nil
}

func Verify(manifestPath, signaturePath, publicKeyPath, scriptsDir, expectedVersion string) error {
	manifestFile, err := os.Open(manifestPath)
	if err != nil {
		return fmt.Errorf("open integrity manifest: %w", err)
	}
	manifest, err := LoadManifest(manifestFile)
	_ = manifestFile.Close()
	if err != nil {
		return err
	}
	if expectedVersion != "" && manifest.Version != expectedVersion {
		return fmt.Errorf("integrity manifest version %q does not match expected version %q", manifest.Version, expectedVersion)
	}
	signatureData, err := os.ReadFile(signaturePath)
	if err != nil {
		return fmt.Errorf("read integrity signature: %w", err)
	}
	signature, err := DecodeSignature(signatureData)
	if err != nil {
		return err
	}
	publicKeyData, err := os.ReadFile(publicKeyPath)
	if err != nil {
		return fmt.Errorf("read integrity public key: %w", err)
	}
	publicKey, err := DecodePublicKey(publicKeyData)
	if err != nil {
		return err
	}
	if PublicKeyBase64 != "" {
		trusted, err := DecodePublicKey([]byte(PublicKeyBase64))
		if err != nil {
			return fmt.Errorf("invalid embedded integrity public key: %w", err)
		}
		if !bytes.Equal(publicKey, trusted) {
			return errors.New("integrity public key does not match embedded trust anchor")
		}
	}
	if !ed25519.Verify(publicKey, CanonicalBytes(manifest), signature) {
		return errors.New("integrity manifest signature verification failed")
	}
	for _, name := range RequiredScripts {
		path := filepath.Join(scriptsDir, name)
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("stat script %s: %w", name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("script %s is not a regular file", name)
		}
		actual, err := fileSHA256(path)
		if err != nil {
			return fmt.Errorf("hash script %s: %w", name, err)
		}
		if !strings.EqualFold(actual, manifest.Scripts[name]) {
			return fmt.Errorf("script %s SHA-256 mismatch", name)
		}
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func bytesTrimSpace(data []byte) []byte {
	return []byte(strings.TrimSpace(string(data)))
}
