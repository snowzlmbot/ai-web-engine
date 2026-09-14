package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/snowzlmbot/ai-web-engine/internal/buildinfo"
	"github.com/snowzlmbot/ai-web-engine/internal/config"
	"github.com/snowzlmbot/ai-web-engine/internal/device"
	"github.com/snowzlmbot/ai-web-engine/internal/model"
	"github.com/snowzlmbot/ai-web-engine/internal/session"
	"github.com/snowzlmbot/ai-web-engine/internal/shortx"
	"github.com/snowzlmbot/ai-web-engine/internal/skills"
	"github.com/snowzlmbot/ai-web-engine/web"
)

var messageSequence atomic.Uint64

type Server struct {
	cfgPath    string
	cfgMu      sync.RWMutex
	cfg        config.Config
	store      *session.Store
	client     *model.Client
	skillRoot  string
	skillMu    sync.RWMutex
	skillList  []skills.Skill
	skillText  string
	logger     *log.Logger
	restartFn  func()
	keyMu      sync.RWMutex
	instanceID string
}

func New(cfgPath, skillRoot string, cfg config.Config, store *session.Store, logger *log.Logger) (*Server, error) {
	list, text, err := skills.Load(skillRoot)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, errors.New("no SKILL.md files found")
	}
	found := false
	for _, item := range list {
		if item.Name == "shortx-rule-creator/SKILL.md" || strings.HasSuffix(item.Name, "/shortx-rule-creator/SKILL.md") {
			found = true
			break
		}
	}
	if !found {
		return nil, errors.New("shortx-rule-creator/SKILL.md was not loaded")
	}
	return &Server{
		cfgPath: cfgPath, cfg: cfg, store: store, client: model.NewClient(),
		skillRoot: skillRoot, skillList: list, skillText: text, logger: logger,
		instanceID: fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid()),
	}, nil
}

func (s *Server) SetRestartFunc(fn func()) {
	s.restartFn = fn
}

func (s *Server) providerConfig(id string, requireKey bool) (config.Config, error) {
	s.cfgMu.RLock()
	cfg := s.cfg
	s.cfgMu.RUnlock()
	if len(cfg.Providers) == 0 {
		return cfg, nil
	}
	if id == "" {
		id = cfg.ActiveProviderID
	}
	for _, provider := range cfg.Providers {
		if provider.ID != id {
			continue
		}
		key, err := config.LoadProviderKey(s.cfgPath, id)
		if err != nil && requireKey {
			return config.Config{}, fmt.Errorf("provider %q key is unavailable: %w", id, err)
		}
		return cfg.WithProvider(provider, key), nil
	}
	return config.Config{}, fmt.Errorf("provider %q not found", id)
}

func (s *Server) currentProviderConfig(id string) (config.Config, error) {
	return s.providerConfig(id, true)
}

func (s *Server) providerMetadataConfig(id string) (config.Config, error) {
	return s.providerConfig(id, false)
}

func (s *Server) reloadConfigFromDisk() error {
	cfg, err := config.LoadRuntime(s.cfgPath, os.Getenv)
	if err != nil {
		return err
	}
	if len(cfg.Providers) > 0 {
		provider, providerErr := cfg.ActiveProvider()
		if providerErr != nil {
			return providerErr
		}
		key, keyErr := config.LoadProviderKey(s.cfgPath, provider.ID)
		if keyErr == nil {
			cfg = cfg.WithProvider(provider, key)
		} else {
			cfg = cfg.WithProvider(provider, "")
		}
	}
	s.cfgMu.Lock()
	s.cfg = cfg
	s.cfgMu.Unlock()
	return nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.ui)
	mux.HandleFunc("/health", s.health)
	mux.HandleFunc("/api/config", s.configHandler)
	mux.HandleFunc("/api/config/reload", s.reloadConfigHandler)
	mux.HandleFunc("/api/engine/restart", s.restartHandler)
	mux.HandleFunc("/api/providers", s.providersHandler)
	mux.HandleFunc("/api/providers/", s.providerByIDHandler)
	mux.HandleFunc("/api/models", s.models)
	mux.HandleFunc("/api/device-capabilities", s.deviceCapabilities)
	mux.HandleFunc("/api/sessions", s.sessions)
	mux.HandleFunc("/api/sessions/", s.sessions)
	mux.HandleFunc("/api/skills", s.skillsHandler)
	mux.HandleFunc("/api/skills/reload", s.reloadSkills)
	mux.HandleFunc("/api/chat", s.chat)
	mux.HandleFunc("/api/shortx/validate", s.validateShortX)
	return mux
}

