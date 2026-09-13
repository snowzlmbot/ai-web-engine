package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	ProtocolOpenAI          = "openai"
	ProtocolOpenAIResponses = "openai-responses"
	ProtocolAnthropic       = "anthropic"
)

var providerIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

type Model struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

type Provider struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Endpoint       string  `json:"endpoint"`
	Protocol       string  `json:"protocol"`
	DefaultModelID string  `json:"defaultModelId"`
	Models         []Model `json:"models"`
}

type Config struct {
	// Legacy active-provider fields remain for request compatibility and are
	// populated from Providers[ActiveProviderID]. They are omitted from the
	// persisted multi-provider registry.
	Provider       string  `json:"provider,omitempty"`
	Endpoint       string  `json:"endpoint,omitempty"`
	Protocol       string  `json:"protocol,omitempty"`
	APIKey         string  `json:"apiKey,omitempty"`
	APIKeyEnv      string  `json:"apiKeyEnv,omitempty"`
	DefaultModelID string  `json:"defaultModelId,omitempty"`
	Models         []Model `json:"models,omitempty"`

	Providers        []Provider `json:"providers,omitempty"`
	ActiveProviderID string     `json:"activeProviderId,omitempty"`
	ReasoningLevel   string     `json:"reasoningLevel"`
}

func Default() Config {
	return Config{Providers: []Provider{}, Models: []Model{}, Protocol: ProtocolOpenAI, ReasoningLevel: "xhigh"}
}

func (c Config) IsMultiProvider() bool { return len(c.Providers) > 0 }

func (c Config) ActiveProvider() (Provider, error) {
	if len(c.Providers) == 0 {
		if c.Provider == "" && c.Endpoint == "" && c.DefaultModelID == "" {
			return Provider{}, errors.New("no provider configured")
		}
		return Provider{ID: providerID(c.Provider), Name: c.Provider, Endpoint: c.Endpoint, Protocol: c.Protocol, DefaultModelID: c.DefaultModelID, Models: c.Models}, nil
	}
	for _, provider := range c.Providers {
		if provider.ID == c.ActiveProviderID {
			return provider, nil
		}
	}
	return Provider{}, fmt.Errorf("active provider %q not found", c.ActiveProviderID)
}

