package integrity

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildManifestAndVerifyRejectsTampering(t *testing.T) {
	root := t.TempDir()
	scriptsDir := filepath.Join(root, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range RequiredScripts {
		if err := os.WriteFile(filepath.Join(scriptsDir, name), []byte("#!/system/bin/sh\nprintf '%s\\n' "+name+"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	manifest, err := BuildManifest("1.2.3", scriptsDir)
	if err != nil {
		t.Fatal(err)
	}
	manifestBytes, err := MarshalManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "manifest.json")
	if err := os.WriteFile(manifestPath, manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	previousTrustAnchor := PublicKeyBase64
	PublicKeyBase64 = EncodePublicKey(publicKey)
	defer func() { PublicKeyBase64 = previousTrustAnchor }()
	signature := ed25519.Sign(privateKey, CanonicalBytes(manifest))
	signaturePath := filepath.Join(root, "manifest.sig")
	publicKeyPath := filepath.Join(root, "manifest.pub")
	if err := os.WriteFile(signaturePath, []byte(base64.StdEncoding.EncodeToString(signature)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(publicKeyPath, []byte(base64.StdEncoding.EncodeToString(publicKey)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(manifestPath, signaturePath, publicKeyPath, scriptsDir, "1.2.3"); err != nil {
		t.Fatalf("valid release rejected: %v", err)
	}

	if err := os.WriteFile(filepath.Join(scriptsDir, ScriptStart), []byte("tampered\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Verify(manifestPath, signaturePath, publicKeyPath, scriptsDir, "1.2.3"); err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("tampered script was accepted: %v", err)
	}
}

func TestLoadManifestRejectsMissingOrExtraScript(t *testing.T) {
	_, err := LoadManifest(strings.NewReader(`{"format":1,"version":"1.0.0","scripts":{"init.sh":"00"}}`))
	if err == nil {
		t.Fatal("manifest with missing scripts was accepted")
	}
	_, err = LoadManifest(strings.NewReader(`{"format":1,"version":"1.0.0","scripts":{"init.sh":"0000000000000000000000000000000000000000000000000000000000000000","start.sh":"0000000000000000000000000000000000000000000000000000000000000000","stop.sh":"0000000000000000000000000000000000000000000000000000000000000000","rollback.sh":"0000000000000000000000000000000000000000000000000000000000000000","extra.sh":"0000000000000000000000000000000000000000000000000000000000000000"}}`))
	if err == nil {
		t.Fatal("manifest with an extra script was accepted")
	}
}
