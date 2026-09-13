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

	"github.com/snowzlmbot/ai-web-engine/internal/config"
	"github.com/snowzlmbot/ai-web-engine/internal/model"
	"github.com/snowzlmbot/ai-web-engine/internal/session"
	"github.com/snowzlmbot/ai-web-engine/internal/skills"
	"github.com/snowzlmbot/ai-web-engine/web"
)

type Server struct {
	cfgPath   string
	cfgMu     sync.RWMutex
	cfg       config.Config
	store     *session.Store
	client    *model.Client
	skillRoot string
	skillMu   sync.RWMutex
	skillList []skills.Skill
	skillText string
	logger    *log.Logger
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
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.ui)
	mux.HandleFunc("/health", s.health)
	mux.HandleFunc("/api/config", s.configHandler)
	mux.HandleFunc("/api/models", s.models)
	mux.HandleFunc("/api/sessions", s.sessions)
	mux.HandleFunc("/api/sessions/", s.sessions)
	mux.HandleFunc("/api/skills", s.skillsHandler)
	mux.HandleFunc("/api/skills/reload", s.reloadSkills)
	mux.HandleFunc("/api/chat", s.chat)
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

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	s.skillMu.RLock()
	skillCount := len(s.skillList)
	s.skillMu.RUnlock()
	s.writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "skillCount": skillCount, "configured": s.configured(),
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
		"reasoningLevels": []string{"off", "low", "medium", "high"},
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
		SessionID      string `json:"sessionId"`
		Message        string `json:"message"`
		ModelID        string `json:"modelId"`
		ReasoningLevel string `json:"reasoningLevel"`
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

	s.cfgMu.RLock()
	cfg := s.cfg
	s.cfgMu.RUnlock()
	modelID, err := config.SelectModel(cfg, req.ModelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.ReasoningLevel == "" {
		req.ReasoningLevel = cfg.ReasoningLevel
	}
	if req.ReasoningLevel != "off" && req.ReasoningLevel != "low" && req.ReasoningLevel != "medium" && req.ReasoningLevel != "high" {
		http.Error(w, "invalid reasoningLevel", http.StatusBadRequest)
		return
	}

	messages := append(append([]session.Message(nil), item.Messages...), session.Message{Role: "user", Content: req.Message})
	s.skillMu.RLock()
	system := s.skillText
	s.skillMu.RUnlock()

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
	err = s.client.Stream(cfg, modelID, req.ReasoningLevel, system, messages, func(delta model.Delta) error {
		if delta.Content != "" {
			assistant.WriteString(delta.Content)
			sse(w, map[string]any{"type": "delta", "content": delta.Content})
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

	item.Messages = append(messages, session.Message{Role: "assistant", Content: assistant.String()})
	if err := s.store.Save(item); err != nil {
		s.logger.Printf("session save error: %v", err)
		sse(w, map[string]any{"type": "error", "message": "session save failed"})
		flusher.Flush()
		return
	}
	sse(w, map[string]any{"type": "done", "sessionId": item.ID})
	flusher.Flush()
}

func (s *Server) configured() bool {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return strings.TrimSpace(s.cfg.Provider) != "" && strings.TrimSpace(s.cfg.Endpoint) != "" && strings.TrimSpace(s.cfg.APIKey) != ""
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