func (s *Server) ui(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	data, err := web.Files.ReadFile(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	contentType := "text/plain; charset=utf-8"
	switch {
	case strings.HasSuffix(name, ".html"):
		contentType = "text/html; charset=utf-8"
	case strings.HasSuffix(name, ".js"):
		contentType = "application/javascript; charset=utf-8"
	case strings.HasSuffix(name, ".css"):
		contentType = "text/css; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func (s *Server) validateShortX(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var request struct {
		Text string `json:"text"`
	}
	if err := decodeJSON(r, &request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	doc, err := shortx.Parse(request.Text)
	if err != nil {
		s.writeJSON(w, http.StatusOK, map[string]any{"valid": false, "error": err.Error()})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"valid": true, "kind": doc.Kind, "id": doc.ID, "title": doc.Title,
		"canonical": doc.Canonical, "filename": doc.ID + ".txt",
	})
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	s.skillMu.RLock()
	skillCount := len(s.skillList)
	s.skillMu.RUnlock()
	s.writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "version": buildinfo.Version, "skillCount": skillCount, "configured": s.configured(), "instanceId": s.instanceID,
	})
}

func (s *Server) configHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.cfgMu.RLock()
		cfg := s.cfg
		s.cfgMu.RUnlock()
		s.writeJSON(w, http.StatusOK, config.Redacted(cfg))
	case http.MethodPost:
		var incoming config.Config
		if err := decodeJSON(r, &incoming); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if incoming.APIKey == "" && incoming.APIKeyEnv == "" {
			s.cfgMu.RLock()
			incoming.APIKey = s.cfg.APIKey
			incoming.APIKeyEnv = s.cfg.APIKeyEnv
			s.cfgMu.RUnlock()
		}
		if incoming.Models == nil {
			incoming.Models = []config.Model{}
		}
		// ShortX's environment-backed key is authoritative and is never
		// persisted in model_config.json. Resolve it before publishing the
		// new in-memory configuration so health/chat reflect the saved state.
		incoming = config.ResolveAPIKey(incoming, os.Getenv)
		if err := config.Validate(incoming); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := config.Save(s.cfgPath, incoming); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.cfgMu.Lock()
		s.cfg = incoming
		s.cfgMu.Unlock()
		s.logger.Printf("configuration updated provider=%s protocol=%s", incoming.Provider, incoming.Protocol)
		s.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) reloadConfigHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.reloadConfigFromDisk(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.cfgMu.RLock()
	cfg := s.cfg
	s.cfgMu.RUnlock()
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "configured": s.configured(), "config": config.Redacted(cfg)})
}

func (s *Server) restartHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "status": "restarting"})
	if s.restartFn != nil {
		go func() {
			time.Sleep(100 * time.Millisecond)
			s.restartFn()
		}()
	}
}

func (s *Server) deviceCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.writeJSON(w, http.StatusOK, device.Collect())
}

type providerRequest struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Endpoint       string         `json:"endpoint"`
	Protocol       string         `json:"protocol"`
	DefaultModelID string         `json:"defaultModelId"`
	Models         []config.Model `json:"models"`
	Key            string         `json:"key"`
}

