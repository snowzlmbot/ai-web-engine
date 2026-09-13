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
	"time"

	"github.com/snowzlmbot/ai-web-engine/internal/buildinfo"
	"github.com/snowzlmbot/ai-web-engine/internal/config"
	"github.com/snowzlmbot/ai-web-engine/internal/model"
	"github.com/snowzlmbot/ai-web-engine/internal/session"
)

func newTestServer(t *testing.T, cfg config.Config) *Server {
	t.Helper()
	return newTestServerWithRoot(t, t.TempDir(), "", cfg)
}

func newTestServerWithRoot(t *testing.T, root, configPath string, cfg config.Config) *Server {
	t.Helper()
	configDir := filepath.Join(root, "config")
	skillDir := filepath.Join(root, "skills", "shortx-rule-creator")
	if err := os.MkdirAll(filepath.Join(skillDir, "references"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# shortx-rule-creator\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if configPath == "" {
		configPath = filepath.Join(configDir, "model_config.json")
		if err := config.Save(configPath, cfg); err != nil {
			t.Fatal(err)
		}
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

func TestProviderCRUDAndKeyIsolation(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config", "model_config.json")
	if err := config.Save(configPath, config.Default()); err != nil {
		t.Fatal(err)
	}
	store, err := session.NewStore(filepath.Join(root, "sessions"), filepath.Join(root, "config", "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(root, "skills", "shortx-rule-creator")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# skill\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server, err := New(configPath, filepath.Join(root, "skills"), config.Default(), store, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	handler := server.Handler()
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/providers", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	if rec := post(`{"id":"provider-a","name":"Provider A","endpoint":"https://a.example/v1/chat/completions","protocol":"openai","defaultModelId":"a-model","models":[{"id":"a-model","enabled":true}],"key":"key-a"}`); rec.Code != http.StatusOK {
		t.Fatalf("provider A status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := post(`{"id":"provider-b","name":"Provider B","endpoint":"https://b.example/v1/responses","protocol":"openai-responses","defaultModelId":"b-model","models":[{"id":"b-model","enabled":true}],"key":"key-b"}`); rec.Code != http.StatusOK {
		t.Fatalf("provider B status=%d body=%s", rec.Code, rec.Body.String())
	}
	getReq := httptest.NewRequest(http.MethodGet, "/api/providers", nil)
	getRec := httptest.NewRecorder()
	handler.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK || strings.Contains(getRec.Body.String(), "key-a") || strings.Contains(getRec.Body.String(), "key-b") {
		t.Fatalf("provider list leaked keys or failed: status=%d body=%s", getRec.Code, getRec.Body.String())
	}
	var list struct {
		Active string `json:"activeProviderId"`
		Items  []struct {
			ID            string `json:"id"`
			KeyConfigured bool   `json:"keyConfigured"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 2 || list.Active != "provider-a" || !list.Items[0].KeyConfigured || !list.Items[1].KeyConfigured {
		t.Fatalf("unexpected provider list: %+v", list)
	}
	selectReq := httptest.NewRequest(http.MethodPost, "/api/providers/select", strings.NewReader(`{"id":"provider-b"}`))
	selectRec := httptest.NewRecorder()
	handler.ServeHTTP(selectRec, selectReq)
	if selectRec.Code != http.StatusOK {
		t.Fatalf("select status=%d body=%s", selectRec.Code, selectRec.Body.String())
	}
	if err := server.deleteProvider("provider-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := config.LoadProviderKey(configPath, "provider-a"); !os.IsNotExist(err) {
		t.Fatalf("provider A key was not deleted: %v", err)
	}
	if key, err := config.LoadProviderKey(configPath, "provider-b"); err != nil || key != "key-b" {
		t.Fatalf("provider B key changed after A delete: %q %v", key, err)
	}
}

func TestConfigReloadRehydratesSelectedProviderKey(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config", "model_config.json")
	cfg := config.Config{
		Providers: []config.Provider{
			{ID: "provider-a", Name: "Provider A", Endpoint: "https://a.example/v1/chat/completions", Protocol: config.ProtocolOpenAI, DefaultModelID: "a-model", Models: []config.Model{{ID: "a-model", Enabled: true}}},
			{ID: "provider-b", Name: "Provider B", Endpoint: "https://b.example/v1/chat/completions", Protocol: config.ProtocolOpenAI, DefaultModelID: "b-model", Models: []config.Model{{ID: "b-model", Enabled: true}}},
		},
		ActiveProviderID: "provider-a",
		ReasoningLevel:   "xhigh",
	}
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveProviderKey(configPath, "provider-a", "key-a-before"); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveProviderKey(configPath, "provider-b", "key-b"); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadRuntime(configPath, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	server := newTestServerWithRoot(t, root, configPath, loaded)
	if err := config.SaveProviderKey(configPath, "provider-a", "key-a-after"); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/config/reload", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("reload status=%d body=%s", rec.Code, rec.Body.String())
	}
	got, err := server.currentProviderConfig("provider-a")
	if err != nil {
		t.Fatal(err)
	}
	if got.APIKey != "key-a-after" {
		t.Fatalf("reloaded provider A key=%q", got.APIKey)
	}
	other, err := server.currentProviderConfig("provider-b")
	if err != nil {
		t.Fatal(err)
	}
	if other.APIKey != "key-b" {
		t.Fatalf("provider B key changed during A reload: %q", other.APIKey)
	}
}

func TestRestartHandlerInvokesControlledRestart(t *testing.T) {
	server := newTestServer(t, config.Default())
	called := make(chan struct{}, 1)
	server.SetRestartFunc(func() { called <- struct{}{} })
	req := httptest.NewRequest(http.MethodPost, "/api/engine/restart", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("restart status=%d body=%s", rec.Code, rec.Body.String())
	}
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("controlled restart callback was not invoked")
	}
}

func TestDeviceCapabilitiesEndpointIsReadOnlyAndDocumentsPrivacyBoundary(t *testing.T) {
	handler := newTestHandler(t, config.Default())

	req := httptest.NewRequest(http.MethodGet, "/api/device-capabilities", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("device capabilities status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["readOnly"] != true {
		t.Fatalf("readOnly = %#v, want true", payload["readOnly"])
	}
	if _, ok := payload["commands"].(map[string]any); !ok {
		t.Fatalf("commands = %#v, want object", payload["commands"])
	}
	if _, ok := payload["resolver"].(map[string]any); !ok {
		t.Fatalf("resolver = %#v, want object", payload["resolver"])
	}
	if _, ok := payload["excludedData"].([]any); !ok {
		t.Fatalf("excludedData = %#v, want array", payload["excludedData"])
	}
	for _, forbiddenKey := range []string{"imei", "serial", "mac", "location", "contacts", "apiKey", "sessionContents"} {
		if _, exists := payload[forbiddenKey]; exists {
			t.Fatalf("device response exposed forbidden key %q", forbiddenKey)
		}
	}

	postReq := httptest.NewRequest(http.MethodPost, "/api/device-capabilities", nil)
	postRec := httptest.NewRecorder()
	handler.ServeHTTP(postRec, postReq)
	if postRec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("device capabilities POST status = %d, want %d", postRec.Code, http.StatusMethodNotAllowed)
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

func TestSessionSettingsPersistAcrossReload(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config", "model_config.json")
	cfg := config.Config{
		Providers: []config.Provider{
			{ID: "provider-a", Name: "Provider A", Endpoint: "https://a.example/v1/chat/completions", Protocol: config.ProtocolOpenAI, DefaultModelID: "a-model", Models: []config.Model{{ID: "a-model", Enabled: true}}},
			{ID: "provider-b", Name: "Provider B", Endpoint: "https://b.example/v1/chat/completions", Protocol: config.ProtocolOpenAI, DefaultModelID: "b-model", Models: []config.Model{{ID: "b-model", Enabled: true}}},
		},
		ActiveProviderID: "provider-a",
		ReasoningLevel:   "xhigh",
	}
	server := newTestServerWithRoot(t, root, configPath, cfg)
	createRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(createRec, httptest.NewRequest(http.MethodPost, "/api/sessions", nil))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createRec.Code, createRec.Body.String())
	}
	var created session.Session
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	patchBody := `{"providerId":"provider-b","modelId":"b-model","reasoningLevel":"max"}`
	patchReq := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+created.ID, strings.NewReader(patchBody))
	patchReq.Header.Set("Content-Type", "application/json")
	patchRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(patchRec, patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", patchRec.Code, patchRec.Body.String())
	}
	var patched session.Session
	if err := json.Unmarshal(patchRec.Body.Bytes(), &patched); err != nil {
		t.Fatal(err)
	}
	if patched.ProviderID != "provider-b" || patched.ModelID != "b-model" || patched.ReasoningLevel != "max" {
		t.Fatalf("unexpected patched settings: %+v", patched)
	}
	getRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(getRec, httptest.NewRequest(http.MethodGet, "/api/sessions/"+created.ID, nil))
	var loaded session.Session
	if err := json.Unmarshal(getRec.Body.Bytes(), &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.ProviderID != "provider-b" || loaded.ModelID != "b-model" || loaded.ReasoningLevel != "max" {
		t.Fatalf("settings did not persist: %+v", loaded)
	}
	badReq := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+created.ID, strings.NewReader(`{"modelId":"a-model"}`))
	badReq.Header.Set("Content-Type", "application/json")
	badRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(badRec, badReq)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("cross-provider model status=%d body=%s", badRec.Code, badRec.Body.String())
	}
}

func TestChatSSEAndEncryptedSessionPersistence(t *testing.T) {
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, "unexpected provider request", http.StatusBadRequest)
			return
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		messages, ok := payload["messages"].([]any)
		if !ok || len(messages) == 0 {
			http.Error(w, "missing messages", http.StatusBadRequest)
			return
		}
		first, ok := messages[0].(map[string]any)
		if !ok || first["role"] != "system" || !strings.Contains(first["content"].(string), "本机 Android 设备能力") {
			http.Error(w, "missing device capability context", http.StatusBadRequest)
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

func TestChatUsesSessionProviderKey(t *testing.T) {
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer key-b" {
			http.Error(w, "wrong provider key", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"provider-b\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer provider.Close()

	root := t.TempDir()
	configPath := filepath.Join(root, "config", "model_config.json")
	cfg := config.Config{
		Providers: []config.Provider{
			{ID: "provider-a", Name: "Provider A", Endpoint: provider.URL, Protocol: config.ProtocolOpenAI, DefaultModelID: "a-model", Models: []config.Model{{ID: "a-model", Enabled: true}}},
			{ID: "provider-b", Name: "Provider B", Endpoint: provider.URL, Protocol: config.ProtocolOpenAI, DefaultModelID: "b-model", Models: []config.Model{{ID: "b-model", Enabled: true}}},
		},
		ActiveProviderID: "provider-a",
		ReasoningLevel:   "xhigh",
	}
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveProviderKey(configPath, "provider-a", "key-a"); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveProviderKey(configPath, "provider-b", "key-b"); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadRuntime(configPath, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	server := newTestServerWithRoot(t, root, configPath, loaded)
	server.client = &model.Client{HTTP: provider.Client()}
	handler := server.Handler()

	createRec := httptest.NewRecorder()
	handler.ServeHTTP(createRec, httptest.NewRequest(http.MethodPost, "/api/sessions", nil))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createRec.Code, createRec.Body.String())
	}
	var created session.Session
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	body := strings.NewReader(`{"sessionId":"` + created.ID + `","providerId":"provider-b","modelId":"b-model","reasoningLevel":"max","message":"test"}`)
	chatReq := httptest.NewRequest(http.MethodPost, "/api/chat", body)
	chatReq.Header.Set("Content-Type", "application/json")
	chatRec := httptest.NewRecorder()
	handler.ServeHTTP(chatRec, chatReq)
	if chatRec.Code != http.StatusOK || !strings.Contains(chatRec.Body.String(), "provider-b") {
		t.Fatalf("provider B chat status=%d body=%s", chatRec.Code, chatRec.Body.String())
	}
}
