package model

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/snowzlmbot/ai-web-engine/internal/config"
	"github.com/snowzlmbot/ai-web-engine/internal/session"
)

func TestEndpointURL(t *testing.T) {
	cases := []struct {
		name, endpoint, protocol, want string
	}{
		{"openai-base", "https://api.example.test", config.ProtocolOpenAI, "https://api.example.test/v1/chat/completions"},
		{"openai-full", "https://api.example.test/v1/chat/completions", config.ProtocolOpenAI, "https://api.example.test/v1/chat/completions"},
		{"anthropic-base", "https://api.example.test", config.ProtocolAnthropic, "https://api.example.test/v1/messages"},
		{"anthropic-full", "https://api.example.test/v1/messages", config.ProtocolAnthropic, "https://api.example.test/v1/messages"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := endpointURL(tc.endpoint, tc.protocol)
			if err != nil || got != tc.want {
				t.Fatalf("endpointURL() = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestRequestShapes(t *testing.T) {
	cfg := config.Config{Protocol: config.ProtocolOpenAI, APIKey: "test-key"}
	body, headers, err := request(cfg, "demo", "low", "skill", []session.Message{{Role: "user", Content: "hello"}})
	if err != nil {
		t.Fatal(err)
	}
	if headers["Authorization"] != "Bearer test-key" {
		t.Fatalf("authorization header = %q", headers["Authorization"])
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["stream"] != true || payload["reasoning_effort"] != "low" {
		t.Fatalf("unexpected OpenAI payload: %#v", payload)
	}
	messages := payload["messages"].([]any)
	if messages[0].(map[string]any)["role"] != "system" {
		t.Fatal("system message was not first")
	}

	cfg.Protocol = config.ProtocolAnthropic
	body, headers, err = request(cfg, "claude", "off", "skill", nil)
	if err != nil {
		t.Fatal(err)
	}
	if headers["x-api-key"] != "test-key" || headers["anthropic-version"] != "2023-06-01" {
		t.Fatalf("unexpected Anthropic headers: %#v", headers)
	}
	payload = map[string]any{}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["reasoning_effort"]; ok {
		t.Fatal("off reasoning level should not be sent")
	}
}

func TestParseSSE(t *testing.T) {
	input := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: [DONE]\n\n"
	var got string
	var done bool
	if err := parseSSE(config.ProtocolOpenAI, strings.NewReader(input), func(delta Delta) error {
		got += delta.Content
		done = done || delta.Done
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got != "hi" || !done {
		t.Fatalf("parsed SSE = %q, done=%v", got, done)
	}
}

func TestClientStreamOverHTTPS(t *testing.T) {
	var gotAuth string
	var gotPath string
	var gotModel string
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		gotModel, _ = payload["model"].(string)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ShortX \"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Rule\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer provider.Close()

	cfg := config.Config{Endpoint: provider.URL, Protocol: config.ProtocolOpenAI, APIKey: "test-key"}
	client := &Client{HTTP: provider.Client()}
	var text string
	var done bool
	if err := client.Stream(cfg, "demo-model", "low", "skill", []session.Message{{Role: "user", Content: "hello"}}, func(delta Delta) error {
		text += delta.Content
		done = done || delta.Done
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/v1/chat/completions" || gotAuth != "Bearer test-key" || gotModel != "demo-model" {
		t.Fatalf("request path/auth/model = %q/%q/%q", gotPath, gotAuth, gotModel)
	}
	if text != "ShortX Rule" || !done {
		t.Fatalf("stream = %q, done=%v", text, done)
	}
}

func TestClientStreamReportsProviderHTTPError(t *testing.T) {
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "provider rejected request", http.StatusUnauthorized)
	}))
	defer provider.Close()

	cfg := config.Config{Endpoint: provider.URL, Protocol: config.ProtocolOpenAI, APIKey: "test-key"}
	client := &Client{HTTP: provider.Client()}
	err := client.Stream(cfg, "demo-model", "off", "skill", nil, func(Delta) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "provider HTTP 401") || !strings.Contains(err.Error(), "provider rejected request") {
		t.Fatalf("unexpected provider error: %v", err)
	}
}
