package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	ProtocolOpenAI    = "openai"
	ProtocolAnthropic = "anthropic"
)

type Model struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

type Config struct {
	Provider       string  `json:"provider"`
	Endpoint       string  `json:"endpoint"`
	Protocol       string  `json:"protocol"`
	APIKey         string  `json:"apiKey"`
	APIKeyEnv      string  `json:"apiKeyEnv,omitempty"`
	DefaultModelID string  `json:"defaultModelId"`
	Models         []Model `json:"models"`
	ReasoningLevel string  `json:"reasoningLevel"`
}

func Default() Config {
	return Config{Models: []Model{}, Protocol: ProtocolOpenAI, ReasoningLevel: "medium"}
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg := Default()
		if err := Save(path, cfg); err != nil {
			return Config{}, err
		}
		return cfg, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Models == nil {
		cfg.Models = []Model{}
	}
	if cfg.Protocol == "" {
		cfg.Protocol = ProtocolOpenAI
	}
	if cfg.ReasoningLevel == "" {
		cfg.ReasoningLevel = "medium"
	}
	if err := Validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func Save(path string, cfg Config) error {
	if err := Validate(cfg); err != nil {
		return err
	}
	if cfg.APIKeyEnv != "" {
		// Environment-backed keys must never be persisted in JSON.
		cfg.APIKey = ""
	}
	if cfg.Models == nil {
		cfg.Models = []Model{}
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".model-config-*")
	if err != nil {
		return fmt.Errorf("create config temp: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return os.Chmod(path, 0o600)
}

func Validate(cfg Config) error {
	if cfg.Provider == "" && cfg.Endpoint == "" && cfg.DefaultModelID == "" {
		return nil
	}
	if strings.TrimSpace(cfg.Provider) == "" {
		return errors.New("provider is required")
	}
	if cfg.Protocol != ProtocolOpenAI && cfg.Protocol != ProtocolAnthropic {
		return fmt.Errorf("unsupported protocol %q", cfg.Protocol)
	}
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return errors.New("endpoint must be an HTTPS URL")
	}
	if strings.TrimSpace(cfg.DefaultModelID) == "" && len(cfg.Models) == 0 {
		return errors.New("defaultModelId or at least one model is required")
	}
	if cfg.ReasoningLevel != "off" && cfg.ReasoningLevel != "low" && cfg.ReasoningLevel != "medium" && cfg.ReasoningLevel != "high" {
		return fmt.Errorf("unsupported reasoningLevel %q", cfg.ReasoningLevel)
	}
	return nil
}

func ResolveAPIKey(cfg Config, lookup func(string) string) Config {
	if value := lookup("AI_WEB_ENGINE_API_KEY"); value != "" {
		cfg.APIKey = value
		cfg.APIKeyEnv = "AI_WEB_ENGINE_API_KEY"
		return cfg
	}
	if cfg.APIKeyEnv != "" {
		if value := lookup(cfg.APIKeyEnv); value != "" {
			cfg.APIKey = value
		}
	}
	return cfg
}
func Redacted(cfg Config) map[string]any {
	masked := ""
	if cfg.APIKey != "" || cfg.APIKeyEnv != "" {
		masked = "configured"
	}
	return map[string]any{
		"provider": cfg.Provider, "endpoint": cfg.Endpoint, "protocol": cfg.Protocol,
		"apiKey": masked, "apiKeyEnv": cfg.APIKeyEnv, "defaultModelId": cfg.DefaultModelID,
		"models": cfg.Models, "reasoningLevel": cfg.ReasoningLevel,
	}
}

func SelectModel(cfg Config, requested string) (string, error) {
	if requested != "" {
		for _, model := range cfg.Models {
			if model.ID == requested {
				return requested, nil
			}
		}
		if requested == cfg.DefaultModelID {
			return requested, nil
		}
		return "", fmt.Errorf("model %q is not configured", requested)
	}
	if cfg.DefaultModelID != "" {
		return cfg.DefaultModelID, nil
	}
	for _, model := range cfg.Models {
		if model.Enabled || len(cfg.Models) == 1 {
			return model.ID, nil
		}
	}
	return "", errors.New("no model configured")
}

func NowUnix() int64 { return time.Now().Unix() }
