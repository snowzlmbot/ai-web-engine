package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/snowzlmbot/ai-web-engine/internal/config"
	"github.com/snowzlmbot/ai-web-engine/internal/model"
	"github.com/snowzlmbot/ai-web-engine/internal/session"
)

func TestGenerationCanBeResubscribedAfterBrowserDisconnect(t *testing.T) {
	release := make(chan struct{})
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"draft\"}}]}\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer provider.Close()

	cfg := config.Config{
		Providers: []config.Provider{{
			ID: "provider-a", Name: "Provider A", Endpoint: provider.URL,
			Protocol: config.ProtocolOpenAI, DefaultModelID: "model-a",
			Models: []config.Model{{ID: "model-a", Enabled: true}},
		}},
		ActiveProviderID: "provider-a", ReasoningLevel: "xhigh",
	}
	root := t.TempDir()
	configPath := root + "/config/model_config.json"
	if err := config.Save(configPath, cfg); err != nil {
		t.Fatal(err)
	}
	if err := config.SaveProviderKey(configPath, "provider-a", "key-a"); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadRuntime(configPath, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	server := newTestServerWithRoot(t, root, configPath, loaded)
	server.client = &model.Client{HTTP: provider.Client()}
	handler := server.Handler()

	create := httptest.NewRecorder()
	handler.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/api/sessions", nil))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	var item session.Session
	if err := json.Unmarshal(create.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}

	body := strings.NewReader(`{"sessionId":"` + item.ID + `","message":"resume me","modelId":"model-a"}`)
	start := httptest.NewRecorder()
	startReq := httptest.NewRequest(http.MethodPost, "/api/generations", body)
	startReq.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(start, startReq)
	if start.Code != http.StatusAccepted {
		t.Fatalf("start status=%d body=%s", start.Code, start.Body.String())
	}
	var started struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(start.Body.Bytes(), &started); err != nil || started.TaskID == "" {
		t.Fatalf("invalid start response: %s", start.Body.String())
	}

	var active struct {
		Active bool `json:"active"`
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		check := httptest.NewRecorder()
		handler.ServeHTTP(check, httptest.NewRequest(http.MethodGet, "/api/generations?sessionId="+item.ID, nil))
		if err := json.Unmarshal(check.Body.Bytes(), &active); err != nil {
			t.Fatal(err)
		}
		if active.Active {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !active.Active {
		t.Fatal("generation was not active before provider release")
	}

	close(release)
	events := httptest.NewRecorder()
	handler.ServeHTTP(events, httptest.NewRequest(http.MethodGet, "/api/generations/"+started.TaskID+"/events", nil))
	stream := events.Body.String()
	if !strings.Contains(stream, `"content":"draft"`) || !strings.Contains(stream, `"content":"answer"`) || !strings.Contains(stream, `"type":"done"`) {
		t.Fatalf("resubscribed stream missing replayed events: %s", stream)
	}
}
