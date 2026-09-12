package config

import (
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
