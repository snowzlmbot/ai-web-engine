package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigRoundTripAndRedaction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model_config.json")
	cfg := Config{Provider: "custom", Endpoint: "https://example.test/v1/chat/completions", Protocol: ProtocolOpenAI, APIKeyEnv: "AI_AGENT_API_KEY", DefaultModelID: "demo", ReasoningLevel: "medium"}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.APIKeyEnv != "AI_AGENT_API_KEY" || loaded.DefaultModelID != "demo" {
		t.Fatalf("unexpected config: %+v", loaded)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" || string(data) == "demo-secret" {
		t.Fatal("unexpected config contents")
	}
	if got := Redacted(loaded)["apiKey"]; got != "configured" {
		t.Fatalf("redaction = %v", got)
	}
}