func (s *Server) providersHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.cfgMu.RLock()
		cfg := s.cfg
		s.cfgMu.RUnlock()
		providers := make([]map[string]any, 0, len(cfg.Providers))
		for _, provider := range cfg.Providers {
			key, err := config.LoadProviderKey(s.cfgPath, provider.ID)
			keyConfigured := err == nil && strings.TrimSpace(key) != ""
			providers = append(providers, map[string]any{
				"id": provider.ID, "name": provider.Name, "endpoint": provider.Endpoint,
				"protocol": provider.Protocol, "defaultModelId": provider.DefaultModelID,
				"models": provider.Models, "keyConfigured": keyConfigured,
			})
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"activeProviderId": cfg.ActiveProviderID, "providers": providers})
	case http.MethodPost:
		var req providerRequest
		if err := decodeJSON(r, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		provider := config.Provider{ID: strings.TrimSpace(req.ID), Name: strings.TrimSpace(req.Name), Endpoint: strings.TrimSpace(req.Endpoint), Protocol: req.Protocol, DefaultModelID: strings.TrimSpace(req.DefaultModelID), Models: req.Models}
		if provider.ID == "" {
			provider.ID = strings.ToLower(strings.NewReplacer(" ", "-", "/", "-", ":", "-").Replace(provider.Name))
		}
		if provider.Models == nil {
			provider.Models = []config.Model{}
		}
		if err := config.ValidateProvider(provider); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.Key) != "" {
			oldKey, oldKeyErr := config.LoadProviderKey(s.cfgPath, provider.ID)
			if err := config.SaveProviderKey(s.cfgPath, provider.ID, req.Key); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if err := s.upsertProvider(provider); err != nil {
				if oldKeyErr == nil {
					_ = config.SaveProviderKey(s.cfgPath, provider.ID, oldKey)
				} else {
					_ = config.DeleteProviderKey(s.cfgPath, provider.ID)
				}
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		} else if err := s.upsertProvider(provider); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": provider.ID})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) providerByIDHandler(w http.ResponseWriter, r *http.Request) {
	id := path.Base(r.URL.Path)
	if r.Method == http.MethodPost && r.URL.Path == "/api/providers/select" {
		var req struct {
			ID string `json:"id"`
		}
		if err := decodeJSON(r, &req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.selectProvider(req.ID); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "activeProviderId": req.ID})
		return
	}
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.deleteProvider(id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) upsertProvider(provider config.Provider) error {
	s.cfgMu.RLock()
	cfg := s.cfg
	s.cfgMu.RUnlock()
	found := false
	for i := range cfg.Providers {
		if cfg.Providers[i].ID == provider.ID {
			cfg.Providers[i] = provider
			found = true
			break
		}
	}
	if !found {
		cfg.Providers = append(cfg.Providers, provider)
	}
	if cfg.ActiveProviderID == "" {
		cfg.ActiveProviderID = provider.ID
	}
	active, err := cfg.ActiveProvider()
	if err != nil {
		return err
	}
	key, _ := config.LoadProviderKey(s.cfgPath, cfg.ActiveProviderID)
	cfg = cfg.WithProvider(active, key)
	if err := config.Save(s.cfgPath, cfg); err != nil {
		return err
	}
	s.cfgMu.Lock()
	s.cfg = cfg
	s.cfgMu.Unlock()
	return nil
}

func (s *Server) selectProvider(id string) error {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	cfg := s.cfg
	provider, found := config.Provider{}, false
	for _, candidate := range cfg.Providers {
		if candidate.ID == id {
			provider, found = candidate, true
			break
		}
	}
	if !found {
		return fmt.Errorf("provider %q not found", id)
	}
	cfg.ActiveProviderID = id
	key, _ := config.LoadProviderKey(s.cfgPath, id)
	cfg = cfg.WithProvider(provider, key)
	if err := config.Save(s.cfgPath, cfg); err != nil {
		return err
	}
	s.cfg = cfg
	return nil
}