func (c Config) WithProvider(provider Provider, key string) Config {
	c.Provider = provider.Name
	if c.Provider == "" {
		c.Provider = provider.ID
	}
	c.Endpoint = provider.Endpoint
	c.Protocol = provider.Protocol
	c.DefaultModelID = provider.DefaultModelID
	c.Models = append([]Model(nil), provider.Models...)
	c.APIKey = key
	c.APIKeyEnv = ""
	return c
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
	if cfg.Providers == nil {
		cfg.Providers = []Provider{}
	}
	if cfg.Models == nil {
		cfg.Models = []Model{}
	}
	if cfg.Protocol == "" {
		cfg.Protocol = ProtocolOpenAI
	}
	if cfg.ReasoningLevel == "" {
		cfg.ReasoningLevel = "xhigh"
	}
	if len(cfg.Providers) > 0 {
		if cfg.ActiveProviderID == "" {
			cfg.ActiveProviderID = cfg.Providers[0].ID
		}
		provider, providerErr := cfg.ActiveProvider()
		if providerErr != nil {
			return Config{}, providerErr
		}
		cfg.Provider = provider.Name
		cfg.Endpoint = provider.Endpoint
		cfg.Protocol = provider.Protocol
		cfg.DefaultModelID = provider.DefaultModelID
		cfg.Models = append([]Model(nil), provider.Models...)
	}
	if err := Validate(cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// LoadRuntime loads the non-sensitive provider registry and hydrates only the
// selected provider's local key. Provider keys are never selected by name
// heuristics or shared across providers.
func LoadRuntime(path string, lookup func(string) string) (Config, error) {
	cfg, err := Load(path)
	if err != nil {
		return Config{}, err
	}
	if len(cfg.Providers) == 0 && strings.TrimSpace(cfg.Provider) != "" {
		provider, providerErr := cfg.ActiveProvider()
		if providerErr != nil {
			return Config{}, providerErr
		}
		key := strings.TrimSpace(cfg.APIKey)
		if key == "" {
			if legacyKey, keyErr := LoadLegacyKey(path); keyErr == nil {
				key = legacyKey
			} else {
				key = strings.TrimSpace(ResolveAPIKey(cfg, lookup).APIKey)
			}
		}
		cfg.Providers = []Provider{provider}
		cfg.ActiveProviderID = provider.ID
		cfg = cfg.WithProvider(provider, key)
		if key != "" {
			if err := SaveProviderKey(path, provider.ID, key); err != nil {
				return Config{}, fmt.Errorf("migrate legacy provider key: %w", err)
			}
		}
		if err := Save(path, cfg); err != nil {
			return Config{}, fmt.Errorf("migrate legacy provider config: %w", err)
		}
	}
	if len(cfg.Providers) > 0 {
		provider, err := cfg.ActiveProvider()
		if err != nil {
			return Config{}, err
		}
		key, keyErr := LoadProviderKey(path, provider.ID)
		if keyErr == nil {
			cfg = cfg.WithProvider(provider, key)
		} else {
			cfg = cfg.WithProvider(provider, "")
		}
		return cfg, nil
	}
	if cfg.APIKey == "" {
		if key, keyErr := LoadLegacyKey(path); keyErr == nil {
			cfg.APIKey = key
		} else {
			cfg = ResolveAPIKey(cfg, lookup)
		}
	}
	return cfg, nil
}

func Save(path string, cfg Config) error {
	if err := Validate(cfg); err != nil {
		return err
	}
	persisted := cfg
	if len(cfg.Providers) == 0 && strings.TrimSpace(cfg.APIKey) != "" {
		if err := SaveLegacyKey(path, cfg.APIKey); err != nil {
			return fmt.Errorf("save legacy provider key: %w", err)
		}
	}
	persisted.APIKey = ""
	if len(cfg.Providers) == 0 {
		// Keep the legacy environment variable name as non-secret metadata so
		// older single-provider configurations remain restartable.
		persisted.APIKeyEnv = cfg.APIKeyEnv
	} else {
		persisted.APIKeyEnv = ""
	}
	if len(cfg.Providers) > 0 {
		persisted.Provider = ""
		persisted.Endpoint = ""
		persisted.Protocol = ""
		persisted.DefaultModelID = ""
		persisted.Models = nil
	}
	if persisted.Models == nil && len(persisted.Providers) == 0 {
		persisted.Models = []Model{}
	}
	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')
	if err := atomicWrite(path, data, 0o600); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func Validate(cfg Config) error {
	if cfg.ReasoningLevel != "off" && cfg.ReasoningLevel != "low" && cfg.ReasoningLevel != "medium" && cfg.ReasoningLevel != "high" && cfg.ReasoningLevel != "xhigh" && cfg.ReasoningLevel != "max" {
		return fmt.Errorf("unsupported reasoningLevel %q", cfg.ReasoningLevel)
	}
	if len(cfg.Providers) > 0 {
		seen := map[string]bool{}
		for _, provider := range cfg.Providers {
			if !providerIDPattern.MatchString(provider.ID) {
				return fmt.Errorf("invalid provider id %q", provider.ID)
			}
			if seen[provider.ID] {
				return fmt.Errorf("duplicate provider id %q", provider.ID)
			}
			seen[provider.ID] = true
			if err := ValidateProvider(provider); err != nil {
				return err
			}
		}
		if !seen[cfg.ActiveProviderID] {
			return fmt.Errorf("active provider %q not found", cfg.ActiveProviderID)
		}
		return nil
	}
	if cfg.Provider == "" && cfg.Endpoint == "" && cfg.DefaultModelID == "" {
		return nil
	}
	return ValidateProvider(Provider{ID: providerID(cfg.Provider), Name: cfg.Provider, Endpoint: cfg.Endpoint, Protocol: cfg.Protocol, DefaultModelID: cfg.DefaultModelID, Models: cfg.Models})
}

func ValidateProvider(provider Provider) error {
	if !providerIDPattern.MatchString(provider.ID) {
		return fmt.Errorf("invalid provider id %q", provider.ID)
	}
	if strings.TrimSpace(provider.Name) == "" {
		return errors.New("provider is required")
	}
	if provider.Protocol != ProtocolOpenAI && provider.Protocol != ProtocolOpenAIResponses && provider.Protocol != ProtocolAnthropic {
		return fmt.Errorf("unsupported protocol %q", provider.Protocol)
	}
	u, err := url.Parse(provider.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return errors.New("endpoint must be an HTTPS URL")
	}
	if strings.TrimSpace(provider.DefaultModelID) == "" && len(provider.Models) == 0 {
		return errors.New("defaultModelId or at least one model is required")
	}
	return nil
}

func SaveProviderKey(configPath, id, key string) error {
	if !providerIDPattern.MatchString(id) {
		return fmt.Errorf("invalid provider id %q", id)
	}
	if strings.TrimSpace(key) == "" {
		return errors.New("provider key is required")
	}
	return atomicWrite(ProviderKeyPath(configPath, id), []byte(strings.TrimSpace(key)+"\n"), 0o600)
}

func LoadProviderKey(configPath, id string) (string, error) {
	if !providerIDPattern.MatchString(id) {
		return "", fmt.Errorf("invalid provider id %q", id)
	}
	keyPath := ProviderKeyPath(configPath, id)
	info, err := os.Lstat(keyPath)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("provider key is not a regular file")
	}
	if info.Mode().Perm() != 0o600 {
		if err := os.Chmod(keyPath, 0o600); err != nil {
			return "", fmt.Errorf("secure provider key permissions: %w", err)
		}
	}
	data, err := os.ReadFile(keyPath)
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(string(data))
	if key == "" {
		return "", errors.New("provider key is empty")
	}
	return key, nil
}

func DeleteProviderKey(configPath, id string) error {
	if !providerIDPattern.MatchString(id) {
		return fmt.Errorf("invalid provider id %q", id)
	}
	if err := os.Remove(ProviderKeyPath(configPath, id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func ProviderKeyPath(configPath, id string) string {
	return filepath.Join(filepath.Dir(configPath), "providers", id+".key")
}

func LegacyKeyPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "model.key")
}

func SaveLegacyKey(configPath, key string) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("provider key is required")
	}
	return atomicWrite(LegacyKeyPath(configPath), []byte(strings.TrimSpace(key)+"\n"), 0o600)
}

func LoadLegacyKey(configPath string) (string, error) {
	data, err := os.ReadFile(LegacyKeyPath(configPath))
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(string(data))
	if key == "" {
		return "", errors.New("legacy provider key is empty")
	}
	return key, nil
}

func Redacted(cfg Config) map[string]any {
	result := map[string]any{
		"provider": cfg.Provider, "endpoint": cfg.Endpoint, "protocol": cfg.Protocol,
		"apiKey": "", "apiKeyEnv": cfg.APIKeyEnv, "defaultModelId": cfg.DefaultModelID,
		"models": cfg.Models, "reasoningLevel": cfg.ReasoningLevel,
		"activeProviderId": cfg.ActiveProviderID,
	}
	if cfg.APIKey != "" || cfg.APIKeyEnv != "" {
		result["apiKey"] = "configured"
	}
	providers := make([]map[string]any, 0, len(cfg.Providers))
	for _, provider := range cfg.Providers {
		keyConfigured := false
		if cfg.ActiveProviderID == provider.ID && cfg.APIKey != "" {
			keyConfigured = true
		}
		providers = append(providers, map[string]any{
			"id": provider.ID, "name": provider.Name, "endpoint": provider.Endpoint,
			"protocol": provider.Protocol, "defaultModelId": provider.DefaultModelID,
			"models": provider.Models, "keyConfigured": keyConfigured,
		})
	}
	result["providers"] = providers
	return result
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

func ValidReasoningLevel(level string) bool {
	return level == "off" || level == "low" || level == "medium" || level == "high" || level == "xhigh" || level == "max"
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

func providerID(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.NewReplacer(" ", "-", "/", "-", ":", "-").Replace(name)
	if providerIDPattern.MatchString(name) {
		return name
	}
	return "legacy"
}

func NowUnix() int64 { return time.Now().Unix() }
