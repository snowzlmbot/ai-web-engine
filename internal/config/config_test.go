package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveAPIKeyPrefersShortXEnvironment(t *testing.T) {
	cfg := Config{APIKey: "file-key", APIKeyEnv: "OTHER_KEY"}
	got := ResolveAPIKey(cfg, func(name string) string {
		if name == "AI_WEB_ENGINE_API_KEY" {
			return "shortx-key"
		}
		return "other-key"
	})
	if got.APIKey != "shortx-key" || got.APIKeyEnv != "AI_WEB_ENGINE_API_KEY" {
		t.Fatalf("unexpected environment key resolution: %+v", got)
	}
}

func TestConfigRoundTripAndRedaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model_config.json")
	cfg := Config{
		Provider: "custom", Endpoint: "https://example.test/v1/chat/completions",
		Protocol: ProtocolOpenAI, APIKey: "should-not-persist", APIKeyEnv: "AI_WEB_ENGINE_API_KEY",
		DefaultModelID: "demo", ReasoningLevel: "medium",
	}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.APIKeyEnv != "AI_WEB_ENGINE_API_KEY" || loaded.DefaultModelID != "demo" {
		t.Fatalf("unexpected config: %+v", loaded)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" || string(data) == "demo-secret" || strings.Contains(string(data), "should-not-persist") {
		t.Fatal("unexpected config contents")
	}
	if got := Redacted(loaded)["apiKey"]; got != "configured" {
		t.Fatalf("redaction = %v", got)
	}
}

func TestProviderKeyIsolationAndPermissions(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config", "providers.json")
	if err := SaveProviderKey(configPath, "provider-a", "key-a"); err != nil {
		t.Fatal(err)
	}
	if err := SaveProviderKey(configPath, "provider-b", "key-b"); err != nil {
		t.Fatal(err)
	}
	gotA, err := LoadProviderKey(configPath, "provider-a")
	if err != nil || gotA != "key-a" {
		t.Fatalf("provider A key = %q, %v", gotA, err)
	}
	gotB, err := LoadProviderKey(configPath, "provider-b")
	if err != nil || gotB != "key-b" {
		t.Fatalf("provider B key = %q, %v", gotB, err)
	}
	for _, id := range []string{"provider-a", "provider-b"} {
		info, err := os.Stat(ProviderKeyPath(configPath, id))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("provider %s key mode = %o, want 600", id, info.Mode().Perm())
		}
	}
	if err := DeleteProviderKey(configPath, "provider-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProviderKey(configPath, "provider-b"); !os.IsNotExist(err) {
		t.Fatalf("provider B key remains after delete: %v", err)
	}
	if gotA, err := LoadProviderKey(configPath, "provider-a"); err != nil || gotA != "key-a" {
		t.Fatalf("provider A key changed after B delete: %q, %v", gotA, err)
	}
}

func TestReasoningDefaultsIncludeXHighAndMax(t *testing.T) {
	cfg := Default()
	if cfg.ReasoningLevel != "xhigh" {
		t.Fatalf("default reasoning = %q, want xhigh", cfg.ReasoningLevel)
	}
	for _, level := range []string{"xhigh", "max"} {
		cfg.ReasoningLevel = level
		if err := Validate(cfg); err != nil {
			t.Fatalf("reasoning %s rejected: %v", level, err)
		}
	}
}

func TestProviderRegistryRoundTripKeepsProvidersWithoutKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "providers.json")
	cfg := Config{
		Providers: []Provider{
			{ID: "provider-a", Name: "Provider A", Endpoint: "https://a.example/v1/chat/completions", Protocol: ProtocolOpenAI, DefaultModelID: "model-a", Models: []Model{{ID: "model-a", Enabled: true}}},
			{ID: "provider-b", Name: "Provider B", Endpoint: "https://b.example/v1/responses", Protocol: ProtocolOpenAIResponses, DefaultModelID: "model-b", Models: []Model{{ID: "model-b", Enabled: true}}},
		},
		ActiveProviderID: "provider-b",
		ReasoningLevel:   "xhigh",
	}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ActiveProviderID != "provider-b" || len(loaded.Providers) != 2 {
		t.Fatalf("registry round trip = %+v", loaded)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "key-a") || strings.Contains(string(data), "key-b") {
		t.Fatal("provider keys appeared in registry JSON")
	}
}

func TestLoadRuntimeMigratesLegacyProviderAndKey(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "model_config.json")
	legacy := Config{
		Provider:       "Legacy Provider",
		Endpoint:       "https://legacy.example/v1/chat/completions",
		Protocol:       ProtocolOpenAI,
		APIKeyEnv:      "AI_WEB_ENGINE_API_KEY",
		DefaultModelID: "legacy-model",
		Models:         []Model{{ID: "legacy-model", Enabled: true}},
		ReasoningLevel: "high",
	}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveLegacyKey(path, "legacy-key"); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadRuntime(path, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Providers) != 1 || loaded.ActiveProviderID != "legacy-provider" {
		t.Fatalf("legacy provider was not migrated: %+v", loaded)
	}
	if loaded.APIKey != "legacy-key" {
		t.Fatalf("legacy key was not loaded: %q", loaded.APIKey)
	}
	migratedKey, err := LoadProviderKey(path, "legacy-provider")
	if err != nil || migratedKey != "legacy-key" {
		t.Fatalf("migrated provider key = %q, %v", migratedKey, err)
	}
	migratedData, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(migratedData), "legacy-key") {
		t.Fatal("legacy key appeared in migrated registry JSON")
	}
}

func TestLoadProviderKeyRejectsEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "providers.json")
	keyPath := ProviderKeyPath(path, "provider-a")
	if err := os.MkdirAll(filepath.Dir(keyPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProviderKey(path, "provider-a"); err == nil {
		t.Fatal("empty provider key was accepted")
	}
}