func (s *Server) deleteProvider(id string) error {
	s.cfgMu.RLock()
	cfg := s.cfg
	s.cfgMu.RUnlock()
	if len(cfg.Providers) <= 1 {
		return errors.New("cannot delete the last provider")
	}
	providers := make([]config.Provider, 0, len(cfg.Providers)-1)
	found := false
	for _, provider := range cfg.Providers {
		if provider.ID == id {
			found = true
			continue
		}
		providers = append(providers, provider)
	}
	if !found {
		return fmt.Errorf("provider %q not found", id)
	}
	oldCfg := cfg
	cfg.Providers = providers
	if cfg.ActiveProviderID == id {
		cfg.ActiveProviderID = providers[0].ID
	}
	provider, err := cfg.ActiveProvider()
	if err != nil {
		return err
	}
	key, _ := config.LoadProviderKey(s.cfgPath, cfg.ActiveProviderID)
	nextCfg := cfg.WithProvider(provider, key)
	if err := config.Save(s.cfgPath, nextCfg); err != nil {
		return err
	}
	if err := config.DeleteProviderKey(s.cfgPath, id); err != nil {
		_ = config.Save(s.cfgPath, oldCfg)
		return err
	}
	s.cfgMu.Lock()
	s.cfg = nextCfg
	s.cfgMu.Unlock()
	return nil
}

func (s *Server) models(w http.ResponseWriter, _ *http.Request) {
	s.cfgMu.RLock()
	cfg := s.cfg
	s.cfgMu.RUnlock()
	result := make([]config.Model, 0, len(cfg.Models)+1)
	result = append(result, cfg.Models...)
	if cfg.DefaultModelID != "" {
		found := false
		for _, item := range result {
			if item.ID == cfg.DefaultModelID {
				found = true
				break
			}
		}
		if !found {
			result = append(result, config.Model{ID: cfg.DefaultModelID, Enabled: true})
		}
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"models": result, "defaultModelId": cfg.DefaultModelID,
		"reasoningLevels": []string{"off", "low", "medium", "high", "xhigh", "max"},
	})
}

