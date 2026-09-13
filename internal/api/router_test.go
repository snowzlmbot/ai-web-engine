package api

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snowzlmbot/ai-web-engine/internal/buildinfo"
	"github.com/snowzlmbot/ai-web-engine/internal/config"
	"github.com/snowzlmbot/ai-web-engine/internal/model"
	"github.com/snowzlmbot/ai-web-engine/internal/session"
)

func newTestServer(t *testing.T, cfg config.Config) *Server {
	t.Helper()
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	skillDir := filepath.Join(root, "skills", "shortx-rule-creator")
	if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# shortx-rule-creator\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(configDir, "model_config.json")
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	store, err := session.NewStore(filepath.Join(root, "sessions"), filepath.Join(configDir, "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	server, err := New(configPath, filepath.Join(root, "skills"), cfg, store, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func newTestHandler(t *testing.T, cfg config.Config) http.Handler {
	return newTestServer(t, cfg).Handler()
}

func TestHealthAndUIExposeBuildVersionAndNoStore(t *testing.T) {
	handler := newTestHandler(t, config.Default())

	healthReq := httptest.NewRequest(http.MethodGet, "/health", nil)
	healthRec := httptest.NewRecorder()
	handler.ServeHTTP(healthRec, healthReq)
	var health struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(healthRec.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if health.Version != buildinfo.Version {
		t.Fatalf("health version = %q, want %q", health.Version, buildinfo.Version)
	}

	uiReq := httptest.NewRequest(http.MethodGet, "/", nil)
	uiRec := httptest.NewRecorder()
	handler.ServeHTTP(uiRec, uiReq)
	if uiRec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", uiRec.Header().Get("Cache-Control"))
	}
	if !strings.Contains(uiRec.Body.String(), "buildVersion") {
		t.Fatal("UI does not expose build version marker")
	}
}

func TestModelsReturnsJSONEmptyArrayWhenUnconfigured(t *testing.T) {
	handler := newTestHandler(t, config.Default())
	req := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(payload["models"])); got != "[]" {
		t.Fatalf("models = %s, want []", got)
	}
}

func TestConfigPostResolvesShortXEnvironmentAndUpdatesModels(t *testing.T) {
	t.Setenv("AI_WEB_ENGINE_API_KEY", "test-env-key")
	handler := newTestHandler(t, config.Default())
	body := strings.NewReader(`{"provider":"deepseek","endpoint":"https://api.deepseek.com","protocol":"openai","apiKey":"ignored-web-key","defaultModelId":"deepseek-chat","models":[{"id":"deepseek-chat","enabled":true}],"reasoningLevel":"medium"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/config", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("config status = %d, body = %s", rec.Code, rec.Body.String())
	}

	healthReq := httptest.NewRequest(http.MethodGet, "/health", nil)
	healthRec := httptest.NewRecorder()
	handler.ServeHTTP(healthRec, healthReq)
	var health struct {
		Configured bool `json:"configured"`
	}
	if err := json.Unmarshal(healthRec.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if !health.Configured {
		t.Fatal("health remained unconfigured after saving an environment-backed key")
	}

	configReq := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	configRec := httptest.NewRecorder()
	handler.ServeHTTP(configRec, configReq)
	if strings.Contains(configRec.Body.String(), "test-env-key") || strings.Contains(configRec.Body.String(), "ignored-web-key") {
		t.Fatal("configuration response exposed an API key")
	}

	modelsReq := httptest.NewRequest(http.MethodGet, "/api/models", nil)
	modelsRec := httptest.NewRecorder()
	handler.ServeHTTP(modelsRec, modelsReq)
	if !strings.Contains(modelsRec.Body.String(), "deepseek-chat") {
		t.Fatalf("model list did not contain saved model: %s", modelsRec.Body.String())
	}
}

func TestSessionCreateAndLoad(t *testing.T) {
	handler := newTestHandler(t, config.Default())
	createReq := httptest.NewRequest(http.MethodPost, "/api/sessions", nil)
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", createRec.Code, createRec.Body.String())
	}
	var created session.Session
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.ID == "" {
		t.Fatal("created session has no id")
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/sessions/"+created.ID, nil)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", getRec.Code, getRec.Body.String())
	}
}

func TestChatSSEAndEncryptedSessionPersistence(t *testing.T) {
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, "unexpected provider request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ShortX \"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Rule\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer provider.Close()

	server := newTestServer(t, config.Config{
		Provider: "local-test", Endpoint: provider.URL, Protocol: config.ProtocolOpenAI,
		APIKey: "test-key", DefaultModelID: "demo-model",
		Models: []config.Model{{ID: "demo-model", Enabled: true}}, ReasoningLevel: "low",
	})
	server.client = &model.Client{HTTP: provider.Client()}
	handler := server.Handler()

	createReq := httptest.NewRequest(http.MethodPost, "/api/sessions", nil)
	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", createRec.Code, createRec.Body.String())
	}
	var created session.Session
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	body := strings.NewReader(`{"sessionId":"` + created.ID + `","message":"生成一个 ShortX Rule","modelId":"demo-model","reasoningLevel":"low"}`)
	chatReq := httptest.NewRequest(http.MethodPost, "/api/chat", body)
	chatReq.Header.Set("Content-Type", "application/json")
	chatRec := httptest.NewRecorder()
	handler.ServeHTTP(chatRec, chatReq)
	if chatRec.Code != http.StatusOK {
		t.Fatalf("chat status = %d, body = %s", chatRec.Code, chatRec.Body.String())
	}
	stream := chatRec.Body.String()
	var deltas []string
	var done bool
	for _, block := range strings.Split(stream, "\n\n") {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(block), "data:"))
		if line == "" {
			continue
		}
		var event struct {
			Type      string `json:"type"`
			Content   string `json:"content"`
			SessionID string `json:"sessionId"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("invalid SSE event %q: %v", line, err)
		}
		if event.Type == "delta" {
			deltas = append(deltas, event.Content)
		}
		if event.Type == "done" && event.SessionID == created.ID {
			done = true
		}
	}
	if strings.Join(deltas, "") != "ShortX Rule" || !done {
		t.Fatalf("unexpected SSE stream: %s", stream)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/sessions/"+created.ID, nil)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", getRec.Code, getRec.Body.String())
	}
	var saved session.Session
	if err := json.Unmarshal(getRec.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Messages) != 2 || saved.Messages[0].Role != "user" || saved.Messages[1].Content != "ShortX Rule" {
		t.Fatalf("unexpected persisted messages: %+v", saved.Messages)
	}

	files, err := os.ReadDir(filepath.Join(filepath.Dir(server.cfgPath), "..", "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(filepath.Dir(server.cfgPath), "..", "sessions", file.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "生成一个 ShortX Rule") || strings.Contains(string(data), "ShortX Rule") {
			t.Fatal("session plaintext was written to disk")
		}
	}
}