func (s *Server) sessions(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/api/sessions" {
		id := path.Base(r.URL.Path)
		switch r.Method {
		case http.MethodGet:
			item, err := s.store.Get(id)
			if err != nil {
				http.Error(w, "session not found", http.StatusNotFound)
				return
			}
			s.writeJSON(w, http.StatusOK, item)
		case http.MethodPatch:
			var settings struct {
				ProviderID       string `json:"providerId"`
				ModelID          string `json:"modelId"`
				ReasoningLevel   string `json:"reasoningLevel"`
				ReasoningDisplay string `json:"reasoningDisplay"`
			}
			if err := decodeJSON(r, &settings); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			item, err := s.store.Get(id)
			if err != nil {
				http.Error(w, "session not found", http.StatusNotFound)
				return
			}
			providerID := item.ProviderID
			var providerCfg config.Config
			if settings.ProviderID != "" {
				providerID = settings.ProviderID
				var providerErr error
				providerCfg, providerErr = s.providerMetadataConfig(providerID)
				if providerErr != nil {
					http.Error(w, providerErr.Error(), http.StatusBadRequest)
					return
				}
				item.ProviderID = providerID
			} else if providerID != "" {
				var providerErr error
				providerCfg, providerErr = s.providerMetadataConfig(providerID)
				if providerErr != nil {
					http.Error(w, providerErr.Error(), http.StatusBadRequest)
					return
				}
			}
			if settings.ModelID != "" {
				if providerID != "" {
					if _, modelErr := config.SelectModel(providerCfg, settings.ModelID); modelErr != nil {
						http.Error(w, modelErr.Error(), http.StatusBadRequest)
						return
					}
				}
				item.ModelID = settings.ModelID
			} else if settings.ProviderID != "" {
				defaultModel, modelErr := config.SelectModel(providerCfg, "")
				if modelErr != nil {
					http.Error(w, modelErr.Error(), http.StatusBadRequest)
					return
				}
				item.ModelID = defaultModel
			}
			if settings.ReasoningLevel != "" {
				if !config.ValidReasoningLevel(settings.ReasoningLevel) {
					http.Error(w, "invalid reasoningLevel", http.StatusBadRequest)
					return
				}
				item.ReasoningLevel = settings.ReasoningLevel
			}
			if settings.ReasoningDisplay != "" {
				if !session.ValidReasoningDisplay(settings.ReasoningDisplay) {
					http.Error(w, "invalid reasoningDisplay", http.StatusBadRequest)
					return
				}
				item.ReasoningDisplay = settings.ReasoningDisplay
			}
			if err := s.store.Save(item); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			s.writeJSON(w, http.StatusOK, item)
		case http.MethodDelete:
			if err := s.store.Delete(id); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			s.writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		return
	}

	switch r.Method {
	case http.MethodGet:
		items, err := s.store.List()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		type summary struct {
			ID        string `json:"id"`
			Title     string `json:"title"`
			CreatedAt int64  `json:"createdAt"`
			UpdatedAt int64  `json:"updatedAt"`
		}
		out := make([]summary, 0, len(items))
		for _, item := range items {
			out = append(out, summary{item.ID, item.Title, item.CreatedAt, item.UpdatedAt})
		}
		s.writeJSON(w, http.StatusOK, out)
	case http.MethodPost:
		item, err := s.store.Create()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.writeJSON(w, http.StatusCreated, item)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) skillsHandler(w http.ResponseWriter, _ *http.Request) {
	s.skillMu.RLock()
	defer s.skillMu.RUnlock()
	out := make([]map[string]string, 0, len(s.skillList))
	for _, item := range s.skillList {
		out = append(out, map[string]string{"name": item.Name, "path": item.Path})
	}
	s.writeJSON(w, http.StatusOK, out)
}

func (s *Server) reloadSkills(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	list, text, err := skills.Load(s.skillRoot)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.skillMu.Lock()
	s.skillList, s.skillText = list, text
	s.skillMu.Unlock()
	s.logger.Printf("skills reloaded count=%d", len(list))
	s.writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": len(list)})
}

func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		SessionID        string `json:"sessionId"`
		Message          string `json:"message"`
		ProviderID       string `json:"providerId"`
		ModelID          string `json:"modelId"`
		ReasoningLevel   string `json:"reasoningLevel"`
		ReasoningDisplay string `json:"reasoningDisplay"`
	}
	if err := decodeJSON(r, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.SessionID) == "" || strings.TrimSpace(req.Message) == "" {
		http.Error(w, "sessionId and message are required", http.StatusBadRequest)
		return
	}
	item, err := s.store.Get(req.SessionID)
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	providerID := req.ProviderID
	if providerID == "" {
		providerID = item.ProviderID
	}
	s.cfgMu.RLock()
	activeCfg := s.cfg
	s.cfgMu.RUnlock()
	if providerID == "" {
		providerID = activeCfg.ActiveProviderID
	}
	if _, metadataErr := s.providerMetadataConfig(providerID); metadataErr != nil && req.ProviderID == "" {
		providerID = activeCfg.ActiveProviderID
	}
	cfg, err := s.currentProviderConfig(providerID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	modelID, err := config.SelectModel(cfg, req.ModelID)
	if req.ModelID == "" {
		if item.ModelID != "" {
			if selected, itemErr := config.SelectModel(cfg, item.ModelID); itemErr == nil {
				modelID = selected
			} else {
				modelID, err = config.SelectModel(cfg, "")
			}
		} else {
			modelID, err = config.SelectModel(cfg, "")
		}
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.ReasoningLevel == "" {
		req.ReasoningLevel = item.ReasoningLevel
	}
	if req.ReasoningLevel == "" {
		req.ReasoningLevel = cfg.ReasoningLevel
	}
	if !config.ValidReasoningLevel(req.ReasoningLevel) {
		http.Error(w, "invalid reasoningLevel", http.StatusBadRequest)
		return
	}
	if req.ReasoningDisplay == "" {
		req.ReasoningDisplay = item.ReasoningDisplay
	}
	if req.ReasoningDisplay == "" {
		req.ReasoningDisplay = session.ReasoningDisplayPartial
	}
	if !session.ValidReasoningDisplay(req.ReasoningDisplay) {
		http.Error(w, "invalid reasoningDisplay", http.StatusBadRequest)
		return
	}
	item.ProviderID = providerID
	item.ModelID = modelID
	item.ReasoningLevel = req.ReasoningLevel
	item.ReasoningDisplay = req.ReasoningDisplay

	messageID := fmt.Sprintf("msg-%d-%d", time.Now().UnixNano(), messageSequence.Add(1))
	draftID := messageID + "-draft"
	userID := messageID + "-user"
	item.Messages = append(item.Messages, session.Message{ID: userID, Role: "user", Content: req.Message})
	messages := append([]session.Message(nil), item.Messages...)
	s.skillMu.RLock()
	system := s.skillText
	s.skillMu.RUnlock()
	if capabilityPrompt := device.Collect().Prompt(); capabilityPrompt != "" {
		system += "\n\n" + capabilityPrompt
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	var assistant strings.Builder
	reasoningShown := 0
	err = s.client.Stream(cfg, modelID, req.ReasoningLevel, system, messages, func(delta model.Delta) error {
		if delta.Reasoning != "" {
			if req.ReasoningDisplay != session.ReasoningDisplayOff {
				content := delta.Reasoning
				if req.ReasoningDisplay == session.ReasoningDisplayPartial {
					remaining := 4000 - reasoningShown
					if remaining <= 0 {
						content = ""
					} else {
						runes := []rune(content)
						if len(runes) > remaining {
							content = string(runes[:remaining])
						}
						reasoningShown += len([]rune(content))
					}
				}
				if content != "" {
					sse(w, map[string]any{"type": "reasoning", "messageId": draftID, "content": content})
					flusher.Flush()
				}
			}
		}
		if delta.Content != "" {
			assistant.WriteString(delta.Content)
			sse(w, map[string]any{"type": "delta", "messageId": messageID, "content": delta.Content})
			flusher.Flush()
		}
		return nil
	})
	if err != nil {
		s.logger.Printf("chat error: %v", err)
		sse(w, map[string]any{"type": "error", "message": err.Error()})
		flusher.Flush()
		return
	}

	item.Messages = append(messages, session.Message{ID: messageID, Role: "assistant", Content: assistant.String()})
	if err := s.store.Save(item); err != nil {
		s.logger.Printf("session save error: %v", err)
		sse(w, map[string]any{"type": "error", "message": "session save failed"})
		flusher.Flush()
		return
	}
	sse(w, map[string]any{"type": "done", "sessionId": item.ID, "messageId": messageID, "draftId": draftID})
	flusher.Flush()
}

func (s *Server) configured() bool {
	s.cfgMu.RLock()
	cfg := s.cfg
	s.cfgMu.RUnlock()
	if len(cfg.Providers) > 0 {
		if strings.TrimSpace(cfg.ActiveProviderID) == "" || strings.TrimSpace(cfg.APIKey) == "" || strings.TrimSpace(cfg.Endpoint) == "" {
			return false
		}
		return true
	}
	return strings.TrimSpace(cfg.Provider) != "" && strings.TrimSpace(cfg.Endpoint) != "" && strings.TrimSpace(cfg.APIKey) != ""
}

func decodeJSON(r *http.Request, value any) error {
	data, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		http.Error(w, "encode response", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func sse(w io.Writer, value any) {
	data, _ := json.Marshal(value)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
}
